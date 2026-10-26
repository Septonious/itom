"""Offline regression tests of the local Go gate, not execution of Go code."""
import json
from pathlib import Path
import struct
import tempfile
import unittest
from unittest.mock import patch
from contextlib import redirect_stdout
import io
import hashlib
import tarfile
import zipfile

import build
import build_core
import build_android_core
import package_sources


class GoBootstrapTests(unittest.TestCase):
    """Exercise the actual downloader/cache/extractor with tiny local archives.

    The fixture is not an executable Go toolchain and is never run. Only the
    platform selector, fixture checksum and network fallback are substituted.
    """
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory(prefix="itom-go-test-")
        self.cache = Path(self.temp.name) / "cache with spaces"
        self.cache.mkdir()

    def tearDown(self):
        self.temp.cleanup()

    def archive(self, host=("windows", "amd64"), members=None):
        system, arch = host
        executable = "go.exe" if system == "windows" else "go"
        if members is None:
            members = {f"go/bin/{executable}": b"fixture, not an executable", "go/VERSION": b"fixture version"}
        suffix = "zip" if system == "windows" else "tar.gz"
        archive = self.cache / f"go{build_core.GO_VERSION}.{system}-{arch}.{suffix}"
        if suffix == "zip":
            with zipfile.ZipFile(archive, "w") as bundle:
                for name, data in members.items():
                    bundle.writestr(name, data)
        else:
            with tarfile.open(archive, "w:gz") as bundle:
                for name, data in members.items():
                    member = tarfile.TarInfo(name)
                    member.size = len(data)
                    member.mode = 0o755 if name.startswith("go/bin/") else 0o644
                    bundle.addfile(member, io.BytesIO(data))
        return archive, hashlib.sha256(archive.read_bytes()).hexdigest()

    def install(self, host, checksum):
        with patch("build_core.host_pair", return_value=host), \
                patch.dict(build_core.GO_SHA256, {host: checksum}), \
                patch("build.urllib.request.urlopen", side_effect=AssertionError("cached archive must not use network")):
            return build_core.ensure_go(self.cache)

    def test_fresh_zip_and_tar_toolchains_install(self):
        for host in (("windows", "amd64"), ("linux", "amd64"), ("darwin", "arm64")):
            with self.subTest(host=host):
                archive, checksum = self.archive(host)
                binary, actual = self.install(host, checksum)
                self.assertEqual(checksum, actual)
                self.assertEqual(b"fixture, not an executable", binary.read_bytes())
                self.assertEqual("go.exe" if host[0] == "windows" else "go", binary.name)
                self.assertEqual(f"go-{build_core.GO_VERSION}-{host[0]}-{host[1]}", binary.parents[2].name)
                self.assertTrue(archive.exists())
                self.assertEqual([], list(self.cache.glob("go-extract-*")))

    def test_installed_toolchain_reused_without_extraction(self):
        host = ("windows", "amd64")
        _, checksum = self.archive(host)
        first = self.install(host, checksum)
        with patch("build_core.build.download") as download, patch("build_core.build.extract") as extract:
            self.assertEqual(first, self.install(host, checksum))
            download.assert_not_called()
            extract.assert_not_called()

    def test_failed_extraction_cleans_own_staging_and_can_retry(self):
        host = ("windows", "amd64")
        _, checksum = self.archive(host, {"go/bin/go.exe": b"partial", "../escaped": b"bad"})
        with self.assertRaisesRegex(RuntimeError, "Unsafe archive path"):
            self.install(host, checksum)
        self.assertFalse((self.cache / f"go-{build_core.GO_VERSION}-windows-amd64").exists())
        self.assertEqual([], list(self.cache.glob("go-extract-*")))
        self.assertFalse((self.cache / "escaped").exists())
        _, checksum = self.archive(host)
        self.assertTrue(self.install(host, checksum)[0].is_file())

    def test_missing_binary_is_not_published(self):
        host = ("windows", "amd64")
        _, checksum = self.archive(host, {"go/VERSION": b"incomplete"})
        with self.assertRaisesRegex(RuntimeError, "no expected executable"):
            self.install(host, checksum)
        self.assertFalse((self.cache / f"go-{build_core.GO_VERSION}-windows-amd64").exists())
        self.assertEqual([], list(self.cache.glob("go-extract-*")))

    def test_incomplete_destination_and_unrelated_files_are_preserved(self):
        host = ("windows", "amd64")
        _, checksum = self.archive(host)
        destination = self.cache / f"go-{build_core.GO_VERSION}-windows-amd64"
        destination.mkdir()
        marker = destination / "user.txt"
        marker.write_text("preserve", encoding="utf-8")
        unrelated = self.cache / "go-extract-from-another-run"
        unrelated.mkdir()
        with self.assertRaisesRegex(RuntimeError, "Incomplete Go cache directory"):
            self.install(host, checksum)
        self.assertEqual("preserve", marker.read_text(encoding="utf-8"))
        self.assertEqual([unrelated], list(self.cache.glob("go-extract-*")))

    def test_promotion_failure_does_not_leave_partial_installation(self):
        host = ("windows", "amd64")
        archive, checksum = self.archive(host)
        with patch("pathlib.Path.rename", side_effect=PermissionError("destination busy")):
            with self.assertRaisesRegex(PermissionError, "destination busy"):
                self.install(host, checksum)
        self.assertFalse((self.cache / f"go-{build_core.GO_VERSION}-windows-amd64").exists())
        self.assertEqual([], list(self.cache.glob("go-extract-*")))
        self.assertEqual(checksum, hashlib.sha256(archive.read_bytes()).hexdigest())


class CoreGateTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.root = Path(self.temp.name)

    def tearDown(self):
        self.temp.cleanup()

    def events(self, values):
        path = self.root / "events.jsonl"
        path.write_text("".join(json.dumps(x, ensure_ascii=False) + "\n" for x in values), encoding="utf-8")
        return path

    def success(self):
        return [{"Action": "run", "Package": "p", "Test": "TestOne"},
                {"Action": "pass", "Package": "p", "Test": "TestOne"},
                {"Action": "pass", "Package": "p"}]

    def test_complete_go_report_accepted(self):
        result = build_core.summarize_events(self.events(self.success()), {"p"})
        self.assertEqual(1, result["passed_test_events"])

    def test_missing_package_rejected(self):
        with self.assertRaises(RuntimeError):
            build_core.summarize_events(self.events(self.success()), {"p", "q"})

    def test_compiled_api_package_is_distinct_from_a_skipped_test(self):
        compiled = [
            {"Action": "output", "Package": "api", "Output": "?\tapi\t[no test files]\n"},
            {"Action": "skip", "Package": "api"},
        ]
        result = build_core.summarize_events(self.events(self.success() + compiled), {"p", "api"})
        self.assertEqual(["api"], result["compiled_without_tests"])
        self.assertEqual(0, result["skipped"])
        with self.assertRaises(RuntimeError):
            build_core.summarize_events(self.events(self.success() + compiled[1:]), {"p", "api"})
        with self.assertRaises(RuntimeError):
            build_core.summarize_events(self.events(self.success() + compiled + [
                {"Action": "skip", "Package": "api", "Test": "TestRequired"}
            ]), {"p", "api"})

    def test_truncated_test_rejected(self):
        with self.assertRaises(RuntimeError):
            build_core.summarize_events(self.events(self.success()[:1]), {"p"})

    def test_full_core_gate_enables_mandatory_survival_tests(self):
        for previous in (None, "0", "1", "false"):
            with self.subTest(previous=previous):
                env = {"GOMAXPROCS": "4"}
                if previous is not None:
                    env["ITOM_UNSTABLE_SURVIVAL"] = previous
                build_core.enable_required_survival_tests(env)
                self.assertEqual("1", env["ITOM_UNSTABLE_SURVIVAL"])
                self.assertEqual("4", env["GOMAXPROCS"])
                self.assertEqual("1", env["ITOM_CORE_QUALIFICATION"])
                self.assertEqual("0", env["ITOM_STABILITY_QUALIFICATION"])
                self.assertEqual("100", env["ITOM_LOAD_MIN_MBPS"])
                self.assertFalse(any(key.startswith("ITOM_LOAD_") and key != "ITOM_LOAD_MIN_MBPS" for key in env))

    def test_mandatory_survival_tests_cannot_be_skipped(self):
        names = (
            "TestUnstableSequentialAccountOutagesKeepApplicationSockets",
            "TestUnstableEightTCPStreamsProgressAfterOutages",
            "TestUnstableWholeProviderBlackoutRestoresPendingTCP",
        )
        for name in names:
            with self.subTest(name=name):
                events = self.success() + [
                    {"Action": "run", "Package": "itom.local/core/tunnel", "Test": name},
                    {"Action": "skip", "Package": "itom.local/core/tunnel", "Test": name},
                ]
                with self.assertRaisesRegex(RuntimeError, "Go test gate incomplete"):
                    build_core.summarize_events(self.events(events), {"p"})
                events[-1]["Action"] = "pass"
                result = build_core.summarize_events(self.events(events), {"p"})
                self.assertEqual(0, result["skipped"])
                self.assertEqual(2, result["passed_test_events"])

    def test_only_named_optional_measurements_can_skip(self):
        for name in ("TestTransportReworkQualification",):
            events = self.success() + [
                {"Action": "run", "Package": "itom.local/core/tunnel", "Test": name},
                {"Action": "skip", "Package": "itom.local/core/tunnel", "Test": name},
            ]
            result = build_core.summarize_events(self.events(events), {"p"})
            self.assertEqual(1, result["skipped"])
            self.assertEqual(name, result["skipped_optional"][0]["test"])
            events[-1]["Action"] = "fail"
            with self.assertRaises(RuntimeError):
                build_core.summarize_events(self.events(events), {"p"})

    def test_qualification_is_only_deferred_when_it_skips_not_when_it_fails(self):
        key = {"Package": "itom.local/core/tunnel", "Test": "TestTransportReworkQualification"}
        for action in ("skip", "pass", "fail"):
            events = self.success() + [{"Action": "run", **key}, {"Action": action, **key}]
            if action == "fail":
                with self.assertRaisesRegex(RuntimeError, "Go test gate incomplete"):
                    build_core.summarize_events(self.events(events), {"p"})
            else:
                result = build_core.summarize_events(self.events(events), {"p"})
                self.assertEqual(1 if action == "skip" else 0, result["skipped"])
                self.assertEqual(1 if action == "skip" else 2, result["passed_test_events"])

    def test_failed_or_skipped_test_rejected(self):
        for action in ("fail", "skip"):
            with self.subTest(action=action), self.assertRaises(RuntimeError):
                build_core.summarize_events(self.events(self.success() + [{"Action": action, "Package": "p", "Test": "Other"}]), {"p"})

    def test_empty_or_malformed_report_rejected(self):
        for content in ("", "not-json\n", json.dumps({"Action": "pass", "Package": "p"})):
            path = self.root / "empty.jsonl"
            path.write_text(content, encoding="utf-8")
            with self.subTest(content=content), self.assertRaises(RuntimeError):
                build_core.summarize_events(path, {"p"})

    def test_failed_go_test_diagnostics_surface_output_and_stderr(self):
        events = self.events([
            {"Action": "output", "Package": "itom.local/core/tunnel", "Output": "./retirement_test.go:19: cannot use map\n"},
            {"Action": "fail", "Package": "itom.local/core/tunnel"},
        ])
        stderr = self.root / "stderr.log"
        stderr.write_text("compiler stderr\n", encoding="utf-8")
        summary = build_core.test_failure_diagnostics(events, stderr)
        self.assertIn("retirement_test.go:19", summary)
        self.assertIn("FAIL itom.local/core/tunnel", summary)
        self.assertIn("stderr: compiler stderr", summary)

    def test_failed_go_test_diagnostics_pin_early_compiler_error(self):
        noisy = [{"Action": "output", "Package": "p", "Output": f"noise {i}\n"} for i in range(120)]
        noisy.insert(3, {"Action": "output", "Package": "p", "Output": "tunnel\\control.go:297:42: undefined: context\n"})
        events = self.events(noisy + [{"Action": "fail", "Package": "p"}])
        stderr = self.root / "stderr.log"
        stderr.write_text("", encoding="utf-8")
        summary = build_core.test_failure_diagnostics(events, stderr, max_lines=20)
        self.assertIn("undefined: context", summary)

    def test_failed_go_test_diagnostics_name_unfinished_test(self):
        events = self.events([
            {"Action": "run", "Package": "itom.local/core/tunnel", "Test": "TestAuthenticatedRuntimeTCP/TLS_IMAP"},
            {"Action": "output", "Package": "itom.local/core/tunnel", "Test": "TestAuthenticatedRuntimeTCP/TLS_IMAP", "Output": "panic: test timed out after 2m0s\n"},
        ])
        stderr = self.root / "stderr.log"
        stderr.write_text("", encoding="utf-8")
        summary = build_core.test_failure_diagnostics(events, stderr)
        self.assertIn("UNFINISHED at go test exit", summary)
        self.assertIn("TestAuthenticatedRuntimeTCP/TLS_IMAP", summary)

    def test_late_passing_fuzz_cannot_hide_earlier_performance_failures(self):
        values = []
        for test in ("TestLoadedApplicationRTT", "TestLoadedApplicationUploadWithTrace"):
            values += [
                {"Action": "output", "Package": "tunnel", "Test": test,
                 "Output": "load_test.go:354: legacy performance gate: 25 directional Mbps\n", "OutputType": "error"},
                {"Action": "fail", "Package": "tunnel", "Test": test, "Elapsed": 20},
            ]
        values += [{"Action": "output", "Package": "wire", "Test": "FuzzSeed",
                    "Output": f"PASS seed {i}\n"} for i in range(150)]
        events = self.events(values)
        stderr = self.root / "stderr.log"
        stderr.write_text("", encoding="utf-8")
        summary = build_core.test_failure_diagnostics(events, stderr, max_lines=20)
        self.assertIn("FAIL tunnel TestLoadedApplicationRTT", summary)
        self.assertIn("FAIL tunnel TestLoadedApplicationUploadWithTrace", summary)
        self.assertIn("25 directional Mbps", summary)
        self.assertLessEqual(len(summary.splitlines()), 20)
        failures = build_core.failed_test_details(events)
        self.assertEqual(2, len(failures))
        self.assertEqual(20, failures[0]["elapsed"])

    def test_cleanup_telemetry_cannot_hide_timeout_assertion(self):
        name = "TestHTTPMixedTrafficPreservesIntegrityAndCarriers/provider_cap_recovers"
        values = [{"Action": "output", "Package": "tunnel", "Test": name,
                   "Output": "http_scenario_test.go:188: provider cap did not actually degrade DATA capacity context deadline exceeded\n",
                   "OutputType": "error"}]
        values += [{"Action": "output", "Package": "tunnel", "Test": name,
                    "Output": "supervisor_fixture_test.go:190: supervisor_timeline account=a01 event=flow_health trace_errors=0\n"} for _ in range(20)]
        values.append({"Action": "fail", "Package": "tunnel", "Test": name})
        events = self.events(values)
        stderr = self.root / "stderr.log"
        stderr.write_text("", encoding="utf-8")
        reasons = build_core.failed_test_details(events)[0]["reasons"]
        self.assertEqual(1, len(reasons))
        self.assertIn("context deadline exceeded", reasons[0])
        summary = build_core.test_failure_diagnostics(events, stderr, max_lines=20)
        self.assertIn("provider cap did not actually degrade DATA capacity", summary)

    def test_module_pin_and_replacement_checks(self):
        modules = [{"Path": name, "Version": version} for name, version in build_core.PINS.items()]
        raw = "\n".join(json.dumps(x) for x in modules)
        self.assertEqual(build_core.PINS, build_core.verify_modules(raw))
        for modified in ({**modules[0], "Version": "v99.0.0"}, {**modules[0], "Replace": {"Dir": "/tmp/replaced"}}):
            with self.subTest(modified=modified), self.assertRaises(RuntimeError):
                build_core.verify_modules("\n".join(json.dumps(x) for x in [modified, *modules[1:]]))

    def pinned_fork(self):
        core = self.root / "core"
        fork = core / "third_party/go-imap"
        fork.mkdir(parents=True)
        (fork / "LICENSE").write_text("MIT fixture\n", encoding="utf-8")
        (fork / "go.mod").write_text("module github.com/emersion/go-imap/v2\n", encoding="utf-8")
        files = {p.relative_to(fork).as_posix(): hashlib.sha256(p.read_bytes()).hexdigest() for p in fork.iterdir()}
        (core / "third_party/GO_IMAP_SHA256.json").write_text(json.dumps({"base_module": "github.com/emersion/go-imap/v2", "base_version": build_core.PINS["github.com/emersion/go-imap/v2"], "files": files}), encoding="utf-8")
        return core, fork

    def test_packager_rejects_stale_imap_pin_before_writing_archive(self):
        core, fork = self.pinned_fork()
        tools = self.root / "tools"
        tools.mkdir()
        (tools / "archive_main.py").write_text("# fixture\n", encoding="utf-8")
        license_path = fork / "LICENSE"
        original = license_path.read_bytes()
        license_path.write_bytes(b"unreviewed change\n")
        archive = self.root / "candidate.zip"
        with self.assertRaisesRegex(RuntimeError, "pinned manifest"):
            package_sources.package(self.root, archive)
        self.assertFalse(archive.exists())
        license_path.write_bytes(original)
        package_sources.package(self.root, archive)
        with zipfile.ZipFile(archive) as bundle:
            self.assertEqual(original, bundle.read("itom/core/third_party/go-imap/LICENSE"))

    def test_bundled_imap_pin_accepts_only_exact_local_sources(self):
        core, fork = self.pinned_fork()
        modules = [{"Path": name, "Version": version} for name, version in build_core.PINS.items()]
        imap = modules[0]
        imap["Replace"] = {"Path": "./third_party/go-imap", "Dir": str(fork.resolve())}
        raw = "\n".join(json.dumps(x) for x in modules)
        self.assertEqual(build_core.PINS, build_core.verify_modules(raw, core))
        (fork / "LICENSE").write_text("changed\n", encoding="utf-8")
        with self.assertRaisesRegex(RuntimeError, "manifest"):
            build_core.verify_modules(raw, core)

    def test_bundled_imap_pin_rejects_missing_foreign_and_remote_replacements(self):
        core, fork = self.pinned_fork()
        for replacement in (None, {"Path": "../other", "Dir": str(fork)}, {"Path": "./third_party/go-imap", "Dir": str(self.root / "elsewhere")}, {"Path": "./third_party/go-imap", "Dir": str(fork), "Version": "v9"}):
            modules = [{"Path": name, "Version": version} for name, version in build_core.PINS.items()]
            if replacement is not None:
                modules[0]["Replace"] = replacement
            with self.subTest(replacement=replacement), self.assertRaises(RuntimeError):
                build_core.verify_modules("\n".join(json.dumps(x) for x in modules), core)

    def test_native_and_core_must_select_the_same_imap_patch(self):
        core, fork = self.pinned_fork()
        selected = {"Path": "github.com/emersion/go-imap/v2", "Version": build_core.PINS["github.com/emersion/go-imap/v2"], "Replace": {"Path": "../core/third_party/go-imap", "Dir": str(fork)}}
        self.assertTrue(build_core.verify_imap_replacement(selected, core, "../core/third_party/go-imap"))
        selected["Replace"]["Path"] = "./third_party/go-imap"
        with self.assertRaises(RuntimeError):
            build_core.verify_imap_replacement(selected, core, "../core/third_party/go-imap")

    def test_failed_bootstrap_releases_lock_and_records_failure(self):
        cache = self.root / "cache"
        with patch("build_core.ensure_go", side_effect=RuntimeError("download failed")), redirect_stdout(io.StringIO()):
            with self.assertRaisesRegex(RuntimeError, "download failed"):
                build_core.run(self.root, {"revision": "core-1"}, cache)
        self.assertFalse((cache / "core-build.lock").exists())
        report = json.loads((self.root / "dist/core-report.json").read_text(encoding="utf-8"))
        self.assertEqual("failed", report["status"])
        self.assertFalse(report["server_built"])
        log = (self.root / "dist/core-build.log").read_text(encoding="utf-8")
        self.assertIn("RuntimeError: download failed", log)
        self.assertIn("core-1", log)
        self.assertEqual("bootstrap", report["stage"])

    def test_stale_reports_removed_before_bootstrap(self):
        dist = self.root / "dist"
        dist.mkdir()
        report = dist / "core-test-events.jsonl"
        report.write_text("stale pass", encoding="utf-8")
        user_file = dist / "user.txt"
        user_file.write_text("keep", encoding="utf-8")
        with patch("build_core.ensure_go", side_effect=RuntimeError("offline")), redirect_stdout(io.StringIO()), self.assertRaises(RuntimeError):
            build_core.run(self.root, {"revision": "core-1"}, self.root / "cache")
        self.assertFalse(report.exists())
        self.assertEqual("keep", user_file.read_text(encoding="utf-8"))

    def test_existing_lock_is_preserved(self):
        cache = self.root / "cache"
        cache.mkdir()
        lock = cache / "core-build.lock"
        lock.write_text("active", encoding="utf-8")
        with self.assertRaises(RuntimeError):
            build_core.run(self.root, {"revision": "core-1"}, cache)
        self.assertEqual("active", lock.read_text(encoding="utf-8"))

    def test_changed_runtime_copy_cannot_report_success(self):
        from subprocess import CompletedProcess
        core = self.root / "core"
        core.mkdir()
        (core / "runtime.go").write_text("package fixture\n", encoding="utf-8")
        before = build_core.runtime_hash(core)
        original_copy = build_core.shutil.copytree

        def changed_copy(source, destination, *args, **kwargs):
            result = original_copy(source, destination, *args, **kwargs)
            (Path(destination) / "runtime.go").write_text("package changed\n", encoding="utf-8")
            return result

        version = CompletedProcess([], 0, "go version go" + build_core.GO_VERSION + " linux/amd64\n")
        cache = self.root / "cache"
        with patch("build_core.ensure_go", return_value=(Path("fake-go"), "checksum")), \
                patch("build_core.subprocess.run", return_value=version) as command, \
                patch("build_core.shutil.copytree", side_effect=changed_copy), \
                redirect_stdout(io.StringIO()), self.assertRaisesRegex(RuntimeError, "runtime changed"):
            build_core.run(self.root, {"revision": "core-1"}, cache)
        command.assert_called_once()  # Only version, no tests on altered input.
        report = json.loads((self.root / "dist/core-report.json").read_text(encoding="utf-8"))
        self.assertEqual("failed", report["status"])
        self.assertEqual(before, report["core_runtime_sha256"])
        self.assertFalse((cache / "core-build.lock").exists())

    def test_wire_fixture_matches_independent_struct_layout(self):
        fixture = json.loads((build.ROOT / "core/wire/testdata/wire2.json").read_text(encoding="utf-8"))
        header = bytes.fromhex(fixture["header"])
        frame = bytes.fromhex(fixture["frame"])
        self.assertEqual(64, len(header))
        self.assertEqual((b"ITM2", 2, 1, 0, bytes(range(1, 17)), bytes(range(17, 33)), 7, 0, 9, 115),
                         struct.unpack(">4sBBH16s16sIB3xQI4x", header))
        self.assertEqual((32, 0, 0, 1, 3, 0, 3), struct.unpack(">BBHQQQI", frame[:32]))
        self.assertEqual(b"abc", frame[32:])

    def test_core_target_does_not_enter_android_build(self):
        with patch("build.subprocess.run"), patch("build_core.run", return_value=0) as run_core, \
                patch("build.ensure_jdk") as jdk, redirect_stdout(io.StringIO()):
            self.assertEqual(0, build.main(["core", "--cache", str(self.root / "cache")]))
        run_core.assert_called_once()
        jdk.assert_not_called()

    def test_cp1252_console_does_not_affect_file_reads(self):
        # The old launcher defect depended on implicit text decoding. An
        # explicit UTF-8 JSON report with Cyrillic must remain readable.
        path = self.events(self.success() + [{"Action": "output", "Package": "p", "Output": "Проверка"}])
        self.assertEqual(1, build_core.summarize_events(path, {"p"})["passed_test_events"])


