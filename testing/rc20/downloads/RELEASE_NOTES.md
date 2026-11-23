ITOM 1.5.0 RC20 is a field-test prerelease. Update the Linux server first, then install the signed Android build over the existing app.

RC20 uses RC8's field-tested transport behavior as the recovery reference. It keeps bulk delivery from slowing itself down while the mailbox provider delays feedback. TCP setup can interrupt a bulk batch, and paced small writes retain a bounded amount of credit through a brief pause. Global Android routing and bundled GeoSite and GeoIP are included.

The Go tests reproduce RC19's upload collapse. The final five seconds of the trace-driven TLS replay rose from 11.6 to 202.6 Mbps. The separate eight-account 500/500 Mbps home model delivered 296.5/307.6 Mbps in short runs and 345.8/343.7 Mbps in sustained runs.

Two standalone burst runs stayed below 112 ms across the 100 kbps to 5 Mbps workloads. The combined qualification run still recorded a 416.1 ms application peak. The hard RTT qualification remains open. Sustained per-account control peaks were 223.1 ms down and 223.7 ms up. The separate receipt-burst and synchronous-literal light tests recorded application outliers above 200 ms. Physical Wi-Fi and cellular validation remains open.
