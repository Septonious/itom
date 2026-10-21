#!/usr/bin/env python3
"""Preserve complete controller measurements, partial failures and log hashes."""
import argparse,hashlib,json,re
from pathlib import Path

def fields(line):
    return dict(re.findall(r'(\w+)=([^ ]+)',line))

def summarize(root):
    report={}
    for path in sorted(root.rglob('*.log')):
        text=path.read_text(errors='replace')
        row={'sha256':hashlib.sha256(path.read_bytes()).hexdigest(),
             'passed':bool(re.search(r'^--- PASS: (TestV110IdleLightReference|TestTransportReworkQualification|TestModelBulkToQuiet) ',text,re.M)),
             'latency':[],'transitions':[],'bulk':[],'rounds':[],
             'tails30':[],'tails10':[],'acceptance':[],'identity':[],
             'failures':[]}
        for line in text.splitlines():
            for marker,key in [('V110_RESULT ','latency'),('MODEL_TRANSITION ','transitions')]:
                if marker in line:row[key].append(json.loads(line.split(marker,1)[1]))
            for marker,key in [('model_transition_bulk ','bulk'),('supervised_result round=','rounds'),('supervised_tail round=','tails30'),('supervised_tail10 round=','tails10'),('supervised_acceptance round=','acceptance'),('supervised_identity round=','identity')]:
                if marker in line:row[key].append(fields(line))
            if '--- FAIL:' in line or 'duplex performance/cadence gate' in line or 'failure=probe' in line or line.startswith('panic:'):
                row['failures'].append(line)
        report[str(path.relative_to(root))]=row
    return {'schema':1,'classification':'laboratory experiment; not field qualified','logs':report}

def main():
    p=argparse.ArgumentParser();p.add_argument('logs',type=Path);p.add_argument('output',type=Path);a=p.parse_args()
    report=summarize(a.logs)
    a.output.write_text(json.dumps(report,indent=2)+'\n')
    print('logs',len(report['logs']),'passed',sum(r['passed'] for r in report['logs'].values()),'failed',sum(not r['passed'] for r in report['logs'].values()))
if __name__=='__main__':main()
