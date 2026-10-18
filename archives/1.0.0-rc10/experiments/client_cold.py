#!/usr/bin/env python3
"""LAB ONLY: measured C2S APPEND probes; no logical-window or recovery changes.
Actual useful batch sizes must be demonstrated before growing. Fixed command
latency must not turn small demand-limited samples into a permanent rate cap.
"""
from pathlib import Path
r=Path(__file__).resolve().parents[3];d=r/'archives/1.0.0-rc10/source/core/tunnel'
p=d/'lane.go';s=p.read_text();a='peerReadPendingBodies                       atomic.Int64';assert s.count(a)==1
s=s.replace(a,a+'\n\tuploadBatchTarget uint64\n\tuploadBatchFastCount int')
a='''\tappendErr := appendData(opCtx, l.mail, folder, mime, fastBatch)
\tl.observeDataServiceClass(len(mime), time.Since(appendStarted), appendErr == nil, fastBatch)'''
b='''\tappendErr := appendData(opCtx, l.mail, folder, mime, fastBatch)
\telapsed := time.Since(appendStarted)
\tl.observeDataServiceClass(len(mime), elapsed, appendErr == nil, fastBatch)
\tl.observeUploadBatch(uint64(payloadBytes), elapsed, appendErr == nil, fastBatch)'''
assert s.count(a)==1;s=s.replace(a,b);p.write_text(s)
p=d/'service_policy.go';s=p.read_text();a='target := uint64(datapath.BulkBatchTargetBytes)';assert s.count(a)==1
s=s.replace(a,a+'''
\tif !l.side {
\t\tupload := l.uploadBatchTarget
\t\tif upload == 0 { upload = 32<<10 }
\t\ttarget = min(target,upload)
\t}''')
s+='''
// Successful local APPEND measures this endpoint's uplink, not peer BODY
// delivery. Only two complete, sufficiently large bulk turns can grow the next
// tentative batch. The server's authenticated peer-BODY policy is unchanged.
func (l *lane) observeUploadBatch(useful uint64, elapsed time.Duration, accepted, fast bool) {
 if l.side || fast || !accepted || useful < 32<<10 || useful > uint64(dataBatchMIMEHardCap) || elapsed <= 0 { return }
 budget := max(250*time.Millisecond, min(2*time.Second, 2*l.providerBaseline))
 l.serviceMu.Lock()
 defer l.serviceMu.Unlock()
 target := l.uploadBatchTarget
 if target == 0 { target = 32<<10 }
 if target >= uint64(datapath.BulkBatchTargetBytes) { return }
 if useful >= target && elapsed <= budget {
  l.uploadBatchFastCount++
  if l.uploadBatchFastCount >= 2 { l.uploadBatchTarget = min(uint64(datapath.BulkBatchTargetBytes), 2*target); l.uploadBatchFastCount=0 }
 } else { l.uploadBatchFastCount=0 }
}
'''
p.write_text(s)
# Establish real healthy upload capacity in this existing majority-pool fixture.
p=d/'rev5_peer_read_policy_test.go';s=p.read_text();a='''\t\tl.rtt.Store(42)
\t\tg.lanes[l.account.ID] = l''';assert s.count(a)==1
s=s.replace(a,'''\t\tl.rtt.Store(42)
\t\tfor n := 0; n < 8; n++ { l.observeUploadBatch(344064,40*time.Millisecond,true,false) }
\t\tg.lanes[l.account.ID] = l''');p.write_text(s)
# Keep the existing slow-literal test scoped to an established upload path.
p=d/'service_policy_test.go';s=p.read_text();a='l := &lane{}';assert a in s
s=s.replace(a,'l := &lane{}\n\tfor i := 0; i < 8; i++ { l.observeUploadBatch(11*(32<<10),80*time.Millisecond,true,false) }',1);p.write_text(s)
(d/'rc10_client_cold_test.go').write_text('''package tunnel
import("testing";"time";"itom.local/core/datapath")
func TestRC10ClientColdUplinkUsesOnlyConfirmedSizedBulkService(t *testing.T){
 l:=&lane{}
 for i:=0;i<8;i++ {l.observeUploadBatch(344064,time.Millisecond,false,false);l.observeUploadBatch(344064,time.Millisecond,true,true)}
 if l.serviceBatchTarget()!=32<<10 {t.Fatal("unknown/control APPEND trained uplink")}
 for i:=0;i<2;i++ {l.observeUploadBatch(32<<10,80*time.Millisecond,true,false)}
 if l.serviceBatchTarget()!=64<<10 {t.Fatal("confirmed first probe cannot grow")}
 for i:=0;i<12;i++ {l.observeUploadBatch(32<<10,80*time.Millisecond,true,false)}
 if l.serviceBatchTarget()!=64<<10 {t.Fatal("small samples pretended to prove larger batches")}
 for _,n:=range []uint64{64<<10,128<<10,256<<10} {for i:=0;i<2;i++ {l.observeUploadBatch(n,80*time.Millisecond,true,false)}}
 if l.serviceBatchTarget()!=datapath.BulkBatchTargetBytes {t.Fatal("fast upload remained cold")}
 slow:=&lane{}
 for i:=0;i<20;i++ {slow.observeUploadBatch(32<<10,350*time.Millisecond,true,false)}
 if slow.serviceBatchTarget()!=32<<10 {t.Fatal("slow small literal allowed a large burst")}
 mobile:=&lane{providerBaseline:300*time.Millisecond}
 for i:=0;i<8;i++ {mobile.observeUploadBatch(11*(32<<10),350*time.Millisecond,true,false)}
 if mobile.serviceBatchTarget()!=datapath.BulkBatchTargetBytes {t.Fatal("true propagation permanently capped upload")}
 server:=&lane{side:true}
 for i:=0;i<8;i++ {server.observeUploadBatch(344064,time.Millisecond,true,false)}
 if server.uploadBatchTarget!=0||server.peerReadSamples!=0||server.serviceBatchTarget()!=32<<10{t.Fatal("uplink APPEND invented peer download proof")}
}
''')
