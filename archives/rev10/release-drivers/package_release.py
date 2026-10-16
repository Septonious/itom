"""Package the exact targeted Rev10 build; do not imply full qualification."""
from pathlib import Path
import datetime,hashlib,json,os,re,shutil,subprocess,sys,zipfile
W=Path('/workspace/work/rev10');R=W/'repository';S=R/'archives/rev10/source';A=R/'archives/rev10';C=Path('/workspace/work/rev3/build-cache');ST=W/'release-assets';PREFIX='ITOM-0.9.2-Rev10';TAG='v0.9.2-Rev10';CLASS='REV10_TARGETED_TESTING_BUILD'
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
assert android['apk_identity']['version_code']==9210
public_android=dict(android)
if 'java_version' in public_android:
 public_android['java_version']='\n'.join(line for line in public_android['java_version'].splitlines() if not any(x in line for x in ['Picked up','JAVA_TOOL_OPTIONS','JDK_JAVA_OPTIONS']))
(W/'android-build-report-public.json').write_text(json.dumps(public_android,indent=2)+'\n')
checks={}
for kind,name in [('race','final-correctness.jsonl'),('normal','final-recovery.jsonl')]:
 ev=events(W/name);line=next(e['Output'] for e in ev if 'minimum_service_recovery_ms=' in e.get('Output',''))
 checks[kind]={'passed':True,'log_sha256':digest(W/name),'passed_test_events':sum(e['Action']=='pass' and bool(e.get('Test'))for e in ev),'minimum_service_recovery_ms':int(re.search(r'minimum_service_recovery_ms=(\d+)',line).group(1)),'restored_active_lines':20,'original_tcp_retained':True,'fresh_256k_transfer_verified':True,'carrier_restarts':0}
q={'schema':3,'classification':CLASS,'stable_release_ready':False,'all_internal_gates_passed':False,'all_required_targeted_checks_passed':True,'targeted_correctness_passed':True,'final_full_suite_completed':False,'user_requested_targeted_finalization':True,'qualification_source_sha256':identity,'first_party_core_runtime_sha256':runtime,'generated_utc':datetime.datetime.now(datetime.timezone.utc).isoformat(),'recovery_checks':checks,'baseline_regression_reproduced':True,'baseline_log_sha256':digest(W/'before-fix.jsonl'),'baseline_scope':'Same discovery and client-role-loss scenario; final test additionally requires all 20 lines and the split DATA reader to recover.','performance_retested':False,'strict_performance_passed':False,'strict_performance_status':'not_retested','strict_speed_target_mbps':100,'speed_tolerance_percent':0,'jitter_target_passed':False,'jitter_status':'not_retested','android_build_passed':True,'android_domain_tests':{k:android['domain_tests'][k]for k in ('tests','failures','errors','skipped')},'android_app_tests':{k:android['app_unit_tests'][k]for k in ('tests','failures','errors','skipped')},'android_lint':android['lint'],'native_host_tests_passed':True,'vet_passed':True,'launcher_tests_passed':45,'built_targets':['linux/amd64','linux/arm64'],'real_provider_tested':False,'physical_android_device_tested':False,'mobile_crash_resolved':False,'limitations':['Targeted regression checks only; complete final internal suite not run.','Correlated field EOF cause is unconfirmed; the recovery regression does not identify an ISP/provider fault.','No new throughput or jitter qualification; the strict 100 Mbps target remains unchanged.','Android UI compiled and checked by lint/unit tests; no physical-device visual or interaction qualification.','The old mobile whole-app crash has no new fatal-stack root cause.']}
e=S/'docs/rev10/evidence';e.mkdir(exist_ok=True);(e/'REV10_VALIDATION.json').write_text(json.dumps(q,indent=2)+'\n')
for name in ('final-recovery.jsonl','final-correctness.jsonl','before-fix.jsonl','launcher-check.log'):shutil.copy2(W/name,e/name)
notes=f'''# ITOM 0.9.2 Rev10 — targeted recovery and admin UI testing build

A temporary failure of the first replacement IMAP control connection no longer immediately tears down its encrypted carrier. Recovery uses cancellable retries within the existing 15-second budget; minimum DATA capacity no longer adds an unconditional one-second initial sleep. Authentication/TLS errors still fail, shared LOGIN limits remain enforced, and uncertain APPENDs are never blindly replayed or refunded.

The Android admin overview now groups access/communication and system tools, with a section selector and separate Transport and Service categories. Diagnostics export shows one progress bar with a percentage.

Targeted testing reproduced original-code carrier cancellation, then verified 10 accounts and 20 lines recovered after all 50 client physical sockets were closed and each first control replacement returned EOF. Original application sockets and a fresh verified transfer survived. Minimum-service recovery was {checks['race']['minimum_service_recovery_ms']} ms with race instrumentation and {checks['normal']['minimum_service_recovery_ms']} ms normally in the local test fixture. These are laboratory recovery times, not field guarantees. The retained post-load and capped-socket recovery checks passed. Android unit tests/lint, native host checks, vet and 45 source/build launcher checks passed.

The new field capture shows correlated client read EOFs while server IMAP connections remain healthy, but ends about two seconds later. Its root network/provider cause is unconfirmed. No bulk Speedtest transfer was run. This build strengthens recovery; it cannot prevent a remote peer or network from closing physical connections.

**Testing prerelease:** the full internal suite, live provider, physical phone, strict 100 Mbps throughput and jitter targets have not been requalified. Previous Rev9 performance results are historical, not Rev10 measurements. See VALIDATION.json and HANDOFF.md for exact scope.

Update both endpoints and preserve existing profiles, configuration and durable state. APK version code 9210 keeps the prior application ID and signer. AMD64/ARM64 Linux binaries, full sources, complete handoff and hashed evidence are included. GitHub Actions reassembles and verifies these exact locally built assets before publishing; it does not claim an Actions compilation or live performance test.
'''
B=R/'builds/0.9.2-Rev10';B.mkdir(parents=True,exist_ok=True);(B/'RELEASE_NOTES.md').write_text(notes)
ST.mkdir(exist_ok=False);apk=S/'dist'/f'{PREFIX}-debug.apk';assert digest(apk)==android['sha256'];jdk=next((C/'jdk17').rglob('bin/java')).parents[1];env=os.environ.copy();env['JAVA_HOME']=str(jdk);env['PATH']=str(jdk/'bin')+':'+env['PATH']
signer=subprocess.run([str(C/'android-sdk/build-tools/36.0.0/apksigner'),'verify','--verbose','--print-certs',str(apk)],env=env,text=True,capture_output=True,check=True).stdout;cert=re.search(r'certificate SHA-256 digest: ([0-9a-f]+)',signer).group(1);assert cert=='39667cd3f91521b09af012b2ca08f8f0ba0d7e07f14b32a9a1e794cb3f4bf999' and 'Verified using v2 scheme (APK Signature Scheme v2): true'in signer
with zipfile.ZipFile(apk)as z:
 assert z.testzip()is None
 for n,h in android['apk_native_libraries'].items():assert hashlib.sha256(z.read(n)).hexdigest()==h
