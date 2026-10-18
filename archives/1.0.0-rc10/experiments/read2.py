#!/usr/bin/env python3
"""LAB ONLY: limit proof BODY reads, preserve every deletion/refund proof."""
from pathlib import Path
import shutil, sys
slots=int(sys.argv[1]) if len(sys.argv)>1 else 2
assert slots in (2,4)
r=Path(__file__).resolve().parents[3];d=r/'archives/1.0.0-rc10/source/core/tunnel';old=r/'archives/1.0.0-rc9/source/core/tunnel'
shutil.copy(old/'cleanup.go',d/'cleanup.go')
p=d/'cleanup_service.go';s=(old/'cleanup_service.go').read_text().replace('const cleanupDataReadConcurrency = 4','const cleanupDataReadConcurrency = '+str(slots)).replace('Keep four readers','Keep '+str(slots)+' readers');p.write_text(s)
p=d/'stripe.go';s=p.read_text();a='cleanupQueue       []*lane';assert s.count(a)==1;p.write_text(s.replace(a,a+'\n\tcleanupReads chan struct{}'))
shutil.copy(old/'rc9_cleanup_reads_test.go',d/'rc9_cleanup_reads_test.go')