class ServerBuildTests(unittest.TestCase):
    def test_target_paths_hashes_and_environment_are_restored(self):
        with tempfile.TemporaryDirectory(prefix="itom targets ") as tmp:
            root = Path(tmp)
            env = {"CGO_ENABLED": "1", "KEEP": "yes"}
            calls = []
            def command(args, cwd):
                calls.append((env["GOOS"], env["GOARCH"], env["CGO_ENABLED"], args, cwd))
                Path(args[args.index("-o") + 1]).write_bytes(b"fixture-not-executable")
            build_core.build_binaries(command, root, root, env)
            self.assertEqual([("linux", "amd64"), ("linux", "arm64")], [x[:2] for x in calls])
            self.assertTrue(all(x[2] == "0" and x[3][-1] == "./cmd/itom" for x in calls))
            self.assertEqual({"CGO_ENABLED": "1", "KEEP": "yes"}, env)
            self.assertEqual(2, len((root / "SHA256SUMS").read_text(encoding="utf-8").splitlines()))

    def test_public_release_server_binaries_keep_legacy_paths(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            def command(args, cwd):
                Path(args[args.index("-o") + 1]).write_bytes(b"server-fixture")
            build_core.build_binaries(command, root, root, {}, release="FIELD77")
            for arch in ("amd64", "arm64"):
                old = root / f"itom-linux-{arch}"
                named = root / f"ITOM_FIELD77_linux_{arch}"
                self.assertEqual(old.read_bytes(), named.read_bytes())
            sums = (root / "SHA256SUMS").read_text()
            self.assertEqual(4, len(sums.splitlines()))
            self.assertIn("ITOM_FIELD77_linux_amd64", sums)

    def test_failed_compiler_never_marks_complete(self):
        with tempfile.TemporaryDirectory() as tmp:
            env = {"CGO_ENABLED": "0"}
            with self.assertRaises(RuntimeError):
                build_core.build_binaries(lambda *args: None, Path(tmp), Path(tmp), env)
            self.assertFalse((Path(tmp) / "SHA256SUMS").exists())
            self.assertEqual({"CGO_ENABLED": "0"}, env)


class CombinedTargetTests(unittest.TestCase):
    def test_failed_server_gate_stops_before_android(self):
        with patch.object(build.subprocess, "run") as checks, patch.object(build_core, "run", return_value=7) as gate, patch.object(build.platform, "system") as platform, redirect_stdout(io.StringIO()):
            result = build.main(["all", "--accept-android-licenses"])
        self.assertEqual(result, 7)
        self.assertTrue(gate.call_args.kwargs["server"])
        platform.assert_not_called()
        self.assertEqual(checks.call_count, 1)

    def test_passed_server_gate_continues_to_android_setup(self):
        class AndroidSetupReached(Exception):
            pass
        with patch.object(build.subprocess, "run"), patch.object(build_core, "run", return_value=0) as gate, patch.object(build.platform, "system", side_effect=AndroidSetupReached), redirect_stdout(io.StringIO()):
            with self.assertRaises(AndroidSetupReached):
                build.main(["all", "--accept-android-licenses"])
        self.assertTrue(gate.call_args.kwargs["server"])


class AndroidNativePlatformTests(unittest.TestCase):
    def test_minor_sdk_does_not_replace_binding_platform(self):
        with tempfile.TemporaryDirectory() as directory:
            sdk = Path(directory)
            modern = sdk / "platforms/android-37.0/android.jar"
            modern.parent.mkdir(parents=True); modern.touch()
            self.assertEqual(build_android_core.required_android_packages(sdk), ["platforms;android-29", "ndk;" + build_android_core.NDK])
            ndk = sdk / "ndk" / build_android_core.NDK / "source.properties"
            ndk.parent.mkdir(parents=True); ndk.touch()
            self.assertEqual(build_android_core.required_android_packages(sdk), ["platforms;android-29"])
            binding = sdk / "platforms/android-29/android.jar"
            binding.parent.mkdir(parents=True); binding.touch()
            self.assertEqual(build_android_core.required_android_packages(sdk), [])

    def test_launcher_installs_sdk_archives_in_separate_processes(self):
        with tempfile.TemporaryDirectory() as directory:
            cache = Path(directory); sdk = cache / "android-sdk"
            suffix = ".bat" if build.os.name == "nt" else ""
            manager = sdk / "cmdline-tools" / build.CMDLINE_TOOLS / "bin" / ("sdkmanager" + suffix)
            manager.parent.mkdir(parents=True); manager.touch()
            installs = []
            def install(args, **kwargs):
                packages = [arg for arg in args if arg.startswith(("platforms;", "build-tools;"))]
                if not packages: return
                self.assertEqual(len(packages), 1); installs.extend(packages)
                file = sdk / ("platforms/android-" + build.SDK_PACKAGE + "/android.jar" if packages[0].startswith("platforms;") else "build-tools/" + build.BUILD_TOOLS + "/" + ("aapt2.exe" if build.os.name == "nt" else "aapt2"))
                file.parent.mkdir(parents=True); file.touch()
            with patch.dict(build.os.environ, {"ITOM_ANDROID_SDK_ROOT": str(sdk)}), patch.object(build.subprocess, "run", side_effect=install), redirect_stdout(io.StringIO()):
                self.assertTrue(build.ensure_sdk(cache, {}, True, {}).samefile(sdk))
            self.assertEqual(installs, ["platforms;android-" + build.SDK_PACKAGE, "build-tools;" + build.BUILD_TOOLS])

    def test_nested_package_keeps_manager_metadata(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory); (root / "package.xml").write_text("manager-metadata")
            nested = root / "android-16"; nested.mkdir()
            (nested / "aapt2").write_bytes(b"tool-bytes")
            (nested / "source.properties").write_text("Pkg.Revision=36.0.0")
            build.normalize_sdk_package(root, "aapt2")
            self.assertEqual((root / "package.xml").read_text(), "manager-metadata")
            self.assertEqual((root / "aapt2").read_bytes(), b"tool-bytes")
            self.assertFalse(nested.exists()); build.normalize_sdk_package(root, "aapt2")

    def test_ambiguous_package_is_not_promoted(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            for name in ("a", "b"):
                nested = root / name; nested.mkdir(); (nested / "aapt2").touch()
            with self.assertRaises(RuntimeError): build.normalize_sdk_package(root, "aapt2")
            self.assertFalse((root / "aapt2").exists())


if __name__ == "__main__":
    unittest.main(verbosity=1)
