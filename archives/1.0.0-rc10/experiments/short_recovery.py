#!/usr/bin/env python3
"""LAB ONLY: bounded, authenticated recovery of unresolved short DATA.
Keep bulk polling/grace, physical quota, unknown outcomes and RC2 windows intact.
Timers advertise progress only; a fresh authenticated NACK still gates DATA.
"""
from pathlib import Path
import sys
root=Path(__file__).resolve().parents[3]
core=Path(sys.argv[1]) if len(sys.argv)>1 else root/'archives/1.0.0-rc10/source/core'
d=core/'tunnel';p=d/'lane.go';s=p.read_text()
a='cfg.InteractiveProbeInterval = 400 * time.Millisecond';assert s.count(a)==1
s=s.replace(a,'cfg.InteractiveProbeInterval = 200 * time.Millisecond');p.write_text(s)
p=d/'delivery_control.go';s=p.read_text()
a='min(650*time.Millisecond, d.fastAgeBudget()/2)';assert s.count(a)==1
s=s.replace(a,'min(650*time.Millisecond, d.fastPathBaseline()/2)');p.write_text(s)
p=d/'stripe.go';s=p.read_text()
a='\t\t// Bulk cumulative receive feedback shares stable non-reserved readers.';assert s.count(a)==1
s=s.replace(a,'''\t\t// Recovery progress must not be tied to the physical path still holding
\t\t// this stream's unacknowledged short BODY. Use an available sibling for
\t\t// STATUS; retain the original path as a last resort. No DATA is retried
\t\t// here, and account-scoped grant/cleanup control never enters this route.
\t\tif f.Type==wire.Status && f.Offset>0 {
\t\t\tblocked:=map[string]bool{}
\t\t\tfor _,r:=range g.delivery.ranges {if r.key.stream==f.Stream && r.fast && r.published && r.carrier!="" {blocked[r.carrier]=true}}
\t\t\tif len(blocked)>0 {
\t\t\t\tvar alternatives []*lane
\t\t\t\tfor _,c:=range candidates {if !blocked[c.account.ID]{alternatives=append(alternatives,c)}}
\t\t\t\tif len(alternatives)>0 {candidates=alternatives}
\t\t\t}
\t\t}
'''+a);p.write_text(s)
(d/'rc10_short_recovery_test.go').write_text('''package tunnel
import("testing";"context";"time";"itom.local/core/wire")
func TestRC10RecoveryStatusAvoidsPendingShortBodyAndKeepsLastResort(t *testing.T){
 g:=newStripeGroup(wire.ID{1},1)
 a,b:=stripeTestLane(g,"a01",false),stripeTestLane(g,"a02",false);defer a.cancel();defer b.cancel()
 for _,l:=range []*lane{a,b}{l.controlQueue=make(chan queuedControl,8);l.urgentQueue=make(chan queuedControl,8);l.maintenanceQueue=make(chan queuedControl,8);if err:=g.attach(l);err!=nil{t.Fatal(err)}}
 if err:=g.register(1,a);err!=nil{t.Fatal(err)}
 g.shortAffinity[1]="a01"
 r:=&deliveryRange{key:deliveryKey{1,0},end:9,fast:true,published:true,accepted:time.Now(),carrier:"a01"}
 g.delivery.ranges[r.key]=r;g.delivery.fast=9
 f,err:=wire.EncodeControl(1,wire.Progress{Attempted:9,MaxStream:runtimeStreamWindow,MaxData:runtimeReceiveBytes});if err!=nil{t.Fatal(err)}
 if err=g.sendStreamControl(context.Background(),a,f);err!=nil{t.Fatal(err)}
 if len(b.urgentQueue)!=1||len(a.urgentQueue)!=0{t.Fatal("progress recovery repeated stalled short path")}
 g.detach(b)
 if err=g.sendStreamControl(context.Background(),a,f);err!=nil{t.Fatal("last physical path was stranded",err)}
 if len(a.urgentQueue)!=1||g.delivery.fast!=9||g.delivery.delivered!=0{t.Fatal("alternate route changed logical debt or lost fallback")}
}
func TestRC10BulkFloorCannotDelayAuthenticatedShortRepair(t *testing.T){
 d:=newDeliveryController();now:=time.Now()
 for i:=range d.lowFlightAges{d.lowFlightAges[i]=2*time.Second};d.lowFlightCount=len(d.lowFlightAges)
 r:=&deliveryRange{key:deliveryKey{1,0},end:9,fast:true,published:true,admitted:now.Add(-time.Second),accepted:now.Add(-300*time.Millisecond)}
 d.ranges[r.key]=r;d.fast=9
 if d.deferAcceptedRepair(1,0,9,now){t.Fatal("loaded bulk floor stretched short repair grace")}
 if d.fast!=9||d.delivered!=0{t.Fatal("eligibility refunded unknown physical or logical bytes")}
 r.accepted=now
 if !d.deferAcceptedRepair(1,0,9,now.Add(200*time.Millisecond)){t.Fatal("short known BODY lost original visibility grace")}
}
''')
(core/'datapath/rc10_short_probe_test.go').write_text('''package datapath
import("testing";"time";"context";"itom.local/core/flow";"itom.local/core/wire")
func TestRC10EarlyShortProbeStillRequiresNACKAndGrace(t *testing.T){
 cfg:=DefaultConfig(1,[]string{"a01"});cfg.DeliveryGrace=1750*time.Millisecond;cfg.InteractiveDeliveryGrace=250*time.Millisecond;cfg.InteractiveProbeInterval=200*time.Millisecond
 a,err:=New(context.Background(),cfg);if err!=nil{t.Fatal(err)};defer a.Stop()
 b,err:=New(context.Background(),cfg);if err!=nil{t.Fatal(err)};defer b.Stop()
 for _,id:=range []uint64{1,3}{a.Activate(1,id);credit,err:=b.Activate(1,id);if err!=nil{t.Fatal(err)};if _,err=a.Receive(1,credit);err!=nil{t.Fatal(err)}}
 if _,err=a.Poll(1,0);err!=nil{t.Fatal(err)}
 var ids []uint64
 for i,n:=range []int{4096,32<<10}{id,err:=a.Queue(1,uint64(1+2*i),make([]byte,n));if err!=nil{t.Fatal(err)};ids=append(ids,id);j,err:=a.Begin(1,id,"a01",uint64(2*n),0);if err!=nil{t.Fatal(err)};if err=a.Complete(1,j.Attempt().ID,flow.Committed);err!=nil{t.Fatal(err)}}
 controls,err:=a.Poll(1,200*time.Millisecond);if err!=nil||len(controls)==0{t.Fatal("no bounded early progress",err)}
 if a.RepairReady(ids[0])||a.RepairReady(ids[1]){t.Fatal("timer authorized a DATA copy")}
 for _,f:=range controls{if f.Stream!=1{t.Fatal("short probe accelerated bulk")};reply,err:=b.Receive(1,f);if err!=nil{t.Fatal(err)};for _,x:=range reply{if _,err=a.Receive(1,x);err!=nil{t.Fatal(err)}}}
 if a.RepairReady(ids[0]){t.Fatal("NACK bypassed known BODY grace")}
 if _,err=a.Poll(1,250*time.Millisecond);err!=nil{t.Fatal(err)}
 if !a.RepairReady(ids[0])||a.RepairReady(ids[1]){t.Fatal("only authenticated short hole should be ready")}
 nack,_:=wire.EncodeControl(3,wire.Missing{ID:1,Ranges:[]wire.ByteRange{{Start:0,End:32<<10}}});if _,err=a.Receive(1,nack);err!=nil{t.Fatal(err)}
 if a.RepairReady(ids[1])||a.tx.LogicalBytes()!=36<<10{t.Fatal("bulk grace or charged logical bytes changed")}
 a.Poll(1,4*time.Second);after,err:=a.Poll(1,4300*time.Millisecond);if err!=nil||len(after)!=0{t.Fatal("outage retained rapid short polling",err,len(after))}
}
''')
