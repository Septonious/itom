#!/usr/bin/env python3
"""Print and retain actual synthetic application echo RTT from each fixture run.

The data are end-to-end HTTPS/TCP echo measurements taken inside the fixture,
not a restatement of provider RTT configuration or a live Rambler measurement.
"""
import argparse
import csv
import json
import math
from pathlib import Path
import statistics


def latency_stats(samples):
    clean = [float(x) for x in samples]
    if any(not math.isfinite(x) or x < 0 for x in clean):
        raise ValueError("invalid application RTT sample")
    if not clean:
        return {"count": 0, "minimum_ms": None, "median_ms": None,
                "p95_ms": None, "p99_ms": None, "maximum_ms": None}
    values = sorted(clean)
    def rounded(value):
        return round(value, 3)
    def nearest_rank(p):
        return rounded(values[max(0, math.ceil(p * len(values)) - 1)])
    return {"count": len(values), "minimum_ms": rounded(values[0]),
            "median_ms": rounded(statistics.median(values)),
            "p95_ms": nearest_rank(.95), "p99_ms": nearest_rank(.99),
            "maximum_ms": rounded(values[-1])}


def build_report(directory, print_report=True):
    directory = Path(directory)
    manifest = json.loads((directory / "manifest.json").read_text())
    profile = manifest.get("rc116_latency_profile") or manifest.get("latency_model", "historical")
    baseline_version = "1.1.5" if manifest.get("baseline_is_mailstream") else "1.0.0-rc2"
    records = []
    for label in ("rc2", "candidate"):
        result_path = directory / (label + ".json")
        if not result_path.exists():
            records.append({"profile": profile, "version": baseline_version if label == "rc2" else "1.1.6-rc1",
                            "completed": False, "error": "result file missing", "rtt": {}})
            continue
        data = json.loads(result_path.read_text())
        phases = {}
        for phase in data.get("Phases") or []:
            for direction in ("upload", "download"):
                value = phase.get(direction)
                if isinstance(value, dict) and phase.get("name") in (direction, "duplex"):
                    phases[direction] = round(float(value.get("full_phase_mbps", 0)), 3)
        record = {"profile": profile, "version": baseline_version if label == "rc2" else "1.1.6-rc1",
                  "completed": data.get("success") is True and data.get("stage") == "complete",
                  "error": data.get("error"),
                  "rtt": {"idle": latency_stats(data.get("IdleMS") or []),
                          "loaded": latency_stats(data.get("LoadedMS") or []),
                          "after": latency_stats(data.get("AfterMS") or [])},
                  "throughput_mbps": phases}
        records.append(record)
    (directory / "SYNTHETIC_RTT_REPORT.json").write_text(
        json.dumps({"network_profile": profile,
                    "meaning": "measured application TLS echo RTT, not simulated RTT input or live network latency",
                    "configured_client_network_rtt_ms": manifest.get("rc116_client_imap_latency_ms"),
                    "observed_home_icmp_reference_ms": manifest.get("observed_home_icmp_reference_ms"),
                    "results": records}, indent=2) + "\n")
    header = ("Profile", "Version", "Phase", "Samples", "Min ms", "Median ms",
              "p95 ms", "p99 ms", "Max ms", "Upload Mbps", "Download Mbps", "Status")
    rows = []
    for rec in records:
        if not rec["rtt"]:
            rows.append((profile, rec["version"], "unavailable", 0, "N/A", "N/A", "N/A", "N/A", "N/A", "N/A", "N/A", "FAILED"))
            continue
        for name in ("idle", "loaded", "after"):
            st = rec["rtt"][name]
            def v(key):
                value = st[key]
                return "N/A" if value is None else str(value)
            rates = rec["throughput_mbps"]
            rows.append((profile, rec["version"], name, st["count"], v("minimum_ms"),
                         v("median_ms"), v("p95_ms"), v("p99_ms"), v("maximum_ms"),
                         rates.get("upload", "N/A"), rates.get("download", "N/A"),
                         "PASS" if rec["completed"] and st["count"] else "INCOMPLETE"))
    with (directory / "SYNTHETIC_RTT_REPORT.csv").open("w", newline="") as handle:
        writer = csv.writer(handle)
        writer.writerow(header)
        writer.writerows(rows)
    lines = ["# Measured synthetic application RTT — " + str(profile),
             "",
             "Actual TLS echo round trips through ITOM, not a configured IMAP delay, ICMP ping or field measurement.",
             "Percentiles use nearest rank; 10 observations are not statistically sufficient to establish field p95.",
             "",
             "| " + " | ".join(header) + " |",
             "|" + "|".join(["---"] * len(header)) + "|"]
    for row in rows:
        lines.append("| " + " | ".join(map(str, row)) + " |")
    (directory / "SYNTHETIC_RTT_REPORT.md").write_text("\n".join(lines) + "\n")
    if print_report:
        print("\n" + "\n".join(lines) + "\n", flush=True)
    return records


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("directory", type=Path)
    args = parser.parse_args()
    build_report(args.directory)


if __name__ == "__main__":
    main()
