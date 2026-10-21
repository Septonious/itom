# Current server-path comparison

Primary server network RTT is20ms, configurable via `ITOM_LOAD_SERVER_RTT_MS`. Client5/40/180ms and varying40–180ms use independent serialization clocks. Provider command processing is a distinct input, not folded into propagation. Runtime controller timings remain measured.

The new workflow reruns matched RC2/DEV3 idle/light, fresh and retained SINGLE/MULTI, variable mobile, full-flight bulk-to-quiet and worst4G. It preserves the100Mbps/tail750ms qualification thresholds, original deadlines, socket identity and all failures. Results are pending; earlier2ms measurements are not relabeled.

Native production bytes and APK identity are unchanged by this test-only correction. ExistingDEV3 artifacts remain experimental, with their original build evidence.
