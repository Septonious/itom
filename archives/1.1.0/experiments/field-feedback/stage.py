#!/usr/bin/env python3
"""Stage the tracked DEV5 source without modifying experiment checkpoints."""
from pathlib import Path
import argparse,shutil
ROOT=Path(__file__).resolve().parents[4]
p=argparse.ArgumentParser();p.add_argument('output',type=Path);a=p.parse_args()
shutil.copytree(ROOT/'archives/1.1.0/source',a.output,ignore=shutil.ignore_patterns('dist','build','.gradle','__pycache__','*.test'))
