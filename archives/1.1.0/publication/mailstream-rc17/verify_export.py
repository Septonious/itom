"""Verify exact-source RC17 builds and export the already signed testing assets."""
from pathlib import Path
import hashlib,json,shutil,subprocess,zipfile

SOURCE='0af1f8891db496a42ac850d84e20d68e8ff7a7b4'
REVISION='itom-1-1-0-mailstream-rc17'
CERT='47d0d87a99b31e32d7f8b9e5f199d3155c5626c324aa7a29f93c42db1d18e0a6'
pub=Path(__file__).resolve().parent
root=Path('release-assets')
def read(p):return json.loads(p.read_text())
def digest(p):return hashlib.sha256(p.read_bytes()).hexdigest()
metadata=read(pub/'MAILSTREAM_RC17_BUILD.json')
validation=read(pub/'ARTIFACT_VALIDATION.json')
assert metadata['source_commit']==validation['source_commit']==SOURCE
assert not metadata['artifact_validation_pending'] and validation['exit_code']==0
assert metadata['checks'] and all(metadata['checks'].values()) and metadata['checks']==validation['checks']
assert metadata['build_run_id']==42246452012 and metadata['source_acceptance_run_id']==42246451745
assert metadata['source_acceptance_commit']=='0af1f8891db496a42ac850d84e20d68e8ff7a7b4' and metadata['source_acceptance_source_files_identical']
assert not metadata['field_qualified'] and not metadata['real_provider_tested']
assert metadata['field_ready_baseline']=='MAILSTREAM-RC10'
assert (root/'SOURCE_COMMIT').read_text().strip()==SOURCE
subprocess.run(['sha256sum','--check','NATIVE_SOURCE_SHA256SUMS'],cwd=root,check=True)
for arch,value in metadata['server_sha256'].items():
    assert digest(root/f'ITOM-1.1.0-MAILSTREAM-RC17-linux-{arch}.bin')==value
parts=read(Path('android-evidence/manifest.json'))
report=read(Path('android-evidence/build-report.json'))
assert parts['source_commit']==SOURCE and report['source_revision']==REVISION and report['status']=='passed'
assert report['apk_identity']==metadata['apk_identity']=={'application_id':'org.itom.client','version_code':10142,'version_name':'1.1.0-mailstream-rc17-debug'}
for name in ('domain_tests','app_unit_tests'):
    result=report[name];assert result['tests']>0 and result['failures']==result['errors']==result['skipped']==0
assert report['lint']['errors']==0
chunks=[]
for part in parts['parts']:
    raw=Path('apk-parts',part['name']).read_bytes()
    assert len(raw)==part['bytes'] and hashlib.sha256(raw).hexdigest()==part['sha256']
    chunks.append(raw)
original=Path('original.apk');original.write_bytes(b''.join(chunks))
assert original.stat().st_size==parts['apk_bytes']
assert digest(original)==parts['apk_sha256']==report['sha256']==metadata['source_ci_apk_sha256']
signed=root/'ITOM-1.1.0-MAILSTREAM-RC17.apk'
subprocess.run(['python3',str(pub/'restore_signed_apk.py'),str(original),str(signed)],check=True)
signer=subprocess.check_output(['java','-jar','signing-tools/apksigner.jar','verify','--verbose','--print-certs',str(signed)],text=True)
assert 'Signer #1 certificate SHA-256 digest: '+CERT in signer
assert 'Verified using v3 scheme (APK Signature Scheme v3): true' in signer
(root/'SIGNER.txt').write_text(signer)
assert digest(signed)==metadata['signed_apk_sha256'] and signed.stat().st_size==metadata['signed_apk_bytes']
with zipfile.ZipFile(signed) as archive:
    for name,value in report['apk_native_libraries'].items():
        raw=archive.read(name)
        assert hashlib.sha256(raw).hexdigest()==value==metadata['apk_native_libraries'][name]
        assert REVISION.encode()in raw and b'itom.local/mailstream'in raw and b'append_completion_ms'in raw and b'bulk_delivery_horizon_ms'in raw and b'bulk_saturated_round_rtt_ms'in raw and b'interactive_queue_limited'in raw
