#!/usr/bin/env python3
"""Offline source/package checks. This is explicitly not a Kotlin compiler or Android test."""
import ast
from pathlib import Path
import re
import subprocess
import sys
import xml.etree.ElementTree as ET
from build import SDK, SDK_MINOR, read_source_info, android_identity
from build_core import verify_imap_fork

ROOT = Path(__file__).resolve().parents[1]


def check(condition, message):
    if not condition:
        raise RuntimeError(message)


def validate_invitation_fixture(source: str):
    # Catch the previously shipped malformed positive fixture before a costly Gradle run.
    match = re.search(r'private fun invitation\(\) = Invitation\(2, profile\(\), "([^"\n]+)"', source)
    check(match is not None, "Cannot find the positive invitation fixture; update its source check")
    check(re.fullmatch(r"[a-f0-9]{32}", match.group(1)) is not None,
          "Invalid positive invitation fixture: pair_id must be 32 lowercase hex characters")


def validate_package_visibility(manifest):
    ns = "{http://schemas.android.com/apk/res/android}"
    ignore = "{http://schemas.android.com/tools}ignore"
    permission = "android.permission.QUERY_ALL_PACKAGES"
    matches = [node for node in manifest.findall("uses-permission") if node.get(ns + "name") == permission]
    check(len(matches) == 1, "Full per-app routing requires exactly one QUERY_ALL_PACKAGES declaration")
    check(matches[0].get(ignore) == "QueryAllPackagesPermission", "Document the intentional package-visibility lint exception on the permission only")
    for node in manifest.iter():
        if ignore in node.attrib:
            check(node is matches[0], "A broad manifest lint suppression is not allowed")


def validate_build_gates(app_gradle: str):
    check(re.search(rf"compileSdk\s*=\s*{re.escape(SDK)}\b", app_gradle),
          "Gradle compileSdk must match the pinned Android SDK")
    check(re.search(rf"compileSdkMinor\s*=\s*{re.escape(SDK_MINOR)}\b", app_gradle),
          "Gradle compileSdkMinor must match the pinned Android SDK minor")
    check(re.search(rf"targetSdk\s*=\s*{re.escape(SDK)}\b", app_gradle),
          "Gradle targetSdk must match the pinned Android SDK")
    check(re.search(r"buildFeatures\s*\{[^}]*\bresValues\s*=\s*true", app_gradle, re.S),
          "AGP 9 requires buildFeatures.resValues=true because build types define resValue entries")
    check(re.search(r"abortOnError\s*=\s*true", app_gradle), "Android Lint must remain fatal on errors")
    check(not re.search(r"(?:abortOnError\s*=\s*false|ignoreFailures\s*=\s*true|\bbaseline\b|\bdisable\s*(?:[+.=\(]))", app_gradle),
          "Do not disable build gates or blanket-suppress lint checks")
    check("kotlinOptions" not in app_gradle and "compilerOptions" in app_gradle and "JvmTarget.JVM_17" in app_gradle,
          "Use typed Kotlin compilerOptions for JVM 17")


def validate_release_identity(root: Path):
    info = read_source_info(root)
    name, code = android_identity(info)
    app = (root / "android/app/build.gradle.kts").read_text(encoding="utf-8")
    if info.get("release") in ("Rev10","Rev1","Rev2","Rev3","Rev4","Rev5","Rev6","Rev7","Rev8","Rev9"):
        check('applicationIdSuffix = ".dev.rev7"' in app,
              "Rev10 test APK package differs from its separate-install identity")
    check(re.search(r'versionName\s*=\s*"' + re.escape(name) + r'"', app) is not None,
          "Android versionName differs from public FIELD number")
    check(re.search(r'versionCode\s*=\s*' + str(code) + r'\b', app) is not None,
          "Android versionCode differs from public FIELD number")
    for relative in ("core/cmd/itom/main.go", "core/manager/trace.go"):
        source = (root / relative).read_text(encoding="utf-8")
        check('"' + info["revision"] + '"' in source,
              "Server revision mismatch in " + relative)
    return info


