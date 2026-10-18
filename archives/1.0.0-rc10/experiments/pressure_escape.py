#!/usr/bin/env python3
"""LAB ONLY: small unmeasured seed, pressure-conditional established escapes.
Keeps RC2 16MiB symmetric maxima, half-window growth and original BODY policy.
"""
from pathlib import Path
import sys
r=Path(__file__).resolve().parents[3];p=r/'archives/1.0.0-rc10/source/core/tunnel/delivery_control.go';s=p.read_text()
def replace(a,b):
 global s
 assert s.count(a)==1,(a,s.count(a))
 s=s.replace(a,b)
replace('type deliveryRange struct {','type deliveryRange struct {\n\tfirstDiscovery bool\n\tdiscoveryWake time.Time')
replace('nextRepairWake                      time.Time','nextRepairWake                      time.Time\n\tnextDiscoveryWake time.Time')
replace('func (d *deliveryController) maintain(now time.Time, demand bool) {\n\td.init()', '''func (d *deliveryController) maintain(now time.Time, demand bool) {
\td.init()
\tif !d.nextDiscoveryWake.IsZero() && !now.Before(d.nextDiscoveryWake) {
\t\td.nextDiscoveryWake = time.Time{}
\t\td.changed()
\t}''')
replace('r.flightAtAdmission <= deliveryInitialWindow/2','r.flightAtAdmission <= min(d.initialWindow, uint64(deliveryInitialWindow))/2 && !now.Before(d.fastPressureUntil) && d.policy.mode != deliveryCongested && d.policy.mode != deliveryRecovery && d.policy.mode != deliveryOutage')
replace('pending.unloadedFast = false', '''pending.unloadedFast = false
\t\t\t\t\tif !w.fast && !w.repair && pending.flightAtAdmission > 0 {
\t\t\t\t\t\tpending.flightAtAdmission = max(pending.flightAtAdmission, d.bulk+newBytes)
\t\t\t\t\t}''')
replace('d.ranges[r.key] = r\n\t\t\t\td.issued', '''d.ranges[r.key] = r
\t\t\t\tif r.firstDiscovery { r.discoveryWake = time.Now().Add(deliveryDecisionEpoch); d.nextDiscoveryWake = r.discoveryWake }
\t\t\t\td.issued''')
replace('var size uint64\n\torderBlocked', 'var size uint64\n\tfirstDiscoverySelected := false\n\tnow := time.Now()\n\torderBlocked')
a='\t\tdiscovery := !w.fast && !w.repair && publishedBulk && perStream[a.Stream] == 0 && n <= wire.MaxData && d.bulk+size+n <= deliveryMaxWindow'
replace(a,a+'''
\t\tfirstDiscovery := discovery && n > remaining && (d.frontiers[a.Stream] == 0 || now.Before(d.fastPressureUntil))
\t\tif firstDiscovery && (firstDiscoverySelected || now.Before(d.nextDiscoveryWake)) { discovery = false }''')
replace('fast: w.fast, admitted: time.Now()})','fast: w.fast, admitted: now, firstDiscovery: firstDiscovery})\n\t\tfirstDiscoverySelected = firstDiscoverySelected || firstDiscovery')
replace('''seed := uint64(deliveryInitialWindow)
\t\t\tif sample >= 250 {
\t\t\t\tseed = 256 << 10
\t\t\t}''','''// NOOP propagation cannot prove access-link capacity.
\t\t\tseed := uint64(deliveryMinWindow)''')
replace('\t\t\tdelete(d.ranges, key)', '\t\t\tif r.firstDiscovery && r.discoveryWake == d.nextDiscoveryWake { d.nextDiscoveryWake = time.Time{} }\n\t\t\tdelete(d.ranges, key)')
if '350' in sys.argv[1:]:
 replace('deliveryFastMaxAge = 650 * time.Millisecond','deliveryFastMaxAge = 350 * time.Millisecond')
p.write_text(s)

(p.parent/"rc10_pressure_test.go").write_bytes((Path(__file__).parent/"pressure_test.go.fixture").read_bytes())
