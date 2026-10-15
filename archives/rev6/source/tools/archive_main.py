"""Entry point embedded at ZIP root as __main__.py; sources live under itom/."""
import hashlib
import json
import os
import shutil
from pathlib import Path, PurePosixPath
import re
import stat
import subprocess
import sys
import tempfile
import zipfile



def export_verified_artifacts(extracted_root: Path, source_archive: Path, info: dict, target: str):
    """Publish *successful, verified* outputs beside the executable source ZIP.

    Build inside the temporary extraction is intentional; public dist must not
    point at that unpredictable directory. Nothing is exported on build failure.
    """
    if target not in ("android", "server", "all"):
        return []
    internal = extracted_root / "itom" / "dist"
    release = info["release"]
    artifacts = []
    if target in ("android", "all"):
        report = json.loads((internal / "build-report.json").read_text(encoding="utf-8"))
        if report.get("status") != "passed" or report.get("source_revision") != info["revision"]:
            raise RuntimeError("Android build was not verified; no APK published")
        name = f"ITOM-{info['version']}-{release}-debug.apk" if release in ("Rev10","Rev1","Rev2","Rev3","Rev4","Rev5","Rev6") else f"ITOM_{release}_debug.apk"
        apk = internal / name
        checksum = internal / (name + ".sha256")
        if not apk.is_file() or not checksum.is_file() or apk.is_symlink():
            raise RuntimeError("Verified APK/checksum absent")
        sha = hashlib.sha256(apk.read_bytes()).hexdigest()
        if report.get("sha256") != sha or checksum.read_text().strip() != sha + "  " + name:
            raise RuntimeError("APK checksum mismatch before publication")
        artifacts.extend((name, name + ".sha256", "build-report.json"))
    if target in ("server", "all"):
        report = json.loads((internal / "core-report.json").read_text(encoding="utf-8"))
        if (report.get("status") != "passed" or report.get("source_revision") != info["revision"]
                or not report.get("server_built")):
            raise RuntimeError("Server build was not verified; no binaries published")
        checks = {}
        for line in (internal / "SHA256SUMS").read_text(encoding="utf-8").splitlines():
            digest, sep, name = line.partition("  ")
            if sep != "  " or not re.fullmatch(r"[a-f0-9]{64}", digest) or name in checks:
                raise RuntimeError("Invalid server SHA256SUMS")
            checks[name] = digest
        for arch in ("amd64", "arm64"):
            name = f"ITOM-{info['version']}-{release}-linux-{arch}" if release in ("Rev10","Rev1","Rev2","Rev3","Rev4","Rev5","Rev6") else f"ITOM_{release}_linux_{arch}"
            binary = internal / name
            if (not binary.is_file() or binary.is_symlink() or binary.stat().st_size == 0
                    or checks.get(name) != hashlib.sha256(binary.read_bytes()).hexdigest()):
                raise RuntimeError("Missing or invalid server binary " + name)
            artifacts.append(name)
        artifacts.extend(("SHA256SUMS", "core-report.json"))
    # Prepare/verify every artifact BEFORE mutating the public output directory.
    for name in artifacts:
        candidate = internal / name
        if not candidate.is_file() or candidate.is_symlink():
            raise RuntimeError("Missing verified output: " + name)
    output = source_archive.parent / "dist"
    if output.is_symlink():
        raise RuntimeError("Refusing symlinked output directory: " + str(output))
    output.mkdir(parents=True, exist_ok=True)
    # Stage all copies before replacement so an interrupted copy cannot publish
    # truncated binaries/APKs. Different revisions have independent filenames.
    import tempfile
    staged = []
    try:
        for name in artifacts:
            source = internal / name
            with tempfile.NamedTemporaryFile(prefix=".itom-", suffix=".partial", dir=output,
                                             delete=False) as handle:
                temporary = Path(handle.name)
            staged.append((temporary, output / name))
            shutil.copy2(source, temporary)
            if hashlib.sha256(temporary.read_bytes()).digest() != hashlib.sha256(source.read_bytes()).digest():
                raise RuntimeError("Artifact copy checksum failed: " + name)
        for temporary, target_path in staged:
            os.replace(temporary, target_path)
            print("Exported:", target_path, flush=True)
        return [output / name for name in artifacts]
    finally:
        for temporary, _ in staged:
            temporary.unlink(missing_ok=True)

def main():
    if sys.version_info < (3, 10):
        raise RuntimeError("Python 3.10+ is required")
    archive = Path(sys.argv[0]).resolve()
    archive_sha = hashlib.sha256(archive.read_bytes()).hexdigest()
    with zipfile.ZipFile(archive) as bundle:
        members = bundle.infolist()
        if len({i.filename for i in members}) != len(members) or sum(i.file_size for i in members) > 64 * 1024 * 1024:
            raise RuntimeError("Invalid source archive")
        manifest = json.loads(bundle.read("SOURCE_MANIFEST.json"))
        expected = {"__main__.py", "SOURCE_MANIFEST.json"} | set(manifest)
        if {i.filename for i in members} != expected:
            raise RuntimeError("Archive contents differ from the source manifest")
        info_bytes = bundle.read("itom/source-info.json")
        if hashlib.sha256(info_bytes).hexdigest() != manifest.get("itom/source-info.json"):
            raise RuntimeError("Source revision metadata checksum mismatch")
        info = json.loads(info_bytes)
        if not isinstance(info, dict) or not isinstance(info.get("revision"), str) or not re.fullmatch(r"[A-Za-z0-9_-]+", info["revision"]):
            raise RuntimeError("Invalid source revision")
        # A fresh sibling directory protects edited sources on repeated invocation.
        destination = Path(tempfile.mkdtemp(prefix="ITOM_" + info["revision"] + "_", dir=Path.cwd()))
        for member in members:
            path = PurePosixPath(member.filename)
            if path.is_absolute() or ".." in path.parts or "\\" in member.filename or ":" in member.filename or stat.S_ISLNK(member.external_attr >> 16):
                raise RuntimeError("Unsafe source archive path")
            data = bundle.read(member)
            if member.filename in manifest and hashlib.sha256(data).hexdigest() != manifest[member.filename]:
                raise RuntimeError("Source checksum mismatch: " + member.filename)
            target = destination.joinpath(*path.parts)
            target.parent.mkdir(parents=True, exist_ok=True)
            with target.open("xb") as output:
                output.write(data)
            if os.name != "nt":
                target.chmod(((member.external_attr >> 16) & 0o777) or 0o644)
    print("ITOM launcher revision:", info["revision"], flush=True)
    print("Archive:", archive, flush=True)
    print("Archive SHA-256:", archive_sha, flush=True)
    print("Sources:", destination / "itom", flush=True)
    env = os.environ.copy()
    env["ITOM_SOURCE_ARCHIVE_SHA256"] = archive_sha
    env["PYTHONIOENCODING"] = "utf-8"
    status = subprocess.call([sys.executable, str(destination / "itom/tools/build.py"), *sys.argv[1:]], env=env)
    if status != 0:
        print("ITOM: build failed; no APK or server binary exported to the public dist", file=sys.stderr)
        return status
    operation = next((arg for arg in sys.argv[1:] if arg in {"android", "core", "server", "all", "check", "model20", "model130"}), "android")
    export_verified_artifacts(destination, archive, info, operation)
    return 0


if __name__ == "__main__":
    try:
        raise SystemExit(main())
    except (RuntimeError, OSError, ValueError, zipfile.BadZipFile) as error:
        print("ITOM:", error, file=sys.stderr)
        raise SystemExit(1)
