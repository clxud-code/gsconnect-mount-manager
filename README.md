# GSConnect Mount Manager

A small Go user daemon that asks GSConnect 73 to create its own supported GVfs
SFTP mounts, verifies the resulting export, and safely publishes it under
`~/Android`. It does not replace GSConnect, KDE Connect, GVfs, or Android's
SFTP server.

## Install

Prerequisites: a paired KDE Connect Android device, active GSConnect 73, and a
graphical user session. On Ubuntu, the installer automatically uses `sudo apt`
once to install GVfs with its SFTP backend and build prerequisites. It then
downloads an official Go toolchain into
`$XDG_DATA_HOME/gsconnect-mount-manager/toolchains` when Go is absent,
downloads the declared Go module dependencies, and builds the daemon.

```bash
./install.sh
gsconnect-mount-manager doctor
systemctl --user status gsconnect-mount-manager
```

Re-running `./install.sh` safely rebuilds the binary and restarts the user
service, so code updates take effect immediately.

The installer builds `~/.local/bin/gsconnect-mount-manager`, installs a user
unit, and creates a mode-0600 config only if none exists. Root is only used for
the one-time Ubuntu package installation; normal operation requires no sudo.
Ensure `~/.local/bin` is on `PATH` for interactive use.

## Commands

```bash
gsconnect-mount-manager devices
gsconnect-mount-manager status
gsconnect-mount-manager mount <device-id-or-unique-name>
gsconnect-mount-manager unmount <device-id-or-unique-name>
gsconnect-mount-manager reconnect <device-id-or-unique-name>
gsconnect-mount-manager doctor
```

`unmount` delegates to GSConnect's own action. It does not delete Android
files, local configuration, or mount directories. To remove the program while
preserving all user data, run `./uninstall.sh`.

## Configuration

Optional configuration is at
`$XDG_CONFIG_HOME/gsconnect-mount-manager/config.toml` (normally
`~/.config/gsconnect-mount-manager/config.toml`). See `config.toml.example`.
Use the stable ID printed by `devices`, not a phone name. Device names are only
for the friendly symlink; conflicting names receive an ID suffix.

## Reliability and limits

The daemon uses DBus signals plus a conservative five-minute recovery scan. It
does not wake a phone or reconnect it merely because it was idle. On a failed
mount it makes a bounded number of jittered retries. A link is published only
after a real GVfs directory enumeration; no ordinary directory is presented as
phone storage.

There is no public D-Bus property mapping a mount URI to a device ID. For an
already mounted device, however, GSConnect 73's own runtime `by-name` link is
used and checked before publication, so restarting this daemon does not require
reconnecting healthy existing GSConnect mounts. See
[the architecture record](docs/architecture.md) for confirmed API details,
path selection and Android restrictions.

## Development

```bash
go test ./...
go test -race ./...
go vet ./...
go build ./...
```
