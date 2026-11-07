# ITOM 1.1.4 testing

Four actual lines per account: control reader, control writer, DATA reader and
DATA writer. Fifteen accounts therefore provide 60 lines. Both endpoints
bootstrap on one control socket, then open their three additional sockets only
after authenticated admission. The same APPEND actor drains both writers before
server-owned collection; timeouts preserve all unverified mailbox debt.

The transport controller, wire protocol, mailbox byte/message caps and folder
names are unchanged. No migration or purge is added. Existing folders can be
cleaned by the user as planned; software never treats a claimed cleanup as proof.

Two simultaneous users were tested using verified TLS IMAP, authenticated Noise,
HTTPS integrity, independent exit policies and disconnect isolation. Their shared
provider account needs at least sixteen allowed connections. The existing ten
connection default permits one active user and returns authenticated BUSY to the
other. Provider limits are not silently raised.

The supplied 45-line traces show repeated provider EXPUNGE timeouts on older
accounts and retained server mail nearing the bounded capacity during the slower
second download. The next field test should confirm behavior after the planned
folder cleanup. No live-speed guarantee or final release qualification is claimed.

Install the matching APK and server binary. The APK uses the same new signing key
as 1.1.3, so no further reinstall is needed. Server-manager option 18 accepts the
binary uploaded into /tmp.

Build checks: full core/mailstream/native normal and race suites, native two-user integration, two 15-account native MULTI runs, both Android JNI architectures, release compilation, unit tests and fatal lint. Synthetic throughput is recorded separately and is not a field-speed guarantee.

The independent 15-account native fixture completed both consecutive runs at 330/337 Mbps upload and 337/346 Mbps download, with 60/60 real lines and no provider connection refusals. These are controlled-fixture results, not a prediction for the live mail provider.
