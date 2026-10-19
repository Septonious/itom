#!/usr/bin/env python3
"""DIAGNOSTIC ONLY: measure account admission wait without changing policy."""
from pathlib import Path
import sys
core=Path(sys.argv[1]);p=core/'tunnel/account_work.go';s=p.read_text()
a='w := &accountWorkWaiter{ready: make(chan struct{})}';assert s.count(a)==1
s=s.replace(a,'waitBegan := time.Now()\n\t'+a)
a='case <-w.ready:\n\t\tstarted := time.Now()';assert s.count(a)==1
s=s.replace(a,'case <-w.ready:\n\t\trc10ObserveWork(class,time.Since(waitBegan))\n\t\tstarted := time.Now()');p.write_text(s)
(core/'tunnel/rc10_work_observation.go').write_text('''package tunnel
import("sync/atomic";"time")
var rc10WorkMax [workClassCount]atomic.Int64
var rc10WorkSlow [workClassCount]atomic.Uint64
var rc10WorkCount [workClassCount]atomic.Uint64
func rc10ObserveWork(class accountWorkClass,wait time.Duration){
 rc10WorkCount[class].Add(1);if wait>=10*time.Millisecond{rc10WorkSlow[class].Add(1)}
 for old:=rc10WorkMax[class].Load();int64(wait)>old;old=rc10WorkMax[class].Load(){if rc10WorkMax[class].CompareAndSwap(old,int64(wait)){break}}
}
''')
p=core/'tunnel/supervised_load_test.go';s=p.read_text()
a='coldBegin := time.Now()';assert s.count(a)==1
s=s.replace(a,'''for i:=range rc10WorkMax {rc10WorkMax[i].Store(0);rc10WorkCount[i].Store(0);rc10WorkSlow[i].Store(0)}
 t.Cleanup(func(){for i:=range rc10WorkMax{t.Logf("diagnostic_only_account_admission class=%d count=%d waits_ge10ms=%d max_wait_ms=%.3f",i,rc10WorkCount[i].Load(),rc10WorkSlow[i].Load(),float64(rc10WorkMax[i].Load())/1e6)}})
 '''+a);p.write_text(s)
