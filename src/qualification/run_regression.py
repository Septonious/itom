"""Run all Go test packages in fresh, bounded, sequential race-test processes."""
import argparse
import json
import os
from pathlib import Path
import re
import signal
import subprocess
import tempfile
import time

MODULES = ("mailstream", "core", "native", "qualification")
ROOT = Path(__file__).resolve().parents[1]
TEST_NAME = re.compile(r"(?:Test|Fuzz|Example)[A-Za-z0-9_]*\Z")


def batches(names, size):
    if size < 1:
        raise ValueError("batch size must be positive")
    for at in range(0, len(names), size):
        yield names[at:at + size]


def selector(names):
    return "^(?:" + "|".join(re.escape(name) for name in names) + ")$" if names else "^$"


def execute(args, cwd, env, log, timeout):
    started = time.monotonic()
    with subprocess.Popen(args, cwd=cwd, env=env, stdout=subprocess.PIPE,
                          stderr=subprocess.STDOUT, start_new_session=True) as proc:
        try:
            output, _ = proc.communicate(timeout=timeout)
            code = proc.returncode
        except subprocess.TimeoutExpired:
            # A killed Go parent must not leave compile/test children running.
            try:
                os.killpg(proc.pid, signal.SIGKILL)
            except ProcessLookupError:
                pass
            output, _ = proc.communicate()
            output, code = output + b"\nExternal timeout\n", 124
    log.write_bytes(output)
    return code, output.decode(errors="replace"), round(time.monotonic() - started, 3)


def save(path, report):
    temp = path.with_suffix(".tmp")
    temp.write_text(json.dumps(report, indent=2) + "\n")
    temp.replace(path)


def run(root, output, modules, batch_size, timeout, build_timeout, race=True):
    output.mkdir(parents=True, exist_ok=False)
    env = dict(os.environ, GOMAXPROCS="2", GOFLAGS="-p=1")
    env.setdefault("GOMEMLIMIT", "1800MiB")
    env.setdefault("GOGC", "50")
    report = {"race": race, "batch_size": batch_size, "modules": list(modules),
              "packages": [], "failures": [], "complete": False}
    manifest = output / "manifest.json"
    save(manifest, report)
    go_flags = ["-mod=readonly", "-p=1"] + (["-race"] if race else [])
    with tempfile.TemporaryDirectory(prefix="itom-tests-") as tmp:
        for module in modules:
            module_dir = root / module
            code, listed, _ = execute(["go", "list", "-mod=readonly", "-f", "{{.Dir}}", "./..."],
                                      module_dir, env, output / (module + "-packages.log"), build_timeout)
            if code:
                report["failures"].append({"module": module, "stage": "list", "code": code})
                save(manifest, report)
                continue
            if not listed.strip():
                report["failures"].append({"module": module, "stage": "no packages"})
                save(manifest, report)
                continue
            for directory in listed.splitlines():
                path = Path(directory)
                if not path.is_absolute() or not path.is_dir():
                    report["failures"].append({"module": module, "stage": "package path", "value": directory})
                    continue
                index = len(report["packages"])
                prefix = f"{index:03d}-{module}"
                entry = {"package": str(path.relative_to(root)), "tests": [], "batches": [], "complete": False}
                report["packages"].append(entry)
                binary = Path(tmp) / "test-bin"
                binary.unlink(missing_ok=True)
                code, _, elapsed = execute(["go", "test", "-c", *go_flags, "-o", str(binary), "."],
                                           path, env, output / (prefix + "-build.log"), build_timeout)
                entry["build"] = {"code": code, "seconds": elapsed}
                if code:
                    report["failures"].append({"package": entry["package"], "stage": "build", "code": code})
                    save(manifest, report)
                    continue
                if not binary.exists():
                    entry["complete"] = True  # go test -c compiled a package with no tests
                    save(manifest, report)
                    continue
                code, listed, _ = execute([str(binary), "-test.list", "."], path, env,
                                          output / (prefix + "-list.log"), timeout)
                if code:
                    report["failures"].append({"package": entry["package"], "stage": "discover", "code": code})
                    save(manifest, report)
                    continue
                names = [line.strip() for line in listed.splitlines() if TEST_NAME.fullmatch(line.strip())]
                if len(names) != len(set(names)):
                    report["failures"].append({"package": entry["package"], "stage": "duplicate test names"})
                    save(manifest, report)
                    continue
                entry["tests"] = names
                save(manifest, report)
                # Even an empty test list runs TestMain once, matching go test behavior.
                groups = list(batches(names, batch_size)) or [[]]
                for number, group in enumerate(groups):
                    log = output / f"{prefix}-batch-{number:03d}.log"
                    code, text, elapsed = execute([str(binary), "-test.v", "-test.count=1", "-test.parallel=1",
                                                   "-test.timeout=" + str(timeout) + "s", "-test.run=" + selector(group)],
                                                  path, env, log, timeout + 15)
                    observed = set(re.findall(r"^=== RUN\s+([^\s/]+)$", text, re.MULTILINE))
                    # Tests, examples and fuzz seed corpora all emit RUN headings.
                    # A custom TestMain that silently omits a test cannot pass.
                    missing = sorted(set(group) - observed)
                    skipped = sorted(set(re.findall(r"^--- SKIP:\s+([^\s/]+)", text, re.MULTILINE)))
                    required_skips = sorted(set(skipped) & {"TestRC20PackagedAssets"})
                    entry["batches"].append({"tests": group, "code": code, "seconds": elapsed,
                                             "missing_tests": missing, "skipped": skipped, "log": log.name})
                    failed = bool(code or missing or required_skips)
                    if failed:
                        report["failures"].append({"package": entry["package"], "batch": number,
                                                   "code": code, "missing_tests": missing,
                                                   "required_skips": required_skips})
                    save(manifest, report)
                    print(entry["package"], f"{number + 1}/{len(groups)}", "FAIL" if failed else "PASS", flush=True)
                entry["complete"] = True
                save(manifest, report)
    report["complete"] = not report["failures"] and all(p["complete"] for p in report["packages"])
    save(manifest, report)
    return 0 if report["complete"] else 1


def main():
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument("output", type=Path)
    p.add_argument("--module", choices=MODULES, action="append")
    p.add_argument("--batch-size", type=int, default=3)
    p.add_argument("--timeout", type=int, default=300, help="seconds per batch")
    p.add_argument("--build-timeout", type=int, default=900)
    args = p.parse_args()
    if min(args.batch_size, args.timeout, args.build_timeout) < 1:
        p.error("all limits must be positive")
    return run(ROOT, args.output.resolve(), args.module or MODULES, args.batch_size, args.timeout, args.build_timeout)


if __name__ == "__main__":
    raise SystemExit(main())
