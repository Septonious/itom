"""Local Go core gates. Does not produce an APK or a deployable server."""
import hashlib
import json
import os
from pathlib import Path
import platform
import re
import shutil
import subprocess
import tempfile
import time
import traceback
from collections import defaultdict, deque

import build

GO_VERSION = "1.27.1"
# Official go.dev/dl JSON, retrieved 2026-09-24. No floating toolchain download.
GO_SHA256 = {
    ("windows", "amd64"): "a3911b5e0e1b1053f25ed0675f4c1c6aad1e2bfcf253df2b9be4caabd2edd95d",
    ("windows", "arm64"): "13b69b87bb0e83f96bc68560a8cace7f0343b1e03469f1110ea18d17e3234069",
    ("linux", "amd64"): "63d339f0da5ab53635a56f2490a7984dfe12dfcff22ad749f63edaf590168445",
    ("linux", "arm64"): "3450b45a3f9ee8568792736a5c5e70a1f2e9b36c35a8f74958c03e51d7d92bec",
    ("darwin", "amd64"): "8f8f52c6649542cf027bbc9b9c68d1ec042f9f34808a40413f0b8b3f66f3caa4",
    ("darwin", "arm64"): "ee215d57e0ec269c60cc9ceca68e6bda321ba9ee5afe24f4b0988703c2d87d12",
}
PINS = {"github.com/emersion/go-imap/v2": "v2.0.0-beta.8", "github.com/emersion/go-message": "v0.18.2", "github.com/emersion/go-sasl": "v0.0.0-20241020182733-b788ff22d5a6", "golang.org/x/term": "v0.46.0", "go.etcd.io/bbolt": "v1.4.3", "github.com/flynn/noise": "v1.1.0", "golang.org/x/crypto": "v0.57.0", "golang.org/x/sys": "v0.48.0"}
EXPECTED_PACKAGES = {"itom.local/core/" + p for p in ("wire", "secure", "flow", "pool", "session", "recovery", "integration", "journal", "wallet", "datapath", "socks", "mailbox", "mailcontrol", "servercfg", "manager", "adminapi", "adminserver", "probe", "netroute", "tunnel", "cmd/itom")}


def host_pair():
    system = {"Windows": "windows", "Linux": "linux", "Darwin": "darwin"}.get(platform.system())
    arch = {"amd64": "amd64", "x86_64": "amd64", "arm64": "arm64", "aarch64": "arm64"}.get(platform.machine().lower())
    if (system, arch) not in GO_SHA256:
        build.fail("Go core launcher supports Windows/Linux/macOS x64 and arm64")
    return system, arch


def ensure_go(cache: Path):
    system, arch = host_pair()
    go_home = cache / f"go-{GO_VERSION}-{system}-{arch}"
    binary = go_home / "go/bin" / ("go.exe" if system == "windows" else "go")
    suffix = "zip" if system == "windows" else "tar.gz"
    name = f"go{GO_VERSION}.{system}-{arch}.{suffix}"
    expected = GO_SHA256[(system, arch)]
    if binary.is_file():
        # The cache is local to this user. Check exact version before executing
        # any build; downloads are always checked against the pinned SHA-256.
        return binary, expected
    if go_home.exists():
        build.fail("Incomplete Go cache directory; remove it before retrying: " + str(go_home))
    archive = build.download("https://go.dev/dl/" + name, cache / name, expected)
    staging = Path(tempfile.mkdtemp(prefix="go-extract-", dir=cache))
    # mkdtemp creates its directory, but extract deliberately requires a NEW
    # destination. Own the temporary parent and extract into an absent child.
    unpack = staging / "unpacked"
    try:
        build.extract(archive, unpack)
        if not (unpack / "go/bin" / binary.name).is_file():
            build.fail("Go archive has no expected executable")
        if go_home.exists():
            build.fail("Incomplete Go cache directory; remove it before retrying: " + str(go_home))
        unpack.rename(go_home)
    finally:
        if staging.exists():
            shutil.rmtree(staging)
    return binary, expected


OPTIONAL_TESTS = {
    # Executed separately by transport_qualification.py on six fresh cases.
    # Full normal builds enable it too; race timing is checked separately.
    ("itom.local/core/tunnel", "TestTransportReworkQualification"),
}


