#!/usr/bin/env python3
"""Local-only build launcher. Python 3.10+, no pip packages, no system-wide writes."""
from __future__ import annotations

import argparse
import hashlib
import json
import os
from pathlib import Path, PurePosixPath
import platform
import re
import shutil
import stat
import subprocess
import sys
import tarfile
import tempfile
import time
import urllib.parse
import urllib.request
import xml.etree.ElementTree as ET
import zipfile

ROOT = Path(__file__).resolve().parents[1]
GRADLE = "9.3.1"
GRADLE_SHA256 = "b266d5ff6b90eada6dc3b20cb090e3731302e553a27c5d3e4df1f0d76beaff06"
CMDLINE_TOOLS = "22.0"
BUILD_TOOLS = "36.0.0"
SDK = "37"
SDK_MINOR = "0"
SDK_PACKAGE = SDK + "." + SDK_MINOR
SDK_CHANNEL = "3"  # Include all repository channels; API 37.0 is an exact package path.
LINT_REPORT_NAMES = ("lint-results.xml", "lint-results.txt")


def fail(message: str):
    raise RuntimeError(message)


def read_source_info(root: Path = ROOT) -> dict:
    info = json.loads((root / "source-info.json").read_text(encoding="utf-8"))
    if not isinstance(info, dict) or not all(isinstance(info.get(k), str) for k in ("version", "revision", "archive_name")):
        fail("Invalid source-info.json")
    if not re.fullmatch(r"[A-Za-z0-9_.-]+", info["version"]) or not re.fullmatch(r"[A-Za-z0-9_-]+", info["revision"]):
        fail("Invalid source version or revision")
    if not re.fullmatch(r"[A-Za-z0-9_.-]+\.zip", info["archive_name"]):
        fail("Invalid source archive name")
    if info.get("release") in ("RC2","RC5","RC6","RC7","RC8","RC9","RC10"):
        if (info["version"] != "1.0.0" or info["revision"] != "itom-1-0-0-" + info["release"].lower()
                or info["archive_name"] != f"ITOM-1.0.0-{info['release']}-sources.zip"
                or info.get("android_version_name") != "1.0.0-" + info["release"].lower()
                or info.get("android_version_code") != {"RC2":10001,"RC5":10004,"RC6":10005,"RC7":10006,"RC8":10007,"RC9":10008,"RC10":10009}[info["release"]]
                or info.get("android_application_id") != ("org.itom.client" if info.get("release") in ("RC7", "RC8", "RC9", "RC10") else "org.itom.client.dev.rev7")):
            fail("Inconsistent ITOM 1.0.0 release identity")
        return info
    if info["version"] == "0.9.2" and info.get("release") in ("Rev1", "Rev2", "Rev3", "Rev4", "Rev5", "Rev6", "Rev7", "Rev8", "Rev9", "Rev10", "Rev11"):
        release = info["release"]
        number = int(release[3:])
        if (info["version"] != "0.9.2" or info["revision"] != f"transport-rework3-rev{number}"
                or info["archive_name"] != f"ITOM-0.9.2-{release}-sources.zip"
                or info.get("android_version_name") != f"0.9.2-{release}"
                or info.get("android_version_code") != 9200 + number
                or info.get("android_application_id") != ("org.itom.client" if info.get("release") in ("RC7", "RC8", "RC9", "RC10") else "org.itom.client.dev.rev7")):
            fail(f"Inconsistent ITOM-0.9.2-{release} release identity")
        return info
    if info.get("release") == "Rev10":
        if (info["version"] != "0.9.1" or info["revision"] != "transport-rework2-rev10"
                or info["archive_name"] != "ITOM-0.9.1-Rev10-sources.zip"
                or info.get("android_version_name") != "0.9.1-Rev10"
                or info.get("android_version_code") != 9089
                or info.get("android_application_id") != ("org.itom.client" if info.get("release") in ("RC7", "RC8", "RC9", "RC10") else "org.itom.client.dev.rev7")):
            fail("Inconsistent ITOM-0.9.1-Rev10 release identity")
        return info
    if not re.fullmatch(r"FIELD[1-9][0-9]*", info.get("release", "")):
        fail("source-info.json must declare public FIELD release")
    release_no = info["release"][5:]
    if not info["revision"].endswith("-field" + release_no):
        fail("Source revision and public FIELD release differ")
    if info["archive_name"] != f"ITOM_TRANSPORT_{info['release']}_SOURCE.zip":
        fail("Archive name and public FIELD release differ")
    return info


