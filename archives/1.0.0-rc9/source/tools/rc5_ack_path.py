#!/usr/bin/env python3
"""Pair authenticated IMAP CONTROL seals and receipts by Noise counter.

Input: the client/server diagnostics ZIP exports (or directories containing
transport-trace.jsonl[.1|.2]). Never stores packet/payload/credentials, and
never guesses a missing physical turn from a missing trace record.

Clock warning: elapsed times across hosts require synchronized UTC clocks.
Negative or unreasonably high differences are reported, not silently fixed.
"""
from __future__ import annotations

import argparse
from collections import defaultdict
from datetime import datetime
import io
import json
from pathlib import Path
import zipfile

TRACES = ('transport-trace.jsonl.2', 'transport-trace.jsonl.1', 'transport-trace.jsonl')


def event_lines(path: Path):
    if zipfile.is_zipfile(path):
        with zipfile.ZipFile(path) as archive:
            for name in TRACES:
                try:
                    with archive.open(name) as raw:
                        yield from io.TextIOWrapper(raw, encoding='utf-8', errors='replace')
                except KeyError:
                    continue
    elif path.is_dir():
        for name in TRACES:
            p = path / name
            if p.is_file():
                with p.open(encoding='utf-8', errors='replace') as f:
                    yield from f
    else:
        with path.open(encoding='utf-8', errors='replace') as f:
            yield from f


def parse_timestamp(value):
    if not isinstance(value, str):
        return None
    try:
        return datetime.fromisoformat(value.replace('Z', '+00:00')).timestamp()
    except ValueError:
        return None


def load_frames(path: Path):
    frames = {}
    counters = defaultdict(int)
    for line in event_lines(path):
        try:
            obj = json.loads(line)
        except (ValueError, TypeError):
            counters['invalid_json'] += 1
            continue
        if obj.get('event') not in ('frame_sealed', 'frame_rx'):
            continue
        if obj.get('counter') is None or not obj.get('session') or not obj.get('account'):
            counters['no_counter'] += 1
            continue
        stamp = parse_timestamp(obj.get('time'))
        if stamp is None:
            counters['invalid_time'] += 1
            continue
        side = obj.get('side')
        if side not in ('server', 'client'):
            counters['no_side'] += 1
            continue
        key = (obj['account'], obj.get('user', ''), obj['session'], side,
               obj['counter'], obj['event'])
        counters[obj['event']] += 1
        if key in frames:
            counters['duplicate'] += 1
            continue
        frames[key] = (stamp, obj.get('name', 'UNKNOWN'))
    return frames, counters



def load_queued_waits(path: Path):
    # control_wait is emitted only for critical or >=100 ms waiting controls.
    # These are sampled subsets; report the sampling count explicitly.
    waits = defaultdict(list)
    for line in event_lines(path):
        try:
            ev = json.loads(line)
        except (ValueError, TypeError):
            continue
        if ev.get('event') != 'control_wait' or ev.get('counter') is None:
            continue
        stamp = parse_timestamp(ev.get('time'))
        if stamp is None or ev.get('side') not in ('client', 'server'):
            continue
        duration = ev.get('duration_us')
        if not isinstance(duration, (int, float)) or duration < 0:
            continue
        key = (ev.get('account'), ev.get('user', ''), ev['side'],
               ev['counter'], ev.get('name', 'UNKNOWN'))
        waits[key].append((stamp, duration / 1000))
    return waits


def percentile(vals, pct):
    if not vals:
        return None
    vals = sorted(vals)
    return round(vals[max(0, min(len(vals)-1, int((len(vals)-1)*pct)))], 3)


def correlate(client_path: Path, server_path: Path):
    client, cs = load_frames(client_path)
    server, ss = load_frames(server_path)
    client_waits, server_waits = load_queued_waits(client_path), load_queued_waits(server_path)
    queued = defaultdict(list)
    totals = defaultdict(list)
    joined = defaultdict(list)
    negative = 0
    for source, dest, sender in ((client, server, 'client'), (server, client, 'server')):
        for (account, user, sess, side, counter, event), (started, name) in source.items():
            if side != sender or event != 'frame_sealed':
                continue
            receiving_side = 'server' if sender == 'client' else 'client'
            got = dest.get((account, user, sess, receiving_side, counter, 'frame_rx'))
            if got is None:
                continue
            finish, recv_name = got
            if recv_name != name:
                continue
            ms = 1000 * (finish - started)
            if ms < 0:
                negative += 1
            joined[(sender, name)].append(ms)
            waits = client_waits if sender == 'client' else server_waits
            key = (account, user, sender, counter, name)
            # Seal and control_wait are adjacent trace events, so a tight
            # local timestamp match also handles encrypted counter reuse.
            local = [w for stamp, w in waits.get(key, ())
                     if abs(stamp - started) <= 0.1]
            if len(local) == 1:
                queued[(sender, name)].append(local[0])
                totals[(sender, name)].append(local[0] + ms)
    groups = []
    for (direction, name), values in sorted(joined.items()):
        groups.append({'sender': direction, 'frame': name, 'pairs': len(values),
                       'median_ms': percentile(values, 0.5), 'p95_ms': percentile(values, 0.95),
                       'max_ms': round(max(values), 3),
                       'greater_than_750ms': sum(v > 750 for v in values),
                       'sampled_preseal_count': len(queued[direction, name]),
                       'sampled_queue_p50_ms': percentile(queued[direction, name], 0.5),
                       'sampled_queue_p95_ms': percentile(queued[direction, name], 0.95),
                       'sampled_queue_plus_wire_p95_ms': percentile(totals[direction, name], 0.95)})
    return {'classification': 'FORENSIC_CORRELATION_NOT_PERFORMANCE_QUALIFICATION',
            'cross_host_clock_sync_required': True,
            'client': dict(cs), 'server': dict(ss),
            'paired_controls': sum(len(x) for x in joined.values()),
            'negative_cross_host_intervals': negative, 'groups': groups}


def main():
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument('client', type=Path, help='client diagnostic ZIP or trace directory')
    p.add_argument('server', type=Path, help='server diagnostic ZIP or trace directory')
    p.add_argument('--out', type=Path, help='write aggregate JSON; never raw events')
    a = p.parse_args()
    report = correlate(a.client, a.server)
    output = json.dumps(report, indent=2) + '\n'
    if a.out:
        a.out.write_text(output, encoding='utf-8')
    else:
        print(output, end='')
    return 0 if report['paired_controls'] else 1


if __name__ == '__main__':
    raise SystemExit(main())
