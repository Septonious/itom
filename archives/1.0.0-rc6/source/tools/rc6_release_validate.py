#!/usr/bin/env python3
"""Fail-closed RC6 stability prerelease qualification from original Go event logs.

User withdrew the 100/100 Mbps speed floor. This does NOT waive integrity,
physical-accounting, post-load socket liveness, or loaded RTT requirements.
"""
import argparse
import hashlib
import json
from pathlib import Path
import re
import sys

RESULT = re.compile(
    r"supervised_result\b.*?upload_mbps=([\d.]+)\s+download_mbps=([\d.]+)"
    r".*?rtt_samples=(\d+).*?max_ms=(\d+)"
    r".*?timeouts=(\d+)\s+missed=(\d+)\s+overlap=(\d+)"
)
ACCEPTED = re.compile(r"supervised_acceptance\b.*?accepted=(true|false)")
TAIL = re.compile(r"supervised_(?:tail|window).*")

def fail(message):
    raise ValueError(message)

def check_report(root: Path, state: str):
    records = list(root.glob("*/results.json"))
    if len(records) != 1:
        fail(f"{state}: expected exactly one results.json, got {len(records)}")
    report = json.loads(records[0].read_text())
    criteria = report.get("criteria", {})
    scenario = report.get("scenario", {})
    if report.get("classification") != "LABORATORY" or report.get("field_qualified") is not False:
        fail(f"{state}: incorrect evidence classification")
    if scenario.get("accounts") != 10 or scenario.get("seconds") != 60 or scenario.get("repeats") != 3:
        fail(f"{state}: incomplete 10-account 60s repeated scope")
    if scenario.get("streams") != "both" or bool(scenario.get("retained_state")) != (state == "retained"):
        fail(f"{state}: wrong mode/retained scope")
    if criteria.get("qualification_mode") != "stability" or criteria.get("throughput_floor_enforced") is not False:
        fail(f"{state}: throughput waiver must be explicit, never implicit")
    cases = report.get("cases", [])
    expected = {f"{mode}-{n}" for mode in ("single", "multi") for n in (1, 2, 3)}
    if {c.get("name") for c in cases} != expected or len(cases) != 6:
        fail(f"{state}: missing or duplicated SINGLE/MULTI repetitions")
    verified = []
    for case in cases:
        name = case["name"]
        if case.get("source_before") != case.get("source_after") or case.get("source_before") != report.get("source_sha256"):
            fail(f"{state}/{name}: sources changed during test")
        if case.get("ok") is not True or case.get("exit_code") != 0:
            fail(f"{state}/{name}: original Go case failed")
        if bool(case.get("fresh_endpoint_state")) != (state == "fresh"):
            fail(f"{state}/{name}: incorrect fresh/retained state")
        if case.get("summary", {}).get("failed") or case.get("summary", {}).get("missing_required"):
            fail(f"{state}/{name}: failed or missing required test events")
        log = records[0].parent / case.get("log", "")
        if not log.is_file():
            fail(f"{state}/{name}: missing original Go JSONL")
        if hashlib.sha256(log.read_bytes()).hexdigest() != case.get("log_sha256"):
            fail(f"{state}/{name}: original Go event log hash mismatch")
        results, accepted = [], []
        post_sockets, post_fresh = set(), 0
        saw_json = False
        for line in log.read_text(errors="replace").splitlines():
            try:
                event = json.loads(line)
            except json.JSONDecodeError:
                # Go prints module-download notices before the first -json test
                # event on a cold runner. Other malformed lines remain fatal.
                if not saw_json and line.startswith("go: downloading "):
                    continue
                fail(f"{state}/{name}: invalid Go JSONL")
            saw_json = True
            out = event.get("Output", "")
            match = RESULT.search(out)
            if match:
                results.append(match)
            ack = ACCEPTED.search(out)
            if ack:
                accepted.append(ack.group(1))
            if "supervised_post_stress_fresh" in out:
                if "integrity_verified=true" not in out or "original_socket_rechecked=true" not in out:
                    fail(f"{state}/{name}: unverified fresh post-load exchange")
                post_fresh += 1
            elif "supervised_post_stress " in out:
                m = re.search(r"supervised_post_stress index=(\d+) same_socket=(true|false)", out)
                if not m or m.group(2) != "true" or int(m.group(1)) in post_sockets:
                    fail(f"{state}/{name}: invalid or duplicated original-socket post-load check")
                post_sockets.add(int(m.group(1)))
        if len(results) != 1 or accepted != ["true"] or post_sockets != set(range(1,7)) or post_fresh != 1:
            fail(f"{state}/{name}: incomplete authenticated load/postload evidence")
        up, down, probes, rtt, timedout, missed, overlap = map(float, results[0].groups())
        if min(up, down) <= 0:
            fail(f"{state}/{name}: no useful duplex throughput")
        if int(probes) != 30 or int(overlap) != 30 or timedout or missed:
            fail(f"{state}/{name}: missing RTT probes or duplex overlap")
        if not 0 < rtt < 750:
            fail(f"{state}/{name}: loaded RTT {rtt}ms exceeds strict 750ms limit")
        verified.append(dict(case=name,download_mbps=down,upload_mbps=up,max_loaded_rtt_ms=rtt,
                             probe_count=int(probes),probe_overlap=int(overlap),postload_checks=len(post_sockets)+post_fresh))
    return dict(state=state,source_fingerprint=report.get("source_sha256"),
                cases=verified,lab_qualified=True)

def main():
    p=argparse.ArgumentParser(description=__doc__)
    p.add_argument("--fresh",type=Path,required=True)
    p.add_argument("--retained",type=Path,required=True)
    p.add_argument("--output",type=Path,required=True)
    a=p.parse_args()
    try:
        fresh=check_report(a.fresh,"fresh")
        retained=check_report(a.retained,"retained")
        if not fresh["source_fingerprint"] or fresh["source_fingerprint"] != retained["source_fingerprint"]:
            fail("fresh/retained test sources differ")
        cases=fresh["cases"]+retained["cases"]
        report=dict(release="1.0.0-RC6",classification="LABORATORY_STABILITY_ONLY",
                    physical_device_qualified=False,real_provider_qualified=False,
                    throughput_floor_waived_by_user=True,
                    original_100_mbps_goal_met=None, # Intentionally not evaluated under the owner's waiver.
                    all_required_lab_cases_passed=True,required_cases=12,
                    strict_max_loaded_rtt_ms=750,
                    source_fingerprint=fresh["source_fingerprint"],
                    minimum_measured_download_mbps=min(c["download_mbps"] for c in cases),
                    minimum_measured_upload_mbps=min(c["upload_mbps"] for c in cases),
                    maximum_measured_loaded_rtt_ms=max(c["max_loaded_rtt_ms"] for c in cases),
                    results=[fresh,retained])
        a.output.parent.mkdir(parents=True,exist_ok=True)
        a.output.write_text(json.dumps(report,indent=2)+"\n")
        print("RC6 LAB STABILITY GATE PASS; no live-provider/device certificate")
        return 0
    except (ValueError,KeyError,TypeError) as exc:
        print("RC6 LAB STABILITY GATE FAIL:",exc,file=sys.stderr)
        return 1

if __name__=="__main__":
    raise SystemExit(main())
