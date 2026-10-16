from pathlib import Path
import datetime
import hashlib
import json
import re
import sys

WORK = Path('/workspace/work/rev7')
ROOT = WORK / 'source/itom'
sys.path.insert(0, str(ROOT/'tools'))
from transport_qualification import source_fingerprint

pipeline = json.loads((WORK/'candidate-validation.json').read_text())
required_steps = {'candidate-python','candidate-core-server',
                  'candidate-race-accounting','candidate-race-recovery','candidate-native-vet',
                  'candidate-android','candidate-speed','candidate-speed-retained','candidate-admin-native-race'}
assert pipeline['complete'] and {s['name'] for s in pipeline['steps']} == required_steps
assert all(s['passed'] and s['exit_code'] == 0 for s in pipeline['steps'])
assert pipeline['source_sha256'] == source_fingerprint()
identity = pipeline['source_sha256']

def latest_report(folder):
    paths = sorted((WORK/folder).glob('*/results.json'))
    reports = [(p,json.loads(p.read_text())) for p in paths]
    matching = [(p,d) for p,d in reports if d.get('source_sha256') == identity]
    assert matching, folder
    return matching[-1]

core = json.loads((ROOT/'dist/core-report.json').read_text())
android = json.loads((ROOT/'dist/build-report.json').read_text())
assert core['status'] == android['status'] == 'passed'
assert core['tests']['failed'] == core['tests']['skipped'] == 0
for summary in (android['domain_tests'],android['app_unit_tests']):
    assert summary['tests'] > 0 and summary['available'] and not summary['report_errors']
    assert summary['failures'] == summary['errors'] == summary['skipped'] == 0
assert android['app_unit_tests']['tests'] == 7

def race_summary(name):
    passed=[];failed=[];skipped=[]
    for line in (WORK/(name+'.log')).read_text().splitlines():
        if not line.startswith('{'):continue
        event=json.loads(line)
        if event.get('Action')=='pass' and event.get('Test'):passed.append(event['Test'])
        if event.get('Action')=='fail':failed.append(event.get('Test',event.get('Package')))
        if event.get('Action')=='skip':skipped.append(event.get('Test'))
    assert passed and not failed and not skipped
    return {'passed_test_events':len(passed),'failed':0,'skipped':0}

races={name:race_summary(name) for name in ('candidate-race-accounting','candidate-race-recovery','candidate-admin-native-race')}
restart_metrics=[]
for line in (WORK/'candidate-admin-native-race.log').read_text().splitlines():
    if not line.startswith('{'):continue
    output=json.loads(line).get('Output','')
    match=re.search(r'admin_server_restart_recovery_ms=(\d+)',output)
    if match:restart_metrics.append(int(match.group(1)))
assert len(restart_metrics)==1 and restart_metrics[0]<60000


# The user requested fewer tests. The full core gate already executes every
# field-derived case; count that single mandatory pass without extra repeats.
from field_qualification import REQUIRED
core_events=ROOT/'dist/core-test-events.jsonl'
field_passed=set()
for line in core_events.read_text().splitlines():
    if not line.startswith('{'): continue
    event=json.loads(line)
    if event.get('Action')=='pass' and event.get('Test') in REQUIRED:
        field_passed.add(event['Test'])
assert field_passed==set(REQUIRED) and len(REQUIRED)==48
field={'source':'mandatory complete core gate','repetitions':1,'required_cases':list(REQUIRED),
       'cases_passed':48,'all_passed':True,'extra_repetitions_requested':False,
       'core_events_sha256':hashlib.sha256(core_events.read_bytes()).hexdigest()}

