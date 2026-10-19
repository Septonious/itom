#!/usr/bin/env python3
"""LAB ONLY: a skipped local pass must still yield mandatory remote proof.
Apply after skip_data_scan.py and skip_cleanup_safety.py. No proof or refund.
"""
from pathlib import Path
import sys
root=Path(__file__).resolve().parents[3]
core=Path(sys.argv[1]) if len(sys.argv)>1 else root/'archives/1.0.0-rc10/source/core'
p=core/'tunnel/cleanup.go';s=p.read_text()
a='''\t\t\tif err==nil && usage.HistoricalLocal.Bytes==0 && usage.HistoricalLocal.Messages==0 {'''
if a not in s:
 a='''\t\t\tif err == nil && usage.HistoricalLocal.Bytes == 0 && usage.HistoricalLocal.Messages == 0 {'''
assert s.count(a)==1
s=s.replace(a,a+'''
\t\t\t\t// A completed local pass, including an empty one, yields the
\t\t\t\t// next turn to pending remote proof. Preserve that handshake
\t\t\t\t// even though no worker/physical command was needed here.
\t\t\t\tl.gcPreferRemote=true''');p.write_text(s)
