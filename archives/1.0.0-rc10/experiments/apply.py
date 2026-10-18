#!/usr/bin/env python3
"""Isolated RC10 A/B experiments, never a release qualification shortcut."""
from pathlib import Path
import argparse, shutil
p=argparse.ArgumentParser();p.add_argument('variant',choices=['reference','cleanup4','selection']);a=p.parse_args()
r=Path(__file__).resolve().parents[3];d=r/'archives/1.0.0-rc10/source';old=r/'archives/1.0.0-rc9/source'
if a.variant=='cleanup4':
 f=d/'core/tunnel/io.go';s=f.read_text();assert s.count('candidateBytes+size > 2<<20')==1
 f.write_text(s.replace('candidateBytes+size > 2<<20','candidateBytes+size > 4<<20'))
elif a.variant=='selection':
 for name in ['carrier_feedback.go','local_selection.go']:shutil.copy(old/'core/tunnel'/name,d/'core/tunnel'/name)
print('LAB ONLY variant='+a.variant+'; original gates unchanged; no artifact publication')
