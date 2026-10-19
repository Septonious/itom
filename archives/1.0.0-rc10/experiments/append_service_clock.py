#!/usr/bin/env python3
"""LAB after publication_feedback: include the COMPLETE APPEND, not just its ACK.
Start after durable permission, immediately before invoking physical I/O.
Unknown outcomes still use original admission age; no timestamp grants credit.
"""
from pathlib import Path
import sys
root=Path(__file__).resolve().parents[3]
core=Path(sys.argv[1]) if len(sys.argv)>1 else root/'archives/1.0.0-rc10/source/core'
p=core/'tunnel/delivery_control.go';s=p.read_text()
assert s.count('type deliveryRange struct {')==1
s=s.replace('type deliveryRange struct {','type deliveryRange struct {\n\toriginalStarted time.Time')
a='!r.fast && !r.repaired && !r.accepted.IsZero()';assert s.count(a)==2
s=s.replace(a,a+' && !r.originalStarted.IsZero()')
a='residence = max(time.Duration(0), now.Sub(r.accepted))';assert s.count(a)==2
s=s.replace(a,'residence = max(time.Duration(0), now.Sub(r.originalStarted))')
a='\t\t\t\t\t\td.bindCarrier(r, "")';assert s.count(a)==1
s=s.replace(a,'\t\t\t\t\t\tif !r.published { r.originalStarted = time.Time{} }\n'+a)
s+='''
// Metadata only. Called after durable PrepareStarted/Spend and before APPEND,
// with no lane/journal lock held. Retirement/ACK races cannot recreate a range.
func(g *stripeGroup) beginDeliveryPublication(jobs []*datapath.Job,started time.Time){
 if g==nil||started.IsZero(){return};g.mu.Lock();defer g.mu.Unlock()
 for _,j:=range jobs{a:=j.Attempt();if a.Repair||j.Interactive(){continue}
  r:=g.delivery.ranges[deliveryKey{a.Stream,a.Offset}]
  if r!=nil&&!r.published&&r.originalStarted.IsZero(){r.originalStarted=started}
 }
}
'''
p.write_text(s)
p=core/'tunnel/lane.go';s=p.read_text()
a='appendStarted := time.Now()';assert s.count(a)==2
s=s.replace(a,a+'\n\t\tif owner.stripe != nil { owner.stripe.beginDeliveryPublication(jobs, appendStarted) }')
p.write_text(s)
p=core/'tunnel/rc10_publication_feedback_test.go';s=p.read_text()
a='r.accepted = now.Add(-200 * time.Millisecond)'
if a not in s:a='r.accepted=now.Add(-200*time.Millisecond)'
assert s.count(a)==1;s=s.replace(a,a+';r.originalStarted=now.Add(-200*time.Millisecond)');p.write_text(s)
(p.parent/'rc10_append_service_clock_test.go').write_text('''package tunnel
import("testing";"time";"context";"itom.local/core/wire";"itom.local/core/flow")
func TestRC10ServiceClockIncludesUploadSerialization(t *testing.T){
 g:=newStripeGroup(wire.ID{1},1);jobs:=deliveryJobs(t,1,1,32<<10)
 r,err:=g.reserveDeliveryBatch(context.Background(),jobs);if err!=nil{t.Fatal(err)}
 now:=time.Now();g.delivery.ranges[deliveryKey{1,0}].admitted=now.Add(-2*time.Second)
 g.beginDeliveryPublication(jobs,now.Add(-900*time.Millisecond))
 r.finish(jobs,committed(jobs,flow.Committed))
 g.delivery.lastDecision=now.Add(-time.Second);g.delivery.maintain(now,true)
 if g.delivery.observedAgeMS!=900{t.Fatal("tagged APPEND hid upload serialization",g.delivery.observedAgeMS)}
 if g.delivery.bulk!=32<<10||g.delivery.delivered!=0{t.Fatal("publication clock returned debt")}
}
func TestRC10UnknownPublicationCannotResetOriginalClock(t *testing.T){
 g:=newStripeGroup(wire.ID{1},1);jobs:=deliveryJobs(t,1,1,32<<10)
 r,err:=g.reserveDeliveryBatch(context.Background(),jobs);if err!=nil{t.Fatal(err)}
 now:=time.Now();g.delivery.ranges[deliveryKey{1,0}].admitted=now.Add(-5*time.Second)
 g.beginDeliveryPublication(jobs,now.Add(-100*time.Millisecond));r.finish(jobs,committed(jobs,flow.CommitUnknown))
 g.delivery.lastDecision=now.Add(-time.Second);g.delivery.maintain(now,true)
 if g.delivery.observedAgeMS!=5000||g.delivery.bulk!=32<<10||g.delivery.delivered!=0{t.Fatal("unknown publication changed its clock or credit",g.delivery.observedAgeMS)}
}
''')
