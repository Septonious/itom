"""Keep native model comparisons separate from field performance claims."""
import json
from pathlib import Path
import sys


def load(root):
    d = json.loads((root/'candidate.json').read_text())
    m = json.loads((root/'manifest.json').read_text())
    assert d['success'] and d['stage'] == 'complete', d.get('error')
    assert len(d['Phases']) == 4 and all(p['success'] for p in d['Phases'])
    assert m['frontend'] == 'native-tun' and m['timed_speedtest']
    assert m['native_unchanged'] and m['mailstream_unchanged'] and all(m['core_unchanged'].values())
    assert d['diagnostics']['QualificationAppTCP']['send_errors'] == 0, 'packet loss in the TUN test fixture'
    assert d['diagnostics']['Native']['TCPSendErrors'] == 0, 'packet send failure in the native TUN stack'
    assert d['diagnostics']['Native']['TUNDroppedPackets'] == 0
    assert d['diagnostics']['Client']['ActiveLines'] == d['diagnostics']['Client']['TotalLines'] == 30
    assert d['diagnostics']['Mail']['failed_accounts'] == 0
    assert not d['diagnostics']['Session']['closed']
    assert d['LoadedMS'] and max(d['LoadedMS']) <= 2000
    for p in d['Phases']:
        if p['name'] == 'download':
            assert p['deadline_aborted_downloads'] + p['completed_downloads'] == p['streams']
        assert p[p['name']]['verified_bytes'] > 0
    samples = sorted(d['LoadedMS'])
    return dict(first_16_mbps=[sum(p[p['name']]['half_second_mbps'][:32])/32 for p in d['Phases']],
                full_phase_mbps=[p[p['name']]['full_phase_mbps'] for p in d['Phases']],
                loaded_p95_ms=samples[int((len(samples)-1)*.95)], loaded_max_ms=max(samples),
                phase_loaded_p95_ms=[sorted(p['loaded_ms'])[int((len(p['loaded_ms'])-1)*.95)] for p in d['Phases']],
                tun_socket_buffers=d['diagnostics']['QualificationTUNBuffers'],
                application_fixture_tcp=d['diagnostics']['QualificationAppTCP'],
                native_tcp={k: d['diagnostics']['Native'].get(k) for k in ('TCPRetransmits', 'TCPTimeouts', 'TCPSendErrors', 'TUNDroppedPackets')})


reference, candidate = map(Path, sys.argv[1:])
before, after = load(reference), load(candidate)
assert all(v is not None for v in after['native_tcp'].values())
report = dict(reference=before, candidate=after, field_performance_qualified=False,
              latency_p95_ratio=after['loaded_p95_ms']/before['loaded_p95_ms'],
              first_16_speed_ratios=[b/a if a else None for a,b in zip(before['first_16_mbps'],after['first_16_mbps'])],
              publication_approved=False)
(candidate/'RADIO_COMPARISON.json').write_text(json.dumps(report, indent=2)+'\n')
print(json.dumps(report, indent=2))
