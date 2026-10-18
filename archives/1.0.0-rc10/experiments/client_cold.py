#!/usr/bin/env python3
"""LAB ONLY: measure actual uplink service before sending large client literals.
This is not a peer BODY receipt, physical quota or logical-flight grant. Server
BODY pacing, RC2 delivery windows, growth, repair and discovery stay unchanged.
"""
from pathlib import Path
r=Path(__file__).resolve().parents[3];d=r/'archives/1.0.0-rc10/source/core/tunnel';p=d/'service_policy.go';s=p.read_text();a='target := uint64(datapath.BulkBatchTargetBytes)';assert s.count(a)==1;s=s.replace(a,a+'''
\t// A new client has measured neither physical uplink throughput nor its
\t// queue. Four successful bulk APPEND turns establish the existing service
\t// estimate; unknown results and fast/control DATA cannot establish it.
\tif !l.side && l.serviceSamples < 4 { target = min(target, uint64(32<<10)) }''');a='if l.serviceSamples >= 4 && l.serviceNS > uint64(400*time.Millisecond) {'
assert s.count(a)==1
s=s.replace(a,'if l.serviceSamples >= 4 && l.serviceNS > 0 {')
s=s.replace('// A slow literal must not occupy its service for seconds. Base64/envelope', '// Normalize bytes by observed service time. A short successful literal\n\t\t// can complete under400ms on a slow link without proving a large one\n\t\t// can do so. Base64/envelope')
p.write_text(s)
# This fixture concerns one paced account versus an established healthy pool.
# Train its healthy upload service explicitly; do not call an unmeasured lane
# established capacity. The four-of-five healthy majority assertion is kept.
p=d/'rev5_peer_read_policy_test.go';s=p.read_text();a='''\t\tl.rtt.Store(42)
\t\tg.lanes[l.account.ID] = l''';assert s.count(a)==1;s=s.replace(a,'''\t\tl.rtt.Store(42)
\t\tfor n := 0; n < 4; n++ { l.observeDataService(350<<10, 40*time.Millisecond, true) }
\t\tg.lanes[l.account.ID] = l''');p.write_text(s)
(d/'rc10_client_cold_test.go').write_text('''package tunnel
import("testing";"time";"itom.local/core/datapath")
func TestRC10ClientColdUplinkUsesOnlyConfirmedBulkService(t *testing.T) {
 l:=&lane{}
 for i:=0;i<3;i++ {
  if got:=l.serviceBatchTarget();got!=32<<10 { t.Fatal("cold upload burst",i,got) }
  l.observeDataService(480<<10,80*time.Millisecond,false)
  l.observeDataServiceClass(480<<10,80*time.Millisecond,true,true)
  if l.serviceSamples!=uint64(i) {t.Fatal("unknown or control success trained bulk service")}
  l.observeDataService(48<<10,80*time.Millisecond,true)
 }
 if l.serviceBatchTarget()!=32<<10 {t.Fatal("unproven fourth turn used large literal")}
 l.observeDataService(48<<10,80*time.Millisecond,true)
 if n:=l.serviceBatchTarget(); n < 64<<10 || n > 128<<10 {t.Fatal("small fast samples authorized a full burst",n)}
 for i:=0;i<8;i++ {l.observeDataService(480<<10,80*time.Millisecond,true)}
 if l.serviceBatchTarget()!=datapath.BulkBatchTargetBytes {t.Fatal("confirmed fast uplink remained throttled")}
 slow:=&lane{}
 for i:=0;i<4;i++ {slow.observeDataService(48<<10,300*time.Millisecond,true)}
 if slow.serviceBatchTarget()!=32<<10 {t.Fatal("slow upload became a large literal")}
 server:=&lane{side:true}
 for i:=0;i<8;i++ {server.observeDataService(480<<10,80*time.Millisecond,true)}
 if server.serviceBatchTarget()!=32<<10 || server.peerReadSamples!=0 {t.Fatal("APPEND invented peer download receipt")}
}
''')
