#!/usr/bin/env python3
"""LAB ONLY. Bound unmeasured epochs, retain RC2 growth and recovery policies.
Two real unloaded authenticated short ACKs initialize early queue feedback.
"""
from pathlib import Path
import sys
root=Path(__file__).resolve().parents[3]
core=Path(sys.argv[1]) if len(sys.argv)>1 else root/'archives/1.0.0-rc10/source/core'
p=core/'tunnel/delivery_control.go';s=p.read_text()
a='''seed := uint64(deliveryInitialWindow)
\t\t\tif sample >= 250 {
\t\t\t\tseed = 256 << 10
\t\t\t}'''
assert s.count(a)==1
s=s.replace(a,'''// Propagation cannot establish capacity. Retain original half-window
\t\t\t// growth and independent-stream discovery after the cold probe.
\t\t\tseed := uint64(128 << 10)''')
assert s.count('d.fastBaselineCount < 4')==1
s=s.replace('d.fastBaselineCount < 4','d.fastBaselineCount < 2')
p.write_text(s)
(p.parent/'rc10_cold_feedback_test.go').write_text('''package tunnel
import("testing";"time";"itom.local/core/wire")
func TestRC10ColdFeedbackUsesRealUnloadedEvidence(t *testing.T){
 g:=newStripeGroup(wire.ID{1},1);g.observeProviderBaseline(80*time.Millisecond)
 d:=&g.delivery
 if d.window!=128<<10||d.initialWindow!=128<<10{t.Fatal("cold propagation invented capacity")}
 if d.fastPathBaseline()!=0{t.Fatal("NOOP invented authenticated short baseline")}
 now:=time.Now()
 for id:=uint64(1);id<=2;id++{
  r:=&deliveryRange{key:deliveryKey{id,0},end:1024,fast:true,unloadedFast:true,isolatedFast:true,admitted:now.Add(-200*time.Millisecond)}
  d.ranges[r.key]=r;d.fast+=1024;d.progress(id,1024,now)
 }
 if d.fastPathBaseline()!=200*time.Millisecond||d.fastAgeBudget()!=300*time.Millisecond{t.Fatal("cold ACKs did not initialize bounded feedback",d.fastPathBaseline(),d.fastAgeBudget())}
 if d.delivered!=2048||d.fast!=0{t.Fatal("calibration changed authenticated logical accounting")}
 g.active.Store(1);g.observeProviderBaseline(2*time.Second)
 if d.initialWindow!=128<<10{t.Fatal("loaded baseline changed epoch seed")}
}
''')
