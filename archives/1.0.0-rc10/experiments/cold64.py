#!/usr/bin/env python3
"""LAB ONLY: small capacity-unmeasured seed plus paced first discovery.
Keeps RC2 16MiB symmetric maxima, half-window growth and original BODY policy.
"""
from pathlib import Path
r=Path(__file__).resolve().parents[3];p=r/'archives/1.0.0-rc10/source/core/tunnel/delivery_control.go';s=p.read_text()
def replace(a,b):
 global s
 assert s.count(a)==1,(a,s.count(a))
 s=s.replace(a,b)
replace('type deliveryRange struct {','type deliveryRange struct {\n\tfirstDiscovery bool')
replace('nextRepairWake                      time.Time','nextRepairWake                      time.Time\n\tnextDiscoveryWake time.Time')
replace('func (d *deliveryController) maintain(now time.Time, demand bool) {\n\td.init()', '''func (d *deliveryController) maintain(now time.Time, demand bool) {
\td.init()
\tif !d.nextDiscoveryWake.IsZero() && !now.Before(d.nextDiscoveryWake) {
\t\td.nextDiscoveryWake = time.Time{}
\t\td.changed()
\t}''')
replace('r.flightAtAdmission <= deliveryInitialWindow/2','r.flightAtAdmission <= min(d.initialWindow, uint64(deliveryInitialWindow))/2')
replace('pending.unloadedFast = false', '''pending.unloadedFast = false
\t\t\t\t\tif !w.fast && !w.repair && pending.flightAtAdmission > 0 {
\t\t\t\t\t\tpending.flightAtAdmission = max(pending.flightAtAdmission, d.bulk+newBytes)
\t\t\t\t\t}''')
replace('d.ranges[r.key] = r\n\t\t\t\td.issued', '''d.ranges[r.key] = r
\t\t\t\tif r.firstDiscovery { d.nextDiscoveryWake = time.Now().Add(deliveryDecisionEpoch) }
\t\t\t\td.issued''')
replace('var size uint64\n\torderBlocked', 'var size uint64\n\tfirstDiscoverySelected := false\n\tnow := time.Now()\n\torderBlocked')
a='\t\tdiscovery := !w.fast && !w.repair && publishedBulk && perStream[a.Stream] == 0 && n <= wire.MaxData && d.bulk+size+n <= deliveryMaxWindow'
replace(a,a+'''
\t\tfirstDiscovery := discovery && n > remaining && d.frontiers[a.Stream] == 0
\t\tif firstDiscovery && (firstDiscoverySelected || now.Before(d.nextDiscoveryWake)) { discovery = false }''')
replace('fast: w.fast, admitted: time.Now()})','fast: w.fast, admitted: now, firstDiscovery: firstDiscovery})\n\t\tfirstDiscoverySelected = firstDiscoverySelected || firstDiscovery')
replace('''seed := uint64(deliveryInitialWindow)
\t\t\tif sample >= 250 {
\t\t\t\tseed = 256 << 10
\t\t\t}''','''// NOOP propagation cannot prove access-link capacity.
\t\t\tseed := uint64(deliveryMinWindow)''')
p.write_text(s)