def android_identity(info):
    if info.get("release") in ("Rev10","Rev1","Rev2","Rev3","Rev4","Rev5","Rev6","Rev7","Rev8","Rev9","Rev11","RC2","RC5","RC6","RC7","RC8","RC9","RC10"):
        return info["android_version_name"], info["android_version_code"]
    return f"{info['version']}-field{info['release'][5:]}", 9000 + int(info["release"][5:])


def public_apk_name(info):
    if info.get("release") in ("Rev10","Rev1","Rev2","Rev3","Rev4","Rev5","Rev6","Rev7","Rev8","Rev9","Rev11","RC2","RC5","RC6","RC7","RC8","RC9","RC10"):
        return f"ITOM-{info['version']}-{info['release']}-debug.apk"
    return f"ITOM_{info['release']}_debug.apk"


def verify_apk_identity(apk: Path, sdk: Path, info: dict):
    """Fail closed if Gradle produced an older APK or the other applicationId."""
    metadata_file = apk.with_name("output-metadata.json")
    if not apk.is_file() or not metadata_file.is_file():
        fail("Gradle finished but APK finalization failed: expected " + str(apk) +
             " and " + str(metadata_file) + "; refusing to export")
    metadata = json.loads(metadata_file.read_text(encoding="utf-8"))
    expected_name, expected_code = android_identity(info)
    expected_name += "-debug"
    if (metadata.get("applicationId") != info["android_application_id"] or
        not any(isinstance(e, dict) and e.get("versionName") == expected_name and e.get("versionCode") == expected_code and
                e.get("outputFile") == apk.name for e in metadata.get("elements", []))):
        fail("Gradle finished but APK output-metadata identity differs from " + info['release'] +
             ". Expected " + expected_name + " (" + str(expected_code) +
             ", " + info["android_application_id"] + ") at " + str(metadata_file))
    aapt2 = sdk / "build-tools" / BUILD_TOOLS / ("aapt2.exe" if os.name == "nt" else "aapt2")
    if not aapt2.is_file():
        fail("Gradle finished, but Android build-tools aapt2 is missing: " + str(aapt2))
    result = subprocess.run([str(aapt2), "dump", "badging", str(apk)], capture_output=True,
                            text=True, encoding="utf-8", errors="replace")
    if result.returncode:
        fail("Gradle finished, but APK identity check (aapt2) failed: " +
             (result.stderr or result.stdout or "unknown aapt2 error")[-1200:])
    line = next((x for x in result.stdout.splitlines() if x.startswith("package: ")), "")
    # Match *whole* aapt2 attribute names: compileSdkVersionCodename='17'
    # contains a suffix "name='17'" that must never shadow package name.
    found = dict(re.findall(r"(?<!\S)(name|versionCode|versionName)='([^']*)'", line))
    if found != {"name":info["android_application_id"], "versionCode":str(expected_code), "versionName":expected_name}:
        fail("Compiled APK identity differs from " + info['release'] + ": " + str(found))
    return {"application_id": found["name"], "version_code": expected_code, "version_name": expected_name}


def require_revision(info: dict, expected: str | None):
    if expected is not None and info["revision"] != expected:
        fail(f"Wrong source revision: expected {expected}, found {info['revision']}. Download the matching source archive.")


def verify_apk_native_revision(apk: Path, info: dict):
    """A new manifest must not conceal an older cached native transport."""
    native = {}
    with zipfile.ZipFile(apk) as archive:
        for abi in ("arm64-v8a", "armeabi-v7a"):
            name = f"lib/{abi}/libgojni.so"
            if name not in archive.namelist():
                fail("Compiled APK is missing native transport: " + name)
            raw = archive.read(name)
            if info["revision"].encode("ascii") not in raw:
                fail("Compiled APK native transport revision differs from " + info["revision"] + ": " + name)
            native[name] = hashlib.sha256(raw).hexdigest()
    return native


