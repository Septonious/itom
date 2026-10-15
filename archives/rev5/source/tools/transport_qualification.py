#!/usr/bin/env python3
"""Fresh, fail-closed transport laboratory evidence; never a field speed claim."""
import argparse
import hashlib
import json
import os
from pathlib import Path
import platform
import subprocess
import sys
import time
from rework_gate import summarize_events
ROOT=Path(__file__).resolve().parents[1]
REQUIRED='TestTransportReworkQualification'

def source_fingerprint():
    h=hashlib.sha256()
    for folder in ('core','native','android','tools'):
        for p in sorted((ROOT/folder).rglob('*')):
            if p.is_file() and p.suffix in ('.go','.mod','.sum','.py','.kt','.kts','.xml','.json') and not any(x in p.parts for x in ('build','.gradle','__pycache__','dist','reports')):
                h.update(str(p.relative_to(ROOT)).encode()+b'\0'+p.read_bytes()+b'\0')
    h.update((ROOT/'source-info.json').read_bytes())
    return h.hexdigest()

def qualification_ok(exit_code,summary,before,after):
    return exit_code==0 and bool(summary['passed']) and not summary['failed'] and not summary['missing_required'] and not set(summary['skipped']).intersection({REQUIRED}) and before==after

def main():
    p=argparse.ArgumentParser(description=__doc__)
    p.add_argument('--go',default='go');p.add_argument('--results',type=Path,default=ROOT/'reports'/'qualification')
    p.add_argument('--streams',choices=('single','multi','both'),default='both');p.add_argument('--accounts',type=int,default=10)
    p.add_argument('--seconds',type=int,default=60);p.add_argument('--repeats',type=int,default=3);p.add_argument('--warmup',type=int,default=10)
    p.add_argument('--command-delay-ms',type=int,default=80);p.add_argument('--provider-mbps',type=float,default=600)
    p.add_argument('--layout',choices=('auto','mixed','split'),default='auto');p.add_argument('--shards',type=int,choices=(1,2,4),default=1)
    p.add_argument('--visibility-delay-ms',type=int,default=0);p.add_argument('--provider-sessions',type=int,default=10)
    p.add_argument('--uid-history',type=int,default=64,help='actual APPEND/EXPUNGE allocations per DATA mailbox before startup')
    a=p.parse_args()
    if not 2<=a.accounts<=128 or not 60<=a.seconds<=7200 or a.seconds%2!=0 or not 1<=a.repeats<=10 or not 1<=a.warmup<=30 or not 0<=a.command_delay_ms<=5000 or not 0<a.provider_mbps<=10000 or not 0<=a.visibility_delay_ms<=5000 or not 2<=a.provider_sessions<=128 or not 0<=a.uid_history<=8192:p.error('invalid scenario bounds')
    if os.environ.get('GOFLAGS','').find('-race')>=0:p.error('performance measurements must use a non-race binary')
    env=os.environ.copy();env.update(GOTOOLCHAIN='local',GOWORK='off',GOMAXPROCS=env.get('GOMAXPROCS','2'),GOFLAGS='-mod=readonly')
    version=subprocess.run([a.go,'version'],env=env,capture_output=True,text=True)
    if version.returncode or 'go1.27.1 ' not in version.stdout:p.error('Go 1.27.1 required')
    info=json.loads((ROOT/'source-info.json').read_text())
    out=a.results.resolve()/(time.strftime('%Y%m%dT%H%M%S',time.gmtime())+'-'+str(os.getpid()));out.mkdir(parents=True,exist_ok=False)
    report=dict(schema=1,release=info,source_sha256=source_fingerprint(),go=version.stdout.strip(),host=platform.platform(),cpu_count=os.cpu_count(),gomaxprocs=env['GOMAXPROCS'],classification='LABORATORY',field_qualified=False,android_device='NOT_RUN',real_provider='NOT_RUN',criteria=dict(nominal_upload_mbps=100,nominal_download_mbps=100,nominal_loaded_rtt_ns_strictly_below=1000000000,tolerance_percent=10,useful_upload_mbps_min=90,useful_download_mbps_min=90,all_loaded_rtt_ns_strictly_below=1100000000,probe_timeouts=0,missed_probes=0,tail_seconds=min(60,a.seconds//2)),scenario=vars(a)|{'results':str(a.results)},cases=[])
    modes=('single','multi') if a.streams=='both' else (a.streams,)
    for mode in modes:
        for repeat in range(1,a.repeats+1):
            name=f'{mode}-{repeat}';before=source_fingerprint();log=out/(name+'.jsonl')
            case_env=env.copy();case_env.update(ITOM_REWORK_QUALIFICATION='1',ITOM_LOAD_ACCOUNTS=str(a.accounts),ITOM_LOAD_STREAMS='1' if mode=='single' else '24',ITOM_LOAD_SECONDS=str(a.seconds),ITOM_LOAD_PROBE_CADENCE_MS='2000',ITOM_LOAD_PROBE_DEADLINE_MS='1100',ITOM_LOAD_WARMUP_SECONDS=str(a.warmup),ITOM_LOAD_COMMAND_DELAY_MS=str(a.command_delay_ms),ITOM_LOAD_PROVIDER_MBPS=str(a.provider_mbps),ITOM_LOAD_PROVIDER_CONNECTION_LIMIT=str(a.provider_sessions),ITOM_LOAD_ACCOUNT_SESSIONS=str(a.provider_sessions),ITOM_LOAD_ENDPOINT_CONNECTIONS=str(max(128,a.accounts*5)),ITOM_LOAD_VISIBILITY_DELAY_MS=str(a.visibility_delay_ms),ITOM_LOAD_LAYOUT=a.layout,ITOM_LOAD_DATA_SHARDS=str(a.shards),ITOM_LOAD_REPEATS='1',ITOM_LOAD_DIAGNOSTICS='0',ITOM_LOAD_TRACE='0',ITOM_LOAD_SNAPSHOTS='0')
            case_env['ITOM_LOAD_UID_HISTORY']=str(a.uid_history)
            command=[a.go,'test','-mod=readonly','-json','-count=1','-timeout='+str(a.seconds+600)+'s','./tunnel','-run','^'+REQUIRED+'$']
            start=time.monotonic();print('RUN',name,flush=True)
            with log.open('w') as stream:result=subprocess.run(command,cwd=ROOT/'core',env=case_env,stdout=stream,stderr=subprocess.STDOUT)
            after=source_fingerprint();summary=summarize_events(log,(REQUIRED,));ok=qualification_ok(result.returncode,summary,before,after)
            case=dict(name=name,streams_per_direction=1 if mode=='single' else 24,fresh_endpoint_state=True,physical_uid_history=a.uid_history,seconds=a.seconds,exit_code=result.returncode,elapsed_seconds=round(time.monotonic()-start,3),source_before=before,source_after=after,log=log.name,log_sha256=hashlib.sha256(log.read_bytes()).hexdigest(),summary=summary,ok=ok)
            report['cases'].append(case);report['all_lab_cases_passed']=all(c['ok']for c in report['cases']);(out/'results.json').write_text(json.dumps(report,indent=2)+'\n');print('RESULT',name,'PASS'if ok else'FAIL',flush=True)
    print('EVIDENCE',out/'results.json',flush=True)
    return 0 if report['all_lab_cases_passed'] else 1
if __name__=='__main__':raise SystemExit(main())
