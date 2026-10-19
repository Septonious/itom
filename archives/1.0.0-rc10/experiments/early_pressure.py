#!/usr/bin/env python3
"""LAB follow-up: isolated pressure policy, original BODY/C2S recovery/growth.
Keep RC2's 256KiB high-propagation seed; use128KiB on unmeasured fast paths.
"""
from pathlib import Path
import sys
here=Path(__file__).resolve().parent
core=Path(sys.argv[1]) if len(sys.argv)>1 else here.parents[2]/'archives/1.0.0-rc10/source/core'
base=(here/'pressure_escape.py').read_text()
a="r=Path(__file__).resolve().parents[3];p=r/'archives/1.0.0-rc10/source/core/tunnel/delivery_control.go';s=p.read_text()"
assert base.count(a)==1
base=base.replace(a,"p=core/'tunnel/delivery_control.go';s=p.read_text()")
exec(compile(base,str(here/'pressure_escape.py'),'exec'),{'__file__':str(here/'pressure_escape.py'),'core':core})
p=core/'tunnel/delivery_control.go';s=p.read_text()
a='seed := uint64(deliveryMinWindow)';assert s.count(a)==1
s=s.replace(a,'seed := uint64(128 << 10)\n\t\t\tif sample >= 250 { seed = 256 << 10 }')
p.write_text(s)
# Keep the arrival-order invariant, substituting only the experimental cold
# seed. Throughput/latency/deadline qualification assertions are untouched.
p=core/'tunnel/rev2_epoch_test.go';s=p.read_text()
a='g.delivery.initialWindow != deliveryInitialWindow || g.delivery.window != deliveryInitialWindow'
assert s.count(a)==1
s=s.replace(a,'g.delivery.initialWindow != 128<<10 || g.delivery.window != 128<<10')
p.write_text(s)