source_manifest=read(pub/'MAILSTREAM_RC17_SOURCE_MANIFEST.json')
assert source_manifest['source_commit']==SOURCE
for name,value in source_manifest['files'].items():assert digest(Path(name))==value,name
safetyfiles=list(Path('evidence/hosted-safety').rglob('core-report.json'));assert len(safetyfiles)==1
safety=read(safetyfiles[0]);assert safety['status']=='passed' and safety['stage']=='complete' and safety['source_revision']==REVISION
for key in ('mailstream_normal_tests','mailstream_race_tests','native_normal_tests','native_race_tests','imap_fork_tests','vet'):assert safety[key]=='passed',key
assert safety['tests']['failed']==safety['race_tests']['failed']==0
import sys
sys.path.insert(0,'archives/1.1.0/qualification')
from compare import fingerprint
def model(label,path):
    result=read(path/'candidate.json');manifest=read(path/'manifest.json')
    assert manifest['mailstream_sha256']==fingerprint(Path('archives/1.1.0/source/mailstream'))
    assert manifest['core_sha256']['candidate']==fingerprint(Path('archives/1.1.0/source/core'))
    assert manifest['mailstream_unchanged'] and manifest['core_unchanged']['candidate']
    assert result['success'] and result['stage']=='complete' and all(p['success']for p in result['Phases']),label
    diag=result['diagnostics']
    assert not diag['Session']['closed'] and diag['Mail']['failed_accounts']==0
    assert diag['Client']['ActiveLines']==diag['Client']['TotalLines']==30
    assert result['Provider']['connection_rejections']==0
    assert max(result['IdleMS']+result['AfterMS'])<=300 and max(result['LoadedMS'])<=2000
    recorded=metadata['models'][label]
    assert result['targets']==recorded['targets']
    assert [{'direction':p['name'],'mbps':p[p['name']]['full_phase_mbps']}for p in result['Phases']]==recorded['phases']
    assert max(result['IdleMS']+result['AfterMS'])==recorded['max_idle_post_ms']
    assert max(result['LoadedMS'])==recorded['max_loaded_ms']
    return result
paged_path=Path('evidence/hosted-paged/paged-collection')
model('paged_collection',paged_path)
subprocess.run(['python3','archives/1.1.0/qualification/check_paged_collection.py',str(paged_path)],check=True)
assert read(paged_path/'PAGED_COLLECTION_VALIDATION.json')==metadata['paged_collection_validation']
assert read(Path('evidence/hosted-paged/collection-regressions/REGRESSION_RESULTS.json'))==metadata['paged_collection_regressions']
assert all(v['verified']for v in metadata['paged_collection_regressions'].values())
assert metadata['mailbox_layout']=={'prefix':'itom-','control_name_chars':31,'data_name_chars':55,'legacy_migration':False,'both_endpoints_required':True}
for label,folder,key in [('mobile_95_80','mobile-candidate','mobile_validation'),('mobile_fast_small_setup','mobile-fast-setup-candidate','mobile_fast_setup_validation')]:
    path=Path('evidence/hosted-mobile')/folder
    model(label,path)
    before=read(path/'MOBILE_VALIDATION.json')
    ref='mobile-reference' if folder=='mobile-candidate' else 'mobile-fast-setup-reference'
    subprocess.run(['python3','archives/1.1.0/qualification/check_mobile.py',str(path),'--reference',str(path.parent/ref)],check=True)
    assert before==read(path/'MOBILE_VALIDATION.json')==metadata[key] and before['verified']
