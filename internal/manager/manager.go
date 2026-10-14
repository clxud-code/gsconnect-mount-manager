package manager

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/GSConnect/gsconnect-mount-manager/internal/config"
	"github.com/GSConnect/gsconnect-mount-manager/internal/model"
)

type Discovery interface {
	Devices(context.Context) ([]model.Device, error)
}
type Manager struct {
	cfg       config.Config
	discovery Discovery
	backend   Backend
	log       *slog.Logger
	mu        sync.Mutex
	opMu      sync.Mutex
	states    map[string]*model.Status
	cancel    map[string]context.CancelFunc
	mounts    map[string]string
}

func New(cfg config.Config, d Discovery, b Backend, l *slog.Logger) *Manager {
	return &Manager{cfg: cfg, discovery: d, backend: b, log: l, states: map[string]*model.Status{}, cancel: map[string]context.CancelFunc{}, mounts: map[string]string{}}
}
// Refresh discovers devices and starts automatic mounting for newly discovered
// connected devices. It is for the long-running daemon.
func (m *Manager) Refresh(ctx context.Context) error {
	return m.refresh(ctx, true)
}

// Discover refreshes device state without starting background mount work. CLI
// commands use it so an explicit mount/reconnect has exactly one owner.
func (m *Manager) Discover(ctx context.Context) error {
	return m.refresh(ctx, false)
}

func (m *Manager) refresh(ctx context.Context, autoMount bool) error {
	ds, e := m.discovery.Devices(ctx)
	if e != nil {
		return e
	}
	m.mu.Lock()
	var cleanup []string
	seen := map[string]bool{}
	for _, d := range ds {
		seen[d.ID] = true
		s := m.states[d.ID]
		if s == nil {
			s = &model.Status{Device: d, State: model.Disconnected}
			m.states[d.ID] = s
		}
		s.Device = d
		if !d.Paired || !d.Connected {
			s.State = model.Disconnected
			s.LastError = ""
			cleanup = append(cleanup, d.ID)
			continue
		}
		if s.State == model.Disconnected {
			s.State = model.Discovered
		}
		auto := m.cfg.AutoMount
		if x, ok := m.cfg.Devices[d.ID]; ok && x.AutoMount != nil {
			auto = *x.AutoMount
		}
		if autoMount && auto && s.State == model.Discovered {
			go m.mount(d.ID, false)
		}
	}
	for id, s := range m.states {
		if !seen[id] {
			s.State = model.Disconnected
			cleanup = append(cleanup, id)
		}
	}
	m.mu.Unlock()
	for _, id := range cleanup {
		m.unpublish(id)
	}
	// GSConnect recreates its by-name link when a phone roams to another IP or
	// SFTP port. Re-adopt that authoritative link on every D-Bus refresh so
	// ~/Android and the Nautilus bookmark never retain the old address.
	for _, d := range ds {
		if d.Paired && d.Connected {
			go m.adoptKnownMount(ctx, d.ID, d.Name)
		}
	}
	return nil
}

func (m *Manager) adoptKnownMount(ctx context.Context, id, name string) {
	path, ok, err := m.backend.NamedMount(ctx, name)
	if err != nil || !ok {
		return
	}
	m.mu.Lock()
	s := m.states[id]
	if s == nil || !s.Device.Paired || !s.Device.Connected || (s.State == model.Mounted && s.MountURI == path) {
		m.mu.Unlock()
		return
	}
	m.mu.Unlock()

	// Keep publication serial with a requested mount/unmount. Re-read the link
	// under that lock because GSConnect may have recreated it meanwhile.
	m.opMu.Lock()
	defer m.opMu.Unlock()
	path, ok, err = m.backend.NamedMount(ctx, name)
	if err != nil || !ok {
		return
	}
	m.finishKnownMount(id, path)
}
func (m *Manager) mount(id string, reconnect bool) {
	m.mu.Lock()
	s := m.states[id]
	if s == nil || s.State == model.Connecting || s.State == model.Mounted {
		return
	}
	if !s.Device.Connected || !s.Device.Paired {
		return
	}
	s.State = model.Connecting
	if reconnect {
		s.State = model.Reconnecting
	}
	ctx, cancel := context.WithTimeout(context.Background(), m.cfg.ConnectTimeout)
	if old := m.cancel[id]; old != nil {
		old()
	}
	m.cancel[id] = cancel
	m.mu.Unlock()
	defer cancel()
	m.opMu.Lock()
	defer m.opMu.Unlock()
	// GSConnect may already own a healthy mount (for example, after this
	// daemon restarts). Its runtime by-name link is the only supported mapping
	// from a device name to an existing GVfs mount, so adopt it before asking
	// GSConnect to start another browsing session.
	if path, ok, err := m.backend.NamedMount(ctx, s.Device.Name); err == nil && ok {
		m.finishKnownMount(id, path)
		return
	}
	before := map[string]bool{}
	if ms, e := m.backend.Mounts(ctx); e == nil {
		for _, x := range ms {
			before[x] = true
		}
	}
	if e := m.backend.Mount(ctx, id); e != nil {
		m.failed(id, e)
		return
	}
	path, e := WaitMount(ctx, m.backend, before)
	if e != nil {
		m.failed(id, e)
		return
	}
	m.finishKnownMount(id, path)
}

