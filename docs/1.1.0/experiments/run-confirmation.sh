#!/bin/bash
set -uo pipefail
root=/workspace/work/v1-reset
# Compile has finished. Keep all measurement runs serial and preserve failures.
for trial in 1 2 3; do
 if test "$trial" = 2; then order='micro control'; else order='control micro'; fi
 for variant in $order; do
  ITOM_LOAD_CLIENT_RTT_MS=40 ITOM_EXPERIMENT_LABEL="$variant" "$root/$variant.test" -test.run '^TestV110IdleLightReference$' -test.v -test.timeout 150s > "$root/confirm$trial-$variant-rtt40.log" 2>&1 || echo "failed confirmation=$trial variant=$variant"
  echo "completed confirmation=$trial variant=$variant"
 done
done
for variant in control micro; do
 ITOM_HEALTHY_SERVER=1 ITOM_LOAD_COMMAND_DELAY_MS=0 ITOM_LOAD_SERVER_RTT_MS=2 ITOM_LOAD_SERVER_MBPS=1000 ITOM_LOAD_CLIENT_RTT_MS=40 ITOM_LOAD_CLIENT_MBPS=500 ITOM_LOAD_LITERAL_PLUS=1 ITOM_LOAD_BINARY_FETCH=1 ITOM_LOAD_ACCOUNTS=10 ITOM_LOAD_STREAMS=4 ITOM_LOAD_SECONDS=60 ITOM_LOAD_WARMUP_SECONDS=2 ITOM_LOAD_REPEATS=2 ITOM_LOAD_PROBE_CADENCE_MS=2000 ITOM_LOAD_PROBE_DEADLINE_MS=2000 ITOM_REWORK_QUALIFICATION=1 ITOM_RC8_RELEASE_GATE=1 ITOM_LOAD_MIN_MBPS=100 "$root/$variant.test" -test.run '^TestTransportReworkQualification$' -test.v -test.timeout 420s > "$root/bulk-$variant.log" 2>&1 || echo "failed bulk=$variant"
 echo "completed bulk=$variant"
done
