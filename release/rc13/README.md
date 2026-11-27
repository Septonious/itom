# ITOM 1.5.1 RC13 development checkpoint

Base RC12: 7d100c90ce978aa60b8c897884b8398a72166645
Expected source tree: d50186280e5840c6730155d7db849c34f53586c2
Patch SHA-256: ce9f64739ced2d1e90dcebbcabef245089b081b74df106edcd1d5def66b4cff1

RC13 uses a five-second RTT decision window and three-second emergency interval, keeps the four-account operational floor, separates physical and serving socket telemetry, drains stalled mobile bulk delivery, bounds optional recovery, removes the Home profile card, and prefers tunneled IPv4 exit identification. The mandatory build is separate from performance qualification. No mobile latency acceptance gate. Local focused core/mailstream race tests and core suite (excluding native-process integration) passed; Android/native and real provider/cellular behavior are unverified. Manager includes revision, initialization guidance, account validation, purge diagnostics and service status. Bulk import, persistent submenus and pre-install version comparison remain outstanding; S2C-01 origin is unconfirmed and it is not blindly protected. Password input stays visible.
