#!/usr/bin/env python3
"""Export aggregate Rev3 failure evidence without identities, keys or payloads."""
import argparse
from collections import Counter
from datetime import datetime
import json
from pathlib import Path
import re


def quantiles(values):
    values = sorted(values)
    if not values:
        return {'count': 0}
    return {'count': len(values), **{name: values[int((len(values)-1)*q)]
            for name, q in [('p50', .5), ('p90', .9), ('p99', .99), ('max', 1)]}}


def analyze(root):
    accounts, events, codes, lifecycle = set(), Counter(), Counter(), Counter()
    health, durations, timeline = {}, {}, {}
    first, last, count = None, None, 0
    for path in sorted(root.glob('transport-trace.jsonl*')):
        for line in path.open():
            row = json.loads(line)
            at = datetime.fromisoformat(row['time'].replace('Z', '+00:00'))
            first = min(first or at, at)
            last = max(last or at, at)
            count += 1
            if row.get('account'):
                accounts.add(row['account'])
            event = row['event']
            events[event] += 1
            code = row.get('code', '')
            if code and code != 'OK' and re.fullmatch('[A-Z0-9_]{1,96}', code):
                codes[event+'/'+code] += 1
            if event == 'lifecycle':
                name = row.get('name', '')
                tag = re.search(r'\bevent=(\w+)', name)
                if tag and tag[1] in ('reconnect', 'data_reader_repair', 'grant_recovery_deferred', 'logical_owner_exit'):
                    reason = re.search(r'\bcode=([A-Z0-9_]{1,96})(?:\s|$)', name)
                    key = tag[1]+'/'+(reason[1] if reason else 'unspecified')
                    lifecycle[key] += 1
                    second = at.strftime('%Y-%m-%dT%H:%M:%SZ')
                    timeline.setdefault(second, Counter())[key] += 1
            if event == 'health':
                state = row['details']['Line']
                for prefix, source in [('line', state), ('governor', state.get('LatencyGovernor', {})), ('scheduler', state.get('Scheduler') or {})]:
                    for key, value in source.items():
                        if type(value) in (int, float):
                            health.setdefault(prefix+'/'+key, []).append(value)
            if 'duration_us' in row and event == 'imap_end':
                name = row.get('name', '')
                if re.fullmatch('[a-z_]{1,64}', name):
                    durations.setdefault(name+'/'+code, []).append(row['duration_us']/1000)
    return {'events': count, 'account_count': len(accounts), 'start_utc': first.isoformat(), 'end_utc': last.isoformat(),
            'event_counts': dict(events), 'error_counts': dict(codes), 'lifecycle_counts': dict(lifecycle),
            'failure_timeline': {k: dict(v) for k,v in sorted(timeline.items())},
            'health_numeric': {k:quantiles(v) for k,v in health.items()},
            'imap_duration_ms': {k:quantiles(v) for k,v in durations.items()}}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--client', type=Path, required=True)
    parser.add_argument('--server', type=Path, required=True)
    parser.add_argument('--output', type=Path, required=True)
    args = parser.parse_args()
    report = {'schema': 1, 'classification': 'REV3_FIELD_FAILURE_EVIDENCE',
              'sides': {'client': analyze(args.client), 'server': analyze(args.server)},
              'limitations': ['Rotating tails do not cover the entire test session.',
                              'Screenshots do not identify mode or UTC timing; exact test-to-trace association is unknown.',
                              'No synchronized cross-host clock calibration or real-provider wire-delay decomposition is available.',
                              'Retained journal debt is visible, but raw grant history is unavailable; tests exercise conservative retained-state scenarios.',
                              'Socket EOF/reset alone cannot establish a carrier crash; reconnect and lifecycle evidence are reported separately.']}
    args.output.parent.mkdir(parents=True, exist_ok=True)
    args.output.write_text(json.dumps(report, indent=2)+'\n')
    print('Numeric evidence exported; no identities, private keys or plaintext included')


if __name__ == '__main__':
    main()