// finishKnownMount publishes a target provided by GSConnect's own by-name
// link. Do not enumerate the FUSE filesystem here: user-systemd services may
// block on GVfs FUSE even though Nautilus can access it. A vanished mount makes
// the published link dangling rather than exposing a local writable directory.
func (m *Manager) finishKnownMount(id, path string) {
	root := "/"
	if override := m.cfg.Devices[id].Root; override != "" {
		root = override
	}
	if e := m.publish(id, path, root); e != nil {
		m.failed(id, e)
		return
	}
	m.addBookmark(id, path, root)
	m.mu.Lock()
	s := m.states[id]
	s.State = model.Mounted
	s.MountURI = path
	s.Root = root
	s.LastError = ""
	s.Attempts = 0
	m.mounts[id] = path
	m.mu.Unlock()
	m.log.Info("device mounted", "device", id, "path", path)
}
func (m *Manager) addBookmark(id, mountPath, root string) {
	m.mu.Lock()
	s := m.states[id]
	if s == nil {
		m.mu.Unlock()
		return
	}
	link, name := s.Link, s.Device.Name
	m.mu.Unlock()
	uri, err := SFTPBookmarkURI(mountPath, root)
	if err != nil {
		m.log.Warn("could not derive native GVfs bookmark", "device", id, "error", err)
		return
	}
	registryPath := bookmarkStateFile()
	registry, err := loadBookmarkRegistry(registryPath)
	if err != nil {
		m.log.Warn("could not load managed bookmark state", "device", id, "error", err)
		return
	}
	if previous := registry[id]; previous != "" && previous != uri {
		if err := RemoveBookmarkURI(gtkBookmarksFile(), previous); err != nil {
			m.log.Warn("could not replace old Nautilus bookmark", "device", id, "error", err)
		}
	}
	if err := EnsureBookmarkURI(gtkBookmarksFile(), uri, name); err != nil {
		m.log.Warn("could not add Nautilus bookmark", "device", id, "error", err)
		return
	}
	registry[id] = uri
	if err := saveBookmarkRegistry(registryPath, registry); err != nil {
		m.log.Warn("could not save managed bookmark state", "device", id, "error", err)
	}
	// Clean up the legacy local bookmark for this exact manager link. The link
	// remains available to command-line programs; only Nautilus uses the native
	// remote URI to avoid expensive local-only previews.
	if link != "" {
		if err := RemoveBookmark(gtkBookmarksFile(), link); err != nil {
			m.log.Warn("could not remove legacy Nautilus bookmark", "device", id, "error", err)
		}
	}
	m.mu.Lock()
	if s := m.states[id]; s != nil {
		s.Bookmark = uri
	}
	m.mu.Unlock()
}
func (m *Manager) unpublish(id string) {
	m.mu.Lock()
	s := m.states[id]
	if s == nil {
		m.mu.Unlock()
		return
	}
	link, bookmark := s.Link, s.Bookmark
	if link == "" {
		link = filepath.Join(m.cfg.BaseDir, SafeName(s.Device.Name, id))
	}
	s.Link = ""
	m.mu.Unlock()

	// Never remove a directory or a user link. Only remove a link that still
	// targets a GSConnect GVfs SFTP mount.
	if target, err := os.Readlink(link); err == nil && isGVfsSFTPLink(target) {
		if err := os.Remove(link); err != nil {
			m.log.Warn("could not remove stale Android link", "path", link, "error", err)
		}
	}
	registryPath := bookmarkStateFile()
	registry, err := loadBookmarkRegistry(registryPath)
	if err != nil {
		m.log.Warn("could not load managed bookmark state", "device", id, "error", err)
	}
	if bookmark == "" {
		bookmark = registry[id]
	}
	if bookmark != "" {
		if err := RemoveBookmarkURI(gtkBookmarksFile(), bookmark); err != nil {
			m.log.Warn("could not remove Nautilus bookmark", "device", id, "error", err)
		}
		delete(registry, id)
		if err := saveBookmarkRegistry(registryPath, registry); err != nil {
			m.log.Warn("could not save managed bookmark state", "device", id, "error", err)
		}
	}
	if err := RemoveBookmark(gtkBookmarksFile(), link); err != nil {
		m.log.Warn("could not remove Nautilus bookmark", "device", id, "error", err)
	}
}
func (m *Manager) finishMount(id, path string, entries []string) {
	rootOverride := m.cfg.Devices[id].Root
	root := SelectRoot(entries, rootOverride)
	if rootOverride != "" && root == "" {
		m.failed(id, fmt.Errorf("configured root %q is not exported", rootOverride))
		return
	}
	if root == "" {
		root = "/"
	}
	if e := m.publish(id, path, root); e != nil {
		m.failed(id, e)
		return
	}
	m.mu.Lock()
	s := m.states[id]
	s.State = model.Mounted
	s.MountURI = path
	s.Root = root
	s.LastError = ""
	s.Attempts = 0
	m.mounts[id] = path
	m.mu.Unlock()
	m.log.Info("device mounted", "device", id, "path", path)
}
func (m *Manager) failed(id string, e error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s := m.states[id]
	if s == nil {
		return
	}
	s.State = model.Degraded
	s.LastError = e.Error()
	s.Attempts++
	s.NextRetry = time.Now().Add(Backoff(s.Attempts, m.cfg.MaxRetries))
	m.log.Warn("mount failed", "device", id, "error", e, "attempt", s.Attempts)
}
func (m *Manager) publish(id, target, root string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	names := map[string]string{}
	for k, v := range m.states {
		names[SafeName(v.Device.Name, k)] = k
	}
	name := LinkName(m.states[id].Device.Name, id, names)
	if e := os.MkdirAll(m.cfg.BaseDir, 0755); e != nil {
		return e
	}
	link := filepath.Join(m.cfg.BaseDir, name)
	desired := filepath.Join(target, root)
	if existing, e := os.Lstat(link); e == nil {
		if existing.Mode()&os.ModeSymlink != 0 {
			if old, e := os.Readlink(link); e == nil {
				if old == desired {
					m.states[id].Link = link
					return nil
				}
				// A device can receive a different IP and SFTP port after Wi-Fi
				// roaming. Replace only a link to a previous GVfs SFTP mount,
				// never a user path or a link to another location.
				if isGVfsSFTPLink(old) {
					if e := os.Remove(link); e != nil {
						return e
					}
				} else {
					return fmt.Errorf("refusing to replace existing path %s", link)
				}
			}
		} else {
			return fmt.Errorf("refusing to replace existing path %s", link)
		}
	}
	if e := os.Symlink(desired, link); e != nil {
		return e
	}
	m.states[id].Link = link
	return nil
}

