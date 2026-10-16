from pathlib import Path
import datetime,hashlib,json,os,re,shutil,subprocess,sys,zipfile
W=Path('/workspace/work/rev8');R=W/'repository';S=R/'archives/rev8/source';A=R/'archives/rev8';C=Path('/workspace/work/rev3/build-cache');ST=W/'release-assets';PREFIX='ITOM-0.9.2-Rev8';TAG='v0.9.2-Rev8';CLASS='REV8_UNSTABLE_TESTING_BUILD'
sys.path.insert(0,str(S/'tools'))
from transport_qualification import source_fingerprint
from build_core import runtime_hash
from package_sources import package
def read(p):return json.loads(p.read_text())
def digest(p):
 with p.open('rb')as f:return hashlib.file_digest(f,'sha256').hexdigest()
def entry(p):return {'name':p.name,'size':p.stat().st_size,'sha256':digest(p)}
def events(p):return [json.loads(x)for x in p.read_text().splitlines()if x.startswith('{')]
def messages(p):return [e.get('Output','')for e in events(p)]
def passed(p):
 ev=events(p);return not any(e.get('Action')in('fail','skip')for e in ev) and any(e.get('Action')=='pass' and not e.get('Test')for e in ev)
built=read(W/'build-complete.json');android=read(S/'dist/build-report.json');identity=source_fingerprint();runtime=runtime_hash(S/'core');assert built['source_sha256']==identity and built['runtime_sha256']==runtime
assert android['status']=='passed' and android['source_revision']=='transport-rework3-rev8';assert passed(W/'final-correctness-race.log') and passed(W/'final-targeted-integration.jsonl')
for g in ('domain_tests','app_unit_tests'):
 assert android[g]['tests']>0 and android[g]['failures']==android[g]['errors']==android[g]['skipped']==0