def speed_summary(folder):
    speed_path,speed = latest_report(folder)
    assert speed['all_lab_cases_passed'] and len(speed['cases']) == 2
    assert speed['scenario']['uid_history'] == 64 and speed['scenario']['accounts'] == 15
    assert speed['scenario']['stability_only'] is True
    assert speed['criteria']['qualification_mode'] == 'stability' and speed['criteria']['throughput_floor_enforced'] is False
    assert speed['criteria']['useful_upload_mbps_min'] is speed['criteria']['useful_download_mbps_min'] is None
    assert speed['criteria']['positive_duplex_every_second_required'] is True
    cases = []
    for case in speed['cases']:
        assert case['ok'] and case['exit_code'] == 0 and case['source_before'] == case['source_after'] == identity
        assert not case['summary']['failed'] and not case['summary']['missing_required']
        assert hashlib.sha256((speed_path.parent/case['log']).read_bytes()).hexdigest()==case['log_sha256']
        selected = {}
        post_stress=[]
        fresh_verified=False
        stability_verified=False
        identity_verified=False
        for line in (speed_path.parent/case['log']).read_text().splitlines():
            if not line.startswith('{'): continue
            text = json.loads(line).get('Output','')
            if 'supervised_post_stress ' in text:
                fields=dict(re.findall(r'([a-z_]+)=([^\s]+)',text))
                assert fields['same_socket']=='true' and int(fields['rtt_ms'])<1000
                post_stress.append(int(fields['index']))
            if 'supervised_post_stress_fresh ' in text:
                fresh_verified='bytes=262144 integrity_verified=true original_socket_rechecked=true' in text
            if 'supervised_stability ' in text:
                stability_verified='windows=60 positive_duplex_every_window=true throughput_floor_enforced=false' in text
            if 'supervised_identity ' in text:
                fields=dict(re.findall(r'([a-z_]+)=([^\s]+)',text))
                identity_verified=fields['preserved']=='true' and int(fields['application_sockets'])==int(fields['target_tcp_dials'])==2*case['streams_per_direction']+1
            for marker in ('supervised_result ','supervised_tail ','supervised_tail10 ','supervised_acceptance '):
                if marker in text:
                    allowed = {'upload_mbps','download_mbps','max_ms','rtt_samples','timeouts','missed','overlap',
                               'minimum_verified_mbps','max_rtt_ns','accepted','duration_ms'}
                    values = {k:v for k,v in re.findall(r'([a-z_]+)=([^\s]+)',text) if k in allowed}
                    selected[marker.strip()] = values
        assert selected['supervised_acceptance']['accepted'] == 'true'
        assert post_stress == [1,2,3,4,5,6] and fresh_verified and stability_verified and identity_verified
        assert float(selected['supervised_acceptance']['minimum_verified_mbps'])>0
        assert 0<int(selected['supervised_acceptance']['max_rtt_ns'])<1100000000
        assert selected['supervised_result']['timeouts']==selected['supervised_result']['missed']=='0'
        assert selected['supervised_result']['rtt_samples']==selected['supervised_result']['overlap']=='30'
        cases.append({'name':case['name'],'streams_per_direction':case['streams_per_direction'],
                      'physical_uid_history':64,'passed':True,'positive_duplex_every_second':True,'post_load_original_socket_exchanges':6,'post_load_fresh_verified_bytes':262144,'original_socket_rechecked_after_fresh_transfer':True,'carrier_epochs_preserved':True,'log_sha256':case['log_sha256'],**selected})
    
    return speed,cases

speed,cases = speed_summary('speed-final')
retained,retained_cases = speed_summary('speed-retained')
assert retained['scenario']['retained_state'] is True and speed['scenario']['retained_state'] is False

report = {
    'schema':1,'classification':'REV7_LAB_QUALIFIED_PRERELEASE','all_internal_gates_passed':True,
    'prerelease_eligible':True,'stable_release_ready':False,'internally_stability_qualified':True,'throughput_floor_waived_by_user':True,'field_limitations':['Whole-app mobile fatal stack unavailable; cause unconfirmed.','No physical phone or live Rambler qualification.','Initial failed Speedtest and remote EOF trigger are outside paired trace retention.'],
    'generated_utc':datetime.datetime.now(datetime.timezone.utc).isoformat(),
    'qualification_source_sha256':identity,'first_party_core_runtime_sha256':core['core_runtime_sha256'],
    'go':core['go_version_output'],'core':core['tests'],'imap_fork_tests':core['imap_fork_tests'],
    'vet':core['vet'],'built_targets':core['built_targets'],'field_suite':field,
    'race_checks':races,
    'android_domain_tests':{k:android['domain_tests'][k] for k in ('tests','failures','errors','skipped')},
    'android_callback_adapter_tests':{k:android['app_unit_tests'][k] for k in ('tests','failures','errors','skipped')},
    'android_lint':{k:android['lint'][k] for k in ('errors','warnings')},
    'android_native_abis':android['native']['targets'],
    'speed_criteria':speed['criteria'],'speed_scenario':{k:v for k,v in speed['scenario'].items() if k not in ('go','results')},
    'speed_cases':cases,'retained_speed_cases':retained_cases,'retained_speed_scenario':{k:v for k,v in retained['scenario'].items() if k not in ('go','results')},'real_provider_tested':False,'physical_android_device_tested':False,
    'mobile_crash_resolved':False,
    'admin_features':{'server_authoritative_role':True,'additional_private_key_challenge':True,'durable_independent_watchdog':True,'loss_timeout_seconds':60,'healthy_trial_seconds':60,'irreversible_operations_cli_only':True,'native_imap_reconnect_race_test':True,'real_systemd_host_tested':False,'actual_server_supervisor_restart_tested':True,'server_restart_recovery_ms':restart_metrics[0]},
    'first_candidate_failures_preserved':True,
    'reduced_testing_by_user_request':True,
    'supplementary_repetition_failure':{'test':'TestRev7ControlAndReceiveRepairBypassQueuedOptionalLogins','cause':'Minimum DATA receive/writer service was queued as optional during correlated failure.','resolution':'Sole DATA receiver and first usable DATA writer now use critical repair priority; extra writers remain optional.','targeted_fixed_repetitions':3,'final_complete_core_pass_required':True},
    'qualification_isolation':'No concurrent log analysis, compression or other builds during final speed matrix; host CPU quota two cores.',
    'new_release_published_at_qualification_time':False,'replacement_assets_published_at_qualification_time':False,
    'pipeline_steps':[{k:s[k] for k in ('name','exit_code','passed','elapsed_seconds')} for s in pipeline['steps']],
}
target=ROOT/'docs/rev7/evidence/rev7_validation.json'
target.parent.mkdir(parents=True,exist_ok=True)
target.write_text(json.dumps(report,indent=2)+'\n')
print('Prepared aggregate validation evidence:',target.name)
