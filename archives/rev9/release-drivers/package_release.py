from pathlib import Path
import datetime,hashlib,json,os,re,shutil,subprocess,sys,zipfile
W=Path('/workspace/work/rev9');R=W/'repository';S=R/'archives/rev9/source';A=R/'archives/rev9';C=Path('/workspace/work/rev3/build-cache');ST=W/'release-assets';PREFIX='ITOM-0.9.2-Rev9';TAG='v0.9.2-Rev9';CLASS='REV9_TARGETED_TESTING_BUILD'
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
assert (W/'validation-complete.json').exists()
built=read(W/'build-complete.json');android=read(S/'dist/build-report.json');identity=source_fingerprint();runtime=runtime_hash(S/'core')
assert built['source_sha256']==identity and built['runtime_sha256']==runtime and android['status']=='passed'
assert passed(W/'final-correctness.jsonl') and passed(W/'final-recovery.jsonl')
for group in ('domain_tests','app_unit_tests'):
 assert android[group]['tests']>0 and android[group]['failures']==android[group]['errors']==android[group]['skipped']==0
assert android['lint']['errors']==android['lint']['warnings']==0
loadpath=sorted((W/'final-performance').glob('*/results.json'))[-1];load=read(loadpath);assert load['source_sha256']==identity
assert load['scenario']['minimum_mbps']==100 and not load['scenario']['stability_only'] and load['scenario']['command_delay_ms']==20
assert load['criteria']['speed_tolerance_percent']==0 and len(load['cases'])==2
case_survival={};measurements={};all_metrics=[]
for case in load['cases']:
 p=loadpath.parent/case['log'];assert digest(p)==case['log_sha256'];lines=messages(p)
 result=next(x for x in lines if 'supervised_result 'in x)
 def number(key):
  found=re.search(key+r'=([0-9.]+)',result);return float(found.group(1)) if found else None
 jitter_line=next((x for x in lines if 'supervised_jitter 'in x),'')
 jitter={k:float(re.search(k+r'=([0-9.]+)',jitter_line).group(1))for k in ('mean_ms','p95_ms','max_ms')} if jitter_line else None
 measurements[case['name']]={'upload_mbps':number('upload_mbps'),'download_mbps':number('download_mbps'),'rtt_max_ms':number('max_ms'),'timeouts':number('timeouts'),'missed':number('missed'),'jitter':jitter,'strict_case_passed':case['ok']}
 count=sum('supervised_post_stress 'in x for x in lines);fresh=any('supervised_post_stress_fresh bytes=262144 integrity_verified=true original_socket_rechecked=true'in x for x in lines)
 case_survival[case['name']]={'same_socket_exchanges':count,'fresh_verified_bytes':262144 if fresh else 0,'original_socket_rechecked':fresh,'full_load_and_post_load_survived':count==6 and fresh and number('timeouts')==number('missed')==0 and number('measured_ms')==60000}
 all_metrics.extend(x.strip()for x in lines if any(m in x for m in ['supervised_result ','supervised_jitter ','supervised_tail','supervised_acceptance ','supervised_post_stress']))