assert android['lint']['errors']==0
loadpath=sorted((W/'final-performance').glob('*/results.json'))[-1];load=read(loadpath);caselog=loadpath.parent/load['cases'][0]['log'];lines=messages(caselog);assert digest(caselog)==load['cases'][0]['log_sha256']
metrics=[x.strip()for x in lines if any(m in x for m in ['supervised_result ','supervised_jitter ','supervised_tail','supervised_acceptance ','supervised_post_stress'])]
post=sum('supervised_post_stress ' in x for x in lines);fresh=any('supervised_post_stress_fresh bytes=262144 integrity_verified=true original_socket_rechecked=true'in x for x in lines)
variation=next(x for x in lines if 'supervised_jitter 'in x);jitter={k:float(re.search(k+r'=([0-9.]+)',variation).group(1))for k in ('mean_ms','p95_ms','max_ms')}
racefail=[e.get('Test')for e in events(W/'final-targeted-race.jsonl')if e.get('Action')=='fail'and e.get('Test')]
correction=read(W/'revision-correction.json');assert correction['before_source_sha256']==load['source_sha256'] and correction['after_source_sha256']==identity
q={'schema':2,'classification':CLASS,'release_tag':TAG,'stable_release_ready':False,'all_internal_gates_passed':False,'all_required_targeted_checks_passed':False,'targeted_correctness_passed':True,'final_full_suite_completed':False,'user_requested_targeted_finalization':True,'throughput_floor_waived_by_user':False,'strict_performance_passed':load['all_lab_cases_passed'],'jitter_target_ms':100,'jitter_metric':'chronological adjacent application-echo RTT absolute difference','jitter_observed':jitter,'jitter_target_passed':jitter['mean_ms']<=100 and jitter['p95_ms']<=100,'qualification_source_sha256':identity,'first_party_core_runtime_sha256':runtime,'post_measurement_revision_correction':correction,'generated_utc':datetime.datetime.now(datetime.timezone.utc).isoformat(),'targeted_race':{'passed':True,'passed_tests':sum(e.get('Action')=='pass'and bool(e.get('Test'))for e in events(W/'final-correctness-race.log')),'log_sha256':digest(W/'final-correctness-race.log')},'normal_recovery_integration':{'passed':True,'tests':['TestRev7CorrelatedControlEOFBetweenTransfersKeepsEveryCarrier','TestRev7MissingIDLENotificationsBoundActiveChatLatency'],'log_sha256':digest(W/'final-targeted-integration.jsonl')},'supplementary_race_failed_tests':racefail,'supplementary_race_failure':'Initial 8 MiB transfer exceeded its 15-second application deadline; no race report; failure is retained, not relabeled as a pass.','final_load':load,'final_load_metrics':metrics,'post_load_same_socket_exchanges':post,'post_load_fresh_verified_bytes':262144 if fresh else 0,'original_socket_rechecked':fresh,'android_build_passed':True,'android_domain_tests':{k:android['domain_tests'][k]for k in ('tests','failures','errors','skipped')},'android_app_tests':{k:android['app_unit_tests'][k]for k in ('tests','failures','errors','skipped')},'android_lint':android['lint'],'native_host_tests_passed':True,'vet_passed':True,'built_targets':['linux/amd64','linux/arm64'],'real_provider_tested':False,'physical_android_device_tested':False,'mobile_crash_resolved':False,'known_failed_requirements':['Strict useful 100 Mbps in each direction without margin was not achieved.','Final MULTI run timed out one loaded application probe while carriers remained connected.','Chronological RTT variation exceeded 100 ms.','One supplementary race-instrumented recovery run timed out its initial bulk transfer.'],'limitations':['Complete final-source internal suite not run, as requested.','Final timed load uses the source fingerprint before the two separately audited capture-revision/source-export corrections; transport code is identical.','The earlier same-runtime run completed all 30 probes and six original-socket post-load exchanges plus a fresh verified 256 KiB exchange, but failed throughput. Its original jitter line used sorted percentiles and is invalid; do not use it as jitter evidence.','The precise real Speedtest error root cause remains unconfirmed; no claim of live-provider stability or 100 Mbps certification.']}
assert not q['strict_performance_passed'] and not q['jitter_target_passed']
evidence_dir=S/'docs/rev8/evidence';evidence_dir.mkdir(exist_ok=True);(evidence_dir/'REV8_VALIDATION.json').write_text(json.dumps(q,indent=2)+'\n');shutil.copy2(W/'field-correlation.json',evidence_dir/'FIELD_CORRELATION.json')
notes='''# ITOM 0.9.2 Rev8 — unstable testing build

**The strict 100+ Mbps and 100 ms jitter objectives were NOT met. This is an unstable testing build, not a stable release.** Published at the user's request for immediate APK/binaries and full handoff after targeted checks. The full internal suite was not rerun.

Changes: cap additional short-path queue allowance at 100 ms once calibrated; shorten the temporary audit after proven missing IMAP IDLE notifications to 50 ms; prevent urgent traffic from extending an existing control flush deadline; retain bounded quota-rollover trace sampling. Fix Rev8 CLI/capture identification and executable source ZIP artifact export.

Targeted correctness/race checks, normal correlated-socket recovery, missing-notification integration, native host checks and Android build checks passed. A supplementary race-instrumented initial bulk transfer timed out and remains disclosed. The final strict retained-state MULTI check used 15 accounts, 24 streams per direction and unchanged provider/deadline limits: 72.072 Mbps upload / 53.579 Mbps download before a loaded probe timed out at 58.1 seconds. Chronological adjacent RTT variation: mean 187.468 ms, p95 390.786 ms, maximum 412.226 ms. No carrier restart was required, but application liveness still failed that run. A preceding same-transport run completed all probes, retained original sockets and passed post-load exchanges; it also failed the speed floor. These are laboratory observations, not phone/provider guarantees.

Install the APK and the appropriate server architecture together. Preserve configuration and durable state. The APK updates the Rev7 application with the same signer. Read HANDOFF.md, STATUS.json and VALIDATION.json; all passing and failing targeted evidence is included. No raw user captures or private signing/provider credentials are published. GitHub Actions reassembles and verifies the exact built artifacts and every uploaded size/hash.
'''
B=R/'builds/0.9.2-Rev8';B.mkdir(parents=True,exist_ok=True);(B/'RELEASE_NOTES.md').write_text(notes)
ST.mkdir(exist_ok=False);apk=S/'dist'/f'{PREFIX}-debug.apk';assert digest(apk)==android['sha256'];jdk=next((C/'jdk17').rglob('bin/java')).parents[1];env=os.environ.copy();env['JAVA_HOME']=str(jdk);env['PATH']=str(jdk/'bin')+':'+env['PATH']
signer=subprocess.run([str(C/'android-sdk/build-tools/36.0.0/apksigner'),'verify','--verbose','--print-certs',str(apk)],env=env,text=True,capture_output=True,check=True).stdout;cert=re.search(r'certificate SHA-256 digest: ([0-9a-f]+)',signer).group(1);assert cert=='39667cd3f91521b09af012b2ca08f8f0ba0d7e07f14b32a9a1e794cb3f4bf999' and 'Verified using v2 scheme (APK Signature Scheme v2): true'in signer
with zipfile.ZipFile(apk)as z:
 assert z.testzip()is None
 for n,h in android['apk_native_libraries'].items():assert hashlib.sha256(z.read(n)).hexdigest()==h
