"""Create the executable source ZIP and a SHA-256 manifest; no compilation."""
import argparse
import hashlib
import json
from pathlib import Path
import zipfile

from build import ROOT, read_source_info
from build_core import verify_imap_fork


def package(root: Path, destination: Path):
    if destination.exists():
        raise RuntimeError("Refusing to overwrite " + str(destination))
    # Verify before creating an archive: the outer ZIP manifest cannot detect
    # a stale inner pin for the reviewed bundled IMAP implementation.
    if (root / "core").is_dir():
        verify_imap_fork(root / "core")
    excluded = {".git", ".gradle", ".kotlin", "__pycache__", "build", "dist", "libs", "reports"}
    entries = {}
    for path in sorted(root.rglob("*")):
        relative = path.relative_to(root)
        # The executable ZIP extracts beside the caller as ITOM_<revision>_<random>.
        # If a developer invokes it while cwd is the source root, never package
        # that generated tree back into the next source archive.
        if relative.parts and relative.parts[0].startswith("ITOM_"):
            continue
        if any(part in excluded for part in relative.parts):
            continue
        if path.is_symlink():
            raise RuntimeError("Source symlink is not allowed: " + str(relative))
        if path.is_file() and path.suffix not in (".pyc", ".pyo"):
            entries["itom/" + relative.as_posix()] = path.read_bytes()
    if sum(map(len, entries.values())) > 60 * 1024 * 1024:
        raise RuntimeError("Source bundle exceeds limit")
    manifest = {name: hashlib.sha256(data).hexdigest() for name, data in entries.items()}
    entries["SOURCE_MANIFEST.json"] = (json.dumps(manifest, sort_keys=True, indent=2) + "\n").encode("utf-8")
    entries["__main__.py"] = (root / "tools/archive_main.py").read_bytes()
    destination.parent.mkdir(parents=True, exist_ok=True)
    handle = destination.open("xb")
    try:
        with handle, zipfile.ZipFile(handle, "w", compression=zipfile.ZIP_DEFLATED, compresslevel=9) as archive:
            for name, data in sorted(entries.items()):
                member = zipfile.ZipInfo(name, date_time=(2026, 10, 14, 0, 0, 0))
                member.create_system = 3
                member.external_attr = 0o100644 << 16
                member.compress_type = zipfile.ZIP_DEFLATED
                archive.writestr(member, data)
    except BaseException:
        destination.unlink(missing_ok=True)
        raise
    return hashlib.sha256(destination.read_bytes()).hexdigest()


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--output", type=Path)
    args = parser.parse_args()
    output = args.output or ROOT / "dist" / read_source_info()["archive_name"]
    sha = package(ROOT, output.resolve())
    print("Source ZIP:", output.resolve())
    print("SHA-256:", sha)


if __name__ == "__main__":
    main()