jitter_ok=all(v['jitter'] is not None and v['jitter']['mean_ms']<=100 and v['jitter']['p95_ms']<=100 for v in measurements.values())
q={'schema':3,'classification':CLASS,'release_tag':TAG,'stable_release_ready':False,'all_internal_gates_passed':False,'all_required_targeted_checks_passed':False,'targeted_correctness_passed':True,'final_full_suite_completed':False,'user_requested_targeted_finalization':True,'throughput_floor_waived_by_user':False,'strict_performance_passed':load['all_lab_cases_passed'],'jitter_target_ms':100,'jitter_metric':'chronological adjacent application-echo RTT absolute difference; not Speedtest jitter','jitter_target_passed':jitter_ok,'qualification_source_sha256':identity,'first_party_core_runtime_sha256':runtime,'generated_utc':datetime.datetime.now(datetime.timezone.utc).isoformat(),'targeted_race':{'passed':True,'passed_test_events':sum(e.get('Action')=='pass'and bool(e.get('Test'))for e in events(W/'final-correctness.jsonl')),'log_sha256':digest(W/'final-correctness.jsonl')},'final_recovery':{'passed':True,'log_sha256':digest(W/'final-recovery.jsonl')},'final_load':load,'final_load_metrics':all_metrics,'measurements':measurements,'case_survival':case_survival,'android_build_passed':True,'android_domain_tests':{k:android['domain_tests'][k]for k in ('tests','failures','errors','skipped')},'android_app_tests':{k:android['app_unit_tests'][k]for k in ('tests','failures','errors','skipped')},'android_lint':android['lint'],'native_host_tests_passed':True,'vet_passed':True,'built_targets':['linux/amd64','linux/arm64'],'ip_pinning_implemented':True,'tls_identity_and_verification_preserved':True,'pin_export_round_trip_tested':True,'persistent_login_reuse_tested':True,'admin_pin_independent_watchdog_tested':True,'real_provider_tested':False,'physical_android_device_tested':False,'mobile_crash_resolved':False,'earlier_candidate_failure':'A supplementary race-instrumented initial bulk transfer exceeded its 15-second deadline before the final Pump optimization. It is retained separately; normal final recovery passed.','limitations':['A complete final internal suite was not rerun.','The 20 ms command-delay input is a declared low-RTT laboratory scenario, not established authenticated IMAP latency from the supplied TCP setup report.','No live Finnish VPS/Rambler or physical phone qualification was performed.','A diagnostic profiled run is not a speed certification.','The older mobile whole-app crash has no confirmed fatal-stack root cause.']}
evidence=S/'docs/rev9/evidence';evidence.mkdir(exist_ok=True);(evidence/'REV9_VALIDATION.json').write_text(json.dumps(q,indent=2)+'\n')
summary=[]
for name,m in measurements.items():
 j=m['jitter'];jitter_text=(f"adjacent RTT variation mean {j['mean_ms']:.3f} ms / p95 {j['p95_ms']:.3f} ms" if j else 'jitter unavailable')
 summary.append(f"- {name}: upload {m['upload_mbps']:.3f} / download {m['download_mbps']:.3f} Mbps; {jitter_text}; strict speed case {'PASS' if m['strict_case_passed'] else 'FAIL'}; post-load survival {case_survival[name]['full_load_and_post_load_survived']}.")
notes='# ITOM 0.9.2 Rev9 — targeted testing build\n\nOptional IMAP IPv4/IPv6 pinning is now available in the server manager, guarded admin UI and Android profile editor, for one account or all accounts. Exported invitations carry the pin. TCP connects to the pin while TLS continues to authenticate the IMAP hostname; blank restores DNS. Existing persistent sockets, IDLE and idle NOOP keepalive remain in use. The tests verify one authenticated connection across multiple operations.\n\nProfile-guided changes remove redundant ITE2 proof hashing, repeated BINARY MIME decoding, repeated carrier-score scans and per-socket recounting of bulk readers. Digest, descriptor, quota, Noise/AEAD, replay and physical-deletion checks remain mandatory. Worker/queue/physical caps are unchanged.\n\nTargeted Go race and normal recovery checks, native host checks, Android tests and lint passed. The full final core suite and physical provider/device qualification are NOT claimed; this remains a testing prerelease. The earlier race-instrumented candidate timeout remains included. The 100 Mbps floor has no speed margin.\n\nFinal retained-state laboratory results (15 accounts; 20 ms declared command delay; 600 Mbps provider model; one 60-second SINGLE and MULTI case):\n\n'+'\n'.join(summary)+'\n\n'+('Strict 100 Mbps duplex qualification passed in both laboratory cases. ' if load['all_lab_cases_passed'] else '**Strict 100 Mbps duplex qualification is still incomplete/failed; do not treat this as a certified stable release.** ')+('The laboratory mean/p95 jitter target passed. ' if jitter_ok else 'The laboratory jitter target was not met in every case. ')+'No live Finnish-provider or phone performance guarantee is implied by this scenario. Read VALIDATION.json and HANDOFF.md for the exact limits.\n\nUpdate both endpoints, retaining existing configuration and durable state. The APK uses version code 9209 and the existing signer/application ID. Server manager: Mail accounts → IMAP IP pinning; phone: edit profile → account pin or Set IP pin for all accounts. Server changes do not silently overwrite an existing phone profile. Private credentials/signing keys and raw user captures are excluded. GitHub Actions verifies and publishes the exact built assets, source, evidence and full handoff.\n'
B=R/'builds/0.9.2-Rev9';B.mkdir(parents=True,exist_ok=True);(B/'RELEASE_NOTES.md').write_text(notes)
ST.mkdir(exist_ok=False);apk=S/'dist'/f'{PREFIX}-debug.apk';assert digest(apk)==android['sha256'];jdk=next((C/'jdk17').rglob('bin/java')).parents[1];env=os.environ.copy();env['JAVA_HOME']=str(jdk);env['PATH']=str(jdk/'bin')+':'+env['PATH']
signer=subprocess.run([str(C/'android-sdk/build-tools/36.0.0/apksigner'),'verify','--verbose','--print-certs',str(apk)],env=env,text=True,capture_output=True,check=True).stdout;cert=re.search(r'certificate SHA-256 digest: ([0-9a-f]+)',signer).group(1);assert cert=='39667cd3f91521b09af012b2ca08f8f0ba0d7e07f14b32a9a1e794cb3f4bf999' and 'Verified using v2 scheme (APK Signature Scheme v2): true'in signer
with zipfile.ZipFile(apk)as z:
 assert z.testzip()is None
 for n,h in android['apk_native_libraries'].items():assert hashlib.sha256(z.read(n)).hexdigest()==h