def test_report_summary(root: Path = ROOT, relative: str = "android/domain/build/test-results/test") -> dict:
    """Read available JUnit XML; reports can belong to up-to-date Gradle tasks."""
    result = {"tests": 0, "failures": 0, "errors": 0, "skipped": 0, "failed_cases": [], "report_errors": []}
    reports = sorted((root / relative).glob("TEST-*.xml"))
    result["available"] = bool(reports)
    for report in reports:
        try:
            if report.stat().st_size > 4 * 1024 * 1024:
                raise ValueError("report exceeds 4 MiB")
            suite = ET.parse(report).getroot()
            if suite.tag != "testsuite":
                raise ValueError("unexpected JUnit report root")
            for field in ("tests", "failures", "errors", "skipped"):
                result[field] += int(suite.get(field, "0"))
            for case in suite.iter("testcase"):
                problems = list(case.findall("failure")) + list(case.findall("error"))
                for problem in problems:
                    result["failed_cases"].append({"test": case.get("name", "?"), "class": case.get("classname", suite.get("name", "?")),
                        "type": problem.get("type", ""), "message": problem.get("message", "")[:2000]})
        except (OSError, ValueError, ET.ParseError) as error:
            result["report_errors"].append({"file": report.name, "error": str(error)[:300]})
    return result


def reset_lint_reports(root: Path, dist: Path):
    """Remove only generated reports so a failed earlier task cannot expose stale lint results."""
    for name in LINT_REPORT_NAMES:
        (root / "android/app/build/reports" / name).unlink(missing_ok=True)
        (dist / name).unlink(missing_ok=True)
    (dist / "lint-summary.json").unlink(missing_ok=True)


def lint_report_summary(root: Path) -> dict:
    result = {"available": False, "errors": 0, "warnings": 0, "issues": [], "report_errors": []}
    report = root / "android/app/build/reports/lint-results.xml"
    if not report.is_file():
        return result
    try:
        if report.stat().st_size > 4 * 1024 * 1024:
            raise ValueError("report exceeds 4 MiB")
        document = ET.parse(report).getroot()
        if document.tag != "issues":
            raise ValueError("unexpected lint report root")
        for issue in document.findall("issue"):
            severity = issue.get("severity", "Unknown")
            result["errors"] += int(severity in ("Fatal", "Error"))
            result["warnings"] += int(severity == "Warning")
            result["issues"].append({"id": issue.get("id", "?"), "severity": severity,
                "message": issue.get("message", ""), "locations": [
                    {field: location.get(field, "") for field in ("file", "line", "column")}
                    for location in issue.findall("location")]})
        result["available"] = True
    except (OSError, ValueError, ET.ParseError) as error:
        result["report_errors"].append(str(error))
    return result


