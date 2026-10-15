import os,time,pathlib,subprocess,json
W=pathlib.Path('/workspace/work/rev5');pid=65716
while pathlib.Path('/proc/'+str(pid)+'/cmdline').exists() and b'validate_candidate.py' in pathlib.Path('/proc/'+str(pid)+'/cmdline').read_bytes():time.sleep(5)
env=os.environ.copy()
for k in list(env):
 if k.startswith(('ITOM_LOAD_','ITOM_FIELD_','ITOM_REV4_TEST_')) or k in ('ITOM_CORE_QUALIFICATION','ITOM_UNSTABLE_SURVIVAL'):env.pop(k)
env.update(GOTOOLCHAIN='local',GOWORK='off',GOFLAGS='-mod=readonly',GOMAXPROCS='2',GOCACHE='/workspace/toolchains/go-cache',GOPATH='/workspace/toolchains/gopath',ITOM_REWORK_QUALIFICATION='1',ITOM_LOAD_ACCOUNTS='15',ITOM_LOAD_STREAMS='1',ITOM_LOAD_SECONDS='60',ITOM_LOAD_PROBE_CADENCE_MS='2000',ITOM_LOAD_PROBE_DEADLINE_MS='1100',ITOM_LOAD_WARMUP_SECONDS='10',ITOM_LOAD_COMMAND_DELAY_MS='80',ITOM_LOAD_PROVIDER_MBPS='600',ITOM_LOAD_PROVIDER_CONNECTION_LIMIT='10',ITOM_LOAD_ACCOUNT_SESSIONS='10',ITOM_LOAD_ENDPOINT_CONNECTIONS='128',ITOM_LOAD_UID_HISTORY='64',ITOM_LOAD_DIAGNOSTICS='1',ITOM_LOAD_SNAPSHOTS='1',ITOM_LOAD_TRACE='0')
with (W/'single-diagnostic.log').open('w') as f:
 p=subprocess.run(['/workspace/toolchains/go/bin/go','test','-json','-count=1','-timeout=660s','./tunnel','-run','^TestTransportReworkQualification$','-cpuprofile='+str(W/'single-cpu.pprof')],cwd=W/'source/itom/core',env=env,stdin=subprocess.DEVNULL,stdout=f,stderr=subprocess.STDOUT)
(W/'single-diagnostic-status.json').write_text(json.dumps({'exit_code':p.returncode}))
