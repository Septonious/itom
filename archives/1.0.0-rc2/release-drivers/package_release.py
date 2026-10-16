"""Package the exact targeted 1.0.0 RC2 build; do not imply full qualification."""
from pathlib import Path
import datetime,hashlib,json,os,re,shutil,subprocess,sys,zipfile
W=Path('/workspace/work/rc2');R=W/'repository';S=R/'archives/1.0.0-rc2/source';A=R/'archives/1.0.0-rc2';C=Path('/workspace/work/rev3/build-cache');ST=W/'release-assets';PREFIX='ITOM-1.0.0-RC2';TAG='v1.0.0-rc2';CLASS='V1_0_0_RC2_TARGETED_TESTING_BUILD'
sys.path.insert(0,str(S/'tools'))
from transport_qualification import source_fingerprint
from build_core import runtime_hash
from package_sources import package

def read(p):return json.loads(p.read_text())
def digest(p):
 with p.open('rb')as f:return hashlib.file_digest(f,'sha256').hexdigest()
def entry(p):return {'name':p.name,'size':p.stat().st_size,'sha256':digest(p)}
def events(p):return [json.loads(x)for x in p.read_text().splitlines()if x.startswith('{')]
def passed(p):
 ev=events(p);return not any(e.get('Action')in('fail','skip')for e in ev) and any(e.get('Action')=='pass' and not e.get('Test')for e in ev)

built=read(W/'build-complete.json');android=read(S/'dist/build-report.json');identity=source_fingerprint();runtime=runtime_hash(S/'core')
assert built['source_sha256']==identity and built['runtime_sha256']==runtime and android['status']=='passed'
assert passed(W/'final-correctness.jsonl') and passed(W/'final-recovery.jsonl')
assert 'Ran 45 tests' in (W/'launcher-check.log').read_text() and (W/'launcher-check.log').read_text().rstrip().endswith('OK')
for group in ('domain_tests','app_unit_tests'):
 assert android[group]['tests']>0 and android[group]['failures']==android[group]['errors']==android[group]['skipped']==0
assert android['lint']['errors']==android['lint']['warnings']==0
assert android['apk_identity']['version_code']==10000
public_android=dict(android)
if 'java_version' in public_android:
 public_android['java_version']='\n'.join(line for line in public_android['java_version'].splitlines() if not any(x in line for x in ['Picked up','JAVA_TOOL_OPTIONS','JDK_JAVA_OPTIONS']))
(W/'android-build-report-public.json').write_text(json.dumps(public_android,indent=2)+'\n')
checks={}
for kind,name in [('race','final-correctness.jsonl'),('normal','final-recovery.jsonl')]:
 ev=events(W/name);line=next(e['Output'] for e in ev if 'minimum_service_recovery_ms=' in e.get('Output',''))
 checks[kind]={'passed':True,'log_sha256':digest(W/name),'passed_test_events':sum(e['Action']=='pass' and bool(e.get('Test'))for e in ev),'minimum_service_recovery_ms':int(re.search(r'minimum_service_recovery_ms=(\d+)',line).group(1)),'restored_active_lines':20,'original_tcp_retained':True,'fresh_256k_transfer_verified':True,'carrier_restarts':0}
required={'TestV1EarlyNackSurvivesWriterAndVisibilityGrace','TestV1NackDuringVisibilityGraceNeedsNoSecondProbe','TestV1EarlyNackDoesNotConvertDefinitivelyUnsentOriginal','TestV1ReceiptCancelsRetainedNackWithoutRefundingPhysicalDebt','TestRev11MultiRampThroughBriefProviderPause','TestRev11BriefProviderSilencePreservesAllReceiveSockets','TestRev11PersistentReceiveBlackholeStillRepairs'}
assert required <= {e.get('Test')for e in events(W/'final-correctness.jsonl')if e['Action']=='pass'}
assert any('concurrent_tcp_streams=12 verified_mib_each_direction=12' in e.get('Output','')for e in events(W/'final-recovery.jsonl'))
q={'schema':4,'classification':CLASS,'stable_release_ready':False,'all_internal_gates_passed':False,'all_required_targeted_checks_passed':False,'targeted_correctness_passed':True,'final_full_suite_completed':False,'user_requested_targeted_finalization':True,'qualification_source_sha256':identity,'first_party_core_runtime_sha256':runtime,'generated_utc':datetime.datetime.now(datetime.timezone.utc).isoformat(),'recovery_checks':checks,'deferred_nack_regressions_passed':True,'multi_ramp_streams':12,'verified_mib_each_direction':12,'temporary_response_stall_ms':2400,'original_receive_generations_preserved':20,'baseline_regression_reproduced':True,'baseline_log_sha256':digest(W/'before-fix.jsonl'),'baseline_scope':'The first two new tests reproduce discarded early NACKs. A third preliminary fixture used an unconfigured account and was corrected before final qualification.','performance_retested':False,'strict_performance_passed':False,'strict_performance_status':'not_retested','strict_speed_target_mbps':100,'speed_tolerance_percent':0,'jitter_target_passed':False,'jitter_status':'not_retested','android_build_passed':True,'android_domain_tests':{k:android['domain_tests'][k]for k in ('tests','failures','errors','skipped')},'android_app_tests':{k:android['app_unit_tests'][k]for k in ('tests','failures','errors','skipped')},'android_lint':android['lint'],'native_host_tests_passed':True,'vet_passed':True,'launcher_tests_passed':45,'built_targets':['linux/amd64','linux/arm64'],'real_provider_tested':False,'physical_android_device_tested':False,'mobile_crash_resolved':False,'known_exploratory_race_timeout':'An older capped mixed-socket test exceeded its fixed 12-second transfer deadline under race instrumentation on this candidate. Its normal rerun passed in approximately 2.7 seconds. No blanket internal-gate pass is claimed.','limitations':['Targeted checks only; complete final internal suite not run.','The older capped-socket race deadline failure remains a qualification limit.','The supplied field captures and screenshot use Rev10; they do not qualify the new candidate.','No new sustained throughput or jitter certificate; strict 100 Mbps target remains unchanged.','No physical Android/provider follow-up or visual interaction qualification in this pass.']}

