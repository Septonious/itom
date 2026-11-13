#!/usr/bin/env python3
"""Offline ITOM field-trace comparison. No network, credentials or payload output.

Supports Android "Export full diagnostics" and Linux manager "Export diagnostics"
ZIPs from 1.1.5/1.1.6 and older compatible JSONL schemas.

Important: periodic snapshots reuse the most recent RTT; p50/p95 values here are
SNAPSHOT-WEIGHTED observations, not distributions of independent packet probes.
Never interpret stale RTT as a failed lane or an 8ms IMAP command as tunnel RTT.
"""
from __future__ import annotations

import argparse
from collections import Counter, defaultdict
import hashlib
import json
import math
from pathlib import Path
import sys
import zipfile

MAX_MEMBER_BYTES = 24 * 1024 * 1024
MAX_RECORDS_PER_MEMBER = 150_000


def number(value):
    if isinstance(value, bool) or value is None:
        return None
    try:
        result = float(value)
    except (TypeError, ValueError):
        return None
    return result if math.isfinite(result) else None


def flag(value):
    if isinstance(value, bool):
        return value
    if isinstance(value, str):
        if value.lower() == "true":
            return True
        if value.lower() == "false":
            return False
    return None


def percentile(values, percent):
    if not values:
        return None
    values = sorted(values)
    index = max(0, math.ceil(len(values) * percent / 100) - 1)
    return round(values[index], 1)


def stats(values):
    return {
        "observations": len(values),
        "min": percentile(values, 0),
        "p50": percentile(values, 50),
        "p95": percentile(values, 95),
        "p99": percentile(values, 99),
        "max": percentile(values, 100),
    }


def alias(identity):
    # Account addresses/IDs are never written into reports.
    return "acct_" + hashlib.sha256(identity.encode("utf-8")).hexdigest()[:10]


def pick(record, *keys):
    if not isinstance(record, dict):
        return None
    for key in keys:
        if key in record:
            return record[key]
    return None


def field_number(record, *keys):
    return number(pick(record, *keys))


def new_account():
    return {
        "fresh_rtt": [], "imap_latency": [], "append_ms": [],
        "queue_bytes": [], "upload_bps": [], "download_bps": [],
        "fresh": 0, "stale": 0, "unknown_freshness": 0,
        "rtt_over_150": 0, "rtt_over_300": 0,
        "not_ready": 0, "writer_recovering": 0,
        "control_backpressure": 0, "peer_unverified": 0,
        "max_writer_reconnects": 0,
        "stale_start": None, "stale_previous": None,
        "stale_episodes_ms": [], "timestamps": [],
    }


