#!/usr/bin/env python3
"""LAB ONLY. Learn proof-FETCH serialization above its own two command turns.
Run after exact_scan/skip_data_scan/skip_cleanup_safety, without fixed8.
Optional argument is an isolated core directory; candidate source is untouched.
"""
from pathlib import Path
import sys
root = Path(__file__).resolve().parents[3]
core = Path(sys.argv[1]) if len(sys.argv)>1 else root/'archives/1.0.0-rc10/source/core'
d=core/'tunnel';p=d/'io.go';s=p.read_text()
assert s.count('readerCheck             time.Time')==1
s=s.replace('readerCheck             time.Time','readerCheck             time.Time\n\tcleanupFetch cleanupFetchPolicy')
start=s.index('func (m *laneMail) FetchCleanupCandidates(');end=s.index('\nfunc extendReaderHot',start);chunk=s[start:end]
assert chunk.count('candidateBytes+size > 2<<20')==1
chunk=chunk.replace('validity := hints[0].Validity','fetchLimit := m.cleanupFetch.limit()\n\tvalidity := hints[0].Validity').replace('candidateBytes+size > 2<<20','candidateBytes+size > fetchLimit')
chunk=chunk.replace('var rows []mailbox.Message','var rows []mailbox.Message\n\tvar elapsed, selection time.Duration')
a='rows, e = m.cleanupClient().FetchExact(ctx, folder, validity, ids)';assert chunk.count(a)==1
chunk=chunk.replace(a,'client := m.cleanupClient()\n\t\tstarted := time.Now()\n\t\trows, e = client.FetchExact(ctx, folder, validity, ids)\n\t\telapsed = time.Since(started)\n\t\tselection, _ = client.SelectionCost(folder)')
a='\treturn rows, err';assert chunk.count(a)==1
chunk=chunk.replace(a,'\tif err == nil {\n\t\tvar observed uint64\n\t\tfor _, row := range rows { observed += uint64(len(row.MIME)) }\n\t\tm.cleanupFetch.observe(fetchLimit, observed, elapsed, selection)\n\t}\n'+a)
p.write_text(s[:start]+chunk+s[end:])
(d/'rc10_cleanup_adaptive.go').write_text('''package tunnel
import("sync";"time")
// Only exact discovery batching changes. First complete BODY is indivisible;
// hints never become deletion/refund evidence. Errors and empty replies cannot
// train capacity. The minimum measured SELECT cost is this same authenticated
// cleaner's command floor, including server paths without a client NOOP.
type cleanupFetchPolicy struct {mu sync.Mutex;bytes uint64;fast,slow int;floor time.Duration}
func(p *cleanupFetchPolicy) limit()uint64{p.mu.Lock();defer p.mu.Unlock();if p.bytes==0{p.bytes=512<<10};return p.bytes}
func(p *cleanupFetchPolicy) observe(requested,observed uint64,elapsed,selection time.Duration){
 if observed==0||elapsed<=0||selection<=0||selection>2*time.Second{return}
 p.mu.Lock();defer p.mu.Unlock()
 if p.bytes==0{p.bytes=512<<10}
 if requested!=p.bytes{return}
 if p.floor==0||selection<p.floor{p.floor=selection}
 // FETCH follows a checked SELECT. Account queue wait is outside elapsed.
 budget:=2*p.floor+160*time.Millisecond
 if elapsed>budget {p.fast=0;p.slow++;if p.slow>=2{p.bytes=max(uint64(64<<10),p.bytes/2);p.slow=0};return}
 p.slow=0
 if observed>=requested*3/4 {p.fast++;if p.fast>=2{p.bytes=min(uint64(8<<20),p.bytes*2);p.fast=0}}else{p.fast=0}
}
''')
(d/'rc10_cleanup_adaptive_test.go').write_text('''package tunnel
import("testing";"time")
func TestRC10CleanupOwnCommandFloorAndCapacity(t *testing.T){
 var p cleanupFetchPolicy
 for i:=0;i<8;i++{n:=p.limit();p.observe(n,n,260*time.Millisecond,100*time.Millisecond)}
 if p.limit()!=8<<20{t.Fatal("fixed command cost throttled healthy large service",p.limit())}
 for i:=0;i<14;i++{n:=p.limit();p.observe(n,n,time.Second,100*time.Millisecond)}
 if p.limit()!=64<<10{t.Fatal("sustained serialization did not contract",p.limit())}
 for i:=0;i<14;i++{n:=p.limit();p.observe(n,n,260*time.Millisecond,100*time.Millisecond)}
 if p.limit()!=8<<20{t.Fatal("recovered service could not probe up",p.limit())}
}
func TestRC10CleanupRejectsDemandUnknownAndLoadedFloor(t *testing.T){
 var p cleanupFetchPolicy;n:=p.limit()
 for i:=0;i<8;i++{p.observe(n,1024,200*time.Millisecond,80*time.Millisecond)}
 if p.limit()!=n{t.Fatal("tiny demand trained full service")}
 p.observe(n,n,time.Second,500*time.Millisecond);p.observe(n,n,time.Second,500*time.Millisecond)
 if p.limit()!=n/2||p.floor!=80*time.Millisecond{t.Fatal("loaded SELECT legitimized queue",p.limit(),p.floor)}
 p.observe(n,n,time.Millisecond,time.Millisecond)
 if p.limit()!=n/2{t.Fatal("stale sample grew new generation")}
 p.observe(n/2,0,time.Second,0)
 if p.limit()!=n/2{t.Fatal("unknown or empty proof changed budget")}
}
''')