def enable_required_survival_tests(env: dict) -> None:
    """Ensure full build gates run the real IMAP outage scenarios.

    An ambient opt-out must never turn a required test into a skipped test.
    `summarize_events` continues to reject every unapproved SKIP or FAIL.
    """
    env["ITOM_UNSTABLE_SURVIVAL"] = "1"
    # The performance test sets its own scoped scenario. Applying load knobs
    # globally would also delay unrelated real-IMAP correctness fixtures.
    env.update(ITOM_CORE_QUALIFICATION="1", ITOM_REWORK_QUALIFICATION="0")
    # Rev8 restores a strict 100 Mbps floor with no speed tolerance.
    # Byte integrity, liveness, recovery and post-load checks remain mandatory.
    env["ITOM_STABILITY_QUALIFICATION"] = "0"
    env["ITOM_LOAD_MIN_MBPS"] = "100"


def summarize_events(path: Path, expected: set[str]):
    packages, tests, running, failures, skipped_optional = set(), set(), set(), [], set()
    with path.open(encoding="utf-8") as stream:
        for line in stream:
            if not line.strip():
                continue
            try:
                event = json.loads(line)
            except json.JSONDecodeError as error:
                raise RuntimeError("Invalid Go test JSON; see " + str(path)) from error
            package, test, action = event.get("Package", ""), event.get("Test"), event.get("Action")
            key = (package, test)
            if action == "run" and test:
                running.add(key)
            if action == "pass" and test:
                tests.add(key)
            if action == "pass" and not test:
                packages.add(package)
            if action == "skip" and key in OPTIONAL_TESTS:
                skipped_optional.add(key)
            elif action in ("fail", "skip"):
                failures.append({"package": package, "test": test, "action": action})
    missing = sorted(expected - packages)
    if failures or missing or not tests or running != tests | skipped_optional or packages != expected:
        raise RuntimeError(f"Go test gate incomplete: missing={missing}, failures={failures}, tests={len(tests)}")
    return {"packages": sorted(packages), "passed_test_events": len(tests), "failed": 0, "skipped": len(skipped_optional),
            "skipped_optional": [{"package": p, "test": t} for p, t in sorted(skipped_optional)]}


def failed_test_details(events: Path):
    """Keep named failures and their reasons despite later passing packages."""
    contexts = defaultdict(lambda: deque(maxlen=8))
    reasons = defaultdict(lambda: deque(maxlen=4))
    failed = []
    with events.open(encoding="utf-8", errors="replace") as stream:
        for raw in stream:
            try:
                event = json.loads(raw)
            except json.JSONDecodeError:
                continue
            key = (event.get("Package", ""), event.get("Test"))
            output = event.get("Output")
            if key[1] and isinstance(output, str):
                for line in output.rstrip("\r\n").splitlines():
                    if line.strip():
                        contexts[key].append(line[:2000])
                        # Go labels assertions; the wording heuristic also matches
                        # cleanup metrics such as trace_errors=0. Keep failure
                        # causes instead of later supervisor health counters.
                        telemetry = re.search(r"\.go:\d+: (?:supervisor_timeline |supervisor_summary )", line)
                        if not telemetry and (event.get("OutputType") == "error" or re.search(r"\.go:\d+: .*?(?:gate|panic|error|expected|got|want|fatal|deadline|never|did not|could not)", line, re.I)):
                            reasons[key].append(line[:2000])
            if event.get("Action") == "fail" and key[1]:
                failed.append({"package": key[0], "test": key[1], "elapsed": event.get("Elapsed")})
    for failure in failed:
        key = (failure["package"], failure["test"])
        failure["reasons"] = list(reasons[key])
        failure["context"] = list(contexts[key])
    return failed


