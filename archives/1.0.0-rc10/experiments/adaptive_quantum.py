#!/usr/bin/env python3
"""LAB ONLY: resize bulk frames with the existing shared delivery window.
No physical credit, repair eligibility, receive window or wire change.
"""
from pathlib import Path
import sys
root=Path(__file__).resolve().parents[3]
core=Path(sys.argv[1]) if len(sys.argv)>1 else root/'archives/1.0.0-rc10/source/core'
p=core/'datapath/pump.go';s=p.read_text()
a='BulkAdmissionLimit        func() uint64';assert s.count(a)==1
s=s.replace(a,a+'\n\t// Optional bulk record quantum. It is not a credit or publication grant.\n\tBulkRecordLimit func() uint64')
a='\t\tcapacity := p.engine.AdmissionCapacityReserved(p.cfg.Engine.Generation, id, reserve)';assert s.count(a)==1
s=s.replace(a,'''\t\trecordBytes := len(r.data)
\t\tif r.bulk && p.cfg.BulkRecordLimit != nil && p.engine.pendingStreamBytes(p.cfg.Engine.Generation,id)==0 {
\t\t\t// Quantize independently of odd remaining queue headroom. Already
\t\t\t// queued records stay indivisible, and short traffic keeps its class.
\t\t\trecordBytes=min(recordBytes,int(max(uint64(4<<10),min(uint64(wire.MaxData),p.cfg.BulkRecordLimit()))))
\t\t}
'''+a)
assert s.count('headroom < uint64(len(r.data))')==2
s=s.replace('headroom < uint64(len(r.data))','headroom < uint64(recordBytes)')
a='n := min(len(r.data), int(capacity))';assert s.count(a)==1
s=s.replace(a,'n := min(recordBytes, int(capacity))')
a='\t\tr.data = r.data[n:]';assert s.count(a)==1
s=s.replace(a,'''\t\t// The actor knows this read was bulk before deliberate prefix segmentation.
\t\t// A small quantum must never spend the short-message reserve or get a
\t\t// fast lane merely because it is smaller than the interactive threshold.
\t\tif r.bulk { p.engine.streams[id].bulk=true }
'''+a)
p.write_text(s)
p=core/'tunnel/lane.go';s=p.read_text();a='BulkAdmissionLimit: l.bulkAdmissionLimit,';assert s.count(a)==1
s=s.replace(a,a+' BulkRecordLimit: l.bulkRecordLimit,');p.write_text(s)
p=core/'tunnel/bulk_admission.go';s=p.read_text();s+='''
// Small windows retain independent stream discovery without multiplying
// 32 KiB of new flight by every empty socket. Full records return at 1 MiB.
func (l *lane) bulkRecordLimit() uint64 {
 if l.stripe==nil { return 32<<10 }
 l.stripe.mu.Lock();defer l.stripe.mu.Unlock()
 quantum:=max(uint64(4<<10),min(uint64(32<<10),l.stripe.delivery.window/32))
 size:=uint64(4<<10);for size*2<=quantum {size*=2};return size
}
''';p.write_text(s)
p=core/'tunnel/delivery_control.go';s=p.read_text();a='deliveryInitialWindow = 1 << 20';assert s.count(a)==1
s=s.replace(a,'deliveryInitialWindow = 256 << 10');p.write_text(s)
(core/'datapath/rc10_quantum_test.go').write_text('''package datapath
import("testing";"context";"itom.local/core/wire")
func TestRC10BulkQuantumKeepsClassOffsetsAndShortReserve(t *testing.T){
 cfg:=DefaultConfig(1,[]string{"a01"});e,err:=New(context.Background(),cfg);if err!=nil{t.Fatal(err)};defer e.Stop()
 for _,id:=range []uint64{1,3}{e.Activate(1,id);f,_:=wire.EncodeControl(id,wire.Credit{MaxStream:1<<20,MaxData:4<<20});if _,err=e.Receive(1,f);err!=nil{t.Fatal(err)}}
 quantum:=uint64(4096);p:=&Pump{engine:e,cfg:PumpConfig{Engine:cfg,BulkAdmissionLimit:func()uint64{return 4<<20},BulkRecordLimit:func()uint64{return quantum}},sockets:map[uint64]*socketState{}}
 r:=&readResult{bulk:true,data:make([]byte,wire.MaxData),ack:make(chan struct{})};s:=&socketState{read:r};p.sockets[1]=s
 for i:=0;i<2;i++{if err=p.acceptRead(1,s);err!=nil{t.Fatal(err)}}
 if s.read!=nil||e.tx.LogicalBytes()!=wire.MaxData||e.ItemInteractive(1,4096){t.Fatal("bulk segmentation changed debt or spent short class")}
 for item:=uint64(1);item<=2;item++{f,n,err:=e.FrameInfo(1,item);wantN,wantOffset:=uint64(4096),uint64(0);if item==2{wantN=wire.MaxData-4096;wantOffset=4096};if err!=nil||n!=wantN||f.Offset!=wantOffset{t.Fatal("lost exact byte order",item,f,n,err)}}
 short:=&socketState{read:&readResult{data:make([]byte,100),ack:make(chan struct{})}};p.sockets[3]=short
 if err=p.acceptRead(3,short);err!=nil||!e.ItemInteractive(3,100){t.Fatal("short exchange lost interactive priority",err)}
 quantum=wire.MaxData;s.read=&readResult{bulk:true,data:make([]byte,wire.MaxData),ack:make(chan struct{})}
 if err=p.acceptRead(1,s);err!=nil||s.read!=nil{t.Fatal("full quantum did not resume",err)}
 _,n,err:=e.FrameInfo(1,4);if err!=nil||n!=wire.MaxData{t.Fatal("warm bulk remained fragmented",n,err)}
}
''')
(core/'tunnel/rc10_quantum_test.go').write_text('''package tunnel
import("testing";"itom.local/core/wire")
func TestRC10QuantumFollowsOnlySharedWindow(t *testing.T){
 g:=newStripeGroup(wire.ID{1},1);l:=stripeTestLane(g,"a01",false);defer l.cancel();l.stripe=g
 for _,x:=range []struct{window,want uint64}{{64<<10,4<<10},{256<<10,8<<10},{512<<10,16<<10},{1<<20,32<<10},{16<<20,32<<10}}{g.delivery.window=x.window;if got:=l.bulkRecordLimit();got!=x.want{t.Fatal(x,got)}}
}
''')
