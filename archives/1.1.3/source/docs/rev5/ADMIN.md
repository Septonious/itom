# Rev5 server administration in Android

An enabled server-side client named exactly `admin` can manage the server from **Settings → Server admin** after connecting. Access is verified by the server using the authenticated client identity and key. The display name of a local Android profile does not determine the server role.

For an existing installation, use the server manager's Clients menu to rename the intended client to `admin`, then start/restart the service and connect that client's existing profile. A new client can instead be created as `admin` and exported using the usual Android invitation. The server still supports two configured clients; an existing client can be renamed when both slots are occupied.

The app uses the existing encrypted IMAP transport. There is no public HTTP administration port. A per-connection challenge additionally requires proof of possession of the app's private client key: an ordinary application using the admin's VPN or SOCKS proxy cannot gain management authority simply by opening the reserved transport destination.

## Available controls

| Server manager function | Android location |
| --- | --- |
| Server status | Overview: service, accounts and clients |
| Mail accounts | Accounts: add, change password, remove registration |
| Client registrations | Clients: add, rename, enable, disable, remove |
| Export Android profile | Clients: export and save the selected client's invitation |
| Account diagnostics | Accounts: check one account or all accounts, with progress |
| Start / stop / restart | Service: guarded service actions |
| Install/update installed service | Service: reinstall the already installed executable and unit |
| Live logs | Diagnostics: Logs, Live |
| Live packet trace | Diagnostics: Packet trace, Live |
| Full server diagnostics | Diagnostics: export the same bounded ZIP as the CLI |
| Server logging/tracing | Diagnostics: server tracing switch |
| Live client traffic | Overview: per-client TCP counts, useful rates and totals |
| Global / individual notices | Messages: editable text with formatting preview |
| Transport resources | Service: mailbox layout, DATA shards and socket/login budgets |

Online account checks verify TLS, login and selection of configured clients' existing mailboxes, including UIDVALIDITY. They never create missing mailboxes. Only the specific provider `NO [SERVERBUG] Mailbox uidvalidity changed` setup failure permits one retry on a fresh connection, within a 45-second total check. The CLI's offline folder-setup check retains its ability to initialize missing transport folders. Neither check retries an ambiguous APPEND or treats a stale stored locator as deletion proof.

Initial server initialization and supplying a newer executable require local server access. Permanent mailbox purges, uninstall and journal-profile migrations remain CLI-only because they cannot provide a true automatic undo. Removing an account or client registration in the app preserves its mailboxes and physical quota journals.

An exported client invitation contains credentials. Save it only to a trusted destination and share it only with the intended client. Status, traffic, logs and the background admin snapshot do not contain account passwords or client keys.

## Automatic recovery of a disruptive change

Every configuration or service mutation has one durable trial. Before applying it, the server starts an independent systemd watchdog outside `itom.service`'s process group. If that watchdog cannot be armed, the change is rejected. The normal installed Linux service and `/usr/local/bin/itom` are required for remote mutation; read-only administration remains available when those mutation prerequisites are unavailable.

The app continues the recovery heartbeat while the admin screen is closed. Each heartbeat echoes a fresh one-use challenge received from the server, proving both directions of the IMAP connection rather than merely a working upload path.

- After **60 continuous seconds without verified recovery**, the watchdog restores the previous configuration and restarts the service.
- After **60 continuous seconds of verified healthy connectivity**, it commits the trial automatically.
- Brief recovery gaps delay confirmation; an unstable trial cannot run longer than fifteen minutes.
- The trial can also be reverted immediately from its status card.
- Only one mutation is pending at a time. Another admin action or CLI configuration edit waits until that trial finishes.

Keep the VPN connected during the trial. Stopping the server intentionally interrupts the admin connection, so it will start again if recovery is not verified within sixty seconds. Disabling, removing or renaming the current `admin` identity can similarly remove the connection's authority and cause automatic recovery.

The private database stores the rollback snapshot and phase before configuration changes are published. A restarted watchdog resumes the existing transaction instead of applying it twice. Server startup re-arms a pending watchdog after a host reboot. A service reinstall trial also retains the original unit representation and boot-enablement link. Restoration is written durably before the trial is closed.

Rollback keeps notice revisions monotonic and fences any newly published client/account identities. It never rewinds wallet, message, usage or physical quota journals, and never deletes mail to manufacture recovery. Completed transactions retain their idempotency identity while dropping the plaintext plan and rollback snapshot. Stale forms are rejected rather than overwriting a newer configuration.

Wait for a trial to finish before replacing the server executable. The CLI installer and uninstaller refuse to run while a trial is active.

## Validation scope

Internal tests cover role and key forgery, ordinary applications using an admin VPN, replayed challenges, stale forms, watchdog startup failure, concurrent configuration edits, exact loss timing, healthy commit, rollback identity/notice invariants, process-restart recovery, original service/boot-link restoration and blocked irreversible commands. Native tests send actual encrypted commands through real TLS IMAP mailboxes and verify background heartbeat recovery across client reconnects and actual server-supervisor restarts; the release run also checks this under the Go race detector.

The workspace has no physical Android device or live systemd server. Those environments remain field validation requirements; successful internal tests do not claim a live-provider or physical-phone qualification.
