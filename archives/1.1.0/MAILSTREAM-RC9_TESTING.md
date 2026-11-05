# ITOM 1.1.0 MAILSTREAM-RC9 — regression recovery candidate

Update both endpoints. Upload the matched server `.bin` to `/tmp` and use manager
option 18; install the signed APK in place preserving app data and profile.

RC8 field captures show large queued bursts, window collapse and retransmissions,
plus cleanup stuck in maintenance_expunge. RC9 restores RC7's measured-rate
controller while retaining deadline-bounded stream admission. The artificial
RC8 infinite-capacity slow-receipt growth test is retired as release evidence;
retained history and actual provider reply ordering are now tested explicitly.

A 300-message EXPUNGE reply burst can fill the client's bounded response channel.
Previously, closing its socket could leave the decoder blocked on that channel,
preventing cleanup and shutdown from joining. RC9 cancels streaming delivery on
close/deadline and completes UID STORE before starting EXPUNGE. Manager service
commands have a 20-second bound and the installed unit a 10-second stop fallback.
Normal shutdown should complete promptly; a failed command is reported as an
error and does not pretend to reset a live ledger.

Automatic cleanup remains limited to owned messages, preserves unrelated mail
and never deletes folders. Empty folders intentionally remain. Leave the server
running after VPN disconnect for idle cleanup. No further manual mailbox cleaning
is part of this update. This is a testing RC; live-device/provider confirmation
is still required.
