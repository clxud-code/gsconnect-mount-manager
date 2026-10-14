package manager

import (
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

// EnsureBookmark adds a Nautilus/GTK bookmark without touching entries owned
// by the user or other applications. GTK bookmarks are the standard way to
// show a local path in Nautilus' left sidebar.
func EnsureBookmark(bookmarkFile, path, label string) error {
	uri := (&url.URL{Scheme: "file", Path: path}).String()
	return EnsureBookmarkURI(bookmarkFile, uri, label)
}

// EnsureBookmarkURI adds a GTK bookmark for a native GVfs URI. Using the SFTP
// URI (rather than the local FUSE symlink) makes Nautilus treat the device as
// remote, avoiding thumbnail and metadata work intended for local disks.
func EnsureBookmarkURI(bookmarkFile, uri, label string) error {
	data, err := os.ReadFile(bookmarkFile)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	for _, line := range strings.Split(string(data), "\n") {
		if fields := strings.Fields(line); len(fields) > 0 && fields[0] == uri {
			return nil
		}
	}
	if err := os.MkdirAll(filepath.Dir(bookmarkFile), 0755); err != nil {
		return err
	}
	entry := uri + " " + label + "\n"
	return os.WriteFile(bookmarkFile, append(data, []byte(entry)...), 0644)
}

// RemoveBookmark removes only the entry for one manager-owned local path and
// preserves every other GTK bookmark byte-for-byte.
func RemoveBookmark(bookmarkFile, path string) error {
	uri := (&url.URL{Scheme: "file", Path: path}).String()
	return RemoveBookmarkURI(bookmarkFile, uri)
}

func RemoveBookmarkURI(bookmarkFile, uri string) error {
	data, err := os.ReadFile(bookmarkFile)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	var kept []string
	changed := false
	for _, line := range strings.Split(strings.TrimSuffix(string(data), "\n"), "\n") {
		if fields := strings.Fields(line); len(fields) > 0 && fields[0] == uri {
			changed = true
			continue
		}
		kept = append(kept, line)
	}
	if !changed {
		return nil
	}
	result := strings.Join(kept, "\n")
	if result != "" {
		result += "\n"
	}
	return os.WriteFile(bookmarkFile, []byte(result), 0644)
}

// SFTPBookmarkURI translates the runtime GVfs mount name produced by GVfs
// into its native URI. The address is read from the active mount every time;
// it is never stored in configuration or guessed from a device.
func SFTPBookmarkURI(mountPath, root string) (string, error) {
	name := filepath.Base(mountPath)
	if !strings.HasPrefix(name, "sftp:") {
		return "", fmt.Errorf("not a GVfs SFTP mount: %q", mountPath)
	}
	fields := map[string]string{}
	for _, part := range strings.Split(strings.TrimPrefix(name, "sftp:"), ",") {
		key, value, ok := strings.Cut(part, "=")
		if ok {
			fields[key] = value
		}
	}
	host, port := fields["host"], fields["port"]
	if host == "" || port == "" {
		return "", fmt.Errorf("GVfs mount lacks host or port")
	}
	if !strings.HasPrefix(root, "/") {
		root = "/" + root
	}
	return (&url.URL{Scheme: "sftp", Host: net.JoinHostPort(host, port), Path: root}).String(), nil
}

func bookmarkStateFile() string {
	base := os.Getenv("XDG_STATE_HOME")
	if base == "" {
		home, _ := os.UserHomeDir()
		base = filepath.Join(home, ".local", "state")
	}
	return filepath.Join(base, "gsconnect-mount-manager", "bookmarks.json")
}

func loadBookmarkRegistry(path string) (map[string]string, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return map[string]string{}, nil
	}
	if err != nil {
		return nil, err
	}
	registry := map[string]string{}
	if err := json.Unmarshal(data, &registry); err != nil {
		return nil, err
	}
	return registry, nil
}

func saveBookmarkRegistry(path string, registry map[string]string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	data, err := json.Marshal(registry)
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func gtkBookmarksFile() string {
	base := os.Getenv("XDG_CONFIG_HOME")
	if base == "" {
		home, _ := os.UserHomeDir()
		base = filepath.Join(home, ".config")
	}
	return filepath.Join(base, "gtk-3.0", "bookmarks")
}