shutil.copy2(apk,ST/apk.name)
for arch,machine in [('amd64',62),('arm64',183)]:
 p=S/'dist'/f'{PREFIX}-linux-{arch}';raw=p.read_bytes();assert raw[:6]==b'\x7fELF\x02\x01'and int.from_bytes(raw[18:20],'little')==machine
 dest=ST/(p.name+'.bin');shutil.copy2(p,dest);dest.chmod(0o755)
assert 'transport-rework3-rev9'in subprocess.check_output([str(ST/(PREFIX+'-linux-amd64.bin')),'version'],text=True)
commit=subprocess.check_output(['git','rev-parse','HEAD'],cwd=R,text=True).strip()
status={'schema':2,'classification':CLASS,'release_tag':TAG,'stable_release_ready':False,'all_internal_gates_passed':False,'all_required_targeted_checks_passed':False,'targeted_correctness_passed':True,'strict_performance_passed':q['strict_performance_passed'],'jitter_target_passed':q['jitter_target_passed'],'qualified_source_commit':commit,'qualification_source_sha256':identity,'core_runtime_sha256':runtime,'apk_sha256':android['sha256'],'apk_native_libraries':android['apk_native_libraries'],'apk_identity':android['apk_identity'],'certificate_sha256':cert,'signature_v2_verified':True,'mobile_crash_resolved':False,'real_provider_tested':False,'physical_android_device_tested':False,'private_key_included':False,'binary_revision_string':'transport-rework3-rev9'}
(ST/'STATUS.json').write_text(json.dumps(status,indent=2)+'\n');(ST/'VALIDATION.json').write_text(json.dumps(q,indent=2)+'\n');(ST/'SIGNER.txt').write_text(signer);(ST/'RELEASE_NOTES.md').write_text(notes)
for src,name in [(S/'HANDOFF.md','HANDOFF.md'),(S/'docs/rev9/REV9_ANALYSIS.md','REV9_ANALYSIS.md'),(W/'route-analysis.json','ROUTE_ANALYSIS.json')]:shutil.copy2(src,ST/name)
assert not (S/'android/local.properties').exists();sourcezip=ST/(PREFIX+'-sources.zip');package(S,sourcezip)
with zipfile.ZipFile(sourcezip)as z:
 assert z.testzip()is None
 assert not any(n.endswith(('.jks','.keystore','.key','.env'))for n in z.namelist())
 for n,h in json.loads(z.read('SOURCE_MANIFEST.json')).items():assert hashlib.sha256(z.read(n)).hexdigest()==h
assert source_fingerprint()==identity
files={'final-correctness-race.jsonl':W/'final-correctness.jsonl','normal-recovery-integration.jsonl':W/'final-recovery.jsonl','earlier-targeted-checks.jsonl':W/'final-targeted.jsonl','earlier-regressions.jsonl':W/'rev9-regressions.jsonl','earlier-candidate-race-failed.jsonl':W/'hotpath-targeted.jsonl','diagnostic-profile-top.txt':W/'profile-top.txt','diagnostic-profile.cpu.pprof':W/'transport.cpu.pprof','diagnostic-profile-load.jsonl':W/'profile-load.jsonl','final-load/results.json':loadpath,'android-build-report.json':W/'android-build-report-public.json'}
for case in load['cases']:files['final-load/'+case['log']]=loadpath.parent/case['log']
p=sorted((W/'low-rtt-baseline').glob('*/results.json'))[-1];d=read(p);files['pre-optimization-baseline/results.json']=p
for case in d['cases']:files['pre-optimization-baseline/'+case['log']]=p.parent/case['log']
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
