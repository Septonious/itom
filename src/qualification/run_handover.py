"""Native Wi-Fi-to-cellular recovery gate using generated local mailboxes."""
import argparse
import json
import os
from pathlib import Path
import subprocess
import tempfile
import time

from run_home import build, output


def run(provider, worker, dest, encrypted):
    dest.mkdir()
    with tempfile.TemporaryDirectory() as name, (dest / "log.txt").open("w") as log:
        state = Path(name)
        descriptor = state / "provider.json"
        proc = subprocess.Popen([provider, "-dir", state, "-accounts", "15", "-client-latency-profile", "home",
                                 "-client-upload-mbps", "500", "-client-download-mbps", "500", "-variable=true",
                                 "-literal-minus", "-append-service", "6ms", "-download-fetch-delay", "15ms",
                                 "-upload-fetch-delay", "20ms"], stdout=log, stderr=log)
        try:
            deadline = time.monotonic() + 12
            while not descriptor.is_file():
                if proc.poll() is not None or time.monotonic() >= deadline:
                    raise RuntimeError("provider startup failed")
                time.sleep(.03)
            cfg = json.loads(descriptor.read_text())
            args = [worker, "-provider", descriptor, "-out", dest / "result.json", "-state", state / "state",
                    "-frontend", "native-tun", "-active-grant", "8", "-idle-probes", "6", "-post-probes", "6",
                    "-handover-outage", "8s", "-probe-timeout", "8s"]
            if encrypted:
                args.append("-encrypted-exit")
            code = subprocess.run(args, env=dict(os.environ, SSL_CERT_FILE=cfg["root"]),
                                  stdout=log, stderr=log, timeout=100).returncode
        finally:
            proc.terminate()
            try:
                proc.wait(timeout=10)
            except subprocess.TimeoutExpired:
                proc.kill()
                proc.wait()
    result = json.loads((dest / "result.json").read_text())
    h = result.get("handover", {})
    passed = (code == 0 and result.get("success") is True and result.get("stage") == "complete"
              and result.get("encrypted_exit") is encrypted and h.get("outage_ms") == 8000
              and h.get("verified_echoes") == 6 and 0 < h.get("verified_after_restore_ms", 0) <= 8000)
    return {"pass": passed, "exit": code, "handover": h}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("output", type=Path)
    parser.add_argument("--repeat", type=int, default=2, choices=(1, 2, 3))
    args = parser.parse_args()
    dest = args.output.resolve()
    dest.mkdir(parents=True, exist_ok=False)
    with tempfile.TemporaryDirectory() as name:
        provider, worker = (Path(name) / key for key in ("provider", "worker"))
        gate = {"provider_sha256": build("provider", provider), "worker_sha256": build("worker", worker), "runs": []}
        for iteration in range(args.repeat):
            for encrypted in (False, True):
                label = f"{'encrypted' if encrypted else 'direct'}-{iteration + 1}"
                row = run(provider, worker, dest / label, encrypted)
                row["case"] = label
                gate["runs"].append(row)
                output(dest / "gate.json", gate)
                print(label, row, flush=True)
        return int(not all(row["pass"] for row in gate["runs"]))


if __name__ == "__main__":
    raise SystemExit(main())