def test_failure_diagnostics(events: Path, stderr_path: Path, max_lines: int = 80):
    """Return a bounded human-readable tail from `go test -json` + stderr.

    The JSONL files remain the canonical artifacts; this summary only prevents
    a compile/test failure from being hidden behind a path-only launcher error.
    """
    lines = []
    important = []
    running = set()
    try:
        with events.open(encoding="utf-8", errors="replace") as stream:
            for raw in stream:
                try:
                    event = json.loads(raw)
                except json.JSONDecodeError:
                    continue
                package, test, action = event.get("Package", ""), event.get("Test"), event.get("Action")
                if test and action == "run":
                    running.add((package, str(test)))
                elif test and action in ("pass", "fail", "skip"):
                    running.discard((package, str(test)))
                output = event.get("Output")
                if isinstance(output, str):
                    for line in output.rstrip("\r\n").splitlines():
                        if line.strip():
                            clean = line[:2000]
                            lines.append(clean)
                            if re.search(r"\.go:\d+:\d+:|^# ", clean):
                                important.append(clean)
                if action == "fail":
                    label = "FAIL " + package
                    if test:
                        label += " " + str(test)
                    lines.append(label.strip())
    except OSError as error:
        lines.append("cannot read go test events: " + str(error))
    try:
        for line in stderr_path.read_text(encoding="utf-8", errors="replace").splitlines():
            if line.strip():
                lines.append("stderr: " + line[:2000])
    except OSError as error:
        lines.append("cannot read go test stderr: " + str(error))
    if running:
        preview = ", ".join((package + " " + test).strip() for package, test in sorted(running)[:16])
        if len(running) > 16:
            preview += f", ... +{len(running) - 16} more"
        lines.append("UNFINISHED at go test exit: " + preview)
    if len(lines) > max_lines:
        tail = lines[-max_lines:]
        pinned = []
        for line in important:
            if line not in pinned and line not in tail:
                pinned.append(line)
        # Compiler diagnostics are often emitted near the beginning of a noisy
        # parallel go test stream. Keep a bounded prefix of them visible even
        # when the ordinary tail is truncated.
        pinned = pinned[-8:]
        keep_tail = max(1, max_lines - len(pinned) - 1)
        omitted = max(0, len(lines) - keep_tail)
        lines = [f"... {omitted} earlier diagnostic lines omitted ...", *pinned, *lines[-keep_tail:]]
    # `go test ./...` continues with wire/fuzz tests after tunnel fails. Its
    # last 80 lines therefore often contain only PASS. Pin the actual failed
    # tests and assertion reasons at the front, not just compiler messages.
    try:
        failed = failed_test_details(events)
    except OSError:
        failed = []
    pinned_failures = []
    for failure in failed[:6]:
        pinned_failures.append("FAIL " + failure["package"] + " " + failure["test"])
        pinned_failures.extend((failure["reasons"] or failure["context"])[-2:])
    if len(failed) > 6:
        pinned_failures.append(f"... {len(failed)-6} more named failures; see core-report.json ...")
    if pinned_failures:
        lines = pinned_failures + lines[-max(1, max_lines-len(pinned_failures)):]
    return "\n".join(lines)


def verify_imap_fork(core_root: Path):
    fork = core_root / "third_party/go-imap"
    manifest = core_root / "third_party/GO_IMAP_SHA256.json"
    metadata = json.loads(manifest.read_text(encoding="utf-8"))
    if metadata.get("base_module") != "github.com/emersion/go-imap/v2" or metadata.get("base_version") != PINS["github.com/emersion/go-imap/v2"]:
        build.fail("Unexpected local IMAP base pin")
    actual = {}
    for path in sorted(fork.rglob("*")):
        if path.is_symlink():
            build.fail("Local IMAP source symlink is forbidden")
        if path.is_file():
            actual[path.relative_to(fork).as_posix()] = hashlib.sha256(path.read_bytes()).hexdigest()
    if not actual or actual != metadata.get("files") or "LICENSE" not in actual:
        build.fail("Local IMAP source differs from its pinned manifest")
    return hashlib.sha256(manifest.read_bytes()).hexdigest()


def verify_imap_replacement(item: dict, core_root: Path, allowed_path="./third_party/go-imap"):
    replacement = item.get("Replace", {})
    expected = (core_root / "third_party/go-imap").resolve()
    if (item.get("Path") != "github.com/emersion/go-imap/v2" or
            item.get("Version") != PINS["github.com/emersion/go-imap/v2"] or
            replacement.get("Path") != allowed_path or replacement.get("Version") or
            not replacement.get("Dir") or Path(replacement["Dir"]).resolve() != expected):
        build.fail("Only the pinned bundled IMAP replacement is allowed")
    return verify_imap_fork(core_root)