shutil.copy2(apk,ST/apk.name)
for arch,machine in [('amd64',62),('arm64',183)]:
 p=S/'dist'/f'{PREFIX}-linux-{arch}';raw=p.read_bytes();assert raw[:6]==b'\x7fELF\x02\x01'and int.from_bytes(raw[18:20],'little')==machine
 dest=ST/(p.name+'.bin');shutil.copy2(p,dest);dest.chmod(0o755)
assert 'transport-rework3-rev8'in subprocess.check_output([str(ST/(PREFIX+'-linux-amd64.bin')),'version'],text=True)
commit=subprocess.check_output(['git','rev-parse','HEAD'],cwd=R,text=True).strip()
status={'schema':2,'classification':CLASS,'release_tag':TAG,'stable_release_ready':False,'all_internal_gates_passed':False,'all_required_targeted_checks_passed':False,'targeted_correctness_passed':True,'strict_performance_passed':False,'jitter_target_passed':False,'qualified_source_commit':commit,'qualification_source_sha256':identity,'core_runtime_sha256':runtime,'apk_sha256':android['sha256'],'apk_native_libraries':android['apk_native_libraries'],'apk_identity':android['apk_identity'],'certificate_sha256':cert,'signature_v2_verified':True,'mobile_crash_resolved':False,'real_provider_tested':False,'physical_android_device_tested':False,'private_key_included':False,'binary_revision_string':'transport-rework3-rev8'}
(ST/'STATUS.json').write_text(json.dumps(status,indent=2)+'\n');(ST/'VALIDATION.json').write_text(json.dumps(q,indent=2)+'\n');(ST/'SIGNER.txt').write_text(signer);(ST/'RELEASE_NOTES.md').write_text(notes)
for src,name in [(S/'HANDOFF.md','HANDOFF.md'),(S/'docs/rev8/REV8_ANALYSIS.md','REV8_ANALYSIS.md'),(W/'field-correlation.json','FIELD_CORRELATION.json')]:shutil.copy2(src,ST/name)
assert not (S/'android/local.properties').exists();sourcezip=ST/(PREFIX+'-sources.zip');package(S,sourcezip)
with zipfile.ZipFile(sourcezip)as z:
 assert z.testzip()is None
 assert not any(n.endswith(('.jks','.keystore','.key','.env'))for n in z.namelist())
 for n,h in json.loads(z.read('SOURCE_MANIFEST.json')).items():assert hashlib.sha256(z.read(n)).hexdigest()==h
assert source_fingerprint()==identity
files={'final-correctness-race.jsonl':W/'final-correctness-race.log','normal-recovery-integration.jsonl':W/'final-targeted-integration.jsonl','supplementary-race-failed.jsonl':W/'final-targeted-race.jsonl','revision-metadata.patch':W/'revision-metadata.patch','revision-correction.json':W/'revision-correction.json','final-load/results.json':loadpath,'final-load/'+caselog.name:caselog,'python-targeted.log':W/'final-python.log','android-build-report.json':S/'dist/build-report.json','final-vet.log':W/'final-vet.log'}
# Keep the earlier passing post-load observation separate; its sorted-RTT
# jitter line is explicitly invalid and is never used for qualification.
p=sorted((W/'final-strict-load').glob('*/results.json'))[-1];d=read(p)
files['earlier-survival-load/results.json']=p
for c in d['cases']:files['earlier-survival-load/'+c['log']]=p.parent/c['log']
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
manifest={'schema':2,'classification':CLASS,'stable_release_ready':False,'all_internal_gates_passed':False,'all_required_targeted_checks_passed':False,'release_tag':TAG,'qualification_source_sha256':identity,'qualified_source_commit':commit,'archive':entry(full),'parts':entries,'assets':[entry(p)for p in sorted(ST.iterdir())if p.is_file()]};(A/'manifest.json').write_text(json.dumps(manifest,indent=2)+'\n')
print('Packaged',len(manifest['assets']),'assets',len(entries),'parts; unstable status and failures preserved')
