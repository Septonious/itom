#!/usr/bin/env python3
"""LAB ONLY: separate pre-publication grant wait from known physical service.
Short application feedback keeps complete admission residence. Unknown APPENDs
and unpublished ranges keep original age and outage protection. Debt unchanged.
"""
from pathlib import Path
import sys
root=Path(__file__).resolve().parents[3]
core=Path(sys.argv[1]) if len(sys.argv)>1 else root/'archives/1.0.0-rc10/source/core'
p=core/'tunnel/delivery_control.go';s=p.read_text()
a='residence := max(time.Duration(0), now.Sub(r.admitted))';assert s.count(a)==1
s=s.replace(a,a+'''
\t\t\tif !r.fast && !r.repaired && !r.accepted.IsZero() {
\t\t\t\tresidence = max(time.Duration(0), now.Sub(r.accepted))
\t\t\t}''')
a='\t\tresidence := originalAge';assert s.count(a)==1
s=s.replace(a,a+'''
\t\t// A definitive original APPEND separates preparation/grant wait from
\t\t// physical BODY/ACK service. Logical flight stays fully charged. Missing
\t\t// or unknown acceptance cannot reset an outage clock, and short DATA
\t\t// keeps its complete application-visible residence.
\t\tif !r.fast && !r.repaired && !r.accepted.IsZero() {
\t\t\tresidence = max(time.Duration(0), now.Sub(r.accepted))
\t\t}''')
p.write_text(s)
(p.parent/'rc10_publication_feedback_test.go').write_text('''package tunnel
import("testing";"time")
func TestRC10GrantPreparationCannotImpersonateKnownBodyQueue(t *testing.T){
 now:=time.Now()
 for _,known:=range []bool{false,true}{
  d:=newDeliveryController();d.window=256<<10
  r:=&deliveryRange{key:deliveryKey{1,0},end:64<<10,published:true,admitted:now.Add(-10*time.Second)}
  if known{r.accepted=now.Add(-200*time.Millisecond)}
  d.ranges[r.key]=r;d.bulk=r.end;d.delivered=4096;d.bulkDelivered=4096
  d.policy.lastProgress=now.Add(-10*time.Second);d.policy.delivered=4096
  d.maintain(now,true)
  if known&&(d.observedAgeMS!=200||d.policy.mode==deliveryOutage){t.Fatal("known physical service includes old grant wait",d.observedAgeMS,d.policy.mode)}
  if !known&&(d.observedAgeMS!=10000||d.policy.mode!=deliveryOutage){t.Fatal("unknown APPEND lost original outage clock",d.observedAgeMS,d.policy.mode)}
  if d.oldestAgeMS!=10000||d.bulk!=r.end||len(d.ranges)!=1||d.delivered!=4096{t.Fatal("service feedback changed debt or admission diagnostics")}
 }
}
func TestRC10ShortResidenceStillIncludesPreparation(t *testing.T){
 now:=time.Now();d:=newDeliveryController()
 r:=&deliveryRange{key:deliveryKey{1,0},end:1024,fast:true,admitted:now.Add(-900*time.Millisecond),accepted:now.Add(-100*time.Millisecond)}
 d.ranges[r.key]=r;d.fast=1024;d.progress(1,1024,now)
 if d.lastFastACKAgeMS!=900||d.delivered!=1024||d.fast!=0{t.Fatal("physical timing hid short application delay",d.lastFastACKAgeMS)}
}
''')
