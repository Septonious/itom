#!/usr/bin/env python3
"""Follow-up safety correction after skip_data_scan.py, before integration.
No-op queued work must return scheduler ownership; an active worker is untouched.
"""
from pathlib import Path
import sys
r=Path(__file__).resolve().parents[3];d=(Path(sys.argv[1])/'tunnel' if len(sys.argv)>1 else r/'archives/1.0.0-rc10/source/core/tunnel');p=d/'cleanup.go';s=p.read_text()
a=s.index('\t// Avoid speculative full-BODY discovery');b=s.index('\tif !l.cleanupQueued && l.deferOwnerCleanup(kind)',a);guard=s[a:b];s=s[:a]+s[b:]
a='''\tif l.gcBusy {
\t\treturn false
\t}
''';assert s.count(a)==1
old='''\t\t\tif err==nil && usage.HistoricalLocal.Bytes==0 && usage.HistoricalLocal.Messages==0 {
\t\t\t\treturn false'''
new='''\t\t\tif err==nil && usage.HistoricalLocal.Bytes==0 && usage.HistoricalLocal.Messages==0 {
\t\t\t\tif l.cleanupQueued {
\t\t\t\t\tl.cleanupQueued=false
\t\t\t\t\tl.cleanupWanted.Store(false)
\t\t\t\t\tif l.stripe!=nil {l.stripe.endCleanup(l)}
\t\t\t\t}
\t\t\t\treturn false'''
assert guard.count(old)==1;guard=guard.replace(old,new);s=s.replace(a,a+guard);p.write_text(s)
p=d/'rc10_cleanup_admission_test.go';s=p.read_text();a='''  if l.gcBusy||l.gcActive.Load()||l.cleanupWanted.Load(){t.Fatal("empty cleanup retained maintenance ownership")}'''
b=a+'''
  g:=newStripeGroup(wire.ID{1},1);l.stripe=g
  for _,active:=range []bool{false,true}{
   l.cleanupQueued=true;l.cleanupQueuedKind=cleanupLocalData;l.cleanupWanted.Store(true)
   if active {g.cleanupActive[l]=true}else{g.cleanupQueue=append(g.cleanupQueue,l)}
   if l.startCleanup(cleanupLocalData)||l.cleanupQueued||l.cleanupWanted.Load()||g.cleanupActive[l]||len(g.cleanupQueue)!=0 {t.Fatal("obsolete no-op retained FIFO ownership")}
  }
'''
assert s.count(a)==1;s=s.replace(a,b);p.write_text(s)