func isGVfsSFTPLink(target string) bool {
	return strings.HasPrefix(target, "/run/user/") && strings.Contains(target, "/gvfs/sftp:")
}
func (m *Manager) Mount(id string) { go m.mount(id, false) }
func (m *Manager) Reconnect(id string) {
	m.mu.Lock()
	if s := m.states[id]; s != nil {
		s.State = model.Discovered
		s.Attempts = 0
	}
	m.mu.Unlock()
	go m.mount(id, true)
}
func (m *Manager) Unmount(ctx context.Context, id string) error {
	m.mu.Lock()
	if c := m.cancel[id]; c != nil {
		c()
	}
	m.mu.Unlock()
	m.opMu.Lock()
	defer m.opMu.Unlock()
	if e := m.backend.Unmount(ctx, id); e != nil {
		return e
	}
	m.mu.Lock()
	if s := m.states[id]; s != nil {
		s.State = model.Discovered
		s.MountURI = ""
	}
	m.mu.Unlock()
	return nil
}
func (m *Manager) Status() []model.Status {
	m.mu.Lock()
	defer m.mu.Unlock()
	r := make([]model.Status, 0, len(m.states))
	for _, x := range m.states {
		r = append(r, *x)
	}
	return r
}
func (m *Manager) Run(ctx context.Context) {
	m.Refresh(ctx)
	t := time.NewTicker(m.cfg.CheckInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			m.retry(ctx)
		}
	}
}
func (m *Manager) retry(ctx context.Context) {
	_ = m.Refresh(ctx)
	m.mu.Lock()
	for id := range m.mounts {
		if s := m.states[id]; s != nil && s.State == model.Mounted {
			if _, ok, e := m.backend.NamedMount(ctx, s.Device.Name); e != nil || !ok {
				if e == nil {
					e = fmt.Errorf("GSConnect by-name link disappeared")
				}
				s.State = model.Degraded
				s.LastError = "stale GVfs mount: " + e.Error()
				s.Attempts++
				s.NextRetry = time.Now().Add(Backoff(s.Attempts, m.cfg.MaxRetries))
			}
		}
	}
	now := time.Now()
	var ids []string
	for id, s := range m.states {
		if s.State == model.Degraded && s.Attempts <= m.cfg.MaxRetries && !s.NextRetry.After(now) {
			ids = append(ids, id)
		}
	}
	m.mu.Unlock()
	for _, id := range ids {
		go m.mount(id, true)
	}
}
