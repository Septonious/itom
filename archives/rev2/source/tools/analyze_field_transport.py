"""Read trace directories and emit numeric, pseudonymized field evidence."""
import argparse
from collections import Counter, defaultdict
from datetime import datetime
import hashlib
import json
from pathlib import Path


def read_events(directory):
    events = []
    for name in ('transport-trace.jsonl.2', 'transport-trace.jsonl.1', 'transport-trace.jsonl'):
        path = directory / name
        if path.exists():
            with path.open() as stream:
                for line in stream:
                    events.append(json.loads(line))
    return events


def timestamp(value):
    return datetime.fromisoformat(value.replace('Z', '+00:00')).timestamp()


def quantiles(values):
    values = sorted(values)
    if not values:
        return {'count': 0}
    return dict(count=len(values), minimum=values[0], p50=values[len(values)//2],
                p95=values[min(len(values)-1, int(len(values)*.95))], maximum=values[-1])


def analyze(client, server):
    events = {'client': read_events(client), 'server': read_events(server)}
    accounts = sorted({e['account'] for rows in events.values() for e in rows if e.get('account')})
    labels = {name: f'A{i+1:02d}' for i, name in enumerate(accounts)}
    report = dict(schema=1, classification='FIELD_DIAGNOSTIC_OBSERVATIONS',
                  field_performance_qualified=False, account_count=len(accounts), sides={})
    for side, rows in events.items():
        operations = defaultdict(list)
        account_ops = defaultdict(lambda: defaultdict(list))
        codes = defaultdict(Counter)
        batches = defaultdict(list)
        per_account = defaultdict(lambda: Counter())
        controls = Counter()
        health = []
        for e in rows:
            name, account, details = e['event'], e.get('account'), e.get('details') or {}
            if name == 'imap_end':
                operations[e['name']].append(e.get('duration_us', 0)/1000)
                codes[e['name']][e.get('code', '')] += 1
                if account:
                    account_ops[labels[account]][e['name']].append(e.get('duration_us', 0)/1000)
            elif name == 'data_batch_completion':
                kind = 'fast' if details.get('fast') else 'bulk'
                batches[kind].append((details.get('payload_bytes', 0), e.get('duration_us', 0)/1000))
                per_account[labels[account]][kind+'_encrypted_body_bytes'] += details.get('payload_bytes', 0)
                per_account[labels[account]][kind+'_batches'] += 1
            elif name == 'control_batch_append':
                controls.update(details.get('kinds', {}))
            elif name == 'health':
                line = details['Line']; governor = line['LatencyGovernor']
                health.append(dict(time=e['time'], account=labels[account],
                    window=governor['DownloadWindowBytes'], flight=governor['DownloadBacklogBytes'],
                    mode=governor['Mode'], bulk_rate_mbps=governor['BulkRateMbps'],
                    bulk_age_ms=governor['ObservedRTTMS'], fast_ack_age_ms=governor['LastFastACKAgeMS'],
                    fast_pressure=governor['FastPressureDecisions'], rtt_ms=line['RTTMillis'],
                    data_commits=line['DataCommits'], control_commits=line['ControlCommits']))
        report['sides'][side] = dict(start=rows[0]['time'], end=rows[-1]['time'], events=len(rows),
            commands={k: quantiles(v)|{'codes': dict(codes[k])} for k, v in operations.items()},
            per_account_commands={a:{k:quantiles(v)for k,v in ops.items()}for a,ops in account_ops.items()},
            batches={k:dict(encrypted_body_bytes=sum(x[0]for x in v),
                           body_size=quantiles([x[0]for x in v]), completion_ms=quantiles([x[1]for x in v]))
                     for k,v in batches.items()}, per_account=dict(per_account), control_kinds=dict(controls), health=health)
    sends = {(e.get('account'),e.get('session'),e.get('counter')):e for e in events['server'] if e['event']=='data_writer_member'}
    latencies = []
    for e in events['client']:
        if e['event']=='frame_rx' and e.get('name')=='DATA':
            sent=sends.get((e.get('account'),e.get('session'),e.get('counter')))
            if sent:
                latencies.append((timestamp(e['time'])-timestamp(sent['time']))*1000)
    report['sampled_s2c_seal_to_receive_ms']=quantiles(latencies)
    report['limitations']=[
        'Rotating traces retain only the final part of a longer session; screenshots lack per-test mode/timestamps.',
        'No matched 5/10/15-account field experiment is present; causal account-count benefit cannot be established.',
        'DATA frames are sampled; batch body bytes contain encryption/framing overhead and are not useful TCP Mbps.',
        'Rev1 independent DATA FETCH and C2S DATA frame ingress were not fully traced.',
        'Cross-host timestamp offsets were not independently calibrated; sampled one-way times are diagnostic only.',
        'The traces show a network-capabilities event during the final upload interval; its performance effect is unproven.'
    ]
    return report


def main():
    p=argparse.ArgumentParser(description=__doc__)
    p.add_argument('--client',type=Path,required=True);p.add_argument('--server',type=Path,required=True)
    p.add_argument('--output',type=Path,required=True);a=p.parse_args()
    report=analyze(a.client,a.server)
    a.output.parent.mkdir(parents=True,exist_ok=True)
    a.output.write_text(json.dumps(report,indent=2)+'\n')
    print('Pseudonymized field report:',a.output,'accounts:',report['account_count'])


if __name__=='__main__':main()
