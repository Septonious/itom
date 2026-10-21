#!/usr/bin/env python3
"""Collect every paired log; additional one-way cases never count as duplex gates."""
import argparse
import hashlib
import json
import re
import statistics
from pathlib import Path


def fields(line, label):
    return dict(re.findall(r"([A-Za-z0-9_]+)=([^ ]+)", line.split(label, 1)[1]))


def collect(root):
    logs = {}
    for path in sorted(root.rglob("*.log")):
        text = path.read_text()
        headers = [int(x) for x in re.findall(r"healthy_reference server_rtt_ms=(\d+)", text)]
        record = {"sha256": hashlib.sha256(path.read_bytes()).hexdigest(),
                  "passed": bool(re.search(r"^--- PASS: Test", text, re.M)) and not bool(re.search(r"^--- FAIL:|^panic:", text, re.M)),
                  "server_rtt_headers_ms": headers, "rounds": [], "acceptance": [],
                  "tails": [], "identities": [], "latency": [], "directional": [],
                  "directional_tails": [], "transitions": [], "bulk": [], "worst4g": [], "failures": []}
        for line in text.splitlines():
            for label, key in [("supervised_result ", "rounds"), ("supervised_acceptance ", "acceptance"),
                               ("supervised_tail ", "tails"), ("supervised_tail10 ", "tails"),
                               ("supervised_identity ", "identities"), ("directional_result ", "directional"),
                               ("directional_tail ", "directional_tails"), ("model_transition_bulk ", "bulk"), ("worst_raw4g ", "worst4g")]:
                if label in line:
                    record[key].append(fields(line, label))
            if "MODEL_TRANSITION " in line:
                record["transitions"].append(json.loads(line.split("MODEL_TRANSITION ", 1)[1]))
            if "V110_RESULT " in line:
                record["latency"].append(json.loads(line.split("V110_RESULT ", 1)[1]))
            if any(x in line for x in ["FAIL:", "stalled stability window", "gate failed", "performance/cadence gate", "i/o timeout", "panic:"]):
                record["failures"].append(line.strip())
        if not headers or set(headers) != {20}:
            raise ValueError(f"Unexpected or missing server path: {path}: {headers}")
        logs[str(path.relative_to(root))] = record
    return logs


def describe(logs):
    for case in sorted({name.split("/")[0] for name in logs}):
        print(case)
        for variant in ("rc2", "model"):
            values = [r for n, r in logs.items() if n.split("/")[0] == case and n.endswith("-" + variant + ".log")]
            gates = [g for r in values for g in r["acceptance"]]
            rounds = [g for r in values for g in r["rounds"]]
            directional = [g for r in values for g in r["directional"]]
            print(" ", variant, "invocations", sum(r["passed"] for r in values), "/", len(values),
                  "duplex gates", sum(g["accepted"] == "true" for g in gates), "/", len(gates))
            if rounds:
                print("  mean up/down", *[round(statistics.mean(float(r[k]) for r in rounds), 3) for k in ("upload_mbps", "download_mbps")])
            if directional:
                print("  directional mean/min-tail", *[round(statistics.mean(float(r[k]) for r in directional), 3) for k in ("useful_mbps", "minimum_full_tail_mbps")])
            for phase in ("idle", "light"):
                samples = [s for r in values for v in r["latency"] if v["phase"] == phase for s in v["samples_ms"]]
                if samples:
                    print(" ", phase, len(samples), "mean/p95/max", *[round(x, 1) for x in (statistics.mean(samples), sorted(samples)[len(samples)*95//100], max(samples))])


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("root", type=Path)
    parser.add_argument("output", type=Path)
    parser.add_argument("--commit", required=True)
    parser.add_argument("--run-id", required=True, type=int)
    args = parser.parse_args()
    logs = collect(args.root)
    if not logs:
        raise ValueError("No measurement logs")
    report = {"schema": 2, "classification": "laboratory experiment; not promoted or field qualified",
              "commit": args.commit, "run_id": args.run_id, "server_network_rtt_ms": 20,
              "original_duplex_gates_changed": False, "logs": logs}
    report["source_inputs"] = {}
    for path in sorted(args.root.rglob("provenance.json")):
        provenance = json.loads(path.read_text())
        if not provenance.get("production_reference_unchanged"):
            raise ValueError(f"Mutated RC2 reference: {path}")
        previous = provenance.get("previous_production", {})
        model = provenance["model_production"]
        authority = ("journal/", "wallet/", "pool/", "wire/", "secure/", "recovery/", "session/", "flow/", "datapath/")
        changed = [name for name in set(previous) | set(model) if name.startswith(authority) and previous.get(name) != model.get(name)]
        if changed:
            raise ValueError(f"Physical/flow authority changed since DEV3: {changed}")
        canonical = lambda value: hashlib.sha256(json.dumps(value, sort_keys=True).encode()).hexdigest()
        report["source_inputs"][str(path.relative_to(args.root))] = {
            "artifact_provenance_sha256": hashlib.sha256(path.read_bytes()).hexdigest(),
            "baseline": provenance["baseline"], "reference_production_unchanged": True,
            "rc2_production_manifest_sha256": canonical(provenance["baseline_production"]),
            "model_production_manifest_sha256": canonical(model),
            "authority_modules_identical_to_dev3": True,
            "remote_cleanup_floor_ms": provenance.get("remote_cleanup_floor_ms"),
            "local_cleanup_divisor": provenance.get("local_cleanup_divisor", 4),
            "refill_trigger": provenance.get("refill_trigger"),
            "periodic_remote_cleanup_ms": provenance.get("periodic_remote_cleanup_ms"),
            "headroom_hint_used_as_periodic_timer": provenance.get("headroom_hint_used_as_periodic_timer", True),
            "shared_fixture_overrides": provenance.get("shared_fixture_overrides", {})}
    args.output.write_text(json.dumps(report, indent=2) + "\n")
    describe(logs)

if __name__ == "__main__":
    main()