def validate_source_tree(root: Path = ROOT):
    validate_release_identity(root)
    verify_imap_fork(root / "core")
    python_files = list((root / "tools").glob("*.py"))
    for file in python_files:
        ast.parse(file.read_text(encoding="utf-8"), filename=str(file))
    xml_files = list((root / "android/app/src").rglob("*.xml"))
    for file in xml_files:
        ET.parse(file)
    ns = "{http://schemas.android.com/apk/res/android}"
    manifest = ET.parse(root / "android/app/src/main/AndroidManifest.xml").getroot()
    validate_package_visibility(manifest)
    validate_build_gates((root / "android/app/build.gradle.kts").read_text(encoding="utf-8"))
    application = manifest.find("application")
    check(application.get(ns + "allowBackup") == "false", "Backup must stay disabled")
    check(application.get(ns + "usesCleartextTraffic") == "false", "Cleartext must stay disabled")
    services = application.findall("service")
    by_name = {node.get(ns + "name"): node for node in services}
    check(set(by_name) == {".backend.ItomTileService", ".backend.ItomVpnService"}, "Unexpected Android service")
    check(by_name[".backend.ItomVpnService"].get(ns + "permission") == "android.permission.BIND_VPN_SERVICE" and
          by_name[".backend.ItomVpnService"].get(ns + "exported") == "false", "VPN service must be protected")
    factories = list((root / "android/app/src").rglob("BackendFactory.kt"))
    common = root / "android/app/src/main/java/org/itom/client/backend/BackendFactory.kt"
    check(factories == [common] and "NativeBackend" in common.read_text(encoding="utf-8"), "Both variants must use the native backend")
    for file in (root / "android/app/src").rglob("*.kt"):
        source = file.read_text(encoding="utf-8")
        check(not any(token in source for token in ("MockBackend", "MockSessionService", "simulateFailure", "addDemoProfile", "ITOM Dev")),
              "Runtime demo functions or obsolete application labels remain: " + str(file))
    # Historical migration fingerprints are data, not interface text. Every
    # other Android runtime source and resource must remain English.
    for source_dir in (root / "android/app/src", root / "android/domain/src/main"):
        for file in source_dir.rglob("*"):
            if file.suffix not in (".kt", ".xml") or file.name == "VaultMigration.kt":
                continue
            check(not re.search(r"[А-Яа-яЁё]", file.read_text(encoding="utf-8")),
                  "Non-English interface text remains: " + str(file))
    test_source = root / "android/domain/src/test/kotlin/org/itom/domain/DomainTest.kt"
    test_text = test_source.read_text(encoding="utf-8")
    validate_invitation_fixture(test_text)
    kotlin_tests = sum(len(re.findall(r"@Test\b", file.read_text(encoding="utf-8")))
                       for file in test_source.parent.rglob("*.kt"))
    check(kotlin_tests >= 25, "Missing domain regression tests")
    return len(python_files), len(xml_files), kotlin_tests


def main():
    python_files, xml_files, kotlin_tests = validate_source_tree()
    # Failure-path tests intentionally print FAILED and fake export locations.
    # Keep their output in logs so it cannot be mistaken for this user's build.
    logs = ROOT / "dist" / "source-checks"
    logs.mkdir(parents=True, exist_ok=True)
    for name in ("test_launcher", "test_core_tools", "test_rework_gate"):
        result = subprocess.run([sys.executable, str(ROOT / "tools" / (name + ".py"))],
                                capture_output=True, text=True, encoding="utf-8", errors="replace")
        (logs / (name + ".log")).write_text(result.stdout + result.stderr, encoding="utf-8")
        if result.returncode:
            print(result.stdout + result.stderr, file=sys.stderr)
            raise RuntimeError(name + " failed; see " + str(logs / (name + ".log")))
        print(name + ": passed", flush=True)
    print(f"Offline source checks passed: {python_files} Python files, {xml_files} XML files, source-set boundaries.")
    print(f"{kotlin_tests} Kotlin test cases are present but NOT executed by this check.")
    print("Android compilation, lint, and device tests are NOT performed by this command.")
    print("Go compilation and Go tests are NOT performed; use target core for the local Go gate.")
    return 0


if __name__ == "__main__":
    try:
        raise SystemExit(main())
    except (RuntimeError, OSError, ET.ParseError, SyntaxError) as error:
        print("Source check failed:", error, file=sys.stderr)
        raise SystemExit(1)
