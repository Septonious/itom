#!/usr/bin/env python3
"""Restore a recorded parent via Go overlay, without mutating current source."""
import argparse
import json
import subprocess
from pathlib import Path

parser = argparse.ArgumentParser(description=__doc__)
parser.add_argument("--output-dir", required=True, type=Path)
args = parser.parse_args()
root = Path(__file__).resolve().parents[5]
parent = "8456731d97b21cc0aa150098f240a93e0d1a520d"
args.output_dir.mkdir(parents=True, exist_ok=True)
replace = {}
for name in ["imap.go", "imap_fetch_turn.go", "imap_uid_set.go", "imap_keywords.go", "imap_diagnostics.go"]:
    source = root / "archives/1.1.0/source/core/transport" / name
    raw = subprocess.check_output(["git", "show", parent + ":" + str(source.relative_to(root))], cwd=root)
    if name == "imap_diagnostics.go":
        lines = raw.decode().splitlines(keepends=True)
        for n, line in enumerate(lines):
            if line.lstrip().startswith("FetchActive "):
                lines[n + 1:n + 1] = [
                    '\tFlagsFetchActive bool `json:"flags_fetch_active"`\n',
                    '\tFetchResponseBytes int `json:"fetch_response_bytes"`\n',
                    '\tConcurrentFetchPairs uint64 `json:"concurrent_fetch_pairs"`\n',
                ]
                break
        else:
            raise RuntimeError("parent diagnostic shape changed")
        raw = "".join(lines).encode()
    replacement = args.output_dir.resolve() / ("parent-" + name)
    replacement.write_bytes(raw)
    replace[str(source)] = str(replacement)
(args.output_dir / "overlay.json").write_text(json.dumps({"Replace": replace}, indent=2) + "\n")