e=S/'docs/v1/evidence';e.mkdir(exist_ok=True);(e/'V1_VALIDATION.json').write_text(json.dumps(q,indent=2)+'\n')
for name in ('final-recovery.jsonl','final-correctness.jsonl','before-fix.jsonl','launcher-check.log'):shutil.copy2(W/name,e/name)
notes=f'''# ITOM 1.0.0 RC2 — transport stabilization candidate

Authenticated missing-range requests are now retained while their original DATA write is completing or its BODY visibility grace is active. When those existing gates end, repair can proceed without another periodic peer probe. A stale scheduling wake is released once; all physical admission, retry, immutable-item, crypto, accounting and cleanup checks remain in force. Definite unpublished originals stay ordinary queued originals, and receipts do not refund physical debt.

Includes bounded receive-stall recovery: a temporary 2400 ms response pause no longer closes otherwise live receive sockets at 1500 ms, while a genuine blackhole still triggers bounded repair. Failed DATA receive first-close causes remain visible after replacement. Existing admin categories, single export indicator, verified-TLS IP pinning and persistent connections are preserved.

The focused early-NACK regressions, selected final race checks and normal recovery checks passed. A ten-account/twelve-stream finite ramp verifies twelve MiB in each direction through the injected pause, preserving twenty original receive sockets and the original application connection. A fresh transfer and quiet-period follow-up also pass. This is an integrity/recovery regression, not a sustained speed benchmark. Android tests/lint, native host tests, vet and 45 source/build launcher checks passed.

**Release candidate, not stable 1.0.0.** The complete final internal suite and live provider/physical phone follow-up are outstanding. An exploratory older capped mixed-socket race test exceeded its fixed transfer deadline; its normal rerun passed and both results remain included. Sustained strict 100 Mbps duplex and jitter have not been requalified. The supplied 101/77.9 Mbps result is Rev10 field evidence, not a new candidate measurement.

Update both endpoints and preserve existing configuration, account identities, profiles and durable state. APK version code 10000 retains the existing app ID and signer. APK, Linux AMD64/ARM64 binaries, executable sources, handoff, analysis and hashed evidence are included. GitHub Actions verifies and publishes these exact locally built assets; it does not claim an Actions compilation or provider performance run. See VALIDATION.json and HANDOFF.md for exact scope.
'''
B=R/'builds/1.0.0-RC2';B.mkdir(parents=True,exist_ok=True);(B/'RELEASE_NOTES.md').write_text(notes)
ST.mkdir(exist_ok=False);apk=S/'dist'/f'{PREFIX}-debug.apk';assert digest(apk)==android['sha256'];jdk=next((C/'jdk17').rglob('bin/java')).parents[1];env=os.environ.copy();env['JAVA_HOME']=str(jdk);env['PATH']=str(jdk/'bin')+':'+env['PATH']
signer=subprocess.run([str(C/'android-sdk/build-tools/36.0.0/apksigner'),'verify','--verbose','--print-certs',str(apk)],env=env,text=True,capture_output=True,check=True).stdout;cert=re.search(r'certificate SHA-256 digest: ([0-9a-f]+)',signer).group(1);assert cert=='39667cd3f91521b09af012b2ca08f8f0ba0d7e07f14b32a9a1e794cb3f4bf999' and 'Verified using v2 scheme (APK Signature Scheme v2): true'in signer
with zipfile.ZipFile(apk)as z:
 assert z.testzip()is None
 for n,h in android['apk_native_libraries'].items():assert hashlib.sha256(z.read(n)).hexdigest()==h
