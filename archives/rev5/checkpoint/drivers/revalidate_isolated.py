import os,time,pathlib,subprocess,shutil,json
w=pathlib.Path('/workspace/work/rev5');pid=65716
while pathlib.Path('/proc/'+str(pid)+'/cmdline').exists() and b'validate_candidate.py' in pathlib.Path('/proc/'+str(pid)+'/cmdline').read_bytes(): time.sleep(5)
a=w/'validation-cycle2';a.mkdir(exist_ok=False)
for name in ('candidate-validation.json','candidate-python.log','candidate-speed.log','validation-progress.log'):
 p=w/name
 if p.exists():shutil.copy2(p,a/name)
(a/'MEASUREMENT_CONTEXT.json').write_text(json.dumps({'source_sha256':'bd189f02120db74a23ecfba202137ef50520b34d7af1532fba5bef0db93d488f','single_1_measurement_utc':['2026-10-15T10:25:07.122735722Z','2026-10-15T10:26:07.131084944Z'],'competing_field_correlation_utc':['2026-10-15T10:25:56.856570Z','2026-10-15T10:26:04.704135Z'],'cpu_quota_cores':2,'classification':'FAILED_RUN_RETAINED_NOT_QUALIFICATION','next_action':'Run all six fresh measurements without concurrent analysis or packaging'},indent=2)+'\n')
p=w/'validation-progress.log'
with p.open('w') as f:
 result=subprocess.run(['python',str(w/'validate_candidate.py')],cwd=w/'source/itom',stdin=subprocess.DEVNULL,stdout=f,stderr=subprocess.STDOUT)
(w/'isolated-cycle-result.json').write_text(json.dumps({'exit_code':result.returncode}))
