#!/usr/bin/env python3
"""LAB ONLY: reject loaded calibration; preserve RC2 windows and escapes."""
from pathlib import Path
r=Path(__file__).resolve().parents[3];d=r/'archives/1.0.0-rc10/source/core/tunnel'
p=d/'delivery_control.go';s=p.read_text()
a='if r.flightAtAdmission > 0 && r.flightAtAdmission <= deliveryInitialWindow/2 && residence > 0 {'
assert s.count(a)==1;s=s.replace(a,'if r.flightAtAdmission > 0 && r.flightAtAdmission <= min(d.initialWindow, uint64(deliveryInitialWindow))/2 && residence > 0 {')
a='\t\t\t\t\tpending.unloadedFast = false'
assert s.count(a)==1;s=s.replace(a,a+"\n\t\t\t\t\tif !w.fast && !w.repair && pending.flightAtAdmission > 0 {\n\t\t\t\t\t\tpending.flightAtAdmission = max(pending.flightAtAdmission, d.bulk+newBytes)\n\t\t\t\t\t}")
p.write_text(s)
(d/"rc10_calibration_test.go").write_text('package tunnel\nimport("context";"itom.local/core/datapath";"itom.local/core/flow";"itom.local/core/wire";"testing";"time")\nfunc TestRC10ColdFullWindowCannotInflateUnloadedACKFloor(t *testing.T) {\n\tfor _, split := range []bool{false, true} {\n\t\tg := newStripeGroup(wire.ID{1}, 1)\n\t\tg.observeProviderBaseline(300 * time.Millisecond)\n\t\tjobs := deliveryJobs(t, 1, 8, 32<<10)\n\t\tbatches := [][]*datapath.Job{jobs}\n\t\tif split {\n\t\t\tbatches = [][]*datapath.Job{jobs[:1], jobs[1:]}\n\t\t}\n\t\tfor _, batch := range batches {\n\t\t\tr, err := g.reserveDeliveryBatch(context.Background(), batch)\n\t\t\tif err != nil {\n\t\t\t\tt.Fatal(err)\n\t\t\t}\n\t\t\tr.finish(r.jobs, committed(r.jobs, flow.Committed))\n\t\t}\n\t\tg.deliveryProgress(1, 256<<10)\n\t\tif g.delivery.lowFlightCount != 0 || g.delivery.acknowledgementFloor() != 0 {\n\t\t\tt.Fatal("loaded cold flight calibrated the unloaded floor", split, g.delivery.lowFlightCount)\n\t\t}\n\t\tif g.delivery.bulk != 0 || g.delivery.bulkDelivered != 256<<10 {\n\t\t\tt.Fatal("calibration changed authenticated credit")\n\t\t}\n\t}\n}\n')
