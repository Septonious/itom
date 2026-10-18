#!/usr/bin/env python3
"""LAB ONLY: adapt existing exact cleanup FETCH size to actual command service.
No quota, proof, polling, writer count, logical flight or socket changes.
"""
from pathlib import Path
import argparse
p=argparse.ArgumentParser();p.add_argument('variant',choices=['fixed4','fixed8','adaptive160','adaptive240']);a=p.parse_args()
r=Path(__file__).resolve().parents[3];d=r/'archives/1.0.0-rc10/source/core/tunnel';p=d/'io.go';s=p.read_text()
if a.variant.startswith('fixed'):
 assert s.count('candidateBytes+size > 2<<20')==1
 p.write_text(s.replace('candidateBytes+size > 2<<20','candidateBytes+size > '+a.variant[5:]+'<<20'))
 raise SystemExit
budget=int(a.variant.removeprefix('adaptive'))
assert s.count('readerCheck             time.Time')==1
s=s.replace('readerCheck             time.Time','readerCheck             time.Time\n\tcleanupFetch cleanupFetchPolicy')
start=s.index('func (m *laneMail) FetchCleanupCandidates(');end=s.index('\nfunc extendReaderHot',start);chunk=s[start:end]
assert chunk.count('validity := hints[0].Validity')==1
chunk=chunk.replace('validity := hints[0].Validity','fetchLimit := m.cleanupFetch.limit()\n\tvalidity := hints[0].Validity').replace('candidateBytes+size > 2<<20','candidateBytes+size > fetchLimit')
assert chunk.count('var rows []mailbox.Message')==1
chunk=chunk.replace('var rows []mailbox.Message','var rows []mailbox.Message\n\tvar elapsed time.Duration')
a='rows, e = m.cleanupClient().FetchExact(ctx, folder, validity, ids)'
assert chunk.count(a)==1
chunk=chunk.replace(a,'started := time.Now()\n\t\t'+a+'\n\t\telapsed = time.Since(started)')
a='\treturn rows, err';assert chunk.count(a)==1
chunk=chunk.replace(a,'''\tif err == nil {
\t\tvar observed uint64
\t\tfor _, row := range rows { observed += uint64(len(row.MIME)) }
\t\tm.cleanupFetch.observe(fetchLimit, observed, elapsed, time.Duration(m.providerRTT.Load()))
\t}
'''+a)
s=s[:start]+chunk+s[end:];p.write_text(s)
(d/'rc10_cleanup_adaptive.go').write_text('''package tunnel
import("sync";"time")
// This controls only the byte budget of the next existing proof discovery.
// A complete first BODY remains indivisible, and every exact UID, digest,
// deletion and journal proof remains required. Demand-limited samples do not
// claim network capacity. Account queue time is excluded by the caller.
type cleanupFetchPolicy struct { mu sync.Mutex; bytes uint64; fast int }
func(p *cleanupFetchPolicy) limit()uint64 {
 p.mu.Lock();defer p.mu.Unlock()
 if p.bytes==0 {p.bytes=512<<10}
 return p.bytes
}
func(p *cleanupFetchPolicy) observe(requested, observed uint64, elapsed, baseline time.Duration) {
 if observed==0 || elapsed<=0 {return}
 service:=max(time.Duration(0),elapsed-min(max(time.Duration(0),baseline),2*time.Second))
 p.mu.Lock();defer p.mu.Unlock()
 if p.bytes==0 {p.bytes=512<<10}
 // A stale concurrent sample cannot grow a later generation's tentative cap.
 if requested!=p.bytes {return}
 budget:=CLEANUP_BUDGET*time.Millisecond
 if service>budget {p.bytes=max(uint64(64<<10),p.bytes/2);p.fast=0;return}
 if observed>=requested*3/4 {
  p.fast++
  if p.fast>=2 {p.bytes=min(uint64(8<<20),p.bytes*2);p.fast=0}
 } else {p.fast=0}
}
'''.replace('CLEANUP_BUDGET',str(budget)))
(d/'rc10_cleanup_adaptive_test.go').write_text('''package tunnel
import("testing";"time")
func TestRC10CleanupBudgetLearnsActualFullServiceAndRecovers(t *testing.T){
 var p cleanupFetchPolicy
 for i:=0;i<12;i++{p.observe(p.limit(),1024,10*time.Millisecond,0)}
 if p.limit()!=512<<10{t.Fatal("tiny demand proved large service")}
 for i:=0;i<3;i++{n:=p.limit();p.observe(n,n,3*time.Second,0)}
 if p.limit()!=64<<10{t.Fatal("slow proof reads did not contract",p.limit())}
 for i:=0;i<14;i++{n:=p.limit();p.observe(n,n,100*time.Millisecond,0)}
 if p.limit()!=8<<20{t.Fatal("healthy full observations remained throttled",p.limit())}
 p.observe(p.limit(),0,10*time.Second,0)
 if p.limit()!=8<<20{t.Fatal("empty or unknown discovery trained service")}
}
func TestRC10CleanupBudgetRespectsPropagationAndSampleGeneration(t *testing.T){
 var p cleanupFetchPolicy;n:=p.limit()
 p.observe(n,n,1300*time.Millisecond,1200*time.Millisecond)
 p.observe(n,n,1300*time.Millisecond,1200*time.Millisecond)
 if p.limit()!=2*n{t.Fatal("true propagation collapsed proof budget")}
 p.observe(n,n,time.Millisecond,0);p.observe(n,n,time.Millisecond,0)
 if p.limit()!=2*n{t.Fatal("stale observations grew a new budget")}
}
''')
