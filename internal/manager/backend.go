package manager

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Backend delegates transport and authentication to GSConnect/GVfs. It never handles credentials.
type Backend interface {
	Mount(context.Context, string) error
	Unmount(context.Context, string) error
	Mounts(context.Context) ([]string, error)
	NamedMount(context.Context, string) (string, bool, error)
	Check(context.Context, string) ([]string, error)
}
type GSConnect interface {
	Action(context.Context, string, string) error
}
type GVfsBackend struct {
	GS      GSConnect
	Runtime string
	mu      sync.Mutex
}

func NewGVfsBackend(gs GSConnect) *GVfsBackend {
	runtime := os.Getenv("XDG_RUNTIME_DIR")
	if runtime == "" {
		runtime = filepath.Join("/run/user", strconv.Itoa(os.Getuid()))
	}
	return &GVfsBackend{GS: gs, Runtime: runtime}
}
func (b *GVfsBackend) Mount(ctx context.Context, id string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.GS.Action(ctx, id, "mount")
}
func (b *GVfsBackend) Unmount(ctx context.Context, id string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.GS.Action(ctx, id, "unmount")
}
func (b *GVfsBackend) Mounts(ctx context.Context) ([]string, error) {
	entries, e := os.ReadDir(filepath.Join(b.Runtime, "gvfs"))
	if e != nil {
		return nil, e
	}
	var r []string
	for _, x := range entries {
		if strings.HasPrefix(x.Name(), "sftp:") {
			r = append(r, filepath.Join(b.Runtime, "gvfs", x.Name()))
		}
	}
	return r, nil
}

// NamedMount resolves GSConnect's own by-name link. Unlike guessing from an
// IP address, this link is created by the GSConnect SFTP plugin for this exact
// device name and is safe to adopt after a manager restart.
func (b *GVfsBackend) NamedMount(ctx context.Context, name string) (string, bool, error) {
	select {
	case <-ctx.Done():
		return "", false, ctx.Err()
	default:
	}
	target, err := os.Readlink(filepath.Join(b.Runtime, "gsconnect", "by-name", name))
	if os.IsNotExist(err) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	gvfsRoot := filepath.Join(b.Runtime, "gvfs") + string(filepath.Separator)
	if !strings.HasPrefix(filepath.Clean(target)+string(filepath.Separator), gvfsRoot) {
		return "", false, fmt.Errorf("GSConnect by-name target is outside GVfs")
	}
	// GSConnect can leave its by-name symlink behind after GVfs has removed the
	// underlying mount. A dangling symlink is not a mount and must never be
	// published as a successful filesystem.
	if _, err := os.Stat(target); err != nil {
		if os.IsNotExist(err) {
			return "", false, nil
		}
		return "", false, err
	}
	return target, true, nil
}
func (b *GVfsBackend) Check(ctx context.Context, path string) ([]string, error) {
	// Go's os.ReadDir can block indefinitely in a systemd user service when
	// crossing a GVfs FUSE boundary. gio talks to GVfs using its native client
	// path and is both cancellable and the same integration used by Nautilus.
	cmd := exec.CommandContext(ctx, "gio", "list", "-a", "standard::name", path)
	stdout, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("gio list %s: %w", path, err)
	}
	var out []string
	scanner := bufio.NewScanner(strings.NewReader(string(stdout)))
	for scanner.Scan() {
		if name := strings.TrimSpace(scanner.Text()); name != "" {
			out = append(out, name)
		}
	}
	return out, scanner.Err()
}
func WaitMount(ctx context.Context, b Backend, before map[string]bool) (string, error) {
	t := time.NewTicker(250 * time.Millisecond)
	defer t.Stop()
	for {
		m, e := b.Mounts(ctx)
		if e == nil {
			for _, p := range m {
				if !before[p] {
					// The path is a new GVfs SFTP mount. Its creation is the
					// successful result of the GSConnect action; probing FUSE from
					// a user unit can block despite a healthy interactive mount.
					return p, nil
				}
			}
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-t.C:
		}
	}
}