def verify_modules(raw: str, core_root: Path = None):
    modules = {}
    decoder = json.JSONDecoder()
    while raw.strip():
        raw = raw.lstrip()
        item, end = decoder.raw_decode(raw)
        raw = raw[end:]
        if item.get("Replace"):
            if core_root is None:
                build.fail("Module replacements are not allowed without a verified source root")
            verify_imap_replacement(item, core_root)
        elif core_root is not None and item.get("Path") == "github.com/emersion/go-imap/v2":
            build.fail("The bundled IMAP patch is required")
        if not item.get("Main"):
            modules[item["Path"]] = item["Version"]
    for module, version in PINS.items():
        if modules.get(module) != version:
            build.fail(f"Dependency pin changed: {module}: {modules.get(module)} != {version}")
    return modules


def build_binaries(command, work: Path, dist: Path, env: dict, release: str | None = None):
    saved = {k: env.get(k) for k in ("GOOS", "GOARCH", "CGO_ENABLED")}
    files = []
    try:
        for system, arch, name in (("linux", "amd64", "itom-linux-amd64"), ("linux", "arm64", "itom-linux-arm64")):
            env.update({"GOOS": system, "GOARCH": arch, "CGO_ENABLED": "0"})
            destination = dist / name
            destination.unlink(missing_ok=True)
            command(["build", "-mod=readonly", "-trimpath", "-buildvcs=false", "-o", str(destination), "./cmd/itom"], work)
            if not destination.is_file() or destination.stat().st_size == 0:
                build.fail("Missing built binary: " + name)
            sha = hashlib.sha256(destination.read_bytes()).hexdigest()
            files.append((name, sha))
            if release:
                named = (f"ITOM-0.9.1-Rev10-{system}-{arch}" if release == "Rev10"
                         else f"ITOM-0.9.2-{release}-{system}-{arch}" if release in ("Rev1","Rev2","Rev3","Rev4","Rev5","Rev6","Rev7","Rev8","Rev9")
                         else f"ITOM_{release}_{system}_{arch}")
                shutil.copy2(destination, dist / named)
                files.append((named, sha))
        (dist / "SHA256SUMS").write_text("".join(digest + "  " + name + "\n" for name, digest in files), encoding="utf-8")
    finally:
        for key, value in saved.items():
            if value is None:
                env.pop(key, None)
            else:
                env[key] = value


def runtime_hash(core: Path):
    """Fingerprint first-party Go runtime, independent of checkout location."""
    digest = hashlib.sha256()
    for path in sorted(core.rglob("*.go")):
        relative = path.relative_to(core)
        if "third_party" in relative.parts or path.name.endswith("_test.go"):
            continue
        digest.update(("core/" + relative.as_posix()).encode())
        digest.update(path.read_bytes())
    return digest.hexdigest()