class Analysis:
    def __init__(self):
        self.accounts = defaultdict(new_account)
        self.events = Counter()
        self.records = Counter()
        self.global_display_rtt = []
        self.confirmed_probes = []
        self.probe_failures = Counter()
        self.session = defaultdict(list)
        self.revisions = Counter()
        self.builds = Counter()
        self.first_ms = None
        self.last_ms = None
        self.invalid_json = 0

    def close_stale(self, account):
        start, last = account["stale_start"], account["stale_previous"]
        if start is not None and last is not None and last >= start:
            account["stale_episodes_ms"].append(last - start)
        account["stale_start"] = None
        account["stale_previous"] = None

    def add_line(self, line, timestamp):
        if not isinstance(line, dict):
            return
        identity = pick(line, "AccountID", "account", "Account")
        if not isinstance(identity, str) or not identity:
            return
        account = self.accounts[alias(identity)]
        if timestamp is not None:
            account["timestamps"].append(timestamp)
        rtt = field_number(line, "RTTMillis", "rtt_ms")
        fresh = flag(pick(line, "RTTFresh", "rtt_fresh"))
        if fresh is True:
            account["fresh"] += 1
            if rtt is not None and rtt > 0:
                account["fresh_rtt"].append(rtt)
                account["rtt_over_150"] += int(rtt > 150)
                account["rtt_over_300"] += int(rtt > 300)
            self.close_stale(account)
        elif fresh is False:
            account["stale"] += 1
            if timestamp is not None:
                previous = account["stale_previous"]
                # A gap in trace coverage is not evidence of continuous staleness.
                if previous is not None and timestamp - previous > 10_000:
                    self.close_stale(account)
                if account["stale_start"] is None:
                    account["stale_start"] = timestamp
                account["stale_previous"] = timestamp
        else:
            account["unknown_freshness"] += 1
            self.close_stale(account)
        for key, names in (
            ("imap_latency", ("IMAPLatencyMillis", "imap_latency_ms")),
            ("append_ms", ("AppendCompletionMS", "append_completion_ms")),
            ("queue_bytes", ("QueuedMIMEBytes", "queued_mime_bytes")),
            ("upload_bps", ("UpBps",)),
            ("download_bps", ("DownBps",)),
        ):
            value = field_number(line, *names)
            if value is not None and value >= 0:
                account[key].append(value)
        if flag(pick(line, "Ready", "ready")) is False:
            account["not_ready"] += 1
        if flag(pick(line, "WriterRecovering", "writer_recovering")) is True:
            account["writer_recovering"] += 1
        if flag(pick(line, "ControlBackpressure", "control_backpressure")) is True:
            account["control_backpressure"] += 1
        if flag(pick(line, "PeerVerified", "peer_verified")) is False:
            account["peer_unverified"] += 1
        reconnects = field_number(line, "WriterReconnects", "writer_reconnects")
        if reconnects is not None:
            account["max_writer_reconnects"] = max(account["max_writer_reconnects"], int(reconnects))

    def ingest(self, record, origin):
        if not isinstance(record, dict):
            return
        if isinstance(record.get("build"), str):
            self.builds[record["build"]] += 1
        if isinstance(record.get("transport"), dict):
            record = record["transport"]
        event = pick(record, "Event", "event")
        if isinstance(event, str):
            self.events[origin + ":" + event] += 1
        revision = pick(record, "Revision", "revision")
        if isinstance(revision, str):
            self.revisions[revision] += 1
        timestamp = field_number(record, "SampledAtMS", "sampled_at_ms")
        if timestamp is None:
            timestamp = field_number(pick(record, "Client"), "SampledAtMS")
        if timestamp is not None and timestamp > 0:
            timestamp = int(timestamp)
            self.first_ms = timestamp if self.first_ms is None else min(self.first_ms, timestamp)
            self.last_ms = timestamp if self.last_ms is None else max(self.last_ms, timestamp)
        else:
            timestamp = None
        client = pick(record, "Client")
        mail = pick(record, "Mail")
        # Client.Lines duplicates Mail.Lines within one Android snapshot.
        # Prefer client lines, which also expose useful per-account throughput.
        client_lines = pick(client, "Lines")
        mail_lines = pick(mail, "Lines", "lines")
        lines = client_lines if isinstance(client_lines, list) else mail_lines
        if isinstance(lines, list):
            for line in lines:
                self.add_line(line, timestamp)
        if isinstance(client, dict):
            displayed = field_number(client, "RTTMillis")
            if displayed is not None and displayed > 0:
                # This may be an old successful value reused after a failed ping.
                self.global_display_rtt.append(displayed)
        probe = pick(record, "Probe")
        if isinstance(probe, dict):
            failure = pick(probe, "Failure")
            if isinstance(failure, str) and failure:
                self.probe_failures[failure] += 1
            elif field_number(probe, "RTTMillis") is not None:
                self.confirmed_probes.append(field_number(probe, "RTTMillis"))
        session = pick(record, "Session")
        if isinstance(session, dict):
            for key in (
                "InteractiveReceiptRTTMS", "InteractiveReceiptMinRTTMS",
                "BulkSmoothedRTTMS", "BulkSaturatedMinRTTMS",
                "BulkReceiptRTTMS", "DeliveryRTTMS", "BulkPacingDeferrals",
            ):
                value = field_number(session, key)
                if value is not None and value > 0:
                    self.session[key].append(value)
        self.records[origin] += 1

    def report(self):
        for account in self.accounts.values():
            self.close_stale(account)
        rows = {}
        for identity, a in sorted(self.accounts.items()):
            observations = a["fresh"] + a["stale"] + a["unknown_freshness"]
            rows[identity] = {
                "observations": observations,
                "fresh_observations": a["fresh"],
                "stale_observations": a["stale"],
                "stale_percent": round(100 * a["stale"] / observations, 1) if observations else None,
                "longest_observed_stale_run_ms": max(a["stale_episodes_ms"], default=0),
                "fresh_delivery_rtt_ms": stats(a["fresh_rtt"]),
                "fresh_rtt_over_150_percent": round(100 * a["rtt_over_150"] / len(a["fresh_rtt"]), 1) if a["fresh_rtt"] else None,
                "fresh_rtt_over_300_percent": round(100 * a["rtt_over_300"] / len(a["fresh_rtt"]), 1) if a["fresh_rtt"] else None,
                "imap_command_latency_ms": stats(a["imap_latency"]),
                "append_completion_ms": stats(a["append_ms"]),
                "queued_mime_bytes": stats(a["queue_bytes"]),
                "peak_up_bps": max(a["upload_bps"], default=None),
                "peak_down_bps": max(a["download_bps"], default=None),
                "not_ready_observations": a["not_ready"],
                "writer_recovering_observations": a["writer_recovering"],
                "control_backpressure_observations": a["control_backpressure"],
                "peer_unverified_observations": a["peer_unverified"],
                "max_writer_reconnects": a["max_writer_reconnects"],
            }
        return {
            "schema": 1,
            "revisions": dict(self.revisions),
            "android_builds": dict(self.builds),
            "record_counts": dict(self.records),
            "events": dict(self.events),
            "first_timestamp_ms": self.first_ms,
            "last_timestamp_ms": self.last_ms,
            "coverage_seconds": round((self.last_ms - self.first_ms) / 1000, 1) if self.first_ms is not None and self.last_ms is not None else None,
            "invalid_json_records": self.invalid_json,
            "displayed_global_rtt_ms_not_independent_probes": stats(self.global_display_rtt),
            "explicit_probe_snapshot_rtt_ms": stats(self.confirmed_probes),
            "explicit_probe_failures": dict(self.probe_failures),
            "session_snapshot_metrics": {key: stats(value) for key, value in sorted(self.session.items())},
            "accounts": rows,
            "limitations": [
                "Snapshot-weighted RTT quantiles are not independent probe distributions.",
                "Stale per-account RTT means no recent qualifying acknowledged delivery, not necessarily failed connectivity.",
                "IMAP command latency is not the encrypted end-to-end tunnel RTT.",
                "Client displayed RTT may retain the last success after a failed ping.",
                "Client and server clocks are not assumed synchronized; no cross-host subtraction.",
                "Redacted account aliases are stable within reports but not proof of anonymity.",
            ],
        }


