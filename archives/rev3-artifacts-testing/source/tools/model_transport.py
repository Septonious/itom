"""Fixed 20 Mbps application acceptance and paired comparisons; no APK/server build."""
import json
import os
from pathlib import Path
import shutil
import subprocess
import tempfile
import time

import build
import build_core

HARNESS = (
    "field_https_test.go", "field_provider_test.go", "mobile_network_test.go", "imap_test.go",
    "provider_network_test.go", "provider_state_test.go", "provider_visibility_test.go",
    "supervisor_fixture_test.go", "provider_calibration_test.go", "qualification20_test.go",
)


def source_hash(root):
    return build_core.runtime_hash(root / "core")


def read_events(path, access_mbps=20):
    results, calibrations, failures = [], [], []
    finished = False
    for line in path.read_text(encoding="utf-8").splitlines():
        event = json.loads(line)
        output = event.get("Output", "")
        if ("field_https_result " if access_mbps==20 else "field_130_result ") in output:
            results.append(output.strip())
        if "access_calibration " in output:
            calibrations.append(output.strip())
        if event.get("Action") == "fail":
            failures.append({"test": event.get("Test"), "package": event.get("Package")})
        if event.get("Action") == "pass" and not event.get("Test"):
            finished = True
    return {"results": results, "calibration": calibrations, "failures": failures,
            "complete": finished and len(results) == 3 and bool(calibrations)}


def run(root, info, cache, repeats=3, case="all", profile="service", baseline=None, access_mbps=20):
    if access_mbps not in (20,130) or not 1 <= repeats <= 10 or case not in ("all", "single", "browser") or profile not in ("service", "recorded", "correlated", "stalls"):
        build.fail("Invalid model matrix parameters")
    cache = cache.expanduser().resolve()
    cache.mkdir(parents=True, exist_ok=True)
    go, _ = build_core.ensure_go(cache)
    env = os.environ.copy()
    for key in list(env):
        if key.startswith(("ITOM_FIELD_", "ITOM_LOAD_")) or key in ("GOROOT", "GOPATH", "GOOS", "GOARCH", "GOEXPERIMENT", "GODEBUG", "GONOSUMDB", "GONOPROXY", "GOPRIVATE"):
            env.pop(key, None)
    env.update({"GOTOOLCHAIN": "local", "GOWORK": "off", "GOENV": "off", "GOFLAGS": "-buildvcs=false", "CGO_ENABLED": "0",
                "GOPATH": str(cache / "gopath"), "GOMODCACHE": str(cache / "gomod"), "GOCACHE": str(cache / "gocache"),
                "GOPROXY": "https://proxy.golang.org", "GOSUMDB": "sum.golang.org",
                "ITOM_FIELD_LOAD": "1", "ITOM_FIELD_MOBILE": "1", "ITOM_FIELD_SECONDS": "60", "ITOM_FIELD_WARMUP_SECONDS": "10",
                "ITOM_FIELD_STREAMS": "1" if case == "single" else "4", "ITOM_FIELD_BACKGROUND_STREAMS": "40" if case == "browser" else "0",
                "ITOM_FIELD_LATENCY_PROFILE": "recorded" if profile == "recorded" else "service",
                "ITOM_FIELD_CORRELATED_QUEUE": "1" if profile == "correlated" else "0", "ITOM_FIELD_READ_STALLS": "1" if profile == "stalls" else "0"})
    if access_mbps==130:
        env["ITOM_FIELD_130_LOAD"]="1"
        env["ITOM_FIELD_MOBILE"]="0"
        env["ITOM_FIELD_130_RTT_MS"]="250"
        if case=="all":
            env["ITOM_FIELD_STREAMS"]="16"
            env["ITOM_FIELD_BACKGROUND_STREAMS"]="24"
    version = subprocess.run([str(go), "version"], env=env, capture_output=True, text=True, check=True).stdout
    if f"go{build_core.GO_VERSION} " not in version:
        build.fail("Wrong model toolchain")
    destination = root / "dist" / (f"model{access_mbps}-" + time.strftime("%Y%m%dT%H%M%S", time.gmtime()))
    destination.mkdir(parents=True, exist_ok=False)
    report = {"revision": info["revision"], "runtime_sha256": source_hash(root), "case": case, "profile": profile,
              "physical_mode": "full-duplex; shared across ALL client IMAP sockets in each direction",
              "wire_cap_mbps_per_direction": access_mbps, "path_rtt_ms": 250, "warmup_seconds": 10, "measurement_seconds": 60, "last_third_seconds": 20,
              "criteria": "each active useful direction and tail >=5 Mbps; max app RTT <=1500ms; zero errors" if access_mbps==20 else "measurement only; verifies protocol integrity, probes and carrier continuity; no speed/RTT qualification",
              "classification": "acceptance_model" if access_mbps==20 else "measurement_only",
              "provider_service": "assumed; recorded wrapper durations are a distinct stress profile", "real_android_provider": "NOT_RUN", "runs": []}
    with tempfile.TemporaryDirectory(prefix="paired-core-", dir=destination) as temporary:
        roots = {"candidate": root / "core"}
        if baseline is not None:
            baseline = baseline.expanduser().resolve()
            base_info = build.read_source_info(baseline)
            build_core.verify_imap_fork(baseline / "core")
            work = Path(temporary) / "baseline-core"
            shutil.copytree(baseline / "core", work)
            # Compare implementations under the SAME candidate test peer,
            # calibration and clocks. Only test fixtures are replaced, never
            # baseline runtime. Preserve hashes of both inputs in the report.
            if not (work / "tunnel/qualification20_test.go").exists():
                build.fail("Baseline must support the FIELD47 fixed-window harness")
            for name in HARNESS:
                shutil.copy2(root / "core/tunnel" / name, work / "tunnel" / name)
            roots["baseline"] = work
            report["baseline"] = {"revision": base_info["revision"], "runtime_sha256": source_hash(baseline),
                                  "same_harness": list(HARNESS)}
        for repeat in range(1, repeats + 1):
            order = ["baseline", "candidate"] if "baseline" in roots else ["candidate"]
            if repeat % 2 == 0:
                order.reverse()
            env["ITOM_LOAD_FAULT_SEED"] = str(repeat)
            for label in order:
                events = destination / f"{repeat:02d}-{label}.jsonl"
                stderr = events.with_suffix(".stderr.log")
                print(f"Model{access_mbps} {label} repeat={repeat}/{repeats} case={case} profile={profile}", flush=True)
                with events.open("w", encoding="utf-8") as output, stderr.open("w", encoding="utf-8") as errors:
                    result = subprocess.run([str(go), "test", "-mod=readonly", "-json", "-count=1", "-timeout=360s", "./tunnel", "-run", "^TestTransport20ApplicationAcceptance$" if access_mbps==20 else "^TestTransport130ApplicationMeasurement$"],
                                            cwd=roots[label], env=env, stdout=output, stderr=errors)
                try:
                    measured = read_events(events, access_mbps)
                except (ValueError, OSError) as error:
                    measured = {"complete": False, "parse_error": str(error)}
                passed = result.returncode == 0 and measured["complete"]
                report["runs"].append({"repeat": repeat, "implementation": label, "exit_code": result.returncode,
                                       "passed": passed, "events": events.name, **measured})
                # Write after every run: interruption preserves earlier failures.
                (destination / "results.json").write_text(json.dumps(report, indent=2) + "\n", encoding="utf-8")
    print("Model evidence:", destination, flush=True)
    return 0 if all(run["passed"] for run in report["runs"]) else 1
