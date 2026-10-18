#!/usr/bin/env python3
"""LAB ONLY correction: every above-window escape shares the feedback wake.
A genuine useful ACK releases the scheduling wake immediately; invisible BODYs
cannot strand a different stream because the original actor expires it after the original2s BODY visibility budget.
No physical quota, logical charge, original socket or maximum window changes.
Apply after cold64.py in a clean experimental source tree.
"""
from pathlib import Path
r=Path(__file__).resolve().parents[3];p=r/'archives/1.0.0-rc10/source/core/tunnel/delivery_control.go';s=p.read_text()
a='firstDiscovery := discovery && n > remaining && d.frontiers[a.Stream] == 0'
assert s.count(a)==1;s=s.replace(a,'firstDiscovery := discovery && n > remaining')
a='\t\t\tdelete(d.ranges, key)';assert s.count(a)==1
s=s.replace(a,'''\t\t\t// Only this range's authenticated useful ACK releases the escape wake.
\t\t\tif r.firstDiscovery { d.nextDiscoveryWake = time.Time{} }
'''+a)
# A normal authenticated ACK releases this wake immediately. The timer is
# only a fallback for an invisible BODY, not a rate grant every actor epoch.
a='d.nextDiscoveryWake = time.Now().Add(deliveryDecisionEpoch)'
assert s.count(a)==1;s=s.replace(a,'d.nextDiscoveryWake = time.Now().Add(2*time.Second)')
p.write_text(s)
