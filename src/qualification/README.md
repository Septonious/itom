# ITOM 1.5.1 transport qualification

Run on Linux with Go 1.27.1 and Python 3.10 or later. Each trial creates a local TLS IMAP provider, a real server, a native Go client attached to a userspace TCP/IP stack, a TLS echo service, and an HTTPS transfer origin. Test identities, certificates and mailbox contents are generated for that trial. The only destination exception in the worker permits its two local application services.

```sh
python3 src/qualification/run_home.py /tmp/itom-home-screen --screen
python3 src/qualification/run_home.py /tmp/itom-home-matrix
```

Use a fresh output directory for each invocation. The screen runs the short LITERAL- case. The matrix runs 15 and 60 seconds per transfer direction in both IMAP literal modes, five independent times. It stops at the first failing trial and retains its measurements in the output directory. `gate.json` records both binary hashes and the decision. A provider-service holdout follows the full matrix. Run the gate logic unit tests with `python3 -m unittest test_home_gate` in this directory.

The provider provisions 15 independent mailboxes. All 15 are configured in ITOM, and the production handshake must negotiate an eight-account grant, with four sockets at each endpoint per active mailbox. The remaining seven keep server control readers warm and may briefly open one client bootstrap reader; no unallocated client DATA or writer may remain active. The client link has independent 500 Mbps upstream and downstream serialization, shared across its sockets. Client-to-provider RTT varies between 15 and 30 ms. The server link uses a shared 1 Gbps serialization queue and 20–40 ms RTT. These are model inputs. The fixture does not measure a telephone, carrier or public mailbox service.

Two cycles exercise 100 kbps, 500 kbps, 2 Mbps and 5 Mbps of application payload in each direction. Each burst opens one, four or eight TCP streams, checks every echoed byte, closes those streams and returns to three seconds with zero workload traffic. Application and account probes continue during each transition. Short and sustained speed tests then download and upload checked bytes over eight HTTPS streams.

The home release gate requires all of the following.

- Fresh replies on each of the eight named mailboxes in every phase. Each probe starts before admission and requires an authenticated echo on the same account. Timeouts fail.
- At most 200 ms for every application and same-account RTT. The idle and light-load 95th percentile target is 150 ms for both.
- Strictly greater than 200 Mbps of receiver-verified application payload in each direction over the whole phase, including ramp and drain. Every complete sliding ten-second interval after the first five seconds must also exceed 200 Mbps.
- Successful native TCP transfers, bidirectional bytes on all eight accounts and zero unexpected provider logins.

The fixed grant isolates transport performance. Dynamic account allocation, Android routing, upgrade compatibility and physical-device field results require separate verification before distribution.

An additional `python3 src/qualification/run_home.py /tmp/itom-relay-screen --screen --encrypted-exit` run exercises the real TLS 1.3 exit and the native TCP frontend with a fresh 15-to-8 grant. The relay is local; this tests correctness and goodput, not the actual inter-VPS WAN latency. The ordinary screen remains independent.
