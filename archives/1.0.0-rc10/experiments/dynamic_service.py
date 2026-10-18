#!/usr/bin/env python3
"""LAB ONLY: size-proven publication service and shared unloaded observation.
Apply after pressure_escape.py650 and client_cold.py. No quotas, repair/identity,
logical ceilings, new I/O authority, or qualification changes.
"""
from pathlib import Path
import argparse
parser=argparse.ArgumentParser();parser.add_argument('slack',type=int,choices=[250,400]);args=parser.parse_args()
r=Path(__file__).resolve().parents[3];d=r/'archives/1.0.0-rc10/source/core/tunnel'
p=d/'stripe.go';s=p.read_text();a='\tdelivery           deliveryController';assert s.count(a)==1;s=s.replace(a,a+'\n\tunloadedBaselineMS atomic.Int64 // published copy of cold authenticated pool observation');p.write_text(s)
p=d/'delivery_control.go';s=p.read_text();a='d.baselineMS = sample';assert s.count(a)==1;s=s.replace(a,a+'\n\t\tif cold { g.unloadedBaselineMS.Store(sample) }');p.write_text(s)
p=d/'lane.go';s=p.read_text();a='peerReadSlowCount, peerReadFastCount                uint32';assert s.count(a)==1;s=s.replace(a,a+'\n\tpeerReadSmallFloor time.Duration');p.write_text(s)
p=d/'service_policy.go';s=p.read_text()
a='budget := max(time.Second, 4*l.providerBaseline, 4*time.Duration(l.idleRTT.Load())*time.Millisecond)';assert s.count(a)==1
s=s.replace(a,'''// Late accounts reuse only the independently unloaded pool observation.
\t// It remains propagation evidence, never measured BODY capacity.
\tpoolBaseline:=time.Duration(0)
\tif l.stripe!=nil {poolBaseline=time.Duration(l.stripe.unloadedBaselineMS.Load())*time.Millisecond}
\tbudget := max(time.Second,4*l.providerBaseline,4*time.Duration(l.idleRTT.Load())*time.Millisecond,4*poolBaseline)''')
a='\tdefer l.serviceMu.Unlock()\n\tif now.Sub(l.peerReadAt)'
b='''\tdefer l.serviceMu.Unlock()
\t// A real small BODY measures fixed mail-command overhead without
\t// pretending that small demand proves a large literal's capacity. Only
\t// samples within the original propagation-aware budget can set this floor.
\tif now.Sub(l.peerReadAt)>30*time.Second { l.peerReadSmallFloor=0 }
\tif bytes<=64<<10 && elapsed<=budget && (l.peerReadSmallFloor==0 || elapsed<l.peerReadSmallFloor) {l.peerReadSmallFloor=elapsed}
\tif l.peerReadSmallFloor>0 {budget=min(budget,max(350*time.Millisecond,l.peerReadSmallFloor+SERVICE_SLACK*time.Millisecond))}
\tif now.Sub(l.peerReadAt)'''.replace('SERVICE_SLACK',str(args.slack))
assert s.count(a)==1;s=s.replace(a,b)
a='if elapsed > 2*budget {';assert s.count(a)==2
# Both local APPEND and authenticated BODY service reject a repeatedly too-large
# actual literal at their measured budget, rather than waiting twice as long.
s=s.replace(a,'if elapsed > budget {')
a='target := min(uint64(datapath.BulkBatchTargetBytes), max(uint64(32<<10), uint64(bytes)*uint64(budget)/uint64(elapsed)))';assert s.count(a)==1
s=s.replace(a,'''previous:=l.peerReadBatchTarget
\t\t\tif previous==0 {previous=uint64(datapath.BulkBatchTargetBytes)}
\t\t\ttarget := max(uint64(32<<10),min(previous/2,uint64(bytes)*uint64(budget)/uint64(elapsed)))''')
a='if elapsed <= budget && l.peerReadBatchTarget != 0 {';assert s.count(a)==1
s=s.replace(a,'if elapsed <= budget && l.peerReadBatchTarget != 0 && uint64(bytes) >= l.peerReadBatchTarget {')
p.write_text(s)
(d/'rc10_dynamic_service_test.go').write_text('''package tunnel
import("testing";"time";"itom.local/core/wire")
func TestRC10SharedBaselineNeverAcceptsLoadedOrHigherColdObservation(t *testing.T){
 g:=newStripeGroup(wire.ID{1},1);g.observeProviderBaseline(300*time.Millisecond)
 if g.unloadedBaselineMS.Load()!=300{t.Fatal("unloaded baseline not published")}
 g.observeProviderBaseline(350*time.Millisecond)
 if g.unloadedBaselineMS.Load()!=300{t.Fatal("higher cold observation raised baseline")}
 g.active.Store(1);g.observeProviderBaseline(2*time.Second)
 if g.unloadedBaselineMS.Load()!=300{t.Fatal("loaded observation raised baseline")}
 unknown:=newStripeGroup(wire.ID{2},1);unknown.active.Store(1);unknown.observeProviderBaseline(2*time.Second)
 if unknown.unloadedBaselineMS.Load()!=0{t.Fatal("first loaded observation invented an unloaded baseline")}
}
func TestRC10PeerReadNeedsSizedProofAndRejectsRepeatedSlowProbe(t *testing.T){
 g:=newStripeGroup(wire.ID{1},1);g.observeProviderBaseline(300*time.Millisecond)
 l:=&lane{side:true,stripe:g}
 for i:=0;i<2;i++{now:=time.Now();l.observePeerReadService(32<<10,now.Add(-300*time.Millisecond),now)}
 if l.peerReadBatchTarget!=64<<10{t.Fatal("first actual probe did not grow",l.peerReadBatchTarget)}
 for i:=0;i<8;i++{now:=time.Now();l.observePeerReadService(32<<10,now.Add(-300*time.Millisecond),now)}
 if l.peerReadBatchTarget!=64<<10{t.Fatal("small receipts pretended to prove a larger BODY")}
 // The fresh lane has no private idle sample: its true pool propagation must
 // keep two healthy600ms full-size turns from being treated as access queue.
 for i:=0;i<2;i++{now:=time.Now();l.observePeerReadService(64<<10,now.Add(-500*time.Millisecond),now)}
 if l.peerReadBatchTarget!=128<<10{t.Fatal("shared propagation was ignored",l.peerReadBatchTarget)}
 for i:=0;i<2;i++{now:=time.Now();l.observePeerReadService(128<<10,now.Add(-1500*time.Millisecond),now)}
 if l.peerReadBatchTarget>64<<10{t.Fatal("slow observed probe did not shrink",l.peerReadBatchTarget)}
}
func TestRC10SmallServiceFloorExpiresBeforeNewBulkEpoch(t *testing.T){
 l:=&lane{side:true,peerReadSmallFloor:50*time.Millisecond,peerReadAt:time.Now().Add(-31*time.Second)}
 for i:=0;i<2;i++{now:=time.Now();l.observePeerReadService(32<<10,now.Add(-600*time.Millisecond),now)}
 if l.peerReadSmallFloor<600*time.Millisecond || l.peerReadBatchTarget!=64<<10{t.Fatal("old epoch permanently throttled a new service regime",l.peerReadSmallFloor,l.peerReadBatchTarget)}
}

''')

p=d/'rev5_peer_read_policy_test.go';s=p.read_text()
s=s.replace('receipt(32<<10, 100*time.Millisecond, true, false)','receipt(int64(max(uint64(32<<10),l.peerReadBatchTarget)), 100*time.Millisecond, true, false)')
s=s.replace('l.observePeerReadService(32<<10, time.Now().Add(-2*time.Second), time.Now())','l.observePeerReadService(int64(max(uint64(32<<10),l.peerReadBatchTarget)), time.Now().Add(-2*time.Second), time.Now())')
p.write_text(s)
