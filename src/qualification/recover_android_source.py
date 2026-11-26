#!/usr/bin/env python3
"""Recover historical Android source only, with verified archive and safe paths."""
import hashlib
import io
import json
import pathlib
import sys
import tarfile

base, dest = map(pathlib.Path, sys.argv[1:])
manifest=json.loads((base/"manifest.json").read_text())
assert manifest["source_commit"]=="085531fab072478bd8ff60feb78e374ce892eb1b"
meta=manifest["source_snapshot"]
data=b"".join((base/p).read_bytes() for p in meta["parts"])
assert len(data)==1779101==meta["bytes"]
assert hashlib.sha256(data).hexdigest()=="822221e08e9a5110498ac4570cdd65370196167f06ce3f5e6ef18ecfadf6b26f"==meta["sha256"]
assert not dest.exists(), f"refusing to overwrite {dest}"
dest.mkdir(parents=True)
files=0
with tarfile.open(fileobj=io.BytesIO(data),mode="r:gz") as archive:
    for member in archive:
        path=pathlib.PurePosixPath(member.name)
        if not path.parts or path.parts[0]!="android":
            continue
        assert not path.is_absolute() and ".." not in path.parts, path
        if member.isdir():
            continue
        if not member.isfile():
            raise ValueError(f"unexpected nonregular member {path}")
        rel=pathlib.Path(*path.parts[1:])
        if any(p in {".gradle","build",".idea","jniLibs"} for p in rel.parts):
            continue
        if rel.name in {"local.properties","key.properties","keystore.properties"} or rel.suffix.lower() in {".jks",".keystore",".apk",".aab",".aar",".so",".class",".p12",".pem",".key"}:
            continue
        assert member.size <= 16*1024*1024, path
        target=dest/rel
        target.parent.mkdir(parents=True,exist_ok=True)
        target.write_bytes(archive.extractfile(member).read())
        if member.mode & 0o111:
            target.chmod(0o755)
        files+=1
assert (dest/"app/build.gradle.kts").is_file()
assert (dest/"settings.gradle.kts").is_file()
(dest/"RECOVERED_SOURCE.md").write_text("""# Android source recovery

Exact historical 1.3.0 RC3 source snapshot, verified by SHA-256 before extraction.
It is NOT the original RC20 Kotlin implementation or a release-ready 1.5.1 APK.
The current 1.5.1 Go transport and native modules remain in src/ and are not
replaced by historical archive contents. Generated binaries and signing material
are excluded. Adapt Android routing, network handover and native APIs before release.
""")
print("Recovered Android files:",files)
