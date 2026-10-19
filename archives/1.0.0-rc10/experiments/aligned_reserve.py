#!/usr/bin/env python3
"""LAB ONLY: align near-equal quiet accounts across the authenticated session.
Preserve the RC2 unloaded latency eligibility band and bulk-epoch pinning;
replace endpoint-local startup history as the tie-breaker, not polling cadence.
"""
from pathlib import Path
import sys
r=Path(__file__).resolve().parents[3];core=Path(sys.argv[1]) if len(sys.argv)>1 else r/'archives/1.0.0-rc10/source/core';p=core/'tunnel/reader_reserve.go';s=p.read_text()

a='''\tscores := map[*lane]int64{}
\tfor _, l := range candidates {
\t\tscores[l] = g.carrierLatencyLocked(l)
\t}'''
b='''\tscores := map[*lane]int64{}
\tranks := map[*lane]int{}
\tsort.Slice(candidates, func(i,j int) bool { return candidates[i].account.ID < candidates[j].account.ID })
\tstart := int(g.logical[len(g.logical)-1]) % len(candidates)
\tfor i, l := range candidates {
\t\tscores[l] = g.carrierLatencyLocked(l)
\t\tranks[l] = (i-start+len(candidates)) % len(candidates)
\t}'''
assert s.count(a)==1;s=s.replace(a,b)
a='''\t\tfor _, l := range candidates {
\t\t\tif l.account.ID == g.interactiveReserve[i] && scores[l] <= scores[chosen]+max(int64(100), scores[chosen]/4) {
\t\t\t\tchosen = l
\t\t\t\tbreak
\t\t\t}
\t\t}'''
b='''\t\tceiling := scores[chosen]+max(int64(100), scores[chosen]/4)
\t\tfor _, l := range candidates {
\t\t\tif scores[l] <= ceiling && (ranks[l] < ranks[chosen] || ranks[l] == ranks[chosen] && l.account.ID < chosen.account.ID) {
\t\t\t\tchosen = l
\t\t\t}
\t\t}'''
assert s.count(a)==1;s=s.replace(a,b);p.write_text(s)
(p.parent/'rc10_reserve_alignment_test.go').write_text('''package tunnel
import("testing";"fmt";"time";"itom.local/core/wire")
func TestRC10QuietReserveAgreesDespiteArrivalHistory(t *testing.T){
 var expected [2]string
 for side:=0;side<2;side++{
  g:=newStripeGroup(wire.ID{15:7},1)
  for n:=0;n<10;n++{
   i:=n+1;if side==1{i=10-n}
   l:=stripeTestLane(g,fmt.Sprintf("a%02d",i),side==1);defer l.cancel()
   if err:=g.attach(l);err!=nil{t.Fatal(err)}
   g.interactiveReadersLocked()
  }
  if side==0{g.interactiveReserve=[2]string{"a01","a02"}}else{g.interactiveReserve=[2]string{"a05","a06"}}
  quiet:=g.interactiveReadersLocked();got:=[2]string{quiet[0].account.ID,quiet[1].account.ID}
  if side==0{expected=got}else if got!=expected{t.Fatal("near-equal endpoints disagree",expected,got)}
  if got!=[2]string{"a08","a09"}{t.Fatal("session tie-break lost canonical eligibility",got)}
  g.delivery.bulk=32<<10
  for _,l:=range quiet{l.rtt.Store(5000);l.smoothedRTT.Store(5000);l.rttAt.Store(time.Now().UnixMilli())}
  pinned:=g.interactiveReadersLocked()
  if pinned!=quiet{t.Fatal("loaded echo rotated a bulk reader into the quiet reserve")}
 }
}
''')