def run(root: Path, info: dict, cache: Path, race: bool = False, server: bool = False):
    dist = root / "dist"
    dist.mkdir(exist_ok=True)
    cache = cache.expanduser().resolve()
    cache.mkdir(parents=True, exist_ok=True)
    lockfile = cache / "core-build.lock"
    try:
        lock = os.open(lockfile, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
    except FileExistsError:
        build.fail("Another core check may be running. After a terminated run remove " + str(lockfile))
    os.close(lock)
    report = {"source_revision": info["revision"], "go": GO_VERSION, "status": "failed", "race": race, "stage": "prepare",
              "started_utc": time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime()),
              "apk_built": False, "server_built": False, "imap_tested": False}
    archive_sha = os.environ.get("ITOM_SOURCE_ARCHIVE_SHA256", "")
    if re.fullmatch(r"[a-f0-9]{64}", archive_sha):
        report["source_archive_sha256"] = archive_sha
    log_path = dist / "core-build.log"
    try:
        report["core_runtime_sha256"] = runtime_hash(root / "core")
        log_path.write_text(f"ITOM source revision: {info['revision']}\nGo toolchain: {GO_VERSION}\n", encoding="utf-8")
        # Remove only this gate's own reports, so a failed run cannot inherit
        # successful results from an older build. Keep other user artifacts.
        for name in ("core-test-events.jsonl", "core-test-stderr.log", "core-race-events.jsonl", "core-race-stderr.log", "core-go.mod", "core-go.sum", "core-modules.json"):
            (dist / name).unlink(missing_ok=True)
        report["stage"] = "bootstrap"
        with log_path.open("a", encoding="utf-8") as log:
            log.write("Preparing Go toolchain from the verified cache or archive.\n")
        go, checksum = ensure_go(cache)
        env = os.environ.copy()
        # Respect neither ambient go.work nor private module/sumdb bypasses for
        # this public, pinned module. Overrides are limited to this process tree.
        for key in ("GOROOT", "GOPATH", "GOOS", "GOARCH", "GOAMD64", "GOARM64", "GONOSUMDB", "GONOPROXY", "GOPRIVATE", "GOEXPERIMENT", "GODEBUG"):
            env.pop(key, None)
        env.update({"GOTOOLCHAIN": "local", "GOWORK": "off", "GOENV": "off", "GOFLAGS": "-buildvcs=false",
                    "GOPROXY": "https://proxy.golang.org", "GOSUMDB": "sum.golang.org",
                    "GOPATH": str(cache / "gopath"), "GOMODCACHE": str(cache / "gomod"),
                    "GOCACHE": str(cache / "gocache"), "CGO_ENABLED": "1" if race else "0"})
        with log_path.open("a", encoding="utf-8") as log:
            def command(args, cwd, capture=False):
                report["stage"] = "go " + " ".join(args)
                print("Go:", " ".join(args), flush=True)
                log.write("$ go " + " ".join(args) + "\n"); log.flush()
                result = subprocess.run([str(go), *args], cwd=cwd, env=env, text=True, encoding="utf-8",
                                        errors="replace", stdout=subprocess.PIPE if capture else log, stderr=log, check=True)
                return result.stdout if capture else ""

            version = command(["version"], root, capture=True).strip()
            if f"go{GO_VERSION} " not in version:
                build.fail("Unexpected Go executable: " + version)
            report.update({"toolchain_sha256": checksum, "go_version_output": version})
            # Resolve metadata in an isolated copy. Original source hashes and
            # the user's edited checkout remain intact. Never synthesize go.sum.
            with tempfile.TemporaryDirectory(prefix="itom-core-", dir=dist) as temporary:
                work = Path(temporary) / "core"
                shutil.copytree(root / "core", work)
                if runtime_hash(work) != report["core_runtime_sha256"]:
                    build.fail("Core runtime changed while creating the test copy; repeat on final sources")
                command(["mod", "tidy"], work)
                command(["mod", "verify"], work)
                modules = verify_modules(command(["list", "-mod=readonly", "-m", "-json", "all"], work, capture=True), work)
                report["imap_fork_manifest_sha256"] = verify_imap_fork(work)
                (dist / "core-modules.json").write_text(json.dumps(modules, indent=2) + "\n", encoding="utf-8")
                for name in ("go.mod", "go.sum"):
                    shutil.copy2(work / name, dist / ("core-" + name))
                listed = set(command(["list", "-mod=readonly", "./..."], work, capture=True).splitlines())
                if listed != EXPECTED_PACKAGES:
                    build.fail("Unexpected Go package set: " + repr(sorted(listed)))
                # Both stages are mandatory for --race: production performance
                # in the normal binary, full workload/race/integrity afterwards.
                # Go timeout covers the ENTIRE package, including startup, load
                # and all mandatory network-outage tests. Windows FIELD72 was
                # already 240.77s before the three newly mandatory scenarios.
                # Allow setup and slow Windows filesystem overhead (420s), but
                # keep all scenario-specific traffic/rejoin/RTT deadlines as is.
                # These three outage/recovery tests are *mandatory* for the
                # core/server/all gates. Standalone `go test` intentionally
                # leaves them opt-in to avoid expensive fault injection on
                # every developer's quick unit-test run. The release gate
                # must explicitly enable them, never silently permit SKIP.
                enable_required_survival_tests(env)
                report["unstable_survival_tests"] = "enabled (mandatory; SKIP fails the Go gate)"
                report["qualification_policy"] = "stability; 90 Mbps floor waived by user; all liveness, RTT, integrity and recovery checks required"
                log.write("Mandatory IMAP outage tests: ITOM_UNSTABLE_SURVIVAL=1\n")
                log.flush()
                modes = [("normal", False)] + ([("race", True)] if race else [])
                for mode, instrumented in modes:
                    env["CGO_ENABLED"] = "1" if instrumented else "0"
                    # Race overhead cannot qualify physical speed. The same
                    # source must pass its normal performance case above.
                    env["ITOM_CORE_QUALIFICATION"] = "0" if instrumented else "1"
                    # The mandatory field lifecycle suite includes a 30-second
                    # blackout plus real traffic/rejoin. Individual application
                    # deadlines remain unchanged; allow the complete package
                    # enough wall time to run those additional cases serially.
                    # Rev5 adds calibrated paced-body (38s) and application
                    # scenarios to a package already taking about 550s. This
                    # package envelope does not change any per-case deadline.
                    # Rev7 adds correlated-role, handshake and missing-IDLE
                    # regressions to the serial package; allow their total time.
                    args = ["test", "-mod=readonly", "-json", "-count=1", "-timeout=1200s", "-p=1"]
                    if instrumented:
                        args.append("-race")
                    args.append("./...")
                    report["stage"] = "go " + " ".join(args)
                    print("Go:", " ".join(args), flush=True)
                    log.write("$ go " + " ".join(args) + "\n"); log.flush()
                    prefix = "core-race" if instrumented else "core-test"
                    events = dist / (prefix + "-events.jsonl")
                    stderr_path = dist / (prefix + "-stderr.log")
                    with events.open("w", encoding="utf-8") as event_log, stderr_path.open("w", encoding="utf-8") as error_log:
                        result = subprocess.run([str(go), *args], cwd=work, env=env, stdout=event_log, stderr=error_log)
                    if result.returncode:
                        report["failed_tests"] = failed_test_details(events)
                        diagnostic = test_failure_diagnostics(events, stderr_path)
                        if diagnostic:
                            print("Go test diagnostics:\n" + diagnostic, flush=True)
                            log.write("Go test diagnostics:\n" + diagnostic + "\n"); log.flush()
                        build.fail("Go tests failed; read " + str(events) + " and " + str(stderr_path))
                    outcome = summarize_events(events, EXPECTED_PACKAGES)
                    if instrumented:
                        report["race_tests"] = outcome
                    else:
                        report["tests"] = outcome
                        report["performance_mode"] = "normal Go binary; local diagnostic assertions, not field speed qualification"
                        report["field_performance_qualified"] = False
                fork_args = ["test", "-mod=readonly", "-p=1", "-count=1", "-timeout=120s"]
                if race:
                    fork_args.append("-race")
                command([*fork_args, "./..."], work / "third_party/go-imap")
                report["imap_fork_tests"] = "passed"
                command(["vet", "-mod=readonly", "./..."], work)
                if server:
                    build_binaries(command, work, dist, env, release=info["release"])
                    report["server_built"] = True
                    report["built_targets"] = ["linux/amd64", "linux/arm64"]
                report["imap_tested"] = True
                report["live_provider_tested"] = False
                if runtime_hash(root / "core") != report["core_runtime_sha256"]:
                    build.fail("Core runtime changed during checks; repeat on final sources")
                report.update({"status": "passed", "stage": "complete", "vet": "passed", "fuzz": "seed corpus only; no timed fuzz campaign"})
        print("Core gates passed. Report:", dist / "core-report.json", flush=True)
        print("Android IMAP TCP runtime and Linux server integrated. Device qualification remains required.", flush=True)
        if server:
            print("Binaries and SHA256SUMS:", dist, flush=True)
        return 0
    except BaseException as error:
        report["error"] = str(error)
        try:
            with log_path.open("a", encoding="utf-8") as log:
                log.write("\nFAILED at " + report["stage"] + "\n")
                traceback.print_exception(type(error), error, error.__traceback__, file=log)
        except OSError as log_error:
            report["log_error"] = str(log_error)
            print("Cannot write core build log:", log_error, flush=True)
        print("Core gate failed. Log:", log_path, flush=True)
        raise
    finally:
        try:
            (dist / "core-report.json").write_text(json.dumps(report, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")
        finally:
            lockfile.unlink(missing_ok=True)
