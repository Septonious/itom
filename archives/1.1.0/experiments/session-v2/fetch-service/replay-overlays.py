#!/usr/bin/env python3
"""Rebase retained diagnostic overlays onto this checkout; never edit source."""
import argparse
import json
from pathlib import Path

EVIDENCE = Path(__file__).resolve().parent
SOURCE = EVIDENCE.parents[4] / "archives/1.1.0/source/core/transport"
VARIANTS = {
    "competitive-fetch": ("imap_fetch_turn.go", "imap_fetch_turn-competitive.go"),
    "unbackedoff-maintenance": ("inbox_maintenance.go", "inbox_maintenance-unbackedoff.go"),
    "single-group": ("inbox_read_group.go", "inbox_read_group-single.go"),
    "all-single-service": ("roster_service.go", "roster_service-single.go"),
}

parser = argparse.ArgumentParser(description=__doc__)
parser.add_argument("variants", nargs="+", choices=VARIANTS)
parser.add_argument("--output", type=Path, required=True)
args = parser.parse_args()
replacements = {}
for variant in args.variants:
    target, replacement = VARIANTS[variant]
    replacements[str(SOURCE / target)] = str(EVIDENCE / replacement)
args.output.write_text(json.dumps({"Replace": replacements}, indent=2) + "\n")
print(args.output)
