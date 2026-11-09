#!/usr/bin/env python3
"""RC1 synthetic RTT acceptance: preserve tails and the high-capacity 200Mbps floor.

Synthetic results are screening data, not field certification. Rates are
verified application bytes over the complete phase; RTT is actual TLS echo
through the transport, not the configured IMAP/ICMP RTT.
"""
import argparse
import json
from pathlib import Path


def gate(root):
    root = Path(root)
    manifest = json.loads((root / "manifest.json").read_text())
    report = json.loads((root / "SYNTHETIC_RTT_REPORT.json").read_text())
    baseline, candidate = report["results"]
    profile = manifest.get("rc116_latency_profile")
    if profile not in ("home", "home-icmp", "mobile"):
        raise ValueError("expected named 1.1.6 latency profile")
    failures = []
    for name, item in (("1.1.5", baseline), ("1.1.6-rc1", candidate)):
        if not item["completed"]:
            failures.append(name + " did not complete")
        for phase in ("idle", "loaded", "after"):
            if item["rtt"][phase]["count"] < (20 if phase != "loaded" else 6):
                failures.append(name + " lacks measured " + phase + " RTT samples")
    if profile in ("home", "home-icmp"):
        for direction in ("upload", "download"):
            rate = candidate["throughput_mbps"].get(direction, 0)
            if rate < 200:
                failures.append(f"{profile} {direction} throughput {rate:.2f} Mbps below 200 Mbps floor")
    print(f"\nRC1 ACTUAL SYNTHETIC RTT GATES profile={profile}", flush=True)
    for phase in ("idle", "loaded", "after"):
        x, y = baseline["rtt"][phase], candidate["rtt"][phase]
        for stat, fraction, minimum in (("median_ms", .12, 12), ("p95_ms", .15 if phase != "loaded" else .25, 20 if phase != "loaded" else 100)):
            ref, cand = x[stat], y[stat]
            if ref is None or cand is None:
                failures.append(f"{phase} {stat} missing")
                continue
            tolerance = max(minimum, fraction * ref)
            delta = cand - ref
            label = "PASS" if delta <= tolerance else "REGRESSION"
            print(f" {phase:<6} {stat:<10} 1.1.5={ref:>8.3f}ms RC1={cand:>8.3f}ms delta={delta:>+8.3f}ms limit=+{tolerance:.1f}ms {label}", flush=True)
            if delta > tolerance:
                failures.append(f"{profile}: {phase} {stat}: +{delta:.3f} ms > +{tolerance:.3f} ms")
    if failures:
        for issue in failures:
            print("REJECT: " + issue, flush=True)
        raise RuntimeError("RC1 synthetic regression gate failed")
    print("PASS: application RTT tail screen and high-capacity speed floor", flush=True)


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("folders", nargs="+", type=Path)
    args = parser.parse_args()
    for folder in args.folders:
        gate(folder)


if __name__ == "__main__":
    main()
