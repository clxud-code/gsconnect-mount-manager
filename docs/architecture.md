# Architecture and GSConnect 73 investigation

This document records findings from the installed GSConnect 73 source at
`~/.local/share/gnome-shell/extensions/gsconnect@andyholmes.github.io` and a
read-only session-bus introspection on 2026-10-13.

## Confirmed integration boundary

GSConnect owns discovery, pairing and the KDE Connect transport. Its LAN
backend validates the stable KDE Connect `deviceId`, tracks changing network
channels, and exports paired devices on the session bus. The service name is
`org.gnome.Shell.Extensions.GSConnect`; the root object is
`/org/gnome/Shell/Extensions/GSConnect`; it implements
`org.freedesktop.DBus.ObjectManager`. Device objects are at
`.../Device/<deviceId>` and expose
`org.gnome.Shell.Extensions.GSConnect.Device` properties including `Id`,
`Name`, `Type`, `Paired`, and `Connected`.

Each device object also exports `org.gtk.Actions`. The confirmed actions are
`mount` and `unmount`; they are the only control API this project invokes.
Device changes are observed by ObjectManager/property signals, while the
periodic refresh is a five-minute recovery guard for service/bus restarts.

The source in `service/plugins/sftp.js` shows that `mount` sends the supported
`kdeconnect.sftp.request` packet with `startBrowsing: true`. Android replies
with `kdeconnect.sftp`, carrying a port, username and password. GSConnect adds
its private key to the agent and asks `Gio.File.new_for_uri("sftp://host:port/")`
to mount the enclosing volume. Thus GVfs, not sshfs and not this manager,
implements SFTP. Credentials are only passed in-memory by GSConnect to
`Gio.MountOperation` and are never visible to this process or its logs.

GVfs exposes successful mounts under `$XDG_RUNTIME_DIR/gvfs/sftp:host=...,port=...`.
The investigation observed two such mounts. The sandbox intentionally could
not enumerate their FUSE content; that is not interpreted as a failed mount.

## Consequences

The manager never configures an IP address, reuses an SFTP password, implements
KDE Connect/SFTP, alters GSConnect keys, or disables host-key checking. IP
changes are handled by GSConnect before the `mount` action is issued.

The public GSConnect D-Bus API does **not** publish the SFTP response nor map a
mount URI back to a device ID. A just-created GVfs mount is therefore associated
transactionally: the manager snapshots GVfs paths, invokes one serialized
action, waits for a new responsive path, and only then records a runtime mapping.
For an existing mount, GSConnect 73 itself creates
`$XDG_RUNTIME_DIR/gsconnect/by-name/<device name>` pointing at its GVfs path.
The manager resolves and health-checks that GSConnect-owned link before
adopting it; it never guesses from an IP address.

The SFTP response's Android paths are also not public on D-Bus. The manager
enumerates the *actual GVfs export root* after it responds and chooses `/Primary`,
then `/storage/emulated/0`, then `/`, otherwise the first export. A configured
root is accepted only if it is actually exported. Android app-scoped storage and
other sandboxed paths remain inaccessible unless KDE Connect itself exports them.

## Published paths and lifecycle

No local mount directory is ever used as a fallback. On a verified GVfs export
(a successful real directory enumeration, including a valid empty export), an
`~/Android/<safe name>` symlink is made directly to it. If GVfs
goes away, that link becomes dangling, so writes fail rather than landing in an
empty local directory. Existing non-symlink paths are never replaced.

Per-device states are `DISCONNECTED`, `DISCOVERED`, `CONNECTING`, `MOUNTED`,
`DEGRADED`, and `RECONNECTING`. Operations are serialized by the backend and
per-device state prevents duplicate requests. Connects are context-bounded;
retries use jittered exponential backoff (1 s through 64 s) and stop after the
configured maximum. The manager never reconnects merely because a device is
idle; it reacts to signals or explicitly failed mounting.