def read_archive(path, analysis, origin):
    with zipfile.ZipFile(path) as archive:
        members = [m for m in archive.infolist() if not m.is_dir()]
        def rank(m):
            name = Path(m.filename).name
            if name.endswith(".jsonl.2"):
                return 0
            if name.endswith(".jsonl.1"):
                return 1
            if name.endswith(".jsonl"):
                return 2
            return 3
        for member in sorted(members, key=rank):
            name = Path(member.filename).name
            is_trace = name.startswith(("transport-trace.jsonl", "startup-trace.jsonl"))
            is_snapshot = name in ("diagnostics.json", "capture.json") or (name.startswith("mailstream-") and name.endswith(".json"))
            if not (is_trace or is_snapshot):
                continue
            if member.file_size > MAX_MEMBER_BYTES:
                raise ValueError(f"oversized member in {path.name}: {name}")
            with archive.open(member) as handle:
                if is_trace:
                    for index, raw in enumerate(handle):
                        if index >= MAX_RECORDS_PER_MEMBER:
                            raise ValueError(f"too many records in {path.name}: {name}")
                        try:
                            analysis.ingest(json.loads(raw), origin)
                        except (json.JSONDecodeError, UnicodeDecodeError):
                            analysis.invalid_json += 1
                else:
                    try:
                        analysis.ingest(json.load(handle), origin)
                    except (json.JSONDecodeError, UnicodeDecodeError):
                        analysis.invalid_json += 1


def collect(client, server):
    analysis = Analysis()
    for origin, files in (("client", client), ("server", server)):
        for path in files:
            read_archive(Path(path), analysis, origin)
    return analysis.report()


def compare(before, after):
    result = {}
    for account, current in after["accounts"].items():
        old = before["accounts"].get(account)
        if old is None:
            continue
        delta = {}
        for key, field in (
            ("fresh_delivery_rtt_ms", "p50"),
            ("fresh_delivery_rtt_ms", "p95"),
            ("imap_command_latency_ms", "p95"),
            ("queued_mime_bytes", "p95"),
        ):
            a = current[key][field]
            b = old[key][field]
            if a is not None and b is not None:
                delta[key + "_" + field + "_delta"] = round(a - b, 1)
        if current["stale_percent"] is not None and old["stale_percent"] is not None:
            delta["stale_percentage_points_delta"] = round(current["stale_percent"] - old["stale_percent"], 1)
        result[account] = delta
    return result


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--client", type=Path, action="append", default=[], help="current Android diagnostic ZIP (repeatable)")
    parser.add_argument("--server", type=Path, action="append", default=[], help="current server trace ZIP (repeatable)")
    parser.add_argument("--baseline-client", type=Path, action="append", default=[], help="older Android diagnostic ZIP")
    parser.add_argument("--baseline-server", type=Path, action="append", default=[], help="older server trace ZIP")
    parser.add_argument("--output", type=Path, help="write sanitized JSON summary to this path")
    args = parser.parse_args()
    if not args.client and not args.server:
        parser.error("supply at least --client or --server")
    current = collect(args.client, args.server)
    result = {"current": current}
    if args.baseline_client or args.baseline_server:
        baseline = collect(args.baseline_client, args.baseline_server)
        result["baseline"] = baseline
        result["per_account_delta"] = compare(baseline, current)
    rendered = json.dumps(result, indent=2, sort_keys=True)
    if args.output:
        args.output.write_text(rendered + "\n", encoding="utf-8")
        print(f"Summary written to {args.output}; no credentials or payload bytes exported.")
    else:
        print(rendered)


if __name__ == "__main__":
    try:
        main()
    except (OSError, ValueError, zipfile.BadZipFile) as exc:
        print(f"ITOM trace analysis failed: {exc}", file=sys.stderr)
        sys.exit(1)
