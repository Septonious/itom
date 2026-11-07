ITOM 1.1.3 testing files

Source: d7a1b4c0d74aa77e9e5379c5c79b742b20ae20a3
This is a testing package, not a final release.

The APK retains org.itom.client, version 1.1.3, code 10148. The user authorized
a new signing key and reinstall. Export/save the profile before reinstalling.
The signing certificate SHA-256 is:
0fdd51e00489c93b13ed5ce35e13257a9c8964f2f93b1660e5e542cfbf6fb4f0

Use ITOM-1.1.3-linux-amd64.bin for ordinary x86-64 Linux servers, or the arm64
binary for ARM64 servers. Upload it into /tmp and use server-manager option 18.
There is no need to purge mailboxes or reset account capacity counters.

102 Android unit tests and fatal lint pass. Both JNI libraries and all APK
contents match the CI build after signing. Both APK signature schemes verify.
18 of 19 build/regression jobs pass; independent application acceptance passes.
The remaining gate is retained-debt throughput: 87.6 Mbps cold and 118.2 Mbps on
the next upload, below the existing 90/120 floors. Later uploads reached 139.5
and 146.5 Mbps. Generated models are not a live mobile speed or RTT guarantee.

The signing-key backup is encrypted with AES-256-GCM. Its recovery key was
provided privately to the owner, and is not present in this release or repo.
To restore it, install Python's cryptography package, then run:
python decrypt-signing-backup.py ITOM-signing-key-backup.encrypted.json
Enter the private recovery key when prompted. Keep the restored ZIP private.
