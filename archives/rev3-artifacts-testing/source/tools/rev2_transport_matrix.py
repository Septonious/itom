#!/usr/bin/env python3
"""Serial, fresh-state Rev1/Rev2 laboratory diagnostics, including cold ramp-up."""
import argparse, hashlib, json, os, re, subprocess, time
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]

def fingerprint(root):
    h=hashlib.sha256()
    for p in sorted((root/'core').rglob('*.go')):
        h.update(str(p.relative_to(root)).encode()+b'\0'+p.read_bytes()+b'\0')
    return h.hexdigest()

def fields(line):
    return dict(re.findall(r'([a-z0-9_]+)=([^\s]+)',line))

def summarize(log):
    result=dict(startup_windows=[],steady_windows=[],results=[],cold_probes=[],acceptance=[],failed=[])
    startup=True
    for line in log.read_text().splitlines():
        if not line.startswith('{'):continue
        e=json.loads(line);s=e.get('Output','')
        if e.get('Action')=='fail':result['failed'].append(e.get('Test',e.get('Package')))
        if 'production_window user=' in s:
            result['startup_windows' if startup else 'steady_windows'].append(fields(s))
        elif 'supervised_startup ' in s:
            result['startup_windows'].append(fields(s))
        elif 'supervised_round_schedule ' in s:startup=False
        elif 'supervised_cold_probes ' in s:
            result['cold_probes'].append(fields(s));startup=False
        elif 'supervised_result ' in s:result['results'].append(fields(s))
        elif 'supervised_acceptance ' in s:result['acceptance'].append(fields(s))
        elif 'wrapper_regular_stratum ' in s:result['wrapper_stratum']=fields(s)
    result['strict_100_100_lt1000']=bool(result['results']) and not result['failed'] and all(float(r.get('upload_mbps','0'))>=100 and float(r.get('download_mbps','0'))>=100 and 0<int(r.get('max_ms','1000'))<1000 and int(r.get('timeouts','1'))==0 and int(r.get('missed','1'))==0 and int(r.get('overlap','0'))==int(r.get('rtt_samples','-1')) for r in result['results'])
    return result

def main():
    p=argparse.ArgumentParser(description=__doc__)
    p.add_argument('--go',default='go');p.add_argument('--baseline',type=Path,required=True);p.add_argument('--results',type=Path,required=True)
    p.add_argument('--profiles',choices=('both','uniform','wrapper-regular'),default='both');p.add_argument('--no-baseline',action='store_true');p.add_argument('--multi-streams',type=int,default=8);p.add_argument('--seconds',type=int,default=20);p.add_argument('--warmup',type=int,default=10)
    a=p.parse_args()
    if not 5<=a.seconds<=180 or not 5<=a.warmup<=30 or not 2<=a.multi_streams<=32:p.error('invalid duration')
    out=a.results.resolve()/time.strftime('%Y%m%dT%H%M%S',time.gmtime());out.mkdir(parents=True,exist_ok=False)
    env={k:v for k,v in os.environ.items() if not k.startswith('ITOM_')}
    env.update(GOTOOLCHAIN='local',GOWORK='off',GOMAXPROCS='2',GOFLAGS='-mod=readonly')
    env.update(ITOM_LONG_LOAD='1',ITOM_LOAD_SUPERVISORS='1',ITOM_LOAD_SECONDS=str(a.seconds),ITOM_LOAD_WARMUP_SECONDS=str(a.warmup),ITOM_LOAD_PROVIDER_MBPS='600',ITOM_LOAD_REPEATS='1',ITOM_LOAD_DIAGNOSTICS='0',ITOM_LOAD_TRACE='0',ITOM_SPEEDTEST_GATE='1',ITOM_SPEEDTEST_VALIDATE='0')
    report=dict(schema=1,classification='LABORATORY_ACCOUNT_SCALING_DIAGNOSTIC',field_qualified=False,baseline_commit='a1a1806',baseline_harness_overlay=True,seconds=a.seconds,warmup=a.warmup,first_seconds_included_in_separate_cold_measurement=True,cases=[])
    scenarios=[]
    for profile in (('uniform','wrapper-regular') if a.profiles=='both' else (a.profiles,)):
        for n in (5,8,10,15):
            for mode,streams in (('single',1),('multi',a.multi_streams)):
                if profile=='wrapper-regular' and n in (10,15) and not a.no_baseline:scenarios.append((profile,n,mode,streams,'Rev1',a.baseline.resolve()))
                scenarios.append((profile,n,mode,streams,'Rev2',ROOT))
    for profile,n,mode,streams,release,root in scenarios:
        name=f'{profile}-{n}-{mode}-{release}';log=out/(name+'.jsonl');case_env=env.copy()
        case_env.update(ITOM_LOAD_ACCOUNTS=str(n),ITOM_LOAD_STREAMS=str(streams),ITOM_LOAD_COMMAND_DELAY_MS='80' if profile=='uniform' else '0')
        if profile=='wrapper-regular':case_env.update(ITOM_FIELD_PROVIDER_MODEL='1',ITOM_FIELD_LATENCY_PROFILE='rev1-wrapper',ITOM_FIELD_WRAPPER_MAX_US='500000',ITOM_LOAD_SERVER_COMMIT_US='12000',ITOM_LOAD_CLIENT_COMMIT_US='500')
        before=fingerprint(root);begin=time.monotonic();print('RUN',name,flush=True)
        command=[a.go,'test','-tags=itomtest','-json','-count=1','-timeout=240s','./tunnel','-run','^TestLoadedApplicationProductionHarness$']
        with log.open('w') as stream:process=subprocess.run(command,cwd=root/'core',env=case_env,stdout=stream,stderr=subprocess.STDOUT)
        case=dict(name=name,profile=profile,accounts=n,mode=mode,release=release,exit_code=process.returncode,elapsed_seconds=round(time.monotonic()-begin,3),source_before=before,source_after=fingerprint(root),log=log.name,log_sha256=hashlib.sha256(log.read_bytes()).hexdigest(),measurement=summarize(log))
        report['cases'].append(case);(out/'results.json').write_text(json.dumps(report,indent=2)+'\n')
        print('RESULT',name,json.dumps(case['measurement']['results']),'strict_goal',case['measurement']['strict_100_100_lt1000'],'exit',process.returncode,flush=True)
    print('EVIDENCE',out/'results.json',flush=True)
    # This records diagnostics; individual FAILs remain FAILs in the report.
    return 0
if __name__=='__main__':raise SystemExit(main())
