#!/usr/bin/env python3
"""Fail closed on *any measured* RC1 -> RC2 RTT increase, or >25% goodput loss.

This compares fully completed paired, measured TLS application-echo runs, not
configured network RTT. It is a conservative screening gate, not a proof that
real mobile conditions can never worsen. It intentionally has ZERO positive
RTT tolerance; if runner jitter produces a positive result, investigate and
rerun paired trials rather than silently waiving it.
"""
import argparse
import json
import math
from pathlib import Path

PHASES = ("idle", "loaded", "after")
RTT_FIELDS = ("minimum_ms", "median_ms", "p95_ms", "p99_ms", "maximum_ms")
DIRECTIONS = ("upload", "download")
MIN_THROUGHPUT_RATIO = 0.75


def load_pair(folder):
    report = json.loads((Path(folder) / "SYNTHETIC_RTT_REPORT.json").read_text())
    older, newer = report["results"]
    if (older.get("version"), newer.get("version")) != ("1.1.6-rc1", "1.1.7-rc2"):
        raise ValueError(f"{folder}: expected immutable released RC1 vs 1.1.7 RC2, "
                         f"got {older.get('version')} vs {newer.get('version')}")
    if not older["completed"] or not newer["completed"]:
        raise ValueError(f"{folder}: incomplete A/B trial")
    return older, newer


def check(folders, profile):
    if len(folders) < 3:
        raise ValueError("require at least three independent paired A/B trials")
    pairs = [load_pair(folder) for folder in folders]
    issues = []
    for index, (baseline, candidate) in enumerate(pairs, 1):
        for phase in PHASES:
            before, after = baseline["rtt"][phase], candidate["rtt"][phase]
            if before["count"] < (6 if phase == "loaded" else 20):
                issues.append(f"trial {index}: insufficient RC1 {phase} samples")
            if after["count"] < (6 if phase == "loaded" else 20):
                issues.append(f"trial {index}: insufficient RC2 {phase} samples")
            for metric in RTT_FIELDS:
                first, second = before[metric], after[metric]
                if first is None or second is None or not (
                    math.isfinite(first) and math.isfinite(second)
                ):
                    issues.append(f"trial {index}: missing/nonfinite {phase} {metric}")
                    continue
                delta = second - first
                print(f"{profile}: trial={index} {phase} {metric}: "
                      f"RC1={first:.3f}ms RC2={second:.3f}ms delta={delta:+.3f}ms",
                      flush=True)
                if delta > 0:
                    issues.append(f"trial {index}: {phase} {metric} +{delta:.3f}ms")
        for direction in DIRECTIONS:
            first = baseline["throughput_mbps"].get(direction)
            second = candidate["throughput_mbps"].get(direction)
            if first is None or second is None or not all(
                math.isfinite(v) and v > 0 for v in (first, second)
            ):
                issues.append(f"trial {index}: invalid {direction} throughput")
                continue
            ratio = second / first
            print(f"{profile}: trial={index} {direction}: "
                  f"RC1={first:.3f}Mbps RC2={second:.3f}Mbps ratio={ratio:.3f}",
                  flush=True)
            if ratio < MIN_THROUGHPUT_RATIO:
                issues.append(f"trial {index}: {direction} lost "
                              f"{100*(1-ratio):.2f}% (>25%)")
    for direction in DIRECTIONS:
        old_max = max(p[0]["throughput_mbps"].get(direction, 0) for p in pairs)
        new_max = max(p[1]["throughput_mbps"].get(direction, 0) for p in pairs)
        if old_max <= 0 or new_max / old_max < MIN_THROUGHPUT_RATIO:
            issues.append(f"maximum {direction} throughput fell below 75% of RC1")
    if issues:
        for item in issues:
            print("REJECT: " + item, flush=True)
        raise RuntimeError(f"{profile}: RC2 fails zero-positive-RTT/75%-throughput gates")
    print(f"PASS: {profile}: no positive RTT deltas in measured A/B "
          f"trials; throughput >=75% of RC1", flush=True)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--profile", required=True)
    parser.add_argument("folders", nargs="+", type=Path)
    args = parser.parse_args()
    check(args.folders, args.profile)


if __name__ == "__main__":
    main()
