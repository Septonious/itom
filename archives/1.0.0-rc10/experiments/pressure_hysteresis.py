#!/usr/bin/env python3
"""LAB ONLY after early_pressure: don't burst past contracted flight on one ACK.
No additional window, physical gate, quota credit, or per-job timer.
"""
from pathlib import Path
import sys
import re
root=Path(__file__).resolve().parents[3]
core=Path(sys.argv[1]) if len(sys.argv)>1 else root/'archives/1.0.0-rc10/source/core'
p=core/'tunnel/delivery_control.go';s=p.read_text()
a=re.search(r'nextDiscoveryWake\s+time.Time',s).group();assert s.count(a)==1
s=s.replace(a,a+'\n\tdiscoveryPressureUntil time.Time')
a='d.fastPressureUntil = now.Add(deliveryFastHold)';assert s.count(a)==2
s=s.replace(a,a+'\n\t\t\t\t\t\td.discoveryPressureUntil = d.fastPressureUntil')
a='d.frontiers[a.Stream] == 0 || now.Before(d.fastPressureUntil)';assert s.count(a)==1
s=s.replace(a,'d.frontiers[a.Stream] == 0 || now.Before(d.fastPressureUntil) || !d.discoveryPressureUntil.IsZero()')
a='\td.ackFlight = 0\n\td.ageCount = 0';assert s.count(a)==1
s=s.replace(a,'''\t// A healthy short ACK may release the controller's growth hold, but it
\t// cannot immediately restore every stream's bypass while old excess flight
\t// remains. Ordinary admission and RC2 growth continue during this drain.
\t// Invisible BODYs retain the existing bounded discovery fallback.
\tif !d.discoveryPressureUntil.IsZero() && !now.Before(d.discoveryPressureUntil) &&
\t\t!now.Before(d.fastPressureUntil) && d.bulk <= d.window &&
\t\t!d.lastFastACKAt.IsZero() && time.Duration(d.lastFastACKAgeMS)*time.Millisecond <= d.fastAgeBudget() {
\t\td.discoveryPressureUntil = time.Time{}
\t}
'''+a)
p.write_text(s)
(p.parent/'rc10_pressure_hysteresis_test.go').write_text('''package tunnel
import("testing";"time")
func TestRC10EscapePressureWaitsForActualDebtDrain(t *testing.T){
 d:=newDeliveryController();now:=time.Now()
 d.window=deliveryMinWindow;d.initialWindow=deliveryMinWindow
 d.bulk=128<<10;d.discoveryPressureUntil=now.Add(-time.Second)
 d.lastFastACKAt=now;d.lastFastACKAgeMS=100
 d.bulkDemandAt=now;d.maintain(now,false)
 if d.discoveryPressureUntil.IsZero(){t.Fatal("one healthy ACK reopened every bypass with excess debt")}
 if d.bulk!=128<<10||d.delivered!=0{t.Fatal("hysteresis invented delivery credit")}
 d.bulk=0;d.lastDecision=now.Add(-time.Second);d.bulkDemandAt=time.Time{}
 d.maintain(now.Add(2*time.Second),false)
 if !d.discoveryPressureUntil.IsZero(){t.Fatal("drained recovered path retained bypass restriction")}
}
''')