mobile_regressions=read(Path('evidence/hosted-mobile/mobile-regressions/REGRESSION_RESULTS.json'))
assert mobile_regressions==metadata['mobile_regressions'] and all(x['verified']for x in mobile_regressions.values())
for n in (1,4,32):model('ordinary_'+str(n),Path(f'evidence/hosted-throughput/throughput-{n}'))
weakdirs=[p.parent for p in Path('evidence/hosted-safety').rglob('candidate.json')];assert len(weakdirs)==1
weak=model('weak_7mbps',weakdirs[0])
assert weak['diagnostics']['Session']['delivery_flight_bytes']==weak['diagnostics']['Session']['bulk_outstanding_bytes']==0
recovered_path=Path('evidence/hosted-recovered/recovered-client')
model('recovered_client',recovered_path)
subprocess.run(['python3','archives/1.1.0/qualification/check_recovered.py',str(recovered_path)],check=True)
repeated_path=Path('evidence/hosted-repeated/repeated-cleanup')
model('repeated_cleanup',repeated_path)
subprocess.run(['python3','archives/1.1.0/qualification/check_repeated.py',str(repeated_path),'--require-goal'],check=True)
model('retained_control',Path('evidence/hosted-field/retained-history'))
model('account_metadata_interference',Path('evidence/hosted-field/account-interference'))
upload=model('delayed_upload_fetch',Path('evidence/hosted-upload/upload-delayed'))
assert upload['Provider']['commands']['MAX_DATA_FETCH_ROWS']>8 and upload['Provider']['commands']['DELAYED_DATA_FETCH']>0
corporate=model('corporate_50_35',Path('evidence/hosted-corporate/corporate-candidate'))
assert corporate['Provider']['commands'].get('STALLED_EXISTING_CONTROL_CREATE',0)==0
home=model('home_append_service',Path('evidence/hosted-home/home-service'))
assert home['Provider']['commands'].get('REJECTED_SELECTED_STATUS',0)==0 and home['diagnostics']['Mail']['retired_segments']>0
for line in home['diagnostics']['Mail']['lines']:assert line['data_appended_messages']>0 and line['append_completion_ms']>0
for key,path in [('rc8','hosted-field/regression-evidence'),('rc9','hosted-field/feedback-evidence'),('rc10','hosted-upload/upload-regressions'),('rc11','hosted-corporate/corporate-regressions'),('rc12','hosted-home/home-regressions'),('rc13','hosted-repeated/repeated-regressions')]:
    result=read(Path('evidence')/path/'REGRESSION_RESULTS.json')
    assert result==metadata['regressions'][key] and all(v['verified']for v in result.values())
assert metadata['regressions']['rc12']['candidate_storage_reuse']['exit_status']==0
for streams in (1,16):
    path=Path('evidence/hosted-short')/f'short-{streams}'
    model('short_'+str(streams),path)
    subprocess.run(['python3','archives/1.1.0/qualification/short_runs.py',str(path),'--require-sustained'],check=True)
    assert read(path/'SHORT_RUN_VALIDATION.json')==metadata['short_run_validations'][str(streams)]
short_regressions=read(Path('evidence/hosted-short/short-regressions/REGRESSION_RESULTS.json'))
assert short_regressions==metadata['short_regressions'] and all(v['verified']for v in short_regressions.values())
assert read(Path('evidence/hosted-short/short-rc14/SHORT_RUN_VALIDATION.json'))==metadata['frozen_rc14_short_runs']
assert read(Path('evidence/hosted-short/short-rc14/manifest.json'))['mailstream_sha256']==metadata['frozen_rc14_runtime_fingerprint']

with zipfile.ZipFile(pub/'MAILSTREAM-RC17-evidence.zip') as archive:
    journal=json.loads(archive.read('hosted-repeated/repeated-regressions/REGRESSION_RESULTS.json'))
assert journal==metadata['journal_regressions'] and all(v['verified']for v in journal.values())
assert journal['rc13_journal_blocking']['expected_reference_failure']
assert journal['candidate_journal_isolation']['exit_status']==0
assert metadata['startup_cycles']==len(metadata['startup_results'])==6 and metadata['signer_unchanged']
for row in metadata['startup_results']:assert row['ActiveLines']==row['TotalLines']==row['accounts']*3
for name in ('MAILSTREAM_RC17_BUILD.json','MAILSTREAM_RC17_SOURCE_MANIFEST.json','ARTIFACT_VALIDATION.json','MAILSTREAM-RC17-evidence.zip'):
    shutil.copy2(pub/name,root/name)
shutil.copy2('android-evidence/build-report.json',root/'ANDROID_BUILD.json')
shutil.copy2('archives/1.1.0/MAILSTREAM-RC17_TESTING.md',root/'MAILSTREAM-RC17_TESTING.md')
(root/'SHA256SUMS').write_text(''.join(digest(p)+'  '+p.name+'\n'for p in sorted(root.iterdir())if p.is_file()and p.name!='SHA256SUMS'))
expected=read(pub/'EXPECTED_EXPORTED_ASSETS.json')
actual={p.name:{'sha256':digest(p),'bytes':p.stat().st_size}for p in root.iterdir()if p.is_file()}
assert actual==expected,(actual.keys()^expected.keys())
print('Verified all',len(actual),'exact-source RC17 testing assets; no field qualification claimed.')
