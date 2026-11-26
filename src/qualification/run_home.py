"""Use local generated mailboxes to exercise the production native client and server."""
import argparse
import hashlib
import json
import os
from pathlib import Path
import subprocess
import tempfile
import time

from home_gate import check, ramp_summary

HERE = Path(__file__).resolve().parent


def output(path, obj):
    path.write_text(json.dumps(obj, indent=2, allow_nan=False) + "\n")


def build(package, dest):
    subprocess.run(["go", "build", "-trimpath", "-mod=readonly", "-o", dest, "./" + package],
                   cwd=HERE, check=True)
    return hashlib.sha256(dest.read_bytes()).hexdigest()


def run(provider, worker, dest, seconds, mode, cycles, service, encrypted=False):
    dest.mkdir()
    with tempfile.TemporaryDirectory() as name, (dest / "log.txt").open("w") as log:
        state = Path(name)
        descriptor = state / "provider.json"
        args = [provider, "-dir", state, "-accounts", "15", "-client-latency-profile", "home",
                "-client-upload-mbps", "500", "-client-download-mbps", "500", "-variable=true"]
        if mode == "minus":
            args.append("-literal-minus")
        if service:
            args.extend(["-append-service", "6ms", "-download-fetch-delay", "15ms",
                         "-upload-fetch-delay", "20ms"])
        proc = subprocess.Popen(args, stdout=log, stderr=log)
        try:
            deadline = time.monotonic() + 12
            while not descriptor.is_file():
                if proc.poll() is not None or time.monotonic() >= deadline:
                    raise RuntimeError("fixture provider startup failed")
                time.sleep(.03)
            cfg = json.loads(descriptor.read_text())
            command = [worker, "-provider", descriptor, "-out", dest / "result.json",
                       "-state", state / "state", "-label", "candidate", "-frontend", "native-tun",
                       "-active-grant", "8", "-production-grant", "-account-probes", "-light-cycles", str(cycles),
                       "-duration", f"{seconds}s", "-streams", "8", "-download-first",
                       "-idle-probes", "24", "-post-probes", "24"]
            if encrypted:
                command.append("-encrypted-exit")
            result = subprocess.run(command, env=dict(os.environ, SSL_CERT_FILE=cfg["root"]),
                                    stdout=log, stderr=log, timeout=2*seconds + 100 + 75*cycles)
        finally:
            proc.terminate()
            try:
                proc.wait(timeout=10)
            except subprocess.TimeoutExpired:
                proc.kill()
                proc.wait()
    data = json.loads((dest / "result.json").read_text())
    problems = check(data, seconds, cycles)
    if encrypted and data.get("encrypted_exit") is not True:
        problems.append("missing encrypted-exit fixture evidence")
    ramp = {}
    for phase in data.get("Phases", []):
        direction = phase.get("name")
        rates = phase.get(direction, {}).get("half_second_mbps", [])
        if direction in ("download", "upload") and len(rates) >= seconds * 2:
            ramp[direction] = ramp_summary(rates, seconds)
    output(dest / "ramp.json", ramp)
    if result.returncode:
        problems.append(f"worker exit {result.returncode}")
    return problems


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("output", type=Path)
    parser.add_argument("--screen", action="store_true", help="one short measurement; never qualifies an RC")
    parser.add_argument("--encrypted-exit", action="store_true", help="test the same native path via the OLCRTC-compatible TLS exit")
    args = parser.parse_args()
    out = args.output.resolve()
    out.mkdir(parents=True, exist_ok=False)
    with tempfile.TemporaryDirectory() as name:
        provider, worker = (Path(name) / n for n in ("provider", "worker"))
        manifest = {"provider_sha256": build("provider", provider), "worker_sha256": build("worker", worker),
                    "client_full_duplex_mbps": 500, "modes": ["minus", "plus"],
                    "repeats": 1 if args.screen else 5, "screen": args.screen, "runs": []}
        output(out / "gate.json", manifest)
        for repeat in range(manifest["repeats"]):
            matrix = [(15, "minus")] if args.screen else [(d, m) for m in ("minus", "plus") for d in (15, 60)]
            for seconds, mode in matrix:
                label = f"run-{repeat+1}-{mode}-{seconds}s"
                problems = run(provider, worker, out / label, seconds, mode, 2, False, args.encrypted_exit)
                manifest["runs"].append({"name": label, "errors": problems})
                output(out / "gate.json", manifest)
                if problems:
                    print(label, *problems, sep="\n", flush=True)
                    return 1
                print(label, "passed", flush=True)
        if not args.screen:
            problems = run(provider, worker, out / "provider-delay-holdout", 15, "minus", 2, True, args.encrypted_exit)
            manifest["runs"].append({"name": "provider-delay-holdout", "errors": problems})
            output(out / "gate.json", manifest)
            if problems:
                print(*problems, sep="\n")
                return 1
        manifest["qualified_home_matrix"] = not args.screen
        output(out / "gate.json", manifest)
        return 0


if __name__ == "__main__":
    raise SystemExit(main())
