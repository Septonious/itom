#!/usr/bin/env python3
"""LAB ONLY: align near-equal quiet accounts across the authenticated session.
Preserve the RC2 unloaded latency eligibility band and bulk-epoch pinning;
replace endpoint-local startup history as the tie-breaker, not polling cadence.
"""
from pathlib import Path
r=Path(__file__).resolve().parents[3];p=r/'archives/1.0.0-rc10/source/core/tunnel/reader_reserve.go';s=p.read_text()
s=s.replace('"sort"','"sort"\n "hash/fnv"')
a='''\tscores := map[*lane]int64{}
\tfor _, l := range candidates {
\t\tscores[l] = g.carrierLatencyLocked(l)
\t}'''
b='''\tscores := map[*lane]int64{}
\tranks := map[*lane]uint64{}
\tfor _, l := range candidates {
\t\tscores[l] = g.carrierLatencyLocked(l)
\t\th := fnv.New64a()
\t\t_, _ = h.Write(g.logical[:])
\t\t_, _ = h.Write([]byte(l.account.ID))
\t\tranks[l] = h.Sum64()
\t}'''
assert s.count(a)==1;s=s.replace(a,b)
a='''\t\tfor _, l := range candidates {
\t\t\tif l.account.ID == g.interactiveReserve[i] && scores[l] <= scores[chosen]+max(int64(100), scores[chosen]/4) {
\t\t\t\tchosen = l
\t\t\t\tbreak
\t\t\t}
\t\t}'''
b='''\t\tceiling := scores[chosen]+max(int64(100), scores[chosen]/4)
\t\tfor _, l := range candidates {
\t\t\tif scores[l] <= ceiling && (ranks[l] < ranks[chosen] || ranks[l] == ranks[chosen] && l.account.ID < chosen.account.ID) {
\t\t\t\tchosen = l
\t\t\t}
\t\t}'''
assert s.count(a)==1;s=s.replace(a,b);p.write_text(s)
