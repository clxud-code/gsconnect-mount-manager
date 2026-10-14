package manager

import (
	"context"
	"errors"
	"github.com/GSConnect/gsconnect-mount-manager/internal/config"
	"github.com/GSConnect/gsconnect-mount-manager/internal/model"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

type fakeDiscovery struct{ ds []model.Device }

func (f fakeDiscovery) Devices(context.Context) ([]model.Device, error) { return f.ds, nil }

type fakeBackend struct {
	mu    sync.Mutex
	calls int
	paths []string
	err   error
}

func TestBookmarkPreservesExistingEntries(t *testing.T) {
	file := filepath.Join(t.TempDir(), "gtk-3.0", "bookmarks")
	if err := os.MkdirAll(filepath.Dir(file), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte("file:///tmp/existing Existing\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := EnsureBookmark(file, "/tmp/Pixel 10a", "Pixel 10a"); err != nil {
		t.Fatal(err)
	}
	if err := EnsureBookmark(file, "/tmp/Pixel 10a", "Renamed"); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(b); got != "file:///tmp/existing Existing\nfile:///tmp/Pixel%2010a Pixel 10a\n" {
		t.Fatalf("bookmarks = %q", got)
	}
}

func TestRemoveBookmarkPreservesOtherEntries(t *testing.T) {
	file := filepath.Join(t.TempDir(), "bookmarks")
	input := "file:///tmp/keep Keep\nfile:///tmp/Pixel%2010a Pixel 10a\n"
	if err := os.WriteFile(file, []byte(input), 0644); err != nil {
		t.Fatal(err)
	}
	if err := RemoveBookmark(file, "/tmp/Pixel 10a"); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(b); got != "file:///tmp/keep Keep\n" {
		t.Fatalf("bookmarks = %q", got)
	}
}

func TestSFTPBookmarkURIUsesActiveGVfsMount(t *testing.T) {
	uri, err := SFTPBookmarkURI("/run/user/1000/gvfs/sftp:host=192.168.1.115,port=1739", "/storage/emulated/0")
	if err != nil {
		t.Fatal(err)
	}
	if uri != "sftp://192.168.1.115:1739/storage/emulated/0" {
		t.Fatalf("uri = %q", uri)
	}
}

func TestManagedGVfsLinkRecognition(t *testing.T) {
	if !isGVfsSFTPLink("/run/user/1000/gvfs/sftp:host=192.168.1.102,port=1740/storage/emulated/0") {
		t.Fatal("GVfs SFTP link was not recognised")
	}
	if isGVfsSFTPLink("/home/user/important-files") {
		t.Fatal("user path was accepted as a managed link")
	}
}

func TestNamedMountRejectsDanglingGSConnectLink(t *testing.T) {
	runtime := t.TempDir()
	byName := filepath.Join(runtime, "gsconnect", "by-name")
	if err := os.MkdirAll(byName, 0755); err != nil {
		t.Fatal(err)
	}
	missing := filepath.Join(runtime, "gvfs", "sftp:host=192.168.1.102,port=1739")
	if err := os.Symlink(missing, filepath.Join(byName, "Pixel")); err != nil {
		t.Fatal(err)
	}
	b := &GVfsBackend{Runtime: runtime}
	if _, ok, err := b.NamedMount(context.Background(), "Pixel"); err != nil || ok {
		t.Fatalf("NamedMount = ok:%t err:%v; want dangling link rejected", ok, err)
	}
}

func (f *fakeBackend) Mount(context.Context, string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	return f.err
}
func (f *fakeBackend) Unmount(context.Context, string) error { return nil }
func (f *fakeBackend) Mounts(context.Context) ([]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.paths...), nil
}
func (f *fakeBackend) NamedMount(context.Context, string) (string, bool, error) {
	return "", false, nil
}
func (f *fakeBackend) Check(context.Context, string) ([]string, error) {
	return []string{"Primary", "Download"}, nil
}
func TestNames(t *testing.T) {
	if SafeName("../ Pixel/10 ", "x") != "Pixel-10" {
		t.Fatal(SafeName("../ Pixel/10 ", "x"))
	}
	if LinkName("Phone", "abcdefghi", map[string]string{"Phone": "other"}) != "Phone-abcdefgh" {
		t.Fatal("collision")
	}
}
func TestRoot(t *testing.T) {
	if SelectRoot([]string{"/", "Primary"}, "") != "/Primary" {
		t.Fatal("primary")
	}
	if SelectRoot([]string{"Primary"}, "/bad") != "" {
		t.Fatal("override")
	}
}
func TestBackoffBounded(t *testing.T) {
	if Backoff(0, 2) != 0 || Backoff(3, 2) != 0 || Backoff(1, 2) < 500*time.Millisecond {
		t.Fatal("backoff")
	}
}
func TestNoParallelMount(t *testing.T) {
	b := &fakeBackend{paths: []string{"/tmp/gmm-test"}}
	c := config.Defaults()
	c.BaseDir = t.TempDir()
	c.ConnectTimeout = time.Second
	m := New(c, fakeDiscovery{[]model.Device{{ID: "one", Name: "Phone", Connected: true, Paired: true}}}, b, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if e := m.Refresh(context.Background()); e != nil {
		t.Fatal(e)
	}
	time.Sleep(50 * time.Millisecond)
	m.Mount("one")
	m.Mount("one")
	time.Sleep(50 * time.Millisecond)
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.calls != 1 {
		t.Fatalf("calls=%d", b.calls)
	}
}

func TestDiscoverDoesNotStartAutoMount(t *testing.T) {
	b := &fakeBackend{}
	c := config.Defaults()
	c.BaseDir = t.TempDir()
	m := New(c, fakeDiscovery{[]model.Device{{ID: "one", Name: "Phone", Connected: true, Paired: true}}}, b, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err := m.Discover(context.Background()); err != nil {
		t.Fatal(err)
	}
	time.Sleep(30 * time.Millisecond)
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.calls != 0 {
		t.Fatalf("Discover started %d mount operations", b.calls)
	}
}
func TestFailureDegrades(t *testing.T) {
	b := &fakeBackend{err: errors.New("offline")}
	c := config.Defaults()
	c.BaseDir = t.TempDir()
	m := New(c, fakeDiscovery{[]model.Device{{ID: "one", Connected: true, Paired: true}}}, b, slog.New(slog.NewTextHandler(io.Discard, nil)))
	_ = m.Refresh(context.Background())
	time.Sleep(30 * time.Millisecond)
	if m.Status()[0].State != model.Degraded {
		t.Fatal(m.Status()[0])
	}
}
