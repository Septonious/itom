#!/usr/bin/env python3
"""Meaningful offline tests for archive extraction and verified download behavior."""
import hashlib
import io
import json
from pathlib import Path
import re
import stat
import tarfile
import tempfile
import unittest
import subprocess
from unittest.mock import patch
import zipfile
from contextlib import redirect_stdout, redirect_stderr
import xml.etree.ElementTree as ET
import build
import archive_main
import check_sources
import package_sources


class LauncherTests(unittest.TestCase):
    def test_new_apk_manifest_cannot_export_an_old_native_transport(self):
        info = build.read_source_info()
        apk = self.root / "native.apk"
        for revision, accepted in (("transport-rework1-rev2", False), (info["revision"], True)):
            with zipfile.ZipFile(apk, "w") as archive:
                for abi in ("arm64-v8a", "armeabi-v7a"):
                    archive.writestr(f"lib/{abi}/libgojni.so", revision.encode("ascii"))
            if accepted:
                self.assertEqual(2, len(build.verify_apk_native_revision(apk, info)))
            else:
                with self.assertRaisesRegex(RuntimeError, "native transport revision differs"):
                    build.verify_apk_native_revision(apk, info)

    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.root = Path(self.temp.name)

    def tearDown(self):
        self.temp.cleanup()

    def archive(self, name, data=b"source", mode=None):
        archive = self.root / "source.zip"
        with zipfile.ZipFile(archive, "w") as out:
            info = zipfile.ZipInfo(name)
            if mode is not None:
                info.external_attr = mode << 16
            out.writestr(info, data)
        return archive

    def test_public_release_consistent_across_gradle_server_and_archive(self):
        info = check_sources.validate_release_identity(check_sources.ROOT)
        self.assertEqual("Rev3", info["release"])
        self.assertEqual("ITOM-0.9.2-Rev3-debug.apk", build.public_apk_name(info))
        self.assertEqual("ITOM-0.9.2-Rev3-sources.zip", info["archive_name"])

    def test_public_release_mismatch_is_fatal(self):
        info = build.read_source_info()
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            altered = dict(info, release="FIELD59")
            (root / "source-info.json").write_text(json.dumps(altered))
            with self.assertRaisesRegex(RuntimeError, "revision and public FIELD"):
                build.read_source_info(root)

    def test_mismatched_android_and_server_sources_cannot_ship(self):
        source = check_sources.ROOT
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            for rel in ("source-info.json", "android/app/build.gradle.kts",
                        "core/cmd/itom/main.go", "core/manager/trace.go"):
                target = root / rel
                target.parent.mkdir(parents=True, exist_ok=True)
                target.write_bytes((source / rel).read_bytes())
            gradle = root / "android/app/build.gradle.kts"
            gradle.write_text(gradle.read_text().replace(build.read_source_info()["android_version_name"], "old-version"))
            with self.assertRaisesRegex(RuntimeError, "Android versionName differs"):
                check_sources.validate_release_identity(root)
            gradle.write_bytes((source / "android/app/build.gradle.kts").read_bytes())
            main = root / "core/cmd/itom/main.go"
            main.write_text(main.read_text().replace(build.read_source_info()["revision"], "old-revision"))
            with self.assertRaisesRegex(RuntimeError, "Server revision mismatch"):
                check_sources.validate_release_identity(root)

    def test_compiled_apk_identity_validation(self):
        info = build.read_source_info()
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            apk = root / "app-debug.apk"
            apk.write_bytes(b"fake fixture")
            version, code = build.android_identity(info)
            version += "-debug"
            metadata = {"applicationId":"org.itom.client.dev.rev7", "elements":[{"versionCode":code,
                         "versionName":version, "outputFile":"app-debug.apk"}]}
            (root / "output-metadata.json").write_text(json.dumps(metadata))
            aapt2 = root / "build-tools" / build.BUILD_TOOLS / ("aapt2.exe" if build.os.name == "nt" else "aapt2")
            aapt2.parent.mkdir(parents=True, exist_ok=True)
            aapt2.touch()
            aapt_stdout = f"package: name='org.itom.client.dev.rev7' versionCode='{code}' versionName='{version}'\n"
            with patch("build.subprocess.run", return_value=subprocess.CompletedProcess([], 0, aapt_stdout, "")):
                self.assertEqual(version, build.verify_apk_identity(apk, root, info)["version_name"])
                bad = dict(metadata, elements=[dict(metadata["elements"][0], versionName="old-version")])
                (root / "output-metadata.json").write_text(json.dumps(bad))
                with self.assertRaisesRegex(RuntimeError, "Gradle.*output-metadata identity differs"):
                    build.verify_apk_identity(apk, root, info)
                (root / "output-metadata.json").write_text(json.dumps(metadata))
                with patch("build.subprocess.run", return_value=subprocess.CompletedProcess([],0,
                    "package: name='org.itom.client.dev.rev7' versionCode='9037' versionName='old'", "")):
                    with self.assertRaisesRegex(RuntimeError, "Compiled APK identity differs"):
                        build.verify_apk_identity(apk, root, info)

    def test_apk_identity_aapt2_compile_sdk_codename_does_not_override_package(self):
        # Real aapt2 badging has compileSdkVersionCodename='17' after package name.
        # An unanchored 'name' regex silently replaces the true package ID with '17'.
        info = build.read_source_info()
        with tempfile.TemporaryDirectory() as temp:
            root = Path(temp)
            apk = root / "app-debug.apk"
            apk.write_bytes(b"compiled APK fixture")
            version, code = build.android_identity(info)
            version += "-debug"
            (root / "output-metadata.json").write_text(json.dumps({
                "applicationId": "org.itom.client.dev.rev7",
                "elements": [{"versionCode": code, "versionName": version,
                              "outputFile": apk.name}]
            }), encoding="utf-8")
            aapt2 = root / "build-tools" / build.BUILD_TOOLS / (
                "aapt2.exe" if build.os.name == "nt" else "aapt2")
            aapt2.parent.mkdir(parents=True)
            aapt2.touch()
            badging = (f"package: name='org.itom.client.dev.rev7' versionCode='{code}' "
                       f"versionName='{version}' compileSdkVersion='37' "
                       "compileSdkVersionCodename='17'\n")
            with patch("build.subprocess.run", return_value=subprocess.CompletedProcess(
                [], 0, badging, "")):
                identity = build.verify_apk_identity(apk, root, info)
                self.assertEqual(identity["application_id"], "org.itom.client.dev.rev7")
                self.assertEqual(identity["version_name"], version)
                self.assertEqual(identity["version_code"], code)

            # A misleading suffixed key must not be accepted as package name.
            spoofed = (f"package: fakename='org.itom.client.dev.rev7' versionCode='{code}' "
                       f"versionName='{version}' compileSdkVersionCodename='17'\n")
            with patch("build.subprocess.run", return_value=subprocess.CompletedProcess(
                [], 0, spoofed, "")):
                with self.assertRaisesRegex(RuntimeError, "Compiled APK identity differs"):
                    build.verify_apk_identity(apk, root, info)

    def test_apk_identity_missing_metadata_fails_closed_with_output_paths(self):
        info = build.read_source_info()
        with tempfile.TemporaryDirectory() as temp:
            apk = Path(temp) / "app-debug.apk"
            apk.write_bytes(b"compiled fixture")
            with self.assertRaisesRegex(RuntimeError, "Gradle finished but APK finalization failed"):
                build.verify_apk_identity(apk, Path(temp), info)

    def test_apk_identity_aapt2_failure_is_explicit(self):
        info = build.read_source_info()
        with tempfile.TemporaryDirectory() as temp:
            root = Path(temp)
            apk = root / "app-debug.apk"
            apk.write_bytes(b"compiled fixture")
            version, code = build.android_identity(info)
            version += "-debug"
            (root / "output-metadata.json").write_text(json.dumps({
                "applicationId": "org.itom.client.dev.rev7", "elements": [{
                    "versionCode": code, "versionName": version,
                    "outputFile": "app-debug.apk"}]}))
            aapt2 = root / "build-tools" / build.BUILD_TOOLS / ("aapt2.exe" if build.os.name == "nt" else "aapt2")
            aapt2.parent.mkdir(parents=True)
            aapt2.touch()
            with patch("build.subprocess.run", return_value=subprocess.CompletedProcess([], 2, "", "corrupt APK")):
                with self.assertRaisesRegex(RuntimeError, "corrupt APK"):
                    build.verify_apk_identity(apk, root, info)

    def test_android_37_0_tooling_pin_is_consistent(self):
        self.assertEqual("22.0", build.CMDLINE_TOOLS)
        self.assertEqual("37", build.SDK)
        self.assertEqual("0", build.SDK_MINOR)
        self.assertEqual("37.0", build.SDK_PACKAGE)
        gradle = (check_sources.ROOT / "android/app/build.gradle.kts").read_text(encoding="utf-8")
        self.assertRegex(gradle, r"compileSdk\s*=\s*37\b")
        self.assertRegex(gradle, r"compileSdkMinor\s*=\s*0\b")
        self.assertRegex(gradle, r"targetSdk\s*=\s*37\b")
        self.assertRegex(gradle, r"buildFeatures\s*\{[^}]*\bresValues\s*=\s*true")

    def test_source_extracts_with_bytes_intact(self):
        archive = self.archive("itom/file.kt", b"hello")
        build.extract(archive, self.root / "fresh")
        self.assertEqual(b"hello", (self.root / "fresh/itom/file.kt").read_bytes())

    def test_source_packager_excludes_launcher_extraction_tree(self):
        source = self.root / "source"
        source.mkdir()
        (source / ".git").write_text("gitdir: private-worktree-pointer")
        (source / "keep.txt").write_text("keep", encoding="utf-8")
        generated = source / "ITOM_transport-s1_deadbeef"
        generated.mkdir()
        (generated / "must-not-ship.txt").write_text("nested", encoding="utf-8")
        tools = source / "tools"
        tools.mkdir()
        (tools / "archive_main.py").write_text("# launcher\n", encoding="utf-8")
        out = self.root / "packed.zip"
        package_sources.package(source, out)
        with zipfile.ZipFile(out) as bundle:
            names = set(bundle.namelist())
        self.assertIn("itom/keep.txt", names)
        self.assertNotIn("itom/.git", names)
        self.assertNotIn("itom/ITOM_transport-s1_deadbeef/must-not-ship.txt", names)

    def test_zip_launcher_exports_verified_apk_and_server_to_public_dist(self):
        source = self.root / "ITOM_FIELD77_tmp" / "itom" / "dist"
        source.mkdir(parents=True)
        archive = self.root / "ITOM_TRANSPORT_FIELD77_SOURCE.zip"
        archive.write_bytes(b"test")
        info = build.read_source_info()
        apk_name = build.public_apk_name(info)
        apk = source / apk_name
        apk.write_bytes(b"verified-test-apk")
        sha = hashlib.sha256(apk.read_bytes()).hexdigest()
        (source / (apk_name + ".sha256")).write_text(sha + "  " + apk_name + "\n")
        (source / "build-report.json").write_text(json.dumps({"status":"passed", "source_revision":info["revision"], "sha256":sha}))
        sums = []
        for arch in ("amd64", "arm64"):
            name = f"ITOM-0.9.2-Rev3-linux-{arch}"
            data = b"fixture-" + arch.encode()
            (source / name).write_bytes(data)
            sums.append(hashlib.sha256(data).hexdigest() + "  " + name)
        (source / "SHA256SUMS").write_text("\n".join(sums)+"\n")
        (source / "core-report.json").write_text(json.dumps({"status":"passed", "server_built":True, "source_revision":info["revision"]}))
        result = archive_main.export_verified_artifacts(self.root / "ITOM_FIELD77_tmp", archive, info, "all")
        self.assertEqual(7, len(result))
        self.assertEqual(b"verified-test-apk", (self.root / "dist" / apk_name).read_bytes())
        self.assertTrue((self.root / "dist" / "ITOM-0.9.2-Rev3-linux-arm64").is_file())
        # Failed gate and tampered APK must NEVER overwrite the public dist.
        apk.write_bytes(b"tampered")
        with self.assertRaisesRegex(RuntimeError, "APK checksum mismatch"):
            archive_main.export_verified_artifacts(self.root / "ITOM_FIELD77_tmp", archive, info, "all")
        self.assertEqual(b"verified-test-apk", (self.root / "dist" / apk_name).read_bytes())

    def test_zip_launcher_refuses_incomplete_server_exports(self):
        source = self.root / "extract" / "itom" / "dist"
        source.mkdir(parents=True)
        info = build.read_source_info()
        (source / "core-report.json").write_text(json.dumps({"status":"passed", "server_built":True, "source_revision":info["revision"]}))
        (source / "SHA256SUMS").write_text("")
        with self.assertRaisesRegex(RuntimeError, "Missing or invalid server binary"):
            archive_main.export_verified_artifacts(self.root / "extract", self.root / "source.zip", info, "server")
        self.assertFalse((self.root / "dist").exists())

    def test_parent_traversal_rejected(self):
        with self.assertRaises(RuntimeError):
            build.extract(self.archive("../outside"), self.root / "fresh")
        self.assertFalse((self.root / "outside").exists())

    def test_absolute_path_rejected(self):
        with self.assertRaises(RuntimeError):
            build.extract(self.archive("/absolute/file"), self.root / "fresh")

    def test_windows_paths_rejected_on_all_hosts(self):
        for name in ("C:/file", "..\\file", "file:stream"):
            with self.subTest(name=name), self.assertRaises(RuntimeError):
                build.child_path(self.root, name)

    def test_zip_symlink_rejected(self):
        archive = self.archive("link", b"../../outside", stat.S_IFLNK | 0o777)
        with self.assertRaises(RuntimeError):
            build.extract(archive, self.root / "fresh")

    def test_existing_directory_is_not_overwritten(self):
        destination = self.root / "edited"; destination.mkdir()
        (destination / "file").write_text("user changes", encoding="utf-8")
        with self.assertRaises(FileExistsError):
            build.extract(self.archive("file", b"new"), destination)
        self.assertEqual("user changes", (destination / "file").read_text(encoding="utf-8"))

    def test_source_check_with_windows_cp1252_default(self):
        # Exercise the actual checker against its real Cyrillic sources, without
        # relying on the Linux runner's UTF-8 locale or Python UTF-8 mode.
        original_open = io.open

        def windows_open(file, mode="r", buffering=-1, encoding=None,
                         errors=None, newline=None, closefd=True, opener=None):
            if "b" not in mode and encoding in (None, "locale"):
                encoding = "cp1252"
            return original_open(file, mode, buffering, encoding, errors,
                                 newline, closefd, opener)

        control = self.root / "cyrillic.kt"
        control.write_bytes("// я: проверка UTF-8\n".encode("utf-8"))
        with patch("io.open", side_effect=windows_open):
            with self.assertRaises(UnicodeDecodeError):
                control.read_text(encoding="locale")
            _, _, kotlin_tests = check_sources.validate_source_tree()
        self.assertGreaterEqual(kotlin_tests, 25)

    def test_old_invalid_invitation_fixture_is_rejected_before_gradle(self):
        source = (check_sources.ROOT / "android/domain/src/test/kotlin/org/itom/domain/DomainTest.kt").read_text(encoding="utf-8")
        check_sources.validate_invitation_fixture(source)
        broken, count = re.subn(
            r'(private fun invitation\(\) = Invitation\(2, profile\(\), ")[a-f0-9]+(")',
            lambda match: match.group(1) + "abcdef1234567890abcdef1234567890ab" + match.group(2), source, count=1)
        self.assertEqual(1, count)
        with self.assertRaisesRegex(RuntimeError, "pair_id must be 32"):
            check_sources.validate_invitation_fixture(broken)

    def test_wrong_revision_does_not_start_checks_downloads_or_gradle(self):
        with patch("build.subprocess.run") as run, patch("build.ensure_jdk") as jdk:
            with self.assertRaisesRegex(RuntimeError, "Wrong source revision"):
                build.main(["android", "--expect-revision", "obsolete-revision"])
            run.assert_not_called()
            jdk.assert_not_called()

    def test_junit_failure_summary_contains_actual_reason(self):
        reports = self.root / "android/domain/build/test-results/test"; reports.mkdir(parents=True)
        (reports / "TEST-domain.xml").write_text(
            '<testsuite name="DomainTest" tests="2" failures="1" errors="0" skipped="0">'
            '<testcase name="good" classname="DomainTest"/>'
            '<testcase name="bad" classname="DomainTest"><failure type="ComparisonFailure" '
            'message="expected: INVITATION_CREDENTIALS; actual: PAIR_ID">trace</failure></testcase>'
            '</testsuite>', encoding="utf-8")
        result = build.test_report_summary(self.root)
        self.assertTrue(result["available"])
        self.assertEqual(2, result["tests"])
        self.assertEqual(1, result["failures"])
        self.assertIn("actual: PAIR_ID", result["failed_cases"][0]["message"])
        self.assertEqual([], result["report_errors"])

    def test_invalid_junit_report_does_not_hide_gradle_error(self):
        reports = self.root / "android/domain/build/test-results/test"; reports.mkdir(parents=True)
        (reports / "TEST-broken.xml").write_text("<testsuite", encoding="utf-8")
        result = build.test_report_summary(self.root)
        self.assertEqual(1, len(result["report_errors"]))

    def test_missing_junit_report_is_explicitly_unavailable(self):
        self.assertFalse(build.test_report_summary(self.root)["available"])

    def test_lint_reports_all_errors_and_preserves_full_reports(self):
        reports = self.root / "android/app/build/reports"; reports.mkdir(parents=True)
        xml = ('<?xml version="1.0" encoding="UTF-8"?><issues format="6">'
               '<issue id="StartActivityAndCollapseDeprecated" severity="Error" message="Use PendingIntent">'
               '<location file="C:/source/ItomTileService.kt" line="18" column="7"/></issue>'
               '<issue id="LocalContextCastToActivity" severity="Error" message="Use LocalActivity">'
               '<location file="C:/source/ProfileScreens.kt" line="142"/></issue>'
               '<issue id="ExampleFatal" severity="Fatal" message="fatal detail"/>'
               '<issue id="ExampleWarning" severity="Warning" message="предупреждение &amp; detail"/>'
               '</issues>')
        (reports / "lint-results.xml").write_text(xml, encoding="utf-8")
        full_text = "All diagnostics, including warnings: проверка\n"
        (reports / "lint-results.txt").write_text(full_text, encoding="utf-8")
        dist = self.root / "dist"; dist.mkdir()
        log = dist / "build.log"; log.write_text("Original Gradle error\n", encoding="utf-8")
        output = io.StringIO()
        with redirect_stdout(output):
            result = build.save_lint_diagnostics(self.root, dist, log)
        self.assertTrue(result["available"])
        self.assertEqual((3, 1), (result["errors"], result["warnings"]))
        for issue in ("StartActivityAndCollapseDeprecated", "LocalContextCastToActivity", "ExampleFatal"):
            self.assertIn(issue, output.getvalue())
            self.assertIn(issue, log.read_text(encoding="utf-8"))
        self.assertTrue(log.read_text(encoding="utf-8").startswith("Original Gradle error\n"))
        self.assertEqual(xml, (dist / "lint-results.xml").read_text(encoding="utf-8"))
        self.assertEqual(full_text, (dist / "lint-results.txt").read_text(encoding="utf-8"))
        self.assertIn("предупреждение & detail", result["issues"][-1]["message"])

    def test_lint_missing_report_is_not_reported_as_passed(self):
        self.assertFalse(build.lint_report_summary(self.root)["available"])

    def test_lint_malformed_report_does_not_raise_over_gradle_failure(self):
        reports = self.root / "android/app/build/reports"; reports.mkdir(parents=True)
        for contents in ("<issues", "<unrelated/>"):
            with self.subTest(contents=contents):
                (reports / "lint-results.xml").write_text(contents, encoding="utf-8")
                result = build.lint_report_summary(self.root)
                self.assertFalse(result["available"])
                self.assertTrue(result["report_errors"])

    def test_repeated_build_cannot_reuse_stale_lint_errors(self):
        reports = self.root / "android/app/build/reports"; reports.mkdir(parents=True)
        dist = self.root / "dist"; dist.mkdir()
        for directory in (reports, dist):
            for name in build.LINT_REPORT_NAMES:
                (directory / name).write_text("old result", encoding="utf-8")
            (directory / "keep.txt").write_text("unrelated", encoding="utf-8")
        (dist / "lint-summary.json").write_text("{}", encoding="utf-8")
        build.reset_lint_reports(self.root, dist)
        self.assertFalse(build.lint_report_summary(self.root)["available"])
        self.assertFalse((dist / "lint-summary.json").exists())
        for directory in (reports, dist):
            self.assertEqual(["keep.txt"], sorted(p.name for p in directory.iterdir()))

    def test_tar_device_rejected(self):
        archive = self.root / "tool.tar"
        with tarfile.open(archive, "w") as out:
            item = tarfile.TarInfo("device"); item.type = tarfile.CHRTYPE; out.addfile(item)
        with self.assertRaises(RuntimeError):
            build.extract(archive, self.root / "fresh")

    def test_tar_escape_link_rejected(self):
        archive = self.root / "tool.tar"
        with tarfile.open(archive, "w") as out:
            item = tarfile.TarInfo("lib/link"); item.type = tarfile.SYMTYPE; item.linkname = "../../outside"; out.addfile(item)
        with self.assertRaises(RuntimeError):
            build.extract(archive, self.root / "fresh")

    def test_cached_download_requires_expected_hash(self):
        target = self.root / "tool.zip"; target.write_bytes(b"correct")
        with patch("urllib.request.urlopen", side_effect=AssertionError("must not download verified cache")):
            self.assertEqual(target, build.download("https://example.invalid/tool", target, hashlib.sha256(b"correct").hexdigest()))

    def test_bad_download_never_replaces_cached_file(self):
        class Response(io.BytesIO):
            url = "https://example.invalid/tool"
        target = self.root / "tool.zip"; target.write_bytes(b"old")
        with patch("urllib.request.urlopen", return_value=Response(b"bad")), self.assertRaises(RuntimeError):
            build.download("https://example.invalid/tool", target, hashlib.sha256(b"correct").hexdigest())
        self.assertEqual(b"old", target.read_bytes())
        self.assertFalse(target.with_suffix(".zip.partial").exists())

    def test_https_downgrade_rejected(self):
        class Response(io.BytesIO):
            url = "http://example.invalid/tool"
        with patch("urllib.request.urlopen", return_value=Response(b"data")), self.assertRaises(RuntimeError):
            build.fetch("https://example.invalid/tool")

    def test_gradle_checksum_has_full_sha256_length(self):
        self.assertRegex(build.GRADLE_SHA256, r"^[a-f0-9]{64}$")

    def test_complete_app_catalog_permission_is_reviewed_before_gradle(self):
        source = (check_sources.ROOT / "android/app/src/main/AndroidManifest.xml").read_text(encoding="utf-8")
        manifest = ET.fromstring(source)
        check_sources.validate_package_visibility(manifest)
        for change in ("remove_permission", "remove_exception", "duplicate_permission"):
            with self.subTest(change=change):
                manifest = ET.fromstring(source)
                permission = next(p for p in manifest.findall("uses-permission") if p.get("{http://schemas.android.com/apk/res/android}name") == "android.permission.QUERY_ALL_PACKAGES")
                if change == "remove_permission": manifest.remove(permission)
                elif change == "remove_exception": permission.attrib.pop("{http://schemas.android.com/tools}ignore")
                else: manifest.append(ET.fromstring(ET.tostring(permission)))
                with self.assertRaises(RuntimeError): check_sources.validate_package_visibility(manifest)

    def test_package_visibility_exception_cannot_become_global(self):
        source = (check_sources.ROOT / "android/app/src/main/AndroidManifest.xml").read_text(encoding="utf-8")
        for target in ("root", "application"):
            manifest = ET.fromstring(source)
            node = manifest if target == "root" else manifest.find("application")
            node.set("{http://schemas.android.com/tools}ignore", "all")
            with self.assertRaisesRegex(RuntimeError, "broad manifest"):
                check_sources.validate_package_visibility(manifest)

    def test_build_gate_regressions_are_rejected_before_downloads(self):
        source = (check_sources.ROOT / "android/app/build.gradle.kts").read_text(encoding="utf-8")
        check_sources.validate_build_gates(source)
        broken = [source.replace("abortOnError = true", "abortOnError = false"),
                  source + '\nandroid.lint { baseline = file("lint-baseline.xml") }',
                  source + '\nandroid.lint { disable += "NewApi" }',
                  source + '\ntasks.withType<Test> { ignoreFailures = true }']
        for value in broken:
            with self.assertRaises(RuntimeError): check_sources.validate_build_gates(value)

    def run_simulated_gradle(self, exit_code, junit_xml, lint_xml):
        """Exercise the real launcher orchestration without Java, Gradle, SDK, or network."""
        info = build.read_source_info()
        (self.root / "source-info.json").write_text(json.dumps(info), encoding="utf-8")
        root = self.root

        class Process:
            stdout = io.StringIO(
                "> Task :domain:test" + (" FAILED" if exit_code else "") + "\n"
                "> Task :app:lintDebug" + (" FAILED" if exit_code else "") + "\n")
            def __enter__(self):
                junit = root / "android/domain/build/test-results/test/TEST-domain.xml"
                junit.parent.mkdir(parents=True, exist_ok=True); junit.unlink(missing_ok=True)
                if junit_xml is not None: junit.write_text(junit_xml, encoding="utf-8")
                lint = root / "android/app/build/reports/lint-results.xml"
                lint.parent.mkdir(parents=True, exist_ok=True)
                if lint_xml is not None: lint.write_text(lint_xml, encoding="utf-8")
                apk = root / "android/app/build/outputs/apk/debug/app-debug.apk"
                apk.parent.mkdir(parents=True, exist_ok=True)
                with zipfile.ZipFile(apk, "w") as native_fixture:
                    for abi in ("arm64-v8a", "armeabi-v7a"):
                        native_fixture.writestr(f"lib/{abi}/libgojni.so", info["revision"].encode("ascii"))
                version, code = build.android_identity(info)
                (apk.parent / "output-metadata.json").write_text(json.dumps({"applicationId":"org.itom.client.dev.rev7",
                    "elements":[{"versionCode": code,
                    "versionName": version + "-debug", "outputFile": "app-debug.apk"}]}))
                return self
            def __exit__(self, *args): self.stdout.close()
            def wait(self): return exit_code

        output = io.StringIO(); failure = None; result = None
        aapt2 = root / "sdk" / "build-tools" / build.BUILD_TOOLS / ("aapt2.exe" if build.os.name == "nt" else "aapt2")
        aapt2.parent.mkdir(parents=True, exist_ok=True)
        aapt2.touch()
        with patch("build.ROOT", self.root), \
             patch("build.platform.system", return_value="Linux"), \
             patch("build.platform.machine", return_value="x86_64"), \
             patch("build.ensure_jdk", return_value=root / "jdk"), \
             patch("build.ensure_gradle", return_value=root / "gradle"), \
             patch("build.ensure_sdk", return_value=root / "sdk"), \
             patch("build_android_core.run"), \
             patch("build.subprocess.run", return_value=subprocess.CompletedProcess([], 0,
                   stdout=f"package: name='org.itom.client.dev.rev7' versionCode='{build.android_identity(info)[1]}' versionName='{build.android_identity(info)[0]}-debug'\n",
                   stderr="test JDK fixture")), \
             patch("build.subprocess.Popen", return_value=Process()) as process, redirect_stdout(output):
            try: result = build.main(["android", "--cache", str(root / "cache")])
            except RuntimeError as error: failure = str(error)
            command = process.call_args.args[0]
            self.assertIn("--continue", command)
            for task in (":domain:test", ":app:lintDebug", ":app:compileReleaseKotlin", ":app:assembleDebug"):
                self.assertIn(task, command)
        self.assertFalse((root / "cache/build.lock").exists())
        return result, failure, output.getvalue()

    def test_failed_gradle_preserves_all_diagnostics_and_never_exports_apk(self):
        _, error, output = self.run_simulated_gradle(1,
            '<testsuite tests="1" failures="1"><testcase name="bad"><failure message="actual failure"/></testcase></testsuite>',
            '<issues><issue id="FirstIssue" severity="Error" message="first"/>'
            '<issue id="SecondIssue" severity="Error" message="second"/>'
            '<issue id="ExampleWarning" severity="Warning" message="warning"/></issues>')
        self.assertIn("Gradle failed", error)
        for expected in ("FirstIssue", "SecondIssue", "ExampleWarning", "actual failure"):
            self.assertIn(expected, output)
        self.assertEqual([], list((self.root / "dist").glob("*.apk")))
        report = json.loads((self.root / "dist/build-report.json").read_text(encoding="utf-8"))
        self.assertEqual("failed", report["status"])
        self.assertEqual(2, report["lint"]["errors"])

    def test_failed_rebuild_removes_only_current_revision_artifacts(self):
        self.run_simulated_gradle(0, '<testsuite tests="1" failures="0"/>', '<issues/>')
        artifact = next((self.root / "dist").glob("*.apk"))
        checksum = artifact.with_name(artifact.name + ".sha256")
        self.assertTrue(checksum.exists())
        unrelated = self.root / "dist/keep-another-version.apk"
        unrelated.write_bytes(b"user artifact")
        _, error, _ = self.run_simulated_gradle(1, None, None)
        self.assertIn("Gradle failed", error)
        self.assertFalse(artifact.exists()); self.assertFalse(checksum.exists())
        self.assertEqual(b"user artifact", unrelated.read_bytes())

    def test_bootstrap_failure_also_invalidates_previous_apk(self):
        info = build.read_source_info()
        dist = self.root / "dist"; dist.mkdir()
        artifact = dist / build.public_apk_name(info)
        artifact.write_bytes(b"stale")
        checksum = artifact.with_name(artifact.name + ".sha256"); checksum.write_bytes(b"stale")
        with patch("build.ROOT", self.root), patch("build.read_source_info", return_value=info), \
             patch("build.subprocess.run"), patch("build.ensure_jdk", side_effect=RuntimeError("bootstrap failure")), redirect_stdout(io.StringIO()):
            with self.assertRaisesRegex(RuntimeError, "bootstrap failure"):
                build.main(["android", "--cache", str(self.root / "cache")])
        self.assertFalse(artifact.exists()); self.assertFalse(checksum.exists())
        self.assertFalse((self.root / "cache/build.lock").exists())

    def test_zero_gradle_exit_cannot_hide_failed_or_missing_reports(self):
        good_tests = '<testsuite tests="32" failures="0" errors="0" skipped="0"/>'
        bad_pairs = [(None, '<issues/>'), (good_tests, None),
                     ('<testsuite tests="32" failures="1"/>', '<issues/>'),
                     ('<testsuite tests="32" skipped="1"/>', '<issues/>'),
                     ('<testsuite tests="0"/>', '<issues/>'),
                     (good_tests, '<issues><issue id="AnyIssue" severity="Error"/></issues>'),
                     (good_tests, '<issues')]
        for tests, lint in bad_pairs:
            with self.subTest(tests=tests, lint=lint):
                result, error, _ = self.run_simulated_gradle(0, tests, lint)
                self.assertIsNone(result); self.assertIsNotNone(error)
                self.assertEqual([], list((self.root / "dist").glob("*.apk")))
                report = json.loads((self.root / "dist/build-report.json").read_text())
                self.assertEqual("failed", report["status"])
                self.assertEqual("post_gradle_domain_and_lint_gate", report["failed_stage"])
                self.assertTrue(report["error"])

    def test_successful_checked_run_exports_only_the_expected_artifact(self):
        result, error, _ = self.run_simulated_gradle(0,
            '<testsuite tests="32" failures="0" errors="0" skipped="0"/>',
            '<issues><issue id="ExampleWarning" severity="Warning" message="warning"/></issues>')
        self.assertEqual(0, result); self.assertIsNone(error)
        artifacts = list((self.root / "dist").glob("*.apk"))
        self.assertEqual(1, len(artifacts))
        self.assertEqual(build.public_apk_name(build.read_source_info()), artifacts[0].name)
        report = json.loads((self.root / "dist/build-report.json").read_text(encoding="utf-8"))
        self.assertEqual("passed", report["status"])
        self.assertEqual(build.digest(artifacts[0]), report["sha256"])

    def test_report_write_failure_releases_lock_and_preserves_original_error(self):
        original_write = Path.write_text
        info = build.read_source_info()
        def failing_write(path, *args, **kwargs):
            if path.name == "build-report.json": raise OSError("report storage unavailable")
            return original_write(path, *args, **kwargs)
        with patch("build.ROOT", self.root), patch("build.read_source_info", return_value=info), \
             patch("build.subprocess.run"), patch("build.ensure_jdk", side_effect=RuntimeError("bootstrap fixture failure")), \
             patch.object(Path, "write_text", failing_write), redirect_stdout(io.StringIO()), redirect_stderr(io.StringIO()):
            with self.assertRaisesRegex(RuntimeError, "bootstrap fixture failure"):
                build.main(["android", "--cache", str(self.root / "cache")])
        self.assertFalse((self.root / "cache/build.lock").exists())

    def test_invalid_output_path_never_acquires_cache_lock(self):
        (self.root / "dist").write_text("user file", encoding="utf-8")
        info = build.read_source_info()
        with patch("build.ROOT", self.root), patch("build.read_source_info", return_value=info), \
             patch("build.subprocess.run"), patch("build.ensure_jdk") as jdk, redirect_stdout(io.StringIO()):
            with self.assertRaises(FileExistsError):
                build.main(["android", "--cache", str(self.root / "cache")])
            jdk.assert_not_called()
        self.assertFalse((self.root / "cache/build.lock").exists())

if __name__ == "__main__":
    unittest.main(verbosity=1)
