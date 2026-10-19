#!/usr/bin/env python3
"""LAB ONLY: reject samples exposed to later flight or existing congestion.
Original RC2 seed, growth, independent stream escape and BODY service stay.
"""
from pathlib import Path
import sys
root=Path(__file__).resolve().parents[3]
core=Path(sys.argv[1]) if len(sys.argv)>1 else root/'archives/1.0.0-rc10/source/core'
p=core/'tunnel/delivery_control.go';s=p.read_text()
a='r.flightAtAdmission <= deliveryInitialWindow/2 && residence > 0';assert s.count(a)==1
s=s.replace(a,'r.flightAtAdmission <= min(d.initialWindow,uint64(deliveryInitialWindow))/2 && residence > 0 && !now.Before(d.fastPressureUntil) && d.policy.mode != deliveryCongested && d.policy.mode != deliveryRecovery && d.policy.mode != deliveryOutage')
a='\t\t\t\t\tpending.unloadedFast = false';assert s.count(a)==1
s=s.replace(a,a+'''
\t\t\t\t\t// A range admitted at low flight may still wait through a later
\t\t\t\t\t// full queue before its ACK. Keep the peak of that exposure.
\t\t\t\t\tif !w.fast && !w.repair && pending.flightAtAdmission>0 {
\t\t\t\t\t pending.flightAtAdmission=max(pending.flightAtAdmission,d.bulk+newBytes)
\t\t\t\t\t}''')
p.write_text(s)
fixture=Path(__file__).parent/'calibration.py'
import ast
for node in ast.walk(ast.parse(fixture.read_text())):
 if isinstance(node,ast.Call) and isinstance(node.func,ast.Attribute) and node.func.attr=='write_text' and isinstance(node.func.value,ast.BinOp):
  (core/'tunnel/rc10_calibration_test.go').write_text(ast.literal_eval(node.args[0]))
(core/'tunnel/rc10_honest_floor_test.go').write_text('''package tunnel
import("testing";"time")
func TestRC10CongestedRangesCannotRaiseUnloadedFloor(t *testing.T){
 now:=time.Now()
 for _,mode:=range []deliveryMode{deliveryNormal,deliveryCongested,deliveryRecovery,deliveryOutage}{
  d:=newDeliveryController();d.policy.mode=mode
  for i:=uint64(1);i<=4;i++{r:=&deliveryRange{key:deliveryKey{i,0},end:4096,admitted:now.Add(-time.Second),flightAtAdmission:4096,published:true,accepted:now.Add(-500*time.Millisecond)};d.ranges[r.key]=r;d.bulk+=4096;d.progress(i,4096,now)}
  want:=0;if mode==deliveryNormal{want=4};if d.lowFlightCount!=want{t.Fatal(mode,d.lowFlightCount,want)}
  if d.bulk!=0||d.bulkDelivered!=16384{t.Fatal("calibration changed ACK authority")}
 }
}
''')
