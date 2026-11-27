"""Manually qualify the actual native/IMAP/RU path across an asymmetric rate cycle.

This is deliberately not a mandatory build step. The provider is a local
Rambler model, not a claim about the live provider or carrier.
"""
import argparse
import json
import os
from pathlib import Path
import subprocess
import tempfile
import time

from run_home import build, output


def run(provider, worker, destination, encrypted, exit_rtt):
    destination.mkdir()
    with tempfile.TemporaryDirectory() as name, (destination / "log.txt").open("w") as log:
        state = Path(name)
        descriptor = state / "provider.json"
        args = [provider, "-dir", state, "-accounts", "15", "-client-latency-profile", "rambler-cellular",
                "-rambler-cellular-cycle", "-append-service", "6ms", "-upload-fetch-delay", "20ms",
                "-download-fetch-delay", "15ms"]
        proc = subprocess.Popen(args, stdout=log, stderr=log)
        try:
            deadline = time.monotonic() + 12
            while not descriptor.is_file():
                if proc.poll() is not None or time.monotonic() >= deadline:
                    raise RuntimeError("provider startup failed")
                time.sleep(.03)
            cfg = json.loads(descriptor.read_text())
            command = [worker, "-provider", descriptor, "-out", destination / "result.json",
                       "-state", state / "state", "-label", "candidate", "-frontend", "native-tun",
                       "-active-grant", "8", "-production-grant", "-cellular", "-duration", "55s",
                       "-streams", "8", "-duplex", "-probe-timeout", "15s", "-idle-probes", "6", "-post-probes", "6"]
            if encrypted:
                command.extend(["-encrypted-exit", "-exit-rtt", exit_rtt])
            result = subprocess.run(command, env=dict(os.environ, SSL_CERT_FILE=cfg["root"]),
                                    stdout=log, stderr=log, timeout=240)
        finally:
            proc.terminate()
            try:
                proc.wait(timeout=10)
            except subprocess.TimeoutExpired:
                proc.kill()
                proc.wait()
    problems = []
    if result.returncode:
        problems.append(f"worker exit {result.returncode}")
    data = json.loads((destination / "result.json").read_text())
    if not data.get("success") or data.get("stage") != "complete":
        problems.append("incomplete native session or application recovery")
    phases = data.get("Phases") or []
    if not phases or phases[0].get("name") != "duplex":
        problems.append("missing asymmetric duplex transfer")
    metrics = {}
    if phases:
        for direction in ("download", "upload"):
            rates = phases[0].get(direction, {}).get("half_second_mbps", [])
            # Provider scenario begins immediately before the loaded phase.
            # The 2 s serialization outage ends at 30 s; give recovery 10 s.
            recovery = rates[80:110]
            degraded = rates[40:56]
            if len(recovery) != 30 or len(degraded) != 16:
                problems.append(f"{direction}: incomplete rate-cycle samples")
                continue
            recovered_mbps = sum(recovery) / len(recovery)
            degraded_mbps = sum(degraded) / len(degraded)
            metrics[direction] = {"degraded_mbps": degraded_mbps, "recovery_mbps": recovered_mbps}
            if recovered_mbps <= 0 or recovered_mbps <= degraded_mbps:
                problems.append(f"{direction}: delivery did not improve after bearer recovery")
    if not data.get("AfterMS"):
        problems.append("missing post-load echo evidence")
    output(destination / "assessment.json", {"problems": problems, "directional": metrics,
           "qualification": "synthetic survival and recovery; no live-provider throughput guarantee"})
    return problems


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("output", type=Path)
    parser.add_argument("--exit-rtt", default="80ms", help="configurable inter-VPS RTT, not a measured field value")
    args = parser.parse_args()
    destination = args.output.resolve()
    destination.mkdir(parents=True, exist_ok=False)
    with tempfile.TemporaryDirectory() as name:
        provider, worker = (Path(name) / key for key in ("provider", "worker"))
        manifest = {"provider_sha256": build("provider", provider), "worker_sha256": build("worker", worker), "exit_rtt": args.exit_rtt, "runs": []}
        for encrypted in (False, True):
            label = "encrypted" if encrypted else "direct"
            problems = run(provider, worker, destination / label, encrypted, args.exit_rtt)
            manifest["runs"].append({"mode": label, "problems": problems})
            output(destination / "gate.json", manifest)
            print(label, "passed" if not problems else problems, flush=True)
        return int(any(row["problems"] for row in manifest["runs"]))


if __name__ == "__main__":
    raise SystemExit(main())
