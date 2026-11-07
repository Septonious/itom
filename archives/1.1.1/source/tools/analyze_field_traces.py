"""Read ITOM diagnostic/server ZIPs without unpacking private payloads.

Example: python tools/analyze_field_traces.py <archive1.zip> <archive2.zip> --output field.json
Counts and byte totals are trace-window observations, NOT Speedtest goodput.
"""
import argparse
import collections
import json
import statistics
import zipfile
from pathlib import Path


def summarize(path):
    events = collections.Counter()
    members = collections.Counter()
    batches = collections.Counter()
    health = []
    start = end = None
    version = None
    with zipfile.ZipFile(path) as archive:
        if "diagnostics.json" in archive.namelist():
            data = json.loads(archive.read("diagnostics.json"))
            version = data.get("versionName") or data.get("version")
        if "capture.json" in archive.namelist():
            data = json.loads(archive.read("capture.json"))
            version = data.get("revision") or version
        for name in archive.namelist():
            if not (name.endswith("transport-trace.jsonl") or ".jsonl." in name):
                continue
            with archive.open(name) as reader:
                for line in reader:
                    try:
                        entry = json.loads(line)
                    except (ValueError, UnicodeError):
                        continue
                    stamp = entry.get("time", "")
                    start = min(start, stamp) if start else stamp
                    end = max(end, stamp) if end else stamp
                    event = entry.get("event", "")
                    events[event] += 1
                    details = entry.get("details") or {}
                    if event == "data_batch_append" and entry.get("code") == "OK":
                        # Account for both MIME DATA service and overhead.
                        enc = details.get("encoded_bytes", 0)
                        if isinstance(enc, int):
                            label = "bulk" if details.get("records", 0) > 1 or enc > 8192 else "short"
                            batches[label] += 1
                            batches[label + "_encoded_bytes"] += enc
                    elif event == "data_writer_member":
                        n = entry.get("bytes", 0)
                        if isinstance(n, int):
                            members["payload_bytes_in_logged_members"] += n
                    elif event == "health":
                        gov = ((details.get("Line") or {}).get("LatencyGovernor") or {})
                        if gov:
                            health.append(gov)
    def dist(key):
        samples = sorted(x[key] for x in health if isinstance(x.get(key), (float, int)))
        if not samples:
            return None
        return {"p50": samples[len(samples)//2], "p95": samples[int(.95*(len(samples)-1))], "max": samples[-1]}
    return {"archive": Path(path).name, "revision": version, "trace_start_utc": start,
            "trace_end_utc": end, "events": sum(events.values()),
            "event_counts": {k: events[k] for k in ("data_batch_append", "delivery_admission_wait", "socket_failure", "reader_wake_to_fetch", "imap_end")},
            "data_batches": dict(batches), "members": dict(members), "observed_delivery_age_ms": dist("ObservedRTTMS"),
            "fast_ack_age_ms": dist("LastFastACKAgeMS"), "delivery_window_bytes": dist("DownloadWindowBytes"),
            "useful_progress_rate_mbps": dist("RateMbps")}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("archives", nargs="+", type=Path)
    parser.add_argument("--output", type=Path)
    a = parser.parse_args()
    result = [summarize(path) for path in a.archives]
    output = json.dumps(result, ensure_ascii=False, indent=2)
    if a.output:
        a.output.write_text(output + "\n", encoding="utf-8")
    else:
        print(output)


if __name__ == "__main__":
    main()