def save_lint_diagnostics(root: Path, dist: Path, log: Path) -> dict:
    """Keep full reports and print every blocking issue, not Gradle's first-error excerpt."""
    summary = lint_report_summary(root)
    summary["saved_reports"] = []
    for name in LINT_REPORT_NAMES:
        source = root / "android/app/build/reports" / name
        if source.is_file():
            try:
                shutil.copy2(source, dist / name)
                summary["saved_reports"].append(name)
            except OSError as error:
                summary["report_errors"].append(f"Cannot copy {name}: {error}")
    (dist / "lint-summary.json").write_text(json.dumps(summary, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")
    lines = [f"Lint: {summary['errors']} errors, {summary['warnings']} warnings." if summary["available"]
             else "Lint: no readable XML report was produced by this run."]
    for issue in summary["issues"]:
        if issue["severity"] in ("Fatal", "Error", "Warning"):
            lines.append(f"LINT {issue['severity'].upper()} [{issue['id']}]: {issue['message']}")
            for location in issue["locations"]:
                lines.append(f"  {location['file']}:{location['line']}:{location['column']}")
    lines.extend("Lint report error: " + error for error in summary["report_errors"])
    lines.extend("Full lint report: " + str(dist / name) for name in summary["saved_reports"])
    message = "\n" + "\n".join(lines) + "\n"
    print(message, end="", flush=True)
    with log.open("a", encoding="utf-8") as stream:
        stream.write(message)
    return summary


def verify_gate_reports(tests: dict, lint: dict, label: str = "Domain"):
    """A zero Gradle exit code alone must not turn missing or failed checks into a release."""
    if (not tests["available"] or tests["report_errors"] or tests["tests"] <= 0
            or tests["failures"] or tests["errors"] or tests["failed_cases"] or tests["skipped"]):
        fail(label + " tests are missing, incomplete, failed, or skipped. See dist/test-summary.json")
    if not lint["available"] or lint["report_errors"] or lint["errors"]:
        fail("Lint report is missing, invalid, or contains errors. See dist/lint-summary.json")


def digest(path: Path, algorithm="sha256") -> str:
    h = hashlib.new(algorithm)
    with path.open("rb") as stream:
        for block in iter(lambda: stream.read(1024 * 1024), b""):
            h.update(block)
    return h.hexdigest()


def fetch(url: str, limit: int = 16 * 1024 * 1024) -> bytes:
    if not url.startswith("https://"):
        fail("Only HTTPS downloads are accepted")
    with urllib.request.urlopen(urllib.request.Request(url, headers={"User-Agent": "ITOM-local-build/0.4"}), timeout=90) as response:
        if not response.url.startswith("https://"):
            fail("Insecure redirect")
        data = response.read(limit + 1)
    if len(data) > limit:
        fail("Metadata response is too large")
    return data


def download(url: str, target: Path, expected: str, algorithm="sha256") -> Path:
    if target.exists() and digest(target, algorithm) == expected.lower():
        return target
    if not url.startswith("https://"):
        fail("Only HTTPS downloads are accepted")
    target.parent.mkdir(parents=True, exist_ok=True)
    partial = target.with_suffix(target.suffix + ".partial")
    print("Downloading", target.name, flush=True)
    try:
        with urllib.request.urlopen(urllib.request.Request(url, headers={"User-Agent": "ITOM-local-build/0.4"}), timeout=120) as response, partial.open("wb") as stream:
            if not response.url.startswith("https://"):
                fail("Insecure redirect")
            total = 0
            for block in iter(lambda: response.read(1024 * 1024), b""):
                total += len(block)
                if total > 2 * 1024**3:
                    fail("Download exceeds the build tool size limit")
                stream.write(block)
        if digest(partial, algorithm) != expected.lower():
            fail("Checksum mismatch: " + target.name)
        partial.replace(target)
    finally:
        partial.unlink(missing_ok=True)
    return target


def child_path(root: Path, name: str) -> Path:
    p = PurePosixPath(name)
    if "\\" in name or ":" in name or p.is_absolute() or ".." in p.parts:
        fail("Unsafe archive path")
    target = root.joinpath(*p.parts)
    if not target.resolve().is_relative_to(root.resolve()):
        fail("Archive path escapes destination")
    return target


def extract(archive: Path, destination: Path):
    """Use only a new staging directory. Never extract over the user's sources."""
    destination.mkdir(parents=True, exist_ok=False)
    if zipfile.is_zipfile(archive):
        with zipfile.ZipFile(archive) as bundle:
            if sum(i.file_size for i in bundle.infolist()) > 4 * 1024**3:
                fail("Archive is too large")
            for member in bundle.infolist():
                target = child_path(destination, member.filename)
                mode = member.external_attr >> 16
                if stat.S_ISLNK(mode):
                    fail("ZIP symlinks are not supported")
                if member.is_dir():
                    target.mkdir(parents=True, exist_ok=True)
                else:
                    target.parent.mkdir(parents=True, exist_ok=True)
                    with bundle.open(member) as src, target.open("xb") as dst:
                        shutil.copyfileobj(src, dst)
                    if os.name != "nt":
                        target.chmod((mode & 0o777) or 0o644)
    else:
        with tarfile.open(archive, "r:*") as bundle:
            members = bundle.getmembers()
            if sum(m.size for m in members) > 4 * 1024**3:
                fail("Archive is too large")
            links = []
            for member in members:
                target = child_path(destination, member.name)
                if member.isdir():
                    target.mkdir(parents=True, exist_ok=True)
                elif member.isfile():
                    target.parent.mkdir(parents=True, exist_ok=True)
                    with bundle.extractfile(member) as src, target.open("xb") as dst:
                        shutil.copyfileobj(src, dst)
                    target.chmod(member.mode & 0o777)
                elif member.issym() or member.islnk():
                    links.append((member, target))
                else:
                    fail("Unsupported archive member")
            for member, target in links:
                link = PurePosixPath(member.linkname)
                if link.is_absolute() or "\\" in member.linkname or ":" in member.linkname:
                    fail("Unsafe archive link")
                resolved = ((target.parent if member.issym() else destination) / member.linkname).resolve()
                if not resolved.is_relative_to(destination.resolve()):
                    fail("Archive link escapes destination")
                target.parent.mkdir(parents=True, exist_ok=True)
                if member.issym():
                    target.symlink_to(member.linkname)
                else:
                    os.link(resolved, target)


def executable(path: Path) -> Path:
    if os.name != "nt":
        path.chmod(path.stat().st_mode | 0o111)
    return path


def installed_jdk() -> Path | None:
    candidates = []
    if os.environ.get("JAVA_HOME"):
        candidates.append(Path(os.environ["JAVA_HOME"]))
    javac = shutil.which("javac")
    if javac:
        candidates.append(Path(javac).resolve().parents[1])
    suffix = ".exe" if os.name == "nt" else ""
    for directory in candidates:
        compiler = directory / "bin" / ("javac" + suffix)
        if compiler.is_file():
            result = subprocess.run([str(compiler), "-version"], capture_output=True, text=True, encoding="utf-8", errors="replace")
            if result.returncode == 0 and re.search(r"javac 17(?:\.|\s)", result.stdout + result.stderr):
                return directory
    return None


def ensure_jdk(cache: Path, metadata: dict) -> Path:
    existing = installed_jdk()
    if existing:
        metadata["jdk_source"] = "local JDK 17"
        return existing
    directory = cache / "jdk17"
    suffix = ".exe" if os.name == "nt" else ""
    if directory.exists():
        matches = list(directory.rglob("bin/javac" + suffix))
        if len(matches) == 1:
            metadata["jdk_source"] = "cached Temurin 17"
            return matches[0].parents[1]
        fail("Incomplete JDK cache: " + str(directory))
    host = {"Windows": "windows", "Darwin": "mac", "Linux": "linux"}.get(platform.system())
    arch = {"amd64": "x64", "x86_64": "x64", "aarch64": "aarch64", "arm64": "aarch64"}.get(platform.machine().lower())
    if not host or not arch:
        fail("Install a JDK 17 and set JAVA_HOME for this platform")
    url = "https://api.adoptium.net/v3/assets/latest/17/hotspot?" + urllib.parse.urlencode({"architecture": arch, "image_type": "jdk", "os": host, "vendor": "eclipse"})
    assets = json.loads(fetch(url))
    if not assets:
        fail("No Temurin JDK 17 package for this platform")
    asset = assets[0]
    package = asset["binary"]["package"]
    filename = package["name"]
    if Path(filename).name != filename:
        fail("Invalid JDK download filename")
    metadata["jdk_download"] = {"release": asset.get("release_name"), "sha256": package["checksum"]}
    archive = download(package["link"], cache / "downloads" / filename, package["checksum"])
    with tempfile.TemporaryDirectory(prefix="jdk-", dir=cache) as temporary:
        unpack = Path(temporary) / "unpacked"
        extract(archive, unpack)
        if len(list(unpack.rglob("bin/javac" + suffix))) != 1:
            fail("JDK package does not contain one compiler")
        unpack.rename(directory)
    return next(directory.rglob("bin/javac" + suffix)).parents[1]


def ensure_gradle(cache: Path) -> Path:
    directory = cache / ("gradle-" + GRADLE)
    binary = directory / "bin" / ("gradle.bat" if os.name == "nt" else "gradle")
    if not binary.is_file():
        archive = download(f"https://services.gradle.org/distributions/gradle-{GRADLE}-bin.zip", cache / "downloads" / f"gradle-{GRADLE}-bin.zip", GRADLE_SHA256)
        with tempfile.TemporaryDirectory(prefix="gradle-", dir=cache) as temporary:
            unpack = Path(temporary) / "unpacked"
            extract(archive, unpack)
            (unpack / directory.name).rename(directory)
    return executable(binary)


def sdk_archive_metadata(host: str) -> tuple[str, str, str]:
    root = ET.fromstring(fetch("https://dl.google.com/android/repository/repository2-1.xml"))
    for element in root.iter():
        element.tag = element.tag.split("}")[-1]
    package = next((p for p in root.findall("remotePackage") if p.get("path") == "cmdline-tools;" + CMDLINE_TOOLS), None)
    if package is None:
        fail("Pinned Android command-line tools package is unavailable")
    for archive in package.findall("archives/archive"):
        if archive.findtext("host-os") == host:
            checksum = archive.find("complete/checksum")
            url = archive.findtext("complete/url")
            if checksum is None or not url:
                break
            if not re.fullmatch(r"[A-Za-z0-9_.-]+\.zip", url):
                fail("Invalid Android tools download path")
            return "https://dl.google.com/android/repository/" + url, checksum.text.strip(), checksum.get("type", "sha1")
    fail("Android command-line tools not found for host")


def normalize_sdk_package(directory: Path, marker: str):
    """Promote one archive wrapper; keep sdkmanager's root metadata."""
    if (directory / marker).is_file() or not directory.is_dir(): return
    candidates = [p for p in directory.iterdir() if p.is_dir() and (p / marker).is_file()]
    if not candidates: return
    if len(candidates) != 1: fail("Android SDK package has no unique archive root: " + str(directory))
    source = candidates[0]
    entries = list(source.iterdir())
    for entry in entries:
        if (directory / entry.name).exists() and entry.name != "package.xml":
            fail("Conflicting Android SDK entry: " + str(directory / entry.name))
    for entry in entries:
        if entry.name == "package.xml" and (directory / entry.name).exists(): continue
        entry.rename(directory / entry.name)
    if not any(source.iterdir()): source.rmdir()


def ensure_sdk(cache: Path, env: dict, accept: bool, metadata: dict) -> Path:
    host = {"Windows": "windows", "Darwin": "macosx", "Linux": "linux"}[platform.system()]
    sdk = Path(os.environ.get("ITOM_ANDROID_SDK_ROOT") or cache / "android-sdk").expanduser().resolve()
    suffix = ".bat" if os.name == "nt" else ""
    manager = sdk / "cmdline-tools" / CMDLINE_TOOLS / "bin" / ("sdkmanager" + suffix)
    required = [manager, sdk / "platforms" / ("android-" + SDK_PACKAGE) / "android.jar", sdk / "build-tools" / BUILD_TOOLS / ("aapt2.exe" if os.name == "nt" else "aapt2")]
    normalize_sdk_package(sdk / "platforms" / ("android-" + SDK_PACKAGE), "android.jar")
    normalize_sdk_package(sdk / "build-tools" / BUILD_TOOLS, "aapt2.exe" if os.name == "nt" else "aapt2")
    if all(p.is_file() for p in required):
        metadata["android_sdk"] = "existing SDK " + SDK
        return sdk
    if not manager.is_file():
        url, checksum, algorithm = sdk_archive_metadata(host)
        metadata["cmdline_tools"] = {"version": CMDLINE_TOOLS, "checksum": checksum, "algorithm": algorithm}
        archive = download(url, cache / "downloads" / url.rsplit("/", 1)[-1], checksum, algorithm)
        with tempfile.TemporaryDirectory(prefix="sdk-tools-", dir=cache) as temporary:
            unpack = Path(temporary) / "unpacked"
            extract(archive, unpack)
            destination = manager.parents[1]
            destination.parent.mkdir(parents=True, exist_ok=True)
            (unpack / "cmdline-tools").rename(destination)
    executable(manager)
    env["ANDROID_SDK_ROOT"] = str(sdk)
    env["ANDROID_HOME"] = str(sdk)
    print("Android SDK licenses: https://developer.android.com/studio/terms", flush=True)
    subprocess.run([str(manager), "--sdk_root=" + str(sdk), "--licenses"], env=env, check=True, text=True, encoding="utf-8", input=("y\n" * 200) if accept else None)
    for package in ("platforms;android-" + SDK_PACKAGE, "build-tools;" + BUILD_TOOLS):
        subprocess.run([str(manager), "--sdk_root=" + str(sdk), "--channel=" + SDK_CHANNEL, package], env=env, check=True, text=True, encoding="utf-8", input=("y\n" * 200) if accept else None)
    normalize_sdk_package(sdk / "platforms" / ("android-" + SDK_PACKAGE), "android.jar")
    normalize_sdk_package(sdk / "build-tools" / BUILD_TOOLS, "aapt2.exe" if os.name == "nt" else "aapt2")
    if not all(p.is_file() for p in required):
        fail("Android SDK installation is incomplete")
    return sdk


def main(argv=None) -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("target", choices=["android", "core", "server", "all", "check"], nargs="?", default="android")
    parser.add_argument("--race", action="store_true", help="Run core Go tests with the race detector; needs a local C compiler")
    parser.add_argument("--accept-android-licenses", action="store_true", help="Explicitly accept Android SDK licenses for local unattended installation")
    parser.add_argument("--stacktrace", action="store_true", help="Include Gradle internal stack traces for troubleshooting")
    parser.add_argument("--expect-revision", help="Refuse a different source revision before running checks or downloading tools")
    parser.add_argument("--cache", type=Path, default=Path(os.environ.get("ITOM_BUILD_CACHE", str(Path.home() / ".cache" / "itom-build"))))
    args = parser.parse_args(argv)
    if sys.version_info < (3, 10):
        fail("Python 3.10 or newer is required")
    info = read_source_info(ROOT)
    require_revision(info, args.expect_revision)
    print(f"ITOM source revision: {info['revision']}", flush=True)
    subprocess.run([sys.executable, str(ROOT / "tools" / "check_sources.py")], check=True)
    if args.target == "check":
        return 0
    if args.target in ("core", "server", "all"):
        import build_core
        result = build_core.run(ROOT, info, args.cache, args.race, server=args.target in ("server", "all"))
        if result != 0 or args.target != "all":
            return result
    if args.race and args.target != "all":
        fail("--race applies to core, server or all targets")
    if platform.system() not in ("Linux", "Windows", "Darwin") or (platform.system() != "Darwin" and platform.machine().lower() not in ("amd64", "x86_64")):
        fail("This launcher supports Windows/Linux x64 and macOS x64/arm64")
    # Create the output directory before taking the cache lock. A path/permission error
    # must not strand build.lock and prevent the user's next attempt.
    dist = ROOT / "dist"; dist.mkdir(exist_ok=True)
    cache = args.cache.expanduser().resolve(); cache.mkdir(parents=True, exist_ok=True)
    lockfile = cache / "build.lock"
    try:
        lock = os.open(lockfile, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
    except FileExistsError:
        fail("Another ITOM build may be active. If it stopped unexpectedly, remove " + str(lockfile))
    os.close(lock)
    metadata = {"itom": info["version"], "source_revision": info["revision"], "gradle": GRADLE, "agp": "9.1.1", "sdk": SDK, "build_tools": BUILD_TOOLS, "host": platform.system(), "started_utc": time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime())}
    archive_sha = os.environ.get("ITOM_SOURCE_ARCHIVE_SHA256", "")
    if re.fullmatch(r"[a-f0-9]{64}", archive_sha):
        metadata["source_archive_sha256"] = archive_sha
    log = dist / "build.log"
    target = dist / public_apk_name(info)
    checksum_file = dist / (target.name + ".sha256")
    try:
        # Repeated builds must not leave this revision's previous APK looking like a new result.
        # Leave unrelated versions and user files alone, and do this only while holding the build lock.
        target.unlink(missing_ok=True)
        checksum_file.unlink(missing_ok=True)
        env = os.environ.copy()
        jdk = ensure_jdk(cache, metadata)
        env["JAVA_HOME"] = str(jdk)
        env["PATH"] = str(jdk / "bin") + os.pathsep + env.get("PATH", "")
        # These overrides affect this process tree only. HOME and shell profiles are untouched.
        gradle = ensure_gradle(cache)
        sdk = ensure_sdk(cache, env, args.accept_android_licenses, metadata)
        env["ANDROID_SDK_ROOT"] = str(sdk); env["ANDROID_HOME"] = str(sdk)
        import build_android_core
        metadata["stage"] = "android_native_build"
        build_android_core.run(ROOT, cache, sdk, env, args.accept_android_licenses, metadata)
        metadata["java_version"] = subprocess.run([str(jdk / "bin" / ("java.exe" if os.name == "nt" else "java")), "-version"], env=env, capture_output=True, text=True, encoding="utf-8", errors="replace", check=True).stderr.strip()
        command = [str(gradle), "--no-daemon", "--console=plain", "--continue"]
        if args.stacktrace:
            command.append("--stacktrace")
        command += [":domain:test", ":app:testDebugUnitTest", ":app:lintDebug", ":app:compileReleaseKotlin", ":app:assembleDebug"]
        metadata["stage"] = "gradle_compile_and_test"
        reset_lint_reports(ROOT, dist)
        domain_failed_this_run = False
        with log.open("w", encoding="utf-8") as stream:
            stream.write(f"ITOM {info['version']} / {info['revision']}\n")
            if "source_archive_sha256" in metadata:
                stream.write("Source archive SHA-256: " + metadata["source_archive_sha256"] + "\n")
            stream.flush()
            with subprocess.Popen(command, cwd=ROOT / "android", env=env, stdout=subprocess.PIPE, stderr=subprocess.STDOUT, text=True, encoding="utf-8", errors="replace", bufsize=1) as process:
                for line in process.stdout:
                    if line.strip() == "> Task :domain:test FAILED":
                        domain_failed_this_run = True
                    print(line, end="", flush=True); stream.write(line); stream.flush()
                result = process.wait()
        summary = test_report_summary(ROOT)
        app_summary = test_report_summary(ROOT, "android/app/build/test-results/testDebugUnitTest")
        summary["task_failed_this_run"] = domain_failed_this_run
        metadata["domain_tests"] = summary
        metadata["app_unit_tests"] = app_summary
        (dist / "test-summary.json").write_text(json.dumps(summary, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")
        metadata["lint"] = save_lint_diagnostics(ROOT, dist, log)
        if result:
            for case in summary["failed_cases"] if domain_failed_this_run else []:
                print(f"TEST FAILED: {case['class']}.{case['test']}\n  {case['type']}: {case['message']}", flush=True)
            fail("Gradle failed. Log: " + str(log))
        # Gradle's BUILD SUCCESSFUL is NOT yet a validated release. Keep the
        # failing post-build stage in build-report.json for Windows users.
        metadata["stage"] = "post_gradle_domain_and_lint_gate"
        verify_gate_reports(summary, metadata["lint"])
        verify_gate_reports(app_summary, metadata["lint"], "Android adapter")
        source = ROOT / "android/app/build/outputs/apk/debug/app-debug.apk"
        metadata["stage"] = "post_gradle_apk_identity"
        metadata["apk_identity"] = verify_apk_identity(source, sdk, info)
        metadata["stage"] = "post_gradle_native_revision"
        metadata["apk_native_libraries"] = verify_apk_native_revision(source, info)
        metadata["stage"] = "verified_apk_export"
        shutil.copy2(source, target)
        sha = digest(target); checksum_file.write_text(sha + "  " + target.name + "\n", encoding="utf-8")
        metadata.update({"status": "passed", "stage": "completed", "apk": target.name, "sha256": sha, "checks": ["domain:test", "app:lintDebug", "app:compileReleaseKotlin", "app:assembleDebug"]})
        print("\nAPK:", target, "\nSHA-256:", sha, flush=True)
    except BaseException as error:
        metadata["status"] = "failed"
        metadata["failed_stage"] = metadata.get("stage", "android_setup")
        metadata["error"] = type(error).__name__ + ": " + str(error)
        print("ITOM: Android build FAILED at stage " + metadata["failed_stage"] +
              "; Gradle BUILD SUCCESSFUL by itself does not verify an APK. " +
              "See dist/build-report.json and dist/build.log", file=sys.stderr, flush=True)
        for artifact in (target, checksum_file):
            try: artifact.unlink(missing_ok=True)
            except OSError: pass  # Keep the original failure and release the lock in finally.
        raise
    finally:
        original_error = sys.exc_info()[0] is not None
        try:
            (dist / "build-report.json").write_text(json.dumps(metadata, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")
        except OSError as report_error:
            print("Cannot write build report: " + str(report_error), file=sys.stderr)
            if not original_error:
                raise
        finally:
            lockfile.unlink(missing_ok=True)
    return 0


if __name__ == "__main__":
    try:
        raise SystemExit(main())
    except (RuntimeError, OSError, subprocess.CalledProcessError) as error:
        print("ITOM build:", error, file=sys.stderr)
        raise SystemExit(1)
