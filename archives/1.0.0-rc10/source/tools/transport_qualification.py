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
    p.add_argument('--profile',choices=('healthy-server','legacy-stress'),default='healthy-server')
    p.add_argument('--client-rtt-ms',type=int,default=40);p.add_argument('--server-rtt-ms',type=int,default=20)
    p.add_argument('--server-mbps',type=float,default=1000);p.add_argument('--provider-service-ms',type=int,default=2)
    p.add_argument('--command-delay-ms',type=int,default=None);p.add_argument('--provider-mbps',type=float,default=600)
    p.add_argument('--layout',choices=('auto','mixed','split'),default='auto');p.add_argument('--shards',type=int,choices=(1,2,4),default=1)
    p.add_argument('--visibility-delay-ms',type=int,default=0);p.add_argument('--provider-sessions',type=int,default=10)
    p.add_argument('--uid-history',type=int,default=64,help='actual APPEND/EXPUNGE allocations per DATA mailbox before startup')
    p.add_argument('--retained-state',action='store_true',help='retain 6 MiB / 130 old remote message slots per account')
    p.add_argument('--stability-only',action='store_true',help='waive the configured Mbps floor; require useful duplex progress on every one-second tick, all RTT probes and post-load connection survival')
    p.add_argument('--minimum-mbps',type=float,default=100,help='strict useful Mbps floor per direction across the full measurement and sustained tails; no speed tolerance')
    a=p.parse_args()
    if a.command_delay_ms is None:a.command_delay_ms=0 if a.profile=='healthy-server' else 80
    if not 0<=a.client_rtt_ms<=1000 or not 0<=a.server_rtt_ms<=1000 or not 0<a.server_mbps<=10000 or not 0<=a.provider_service_ms<=100:p.error('invalid endpoint profile')
    if a.profile=='healthy-server' and (a.command_delay_ms!=0 or a.visibility_delay_ms!=0):p.error('healthy-server forbids legacy command stalls/visibility injections; use --profile legacy-stress for those')
    if not a.stability_only and a.minimum_mbps<75:p.error('performance qualification refuses a floor below 75 Mbps')
    if not 0<a.minimum_mbps<=10000:p.error('minimum-mbps must be positive and finite')
    if not 2<=a.accounts<=128 or not 60<=a.seconds<=7200 or a.seconds%2!=0 or not 1<=a.repeats<=10 or not 1<=a.warmup<=30 or not 0<=a.command_delay_ms<=5000 or not 0<a.provider_mbps<=10000 or not 0<=a.visibility_delay_ms<=5000 or not 2<=a.provider_sessions<=128 or not 0<=a.uid_history<=8192:p.error('invalid scenario bounds')
    if os.environ.get('GOFLAGS','').find('-race')>=0:p.error('performance measurements must use a non-race binary')
    env=os.environ.copy();env.update(GOTOOLCHAIN='local',GOWORK='off',GOFLAGS='-mod=readonly')
    version=subprocess.run([a.go,'version'],env=env,capture_output=True,text=True)
    if version.returncode or 'go1.27.1 ' not in version.stdout:p.error('Go 1.27.1 required')
    info=json.loads((ROOT/'source-info.json').read_text())
    out=a.results.resolve()/(time.strftime('%Y%m%dT%H%M%S',time.gmtime())+'-'+str(os.getpid()));out.mkdir(parents=True,exist_ok=False)
    report=dict(schema=2,release=info,source_sha256=source_fingerprint(),go=version.stdout.strip(),host=platform.platform(),cpu_count=os.cpu_count(),gomaxprocs=env.get('GOMAXPROCS','Go runtime automatic quota-aware selection'),classification='HEALTHY_ENDPOINT_REFERENCE' if a.profile=='healthy-server' else 'LEGACY_STRESS',field_qualified=False,android_device='NOT_RUN',real_provider='NOT_RUN',deployment_server_cpu_count=8,cpu_scope='Client, server and provider share the test process; this does not emulate or certify dedicated eight-core server hardware.',criteria=dict(qualification_mode='stability' if a.stability_only else 'performance',throughput_floor_enforced=not a.stability_only,positive_duplex_every_second_required=True,useful_upload_mbps_min=a.minimum_mbps,useful_download_mbps_min=a.minimum_mbps,tolerance_percent=0,speed_tolerance_percent=0,rtt_tolerance_percent=0,max_loaded_rtt_ns=750000000,probe_timeouts=0,missed_probes=0,tail_seconds=[30,10]),scenario=vars(a)|{'results':str(a.results)},cases=[])
    if a.profile=='healthy-server':report['endpoint_model']=dict(server_rtt_ms=a.server_rtt_ms,server_mbps=a.server_mbps,client_rtt_ms=a.client_rtt_ms,client_mbps=a.provider_mbps,provider_processing_ms=a.provider_service_ms,capacity_scope='independent endpoint paths; speed profiles affect client only',server_fault_injection=False,unmeasured_assumptions=['provider processing time','server uplink capacity'])
    if a.stability_only:
        report['criteria'].update(useful_upload_mbps_min=None,useful_download_mbps_min=None,throughput_waiver='Explicit stability-only invocation waives the configured throughput floor; it cannot certify performance.')
    modes=('single','multi') if a.streams=='both' else (a.streams,)
    for mode in modes:
        for repeat in range(1,a.repeats+1):
            name=f'{mode}-{repeat}';before=source_fingerprint();log=out/(name+'.jsonl')
            case_env=env.copy();case_env.update(ITOM_REWORK_QUALIFICATION='1',ITOM_LOAD_ACCOUNTS=str(a.accounts),ITOM_LOAD_STREAMS='1' if mode=='single' else '32',ITOM_LOAD_SECONDS=str(a.seconds),ITOM_LOAD_PROBE_CADENCE_MS='2000',ITOM_LOAD_PROBE_DEADLINE_MS='1100',ITOM_LOAD_WARMUP_SECONDS=str(a.warmup),ITOM_LOAD_COMMAND_DELAY_MS=str(a.command_delay_ms),ITOM_LOAD_PROVIDER_MBPS=str(a.provider_mbps),ITOM_LOAD_PROVIDER_CONNECTION_LIMIT=str(a.provider_sessions),ITOM_LOAD_ACCOUNT_SESSIONS=str(a.provider_sessions),ITOM_LOAD_ENDPOINT_CONNECTIONS=str(max(128,a.accounts*5)),ITOM_LOAD_VISIBILITY_DELAY_MS=str(a.visibility_delay_ms),ITOM_LOAD_LAYOUT=a.layout,ITOM_LOAD_DATA_SHARDS=str(a.shards),ITOM_LOAD_REPEATS='1',ITOM_LOAD_DIAGNOSTICS='0',ITOM_LOAD_TRACE='0',ITOM_LOAD_SNAPSHOTS='0')
            case_env['ITOM_RC8_RELEASE_GATE']='0' if a.stability_only else '1'
            case_env.update(ITOM_HEALTHY_SERVER='1' if a.profile=='healthy-server' else '0',ITOM_LOAD_CLIENT_RTT_MS=str(a.client_rtt_ms),ITOM_LOAD_SERVER_RTT_MS=str(a.server_rtt_ms),ITOM_LOAD_SERVER_MBPS=str(a.server_mbps),ITOM_LOAD_CLIENT_MBPS=str(a.provider_mbps),ITOM_LOAD_PROVIDER_SERVICE_MS=str(a.provider_service_ms))
            if a.profile=='healthy-server':case_env['ITOM_LOAD_MOBILE']='0'
            case_env['ITOM_LOAD_UID_HISTORY']=str(a.uid_history)
            case_env['ITOM_STABILITY_QUALIFICATION']='1' if a.stability_only else '0'
            case_env['ITOM_LOAD_MIN_MBPS']=str(a.minimum_mbps)
            case_env['ITOM_LOAD_RETAINED_STATE']='1' if a.retained_state else '0'
            command=[a.go,'test','-mod=readonly','-json','-count=1','-timeout='+str(a.seconds+600)+'s','./tunnel','-run','^'+REQUIRED+'$']
            start=time.monotonic();print('RUN',name,flush=True)
            with log.open('w') as stream:result=subprocess.run(command,cwd=ROOT/'core',env=case_env,stdout=stream,stderr=subprocess.STDOUT)
            after=source_fingerprint();summary=summarize_events(log,(REQUIRED,));ok=qualification_ok(result.returncode,summary,before,after)
            case=dict(name=name,streams_per_direction=1 if mode=='single' else 32,fresh_endpoint_state=not a.retained_state,retained_bytes_per_account=6291456 if a.retained_state else 0,retained_messages_per_account=130 if a.retained_state else 0,physical_uid_history=a.uid_history,seconds=a.seconds,exit_code=result.returncode,elapsed_seconds=round(time.monotonic()-start,3),source_before=before,source_after=after,log=log.name,log_sha256=hashlib.sha256(log.read_bytes()).hexdigest(),summary=summary,ok=ok)
            report['cases'].append(case);report['all_lab_cases_passed']=all(c['ok']for c in report['cases']);(out/'results.json').write_text(json.dumps(report,indent=2)+'\n');print('RESULT',name,'PASS'if ok else'FAIL',flush=True)
    print('EVIDENCE',out/'results.json',flush=True)
    return 0 if report['all_lab_cases_passed'] else 1
if __name__=='__main__':raise SystemExit(main())
