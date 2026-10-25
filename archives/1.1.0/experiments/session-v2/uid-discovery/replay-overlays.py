#!/usr/bin/env python3
"""Generate diagnostic overlays for this checkout without editing source."""
import argparse
import json
from pathlib import Path

EVIDENCE = Path(__file__).resolve().parent
SOURCE = EVIDENCE.parents[4] / "archives/1.1.0/source/core/transport"
VARIANTS = {
    "rewind": {"inbox.go": "rewind-inbox.go", "inbox_poll.go": "rewind-inbox_poll.go"},
    "highwater": {"inbox.go": "highwater-inbox.go"},
    "highwater-tests": {"inbox_highwater_candidate_test.go": "inbox_highwater_candidate_test.go"},
    "faster-probe": {"inbox_worker.go": "sixteenth-inbox_worker.go"},
}
parser = argparse.ArgumentParser(description=__doc__)
parser.add_argument("variants", nargs="+", choices=VARIANTS)
parser.add_argument("--output", required=True, type=Path)
args = parser.parse_args()
replace = {}
for name in args.variants:
    for target, variant in VARIANTS[name].items():
        replace[str(SOURCE / target)] = str(EVIDENCE / variant)
args.output.write_text(json.dumps({"Replace": replace}, indent=2) + "\n")
print(args.output)
