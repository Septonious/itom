#!/usr/bin/env python3
"""Generate an overlay for the two retained negative controls; mutates no source."""
import argparse
import json
from pathlib import Path

parser = argparse.ArgumentParser(description=__doc__)
parser.add_argument("variant", choices=["parent", "earlier-candidate"])
parser.add_argument("--output", required=True, type=Path)
args = parser.parse_args()
evidence = Path(__file__).resolve().parent
root = evidence.parents[4]
source = root / "archives/1.1.0/source/core/transport/inbox.go"
assert source.is_file(), "run from the repository containing this evidence"
replacement = evidence / (args.variant + "-inbox.go.txt")
args.output.write_text(json.dumps({"Replace": {str(source): str(replacement)}}, indent=2) + "\n")