shutil.copy2(apk,ST/apk.name)
for arch,machine in [('amd64',62),('arm64',183)]:
 p=S/'dist'/f'{PREFIX}-linux-{arch}';raw=p.read_bytes();assert raw[:6]==b'\x7fELF\x02\x01'and int.from_bytes(raw[18:20],'little')==machine
 dest=ST/(p.name+'.bin');shutil.copy2(p,dest);dest.chmod(0o755)
assert 'itom-1-0-0-rc2'in subprocess.check_output([str(ST/(PREFIX+'-linux-amd64.bin')),'version'],text=True)
commit=subprocess.check_output(['git','rev-parse','HEAD'],cwd=R,text=True).strip()
status={'schema':3,'classification':CLASS,'release_tag':TAG,'stable_release_ready':False,'all_internal_gates_passed':False,'all_required_targeted_checks_passed':False,'targeted_correctness_passed':True,'strict_performance_passed':False,'performance_retested':False,'jitter_target_passed':False,'qualified_source_commit':commit,'qualification_source_sha256':identity,'core_runtime_sha256':runtime,'apk_sha256':android['sha256'],'apk_native_libraries':android['apk_native_libraries'],'apk_identity':android['apk_identity'],'certificate_sha256':cert,'signature_v2_verified':True,'mobile_crash_resolved':False,'real_provider_tested':False,'physical_android_device_tested':False,'private_key_included':False,'binary_revision_string':'itom-1-0-0-rc2'}
for name,value in [('STATUS.json',status),('VALIDATION.json',q)]: (ST/name).write_text(json.dumps(value,indent=2)+'\n')
(ST/'SIGNER.txt').write_text(signer);(ST/'RELEASE_NOTES.md').write_text(notes)
for src,name in [(S/'HANDOFF.md','HANDOFF.md'),(S/'docs/v1/INVESTIGATION.md','INVESTIGATION.md')]:shutil.copy2(src,ST/name)
assert not (S/'android/local.properties').exists();sourcezip=ST/(PREFIX+'-sources.zip');package(S,sourcezip)
with zipfile.ZipFile(sourcezip)as z:
 assert z.testzip()is None
 assert not any(n.endswith(('.jks','.keystore','.key','.env'))for n in z.namelist())
 for n,h in json.loads(z.read('SOURCE_MANIFEST.json')).items():assert hashlib.sha256(z.read(n)).hexdigest()==h
assert source_fingerprint()==identity
files={'final-correctness-race.jsonl':W/'final-correctness.jsonl','normal-recovery-integration.jsonl':W/'final-recovery.jsonl','before-fix-regression.jsonl':W/'before-fix.jsonl','launcher-check.log':W/'launcher-check.log','android-build-report.json':W/'android-build-report-public.json','earlier-capped-race-timeout.jsonl':W/'earlier-broad-race.jsonl','normal-capped-rerun.jsonl':W/'capped-normal.jsonl','earlier-instrumented-ramp-timeout.jsonl':Path('/workspace/work/rev11/ramp-diagnostic.jsonl')}
with zipfile.ZipFile(ST/'TEST_EVIDENCE.zip','x',zipfile.ZIP_DEFLATED,compresslevel=6)as z:
 for n,p in files.items():z.write(p,n)
 z.writestr('MANIFEST.json',json.dumps({n:entry(p)for n,p in files.items()},indent=2)+'\n')
(ST/'SHA256SUMS').write_text(''.join(digest(p)+'  '+p.name+'\n'for p in sorted(ST.iterdir())))
full=ST/(PREFIX+'-full.zip')
with zipfile.ZipFile(full,'x',zipfile.ZIP_DEFLATED,compresslevel=6)as z:
 for p in sorted(ST.iterdir()):
  if p!=full:z.write(p,p.name)
(ST/(full.name+'.sha256')).write_text(digest(full)+'  '+full.name+'\n')
parts=A/'parts';parts.mkdir(exist_ok=False);entries=[]
with full.open('rb')as f:
 for i in range(1,100):
  raw=f.read(8*1024*1024)
  if not raw:break
  p=parts/('full.zip.part%03d'%i);p.write_bytes(raw);entries.append({'path':str(p.relative_to(R)),'size':len(raw),'sha256':digest(p)})
manifest={'schema':3,'classification':CLASS,'stable_release_ready':False,'all_internal_gates_passed':False,'all_required_targeted_checks_passed':False,'release_tag':TAG,'qualification_source_sha256':identity,'qualified_source_commit':commit,'archive':entry(full),'parts':entries,'assets':[entry(p)for p in sorted(ST.iterdir())if p.is_file()]};(A/'manifest.json').write_text(json.dumps(manifest,indent=2)+'\n')
print('Packaged',len(manifest['assets']),'assets',len(entries),'parts; targeted testing status retained')
