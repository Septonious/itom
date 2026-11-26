# ITOM RC5 — optional encrypted foreign exit

The Linux binary has two roles: `itom serve` (ingress) and `itom exit` (exit-only listener). The protocol is derived directly from OLCRTC RC3.4: TLS 1.3, mutually pinned PSK-derived Ed25519 identities, ALPN `olcrtc-relay/1`, encrypted length-prefixed destination and an explicit result. No public CA or certificate files. The ITOM listener preserves ITOM's public-destination policy. A configured exit defaults to fail-closed. No config file means ordinary direct egress; set `direct_fallback` true only if intentional. IMAP, internal readiness/administration and app Direct rules keep their previous paths. General UDP forwarding is not added.

## Prepare credentials

On a trusted computer, substitute the foreign VPS address (9444 avoids OLCRTC's 9443). Generate a **fresh** key and transfer the two private files securely. Never commit them.

```bash
umask 077
python3 - <<'PYKEY'
import json, secrets
from pathlib import Path
address = "FOREIGN_IP:9444"
key = secrets.token_hex(32)
for path, data in (("exit.json", {"listen":"0.0.0.0:9444", "address":address, "key":key, "max_connections":2048}), ("egress.json", {"address":address, "key":key, "connect_timeout_ms":2500, "retry_seconds":15, "direct_fallback":False})):
    with Path(path).open("x") as f:
        json.dump(data,f,indent=2)
        f.write("\n")
PYKEY
```

Verify the existing foreign firewall permits this chosen TCP port from the Russian ingress IP and denies other sources. No firewall changes are made. Check that 9444 is free.

## Foreign VPS

Upload the matching Linux binary and `exit.json`. As root:

```bash
install -m 0755 itom-linux-amd64 /usr/local/bin/itom-exit
install -d -m 0700 /etc/itom-exit
install -m 0600 exit.json /etc/itom-exit/exit.json
cat >/etc/systemd/system/itom-exit.service <<'UNITEND'
[Unit]
Description=ITOM encrypted TCP exit
After=network-online.target
Wants=network-online.target
[Service]
Type=simple
DynamicUser=yes
LoadCredential=exit.json:/etc/itom-exit/exit.json
ExecStart=/usr/local/bin/itom-exit exit --config %d/exit.json
Restart=on-failure
RestartSec=5
NoNewPrivileges=yes
PrivateTmp=yes
ProtectSystem=strict
ProtectHome=yes
UMask=0077
[Install]
WantedBy=multi-user.target
UNITEND
systemctl daemon-reload
systemctl enable --now itom-exit
systemctl status itom-exit --no-pager
```

This is a separate listener, not an IMAP server, and leaves OLCRTC untouched. Ubuntu with modern systemd supporting LoadCredential is required for this unit.

## Russian VPS

Stop the old ITOM server before migrating its complete `/var/lib/itom` directory. Preserve `server.db`, durable retention ledgers, account identities, keys, ownership and permissions. Do not run the same ingress twice or reset debt. Restore the state on RU and install the matching RC5 binary using the established ITOM update/install process. Test the route before replacing the active egress configuration:

```bash
/usr/local/bin/itom egress-test --config ./egress.json --target example.com:443
install -m 0600 egress.json /var/lib/itom/egress.json
systemctl restart itom
systemctl status itom --no-pager
```

The probe does not fall back to direct. Startup reads the configuration file; a restart interrupts established TCP streams. Existing Android profiles need no reimport if the server identity/account credentials are unchanged; use the matching RC5 testing APK.

## Measurements and rollback

On RU: `journalctl -u itom -f` and `itom traffic --watch`. On the foreign server: `journalctl -u itom-exit -f` reports cumulative destination-socket payload bytes every two seconds. Compare byte deltas against the actual elapsed transfer time; the ingress counters represent writes into the relay when encrypted egress is enabled. Neither socket counter alone proves remote-application delivery. Use a receiver-verified HTTP test as the final goodput authority. The WAN hop adds latency to connection setup and destination traffic; the local IMAP RTT does not include that hop.

To intentionally revert to direct RU egress, move `/var/lib/itom/egress.json` to a private backup location and restart ITOM. If the exit is unavailable and direct egress is unwanted, leave the file in place and restore the exit listener. Do not open the listener to arbitrary source IPs. No live deployment is performed by CI.
