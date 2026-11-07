# ITOM 1.1.1

Final Android VPN and Linux server using encrypted IMAP TCP transport. The
accepted1.1.0controller and control polling are retained. Startup uses scoped
LIST before opening the two control folders, avoiding missing-folder SELECT or
STATUS stalls and redundant CREATE on existing folders. Large DATA collection
uses bounded pages from its first mutation to avoid cleanup timeouts disrupting
repeated transfers. Diagnostic rates, account names and connection counts are
corrected. Control folders have a separate message-count guard (2,048 ordinary
reservations plus 128 recovery), including queued and uncertain writes. Startup
loads the remote count; restarting cannot bypass this guard. Ordinary traffic
pauses if collection cannot keep up. Quiet control tails are collected at 32
messages; DATA folders retain their 128-message bound. Keep the server running
for background cleanup. Historical design notes retain their original RC headings; the
release identity is defined by source-info.json.

Install the signed ITOM-1.1.1.apk in place (org.itom.client, version code 10146).
For an existing server, upload the matching ITOM-1.1.1-linux-amd64.bin or arm64
binary into /tmp and choose server-manager option 18. Both endpoints should be
updated; no manual mailbox cleanup or profile re-export is required. For a new
server, make the matching binary executable and run it with the install command.

The manager provides service lifecycle and updates, account/client management,
Android profile export, IMAP checks and purge, logging, packet/traffic traces,
full diagnostics, notices and uninstall. The app provides VPN and SOCKS access,
profiles, routing/per-app/network policies, DNS caching, authorized server
administration, notices and diagnostics. Non-DNS UDP transport is deferred.

Build sources with Python 3.10+: python3 ITOM-1.1.1-sources.zip android, server,
or all. The launcher obtains pinned toolchains into its cache, runs normal/race
and Android release tests and fatal lint, then verifies native/application
identity. The Android final target uses the release variant with debugging and
shrinking disabled. To sign locally, supply ITOM_ANDROID_SIGNING_STORE_FILE,
ITOM_ANDROID_STORE_PASSWORD, ITOM_ANDROID_KEY_ALIAS and ITOM_ANDROID_KEY_PASSWORD.
Without these, a local release APK is unsigned and must be signed before install.
Published downloads are signed with the established ITOM certificate; custom
keys cannot replace an existing installation signed with that certificate.

Profiles, credential storage, finite capacity and ownership-audited cleanup are
preserved. No speculative RTT tuning or new transport feature is included.
Speed and latency depend on network and provider behavior; sub-100 ms RTT and
UDP gaming compatibility are not claimed. Phone-specific permissions and vendor
battery management require verification on the actual phone.
