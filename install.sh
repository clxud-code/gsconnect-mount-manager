#!/bin/sh
set -eu
root=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
data_home=${XDG_DATA_HOME:-"$HOME/.local/share"}
log() { printf '\n==> %s\n' "$*" >&2; }

ensure_system_dependencies() {
    log "Checking Ubuntu system dependencies"
    missing=0
    for package in gvfs gvfs-fuse gvfs-backends libglib2.0-bin dbus-user-session openssh-client curl ca-certificates; do
        if ! dpkg-query -W -f='${db:Status-Status}' "$package" 2>/dev/null | grep -qx installed; then
            missing=1
            break
        fi
    done
    if [ "$missing" -eq 0 ]; then
        echo "    GVfs and supporting packages are already installed." >&2
        return
    fi

    command -v sudo >/dev/null 2>&1 || {
        echo "sudo is required once to install Ubuntu dependencies" >&2
        exit 1
    }
    log "Installing GVfs, its SFTP backend, and build prerequisites"
    sudo apt-get update
    sudo apt-get install --assume-yes \
        gvfs gvfs-fuse gvfs-backends libglib2.0-bin dbus-user-session openssh-client \
        curl ca-certificates tar coreutils
}

ensure_go() {
    if command -v go >/dev/null 2>&1; then
        installed_go=$(command -v go)
        installed_minor=$("$installed_go" env GOVERSION 2>/dev/null | sed -n 's/^go1\.\([0-9][0-9]*\).*/\1/p')
        if [ -n "$installed_minor" ] && [ "$installed_minor" -ge 22 ]; then
            echo "    Using system Go: $($installed_go version)" >&2
            printf '%s\n' "$installed_go"
            return
        fi
        echo "    Existing Go is older than 1.22; using a current user-local toolchain." >&2
    fi

    case $(uname -m) in
        x86_64) go_arch=amd64 ;;
        aarch64|arm64) go_arch=arm64 ;;
        *) echo "unsupported CPU architecture: $(uname -m)" >&2; exit 1 ;;
    esac

    metadata=$(curl -fsSL --proto '=https' --tlsv1.2 'https://go.dev/dl/?mode=json')
    compact=$(printf '%s' "$metadata" | tr -d '[:space:]')
    go_version=$(printf '%s' "$compact" | sed -n 's/^\[{"version":"\([^"]*\)".*/\1/p')
    archive=${go_version}.linux-${go_arch}.tar.gz
    [ -n "$go_version" ] || {
        echo "could not select a Go release from go.dev metadata" >&2
        exit 1
    }

    toolchain_dir="$data_home/gsconnect-mount-manager/toolchains/$go_version"
    go_bin="$toolchain_dir/bin/go"
    if [ ! -x "$go_bin" ]; then
        log "Downloading Go $go_version for linux/$go_arch"
        tmp=$(mktemp -d)
        trap 'rm -rf "$tmp"' EXIT HUP INT TERM
        curl -fsSL --proto '=https' --tlsv1.2 "https://go.dev/dl/$archive" -o "$tmp/$archive"
        mkdir -p "$toolchain_dir"
        tar -xzf "$tmp/$archive" -C "$toolchain_dir" --strip-components=1
        rm -rf "$tmp"
        trap - EXIT HUP INT TERM
    else
        echo "    Using cached Go toolchain: $go_version" >&2
    fi
    printf '%s\n' "$go_bin"
}

ensure_system_dependencies
log "Selecting Go toolchain"
go_cmd=$(ensure_go)
log "Using: $($go_cmd version)"
mkdir -p "$HOME/.local/bin" "$HOME/.config/systemd/user" "$HOME/.config/gsconnect-mount-manager"
log "Downloading Go module dependencies and building"
# Deliberately disable Go checksum-database verification for this installation.
(cd "$root" && GOSUMDB=off "$go_cmd" build -mod=mod -o "$HOME/.local/bin/gsconnect-mount-manager" ./cmd/gsconnect-mount-manager)
# `-mod=mod` may create this cache file automatically; do not retain it.
rm -f "$root/go.sum"
log "Installing and starting the systemd user service"
install -m 0644 "$root/systemd/gsconnect-mount-manager.service" "$HOME/.config/systemd/user/gsconnect-mount-manager.service"
if [ ! -e "$HOME/.config/gsconnect-mount-manager/config.toml" ]; then
    install -m 0600 "$root/config.toml.example" "$HOME/.config/gsconnect-mount-manager/config.toml"
    echo "    Created default configuration." >&2
else
    echo "    Preserved existing configuration." >&2
fi
systemctl --user daemon-reload
systemctl --user enable gsconnect-mount-manager.service
# enable --now does not replace an already running process after an upgrade.
systemctl --user restart gsconnect-mount-manager.service
log "Installation complete"
echo "Run: gsconnect-mount-manager doctor" >&2
