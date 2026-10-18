#!/usr/bin/env python3
"""LAB ONLY: use the existing preparation queue for unmeasured pool capacity.
Cold capacity is not multiplied by all accounts at once. Once the same healthy
half-pool that owns batching demonstrates full service, retain original
parallelism across quiet periods and repair. No new I/O gate or credit exists.
"""
from pathlib import Path
r=Path(__file__).resolve().parents[3];d=r/'archives/1.0.0-rc10/source/core/tunnel'
p=d/'lane.go';s=p.read_text();a='uploadBatchTarget uint64';assert s.count(a)==1;s=s.replace(a,a+'\n\tbulkParallelismWarm bool\n\tbulkWarmBodies int');p.write_text(s)
p=d/'preparation.go';s=p.read_text();a='''\towners := map[*lane]bool{}''';assert s.count(a)==1
s=s.replace(a,'''\t// Existing FIFO and cancellation own this admission, before sealing or
\t// any physical Prepare/Spend. Fast DATA bypasses preparation as before.
\tif slots > 2 && !g.bulkParallelismWarmLocked(quiet) { slots = 2 }
'''+a)
s+='''
// Full-sized successful uplink probes or complete authenticated peer BODY
// receipts are capacity observations only, never physical quota or ACK credit.
func (g *stripeGroup) bulkParallelismWarmLocked(quiet [2]*lane) bool {
 ready,warm:=0,0
 for _,l:=range g.lanes {
  if l==nil || l==quiet[0] || l==quiet[1] || !l.streamCarrierReady() || l.ctx.Err()!=nil {continue}
  ready++
  l.serviceMu.Lock()
  measured:=l.uploadBatchTarget>=uint64(datapath.BulkBatchTargetBytes)
  if l.side {measured=l.bulkWarmBodies>=2}
  if measured {l.bulkParallelismWarm=true}
  if l.bulkParallelismWarm {warm++}
  l.serviceMu.Unlock()
 }
 return ready==0 || warm>=(ready+1)/2
}
''';p.write_text(s)
p=d/'service_policy.go';s=p.read_text()
a='''\telapsed := now.Sub(started)
\tl.serviceMu.Lock()
\tdefer l.serviceMu.Unlock()'''
b=a+'''\n\t// Small fast receipts may probe the next batch, but do not prove
\t// that all accounts may concurrently transfer a full-sized BODY.
\tif uint64(bytes) >= uint64(datapath.BulkBatchTargetBytes) && elapsed <= max(250*time.Millisecond, min(2*time.Second, 2*max(l.providerBaseline,time.Duration(l.idleRTT.Load())*time.Millisecond))) { l.bulkWarmBodies++ }'''
assert s.count(a)==1;s=s.replace(a,b);p.write_text(s)
(d/'rc10_preparation_test.go').write_text('''package tunnel
import("context";"fmt";"testing";"time";"itom.local/core/wire")
func TestRC10SmallPeerReceiptsCannotProveFullBodyParallelism(t *testing.T){
 l:=&lane{side:true}
 for i:=0;i<16;i++{l.observePeerReadService(32<<10,time.Now().Add(-100*time.Millisecond),time.Now())}
 if l.bulkWarmBodies!=0{t.Fatal("small receipts proved full BODY service")}
 for i:=0;i<8;i++{l.observePeerReadService(11*(32<<10),time.Now().Add(-600*time.Millisecond),time.Now())}
 if l.bulkWarmBodies!=0{t.Fatal("paced full BODYs proved uncongested parallelism")}
 for i:=0;i<2;i++{l.observePeerReadService(11*(32<<10),time.Now().Add(-100*time.Millisecond),time.Now())}
 if l.bulkWarmBodies!=2{t.Fatal("full physical receipts did not prove service")}
}
func TestRC10PreparationExpandsOnlyAfterUsefulCapacityWithoutLosingLeases(t *testing.T){
 g:=newStripeGroup(wire.ID{1},1)
 var owner *lane
 for i:=1;i<=10;i++{l:=stripeTestLane(g,fmt.Sprintf("a%02d",i),false);defer l.cancel();if err:=g.attach(l);err!=nil{t.Fatal(err)};if owner==nil{owner=l}}
 owner.admitted.Store(1);g.owners[1]=owner
 var release []func()
 for i:=0;i<2;i++{fn,err:=owner.AcquireBulkPreparation(context.Background());if err!=nil{t.Fatal(err)};release=append(release,fn)}
 ctx,cancel:=context.WithCancel(context.Background());defer cancel();got:=make(chan func(),1);errs:=make(chan error,1)
 go func(){fn,err:=owner.AcquireBulkPreparation(ctx);if err!=nil{errs<-err}else{got<-fn}}()
 select{case fn:=<-got:fn();t.Fatal("unmeasured account count multiplied preparation");case err:=<-errs:t.Fatal(err);case <-time.After(20*time.Millisecond):}
 fast,err:=g.acquireForClass(context.Background(),nil,true);if err!=nil{t.Fatal("cold preparation blocked fast DATA",err)};fast.release()
 for _,l:=range g.lanes{for i:=0;i<8;i++{l.observeUploadBatch(11*(32<<10),80*time.Millisecond,true,false)}}
 g.signal()
 select{case fn:=<-got:release=append(release,fn);case err:=<-errs:t.Fatal(err);case <-time.After(time.Second):t.Fatal("confirmed warm capacity did not wake preparation")}
 g.mu.Lock();n:=g.prepared;g.mu.Unlock();if n!=3{t.Fatal("expansion lost old leases",n)}
 for _,fn:=range release{fn();fn()}
 g.mu.Lock();defer g.mu.Unlock();if g.prepared!=0||len(g.preparation)!=0{t.Fatal("preparation lease leaked")}
}
''')