shutil.copy2(apk,ST/apk.name)
for arch,machine in [('amd64',62),('arm64',183)]:
 p=S/'dist'/f'{PREFIX}-linux-{arch}';raw=p.read_bytes();assert raw[:6]==b'\x7fELF\x02\x01'and int.from_bytes(raw[18:20],'little')==machine
 dest=ST/(p.name+'.bin');shutil.copy2(p,dest);dest.chmod(0o755)
assert 'transport-rework3-rev10'in subprocess.check_output([str(ST/(PREFIX+'-linux-amd64.bin')),'version'],text=True)
commit=subprocess.check_output(['git','rev-parse','HEAD'],cwd=R,text=True).strip()
status={'schema':3,'classification':CLASS,'release_tag':TAG,'stable_release_ready':False,'all_internal_gates_passed':False,'all_required_targeted_checks_passed':True,'targeted_correctness_passed':True,'strict_performance_passed':False,'performance_retested':False,'jitter_target_passed':False,'qualified_source_commit':commit,'qualification_source_sha256':identity,'core_runtime_sha256':runtime,'apk_sha256':android['sha256'],'apk_native_libraries':android['apk_native_libraries'],'apk_identity':android['apk_identity'],'certificate_sha256':cert,'signature_v2_verified':True,'mobile_crash_resolved':False,'real_provider_tested':False,'physical_android_device_tested':False,'private_key_included':False,'binary_revision_string':'transport-rework3-rev10'}
for name,value in [('STATUS.json',status),('VALIDATION.json',q)]: (ST/name).write_text(json.dumps(value,indent=2)+'\n')
(ST/'SIGNER.txt').write_text(signer);(ST/'RELEASE_NOTES.md').write_text(notes)
for src,name in [(S/'HANDOFF.md','HANDOFF.md'),(S/'docs/rev10/INVESTIGATION.md','INVESTIGATION.md')]:shutil.copy2(src,ST/name)
assert not (S/'android/local.properties').exists();sourcezip=ST/(PREFIX+'-sources.zip');package(S,sourcezip)
with zipfile.ZipFile(sourcezip)as z:
 assert z.testzip()is None
 assert not any(n.endswith(('.jks','.keystore','.key','.env'))for n in z.namelist())
 for n,h in json.loads(z.read('SOURCE_MANIFEST.json')).items():assert hashlib.sha256(z.read(n)).hexdigest()==h
assert source_fingerprint()==identity
files={'final-correctness-race.jsonl':W/'final-correctness.jsonl','normal-recovery-integration.jsonl':W/'final-recovery.jsonl','before-fix-regression.jsonl':W/'before-fix.jsonl','launcher-check.log':W/'launcher-check.log','android-build-report.json':W/'android-build-report-public.json'}
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
manifest={'schema':3,'classification':CLASS,'stable_release_ready':False,'all_internal_gates_passed':False,'all_required_targeted_checks_passed':True,'release_tag':TAG,'qualification_source_sha256':identity,'qualified_source_commit':commit,'archive':entry(full),'parts':entries,'assets':[entry(p)for p in sorted(ST.iterdir())if p.is_file()]};(A/'manifest.json').write_text(json.dumps(manifest,indent=2)+'\n')
print('Packaged',len(manifest['assets']),'assets',len(entries),'parts; targeted testing status retained')
