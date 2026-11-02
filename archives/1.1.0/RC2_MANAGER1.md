# MAILSTREAM-RC2 — server manager update 1

Use these updated server binaries with the existing
[MAILSTREAM-RC2 APK](https://github.com/Septonious/itom/releases/download/v1.1.0-mailstream-rc2/ITOM-1.1.0-MAILSTREAM-RC2.apk).
The transport, Android app, profiles and private state formats are unchanged.

**18. Update from newest ITOM .bin in /tmp** selects the most recently changed
regular file directly inside `/tmp` whose name contains `ITOM` (case-insensitive)
and ends in `.bin` (also case-insensitive). Linux change time handles uploads
whose original modification time is preserved by SFTP. This is an on-demand
update, not a background directory watcher.

The selected file is read without following symlinks and validated as an ITOM
Linux executable for this server's architecture before installation. It must
belong to root or the invoking sudo user and must not be group/world-writable.
If the newest matching upload is incomplete, invalid or for the wrong
architecture, the update stops; it does not install an older file silently.
The upload itself does not need executable permission.

The existing atomic installer preserves configuration and private state,
restarts a running service, and restores the old binary/unit on installation or
restart failure. An intentionally stopped service stays stopped. After a menu
update, the manager reopens using the newly installed binary.

The same operation is available without the menu:

```sh
sudo itom update-from-tmp
```

For the first installation of this manager feature, upload the appropriate new
binary to `/tmp`, make it executable and run its existing `install` command.
For an amd64 server:

```sh
chmod +x /tmp/ITOM-1.1.0-MAILSTREAM-RC2-manager1-linux-amd64.bin
sudo /tmp/ITOM-1.1.0-MAILSTREAM-RC2-manager1-linux-amd64.bin install
```

Then open `sudo itom`. Future uploads can use option 18 directly. No APK update
or profile re-import is required for this manager-only change.
