from pathlib import Path
import datetime,hashlib,json,os,re,subprocess,sys
W=Path('/workspace/work/rev7');S=W/'source/itom';sys.path.insert(0,str(S/'tools'))
from build_core import build_binaries,runtime_hash
from transport_qualification import source_fingerprint
identity=source_fingerprint();runtime=runtime_hash(S/'core')
env=os.environ.copy();env.update(GOTOOLCHAIN='local',GOWORK='off',GOFLAGS='-mod=readonly',GOMAXPROCS='2',GOCACHE='/workspace/toolchains/go-cache',GOPATH='/workspace/toolchains/gopath')
go='/workspace/toolchains/go/bin/go'
def command(args,cwd,capture=False):
 with (W/'final-build.log').open('a') as log:
  r=subprocess.run([go,*args],cwd=cwd,env=env,stdout=subprocess.PIPE if capture else log,stderr=log,text=True,check=True)
 return r.stdout if capture else ''
assert 'go1.27.1 ' in command(['version'],S,capture=True)
race_path=W/'final-targeted-race.jsonl';events=[json.loads(x) for x in race_path.read_text().splitlines() if x.startswith('{')]
required={'TestRev7ControlAndReceiveRepairBypassQueuedOptionalLogins','TestRev7LoginPriorityCancellationDoesNotLeakCapacity','TestRev7CorrelatedControlEOFBetweenTransfersKeepsEveryCarrier'}
assert not any(e.get('Action') in ('fail','skip') for e in events)
assert required <= {e['Test'] for e in events if e.get('Action')=='pass' and e.get('Test')}
assert any(e.get('Action')=='pass' and not e.get('Test') for e in events)
print('Targeted recovery race checks PASS',flush=True)
command(['vet','-mod=readonly','./tunnel','./datapath'],S/'core')
build_binaries(command,S/'core',S/'dist',env,release='Rev7')
print('Linux AMD64/ARM64 built from final sources',flush=True)
subprocess.run([sys.executable,'tools/transport_qualification.py','--go',go,'--streams','multi','--accounts','15','--seconds','60','--repeats','1','--retained-state','--stability-only','--results',str(W/'final-targeted-load')],cwd=S,env=env,check=True)
reports=sorted((W/'final-targeted-load').glob('*/results.json'));path=reports[-1];load=json.loads(path.read_text())
assert load['all_lab_cases_passed'] and len(load['cases'])==1 and load['source_sha256']==identity
case=load['cases'][0];log=path.parent/case['log'];assert hashlib.sha256(log.read_bytes()).hexdigest()==case['log_sha256']
outputs=[json.loads(x).get('Output','') for x in log.read_text().splitlines() if x.startswith('{')]
assert len([x for x in outputs if 'supervised_post_stress ' in x])==6
assert any('supervised_post_stress_fresh bytes=262144 integrity_verified=true original_socket_rechecked=true' in x for x in outputs)
assert any('supervised_stability windows=60 positive_duplex_every_window=true throughput_floor_enforced=false' in x for x in outputs)
assert any('accepted=true' in x and 'supervised_acceptance ' in x for x in outputs)
android=json.loads((S/'dist/build-report.json').read_text());baseline=json.loads((W/'expanded-validation/core-report.json').read_text())
assert android['status']=='passed' and android['lint']['errors']==android['lint']['warnings']==0
for group in ('domain_tests','app_unit_tests'):
 assert android[group]['tests']>0 and android[group]['failures']==android[group]['errors']==android[group]['skipped']==0
assert baseline['status']=='passed' and baseline['tests']['failed']==baseline['tests']['skipped']==0
assert identity==source_fingerprint() and runtime==runtime_hash(S/'core')
q=dict(schema=2,classification='REV7_TARGETED_VALIDATION_PRERELEASE',release_tag='v0.9.2-Rev7',stable_release_ready=False,
 all_internal_gates_passed=False,all_required_targeted_checks_passed=True,final_full_suite_completed=False,
 user_requested_targeted_finalization=True,throughput_floor_waived_by_user=True,
 qualification_source_sha256=identity,first_party_core_runtime_sha256=runtime,
 generated_utc=datetime.datetime.now(datetime.timezone.utc).isoformat(),
 baseline_full_suite=dict(source_sha256=json.loads((W/'expanded-validation/pipeline.json').read_text())['source_sha256'],core=baseline['tests'],imap_fork_tests=baseline['imap_fork_tests'],vet=baseline['vet'],note='Full suite passed before the final minimum-DATA recovery priority fix. Not claimed as a full final-runtime pass.'),
 final_targeted_race=dict(required=sorted(required),passed=True,failed=0,skipped=0,log_sha256=hashlib.sha256(race_path.read_bytes()).hexdigest()),
 targeted_normal_recovery_repetitions=3,targeted_normal_recovery_passed=True,
 android_domain_tests={k:android['domain_tests'][k] for k in ('tests','failures','errors','skipped')},
 android_app_tests={k:android['app_unit_tests'][k] for k in ('tests','failures','errors','skipped')},android_lint=android['lint'],
 built_targets=['linux/amd64','linux/arm64'],final_load=load,post_load_same_socket_exchanges=6,post_load_fresh_verified_bytes=262144,original_socket_rechecked=True,
 final_load_metrics=[x.strip() for x in outputs if any(m in x for m in ('supervised_result ','supervised_acceptance ','supervised_tail ','supervised_tail10 '))],
 correction='A supplementary repetition exposed minimum DATA service starvation after correlated socket failures. The sole DATA receiver and first usable DATA writer now use critical repair priority; additional writers stay optional.',
 real_provider_tested=False,physical_android_device_tested=False,mobile_crash_resolved=False,
 limitations=['The final full-suite rerun and redundant matrix repetitions were stopped at the user request to conserve usage.','Final validation is targeted; the earlier full-suite pass applies to its recorded pre-fix source.','No physical phone/live Rambler validation; earlier whole-app mobile crash root cause is unconfirmed.'])
p=S/'docs/rev7/evidence/rev7_validation.json';p.write_text(json.dumps(q,indent=2)+'\n');(W/'final-targeted-complete.json').write_text(json.dumps(q,indent=2)+'\n')
print('Final targeted validation complete; full final-suite status explicitly NOT claimed',flush=True)
