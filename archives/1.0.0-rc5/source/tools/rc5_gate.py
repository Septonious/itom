#!/usr/bin/env python3
"""RC5 release gate: no skipped, waived or averaged-away speed failures.

Runs the existing source-fingerprinted qualification tool separately against
fresh and retained physical mail state. This is a laboratory gate, not proof
of phone/provider field performance.
"""
import argparse
import json
import os
import re
from pathlib import Path
import subprocess
import sys
import time

ROOT = Path(__file__).resolve().parents[1]

def main():
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument("--go", default="go", help="Go 1.27.1 binary")
    p.add_argument("--results", type=Path, default=ROOT / "reports" / "rc5")
    p.add_argument("--accounts", type=int, default=15)
    p.add_argument("--repeats", type=int, default=3)
    p.add_argument("--seconds", type=int, default=60)
    p.add_argument("--command-delay-ms", type=int, default=80)
    p.add_argument("--minimum-mbps", type=float, default=100)
    p.add_argument("--rtt-ms", type=float, default=750)
    a = p.parse_args()
    if not 100 <= a.minimum_mbps <= 10000 or a.repeats < 3 or a.seconds < 60:
        p.error("RC5 requires at least 100 Mbps, three repetitions, 60 seconds")
    if a.rtt_ms <= 0 or a.rtt_ms > 750:
        p.error("RC5 requires maximum loaded RTT at most 750 ms")
    a.results.mkdir(parents=True, exist_ok=True)
    all_ok = True
    for retained in (False, True):
        dest = a.results / ("retained" if retained else "fresh")
        cmd = [sys.executable, str(ROOT / "tools" / "transport_qualification.py"),
            "--go", a.go, "--results", str(dest), "--accounts", str(a.accounts),
            "--seconds", str(a.seconds), "--repeats", str(a.repeats),
            "--command-delay-ms", str(a.command_delay_ms),
            "--streams", "both", "--minimum-mbps", str(a.minimum_mbps)]
        if retained:
            cmd.append("--retained-state")
        print("EXEC", " ".join(cmd), flush=True)
        result = subprocess.run(cmd, cwd=ROOT, env=os.environ.copy())
        records = sorted(dest.glob("*/results.json"))
        if not records:
            all_ok = False
            print("FAIL: missing qualification evidence", dest, flush=True)
            continue
        j = json.loads(records[-1].read_text())
        cases = j.get("cases", [])
        if len(cases) != 2 * a.repeats:
            all_ok = False
            print("FAIL: missing test repetitions", len(cases), flush=True)
        for case in cases:
            if not case.get("ok") or case.get("exit_code") != 0:
                all_ok = False
                print("FAIL:", case.get("name"), flush=True)
            log = records[-1].parent / case.get("log", "")
            result_lines = []
            if log.is_file():
                for line in log.read_text(errors="replace").splitlines():
                    if "supervised_result" in line and "max_ms=" in line:
                        result_lines.append(line)
            maxima = []
            for line in result_lines:
                for match in re.finditer(r"supervised_result\b[^\n]*?\bmax_ms=(\d+(?:\.\d+)?)", line):
                    maxima.append(float(match.group(1)))
            if not maxima or max(maxima) >= a.rtt_ms:
                all_ok = False
                print("FAIL: missing or excessive loaded RTT", case.get("name"), maxima, flush=True)
        if result.returncode != 0:
            all_ok = False
    status = {"release":"RC5","gate":"strict-duplex-speed","minimum_mbps":a.minimum_mbps,
        "rtt_limit_ms":a.rtt_ms, "target_sustained_mbps":125,
        "fresh_and_retained":True,"all_passed":all_ok,
        "field_qualified":False,"generated_utc":time.strftime("%Y-%m-%dT%H:%M:%SZ",time.gmtime())}
    (a.results / "RC5_GATE.json").write_text(json.dumps(status, indent=2) + "\n")
    print("RC5 QUALIFICATION", "PASS" if all_ok else "FAIL", flush=True)
    return 0 if all_ok else 1

if __name__ == "__main__":
    raise SystemExit(main())
