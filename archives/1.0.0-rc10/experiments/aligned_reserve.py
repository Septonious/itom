#!/usr/bin/env python3
"""LAB ONLY: align near-equal quiet accounts across the authenticated session.
Preserve the RC2 unloaded latency eligibility band and bulk-epoch pinning;
replace endpoint-local startup history as the tie-breaker, not polling cadence.
"""
from pathlib import Path
r=Path(__file__).resolve().parents[3];p=r/'archives/1.0.0-rc10/source/core/tunnel/reader_reserve.go';s=p.read_text()

a='''\tscores := map[*lane]int64{}
\tfor _, l := range candidates {
\t\tscores[l] = g.carrierLatencyLocked(l)
\t}'''
b='''\tscores := map[*lane]int64{}
\tranks := map[*lane]int{}
\tsort.Slice(candidates, func(i,j int) bool { return candidates[i].account.ID < candidates[j].account.ID })
\tstart := int(g.logical[len(g.logical)-1]) % len(candidates)
\tfor i, l := range candidates {
\t\tscores[l] = g.carrierLatencyLocked(l)
\t\tranks[l] = (i-start+len(candidates)) % len(candidates)
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
