from pathlib import Path
import json
import os
import subprocess
import sys
import time

WORK = Path('/workspace/work/rev6')
ROOT = WORK / 'source/itom'
CACHE = Path('/workspace/work/rev3/build-cache')
GO = '/workspace/toolchains/go/bin/go'
sys.path.insert(0, str(ROOT / 'tools'))
from transport_qualification import source_fingerprint

env = os.environ.copy()
for key in list(env):
    if key.startswith(('ITOM_LOAD_', 'ITOM_FIELD_', 'ITOM_REV4_TEST_')) or key in ('ITOM_REWORK_QUALIFICATION', 'ITOM_CORE_QUALIFICATION', 'ITOM_UNSTABLE_SURVIVAL'):
        del env[key]
env.update(GOTOOLCHAIN='local', GOWORK='off', GOFLAGS='-mod=readonly', GOMAXPROCS='2',
           GOCACHE='/workspace/toolchains/go-cache', GOPATH='/workspace/toolchains/gopath')
env['PATH'] = '/workspace/toolchains/go/bin:' + env['PATH']
fingerprint = source_fingerprint()
report = {'source_sha256': fingerprint, 'classification': 'REV6_CANDIDATE',
          'mobile_field_crash_root_cause': 'UNCONFIRMED_NO_FATAL_STACK', 'complete': False, 'steps': []}

def save():
    (WORK / 'candidate-validation.json').write_text(json.dumps(report, indent=2)+'\n')

def step(name, command, cwd=ROOT, selected_env=env):
    if source_fingerprint() != fingerprint:
        raise RuntimeError('Candidate changed before '+name)
    print('RUN', name, flush=True)
    started = time.monotonic()
    with (WORK / (name+'.log')).open('w') as output:
        result = subprocess.run(command, cwd=cwd, env=selected_env, stdin=subprocess.DEVNULL, stdout=output, stderr=subprocess.STDOUT)
    passed = result.returncode == 0 and source_fingerprint() == fingerprint
    report['steps'].append({'name':name, 'exit_code':result.returncode, 'passed':passed,
                            'elapsed_seconds':round(time.monotonic()-started,3), 'log':name+'.log'})
    save()
    print('RESULT', name, 'PASS' if passed else 'FAIL', flush=True)
    if not passed:
        raise RuntimeError('Candidate gate failed: '+name)

save()

try:
    step('candidate-python', ['python','-m','unittest','discover','-s','tools','-p','test_*.py'])
    # Compile the new message renderer before lengthy transport qualification.
    jdk = next((CACHE/'jdk17').rglob('bin/javac')).parents[1]
    android_env = env.copy()
    android_env.update(JAVA_HOME=str(jdk), GRADLE_USER_HOME=str(CACHE/'gradle-home'), ANDROID_USER_HOME=str(CACHE/'android-user'))
    android_env['PATH'] = str(jdk/'bin')+':'+android_env['PATH']
    android_env['JAVA_TOOL_OPTIONS'] = android_env.get('JAVA_TOOL_OPTIONS','')+' '+(CACHE/'java-options.txt').read_text()
    step('candidate-android', ['python','tools/build.py','android','--cache',str(CACHE),'--expect-revision','transport-rework3-rev6','--accept-android-licenses'], selected_env=android_env)
    step('candidate-core-server', ['python','tools/build.py','server','--cache',str(CACHE),'--expect-revision','transport-rework3-rev6'])
    # Exercise field-derived applications before expensive builds.
    step('candidate-field-repeated', ['python','tools/field_qualification.py','--go',GO,'--repeats','3','--results',str(WORK/'field-repeated')])
    step('candidate-field-commit-delay', ['python','tools/field_qualification.py','--go',GO,'--repeats','1','--commit-delay-ms','12','--results',str(WORK/'field-commit-delay')])
    step('candidate-race-accounting', [GO,'test','-race','-json','-count=1','-timeout=180s','./flow','./wallet','./journal','./datapath','./mailbox','./servercfg','./manager','./adminapi','./adminserver'], ROOT/'core')
    step('candidate-race-recovery', [GO,'test','-race','-json','-count=1','-timeout=300s','./tunnel','-run','^TestRev6|^TestRev5(Historical180Slots|PhysicalReceipts|PeerRead|KnownPeer|PacedAccount|ProviderState|AccountDATARecovery|SplitBulk)|^TestRev4FieldSparseUIDHistory|^TestAndroidNetworkKinds|^TestRev4(OldEnvelope|LogicalOwner|OpenUses|DelayedStream|HealthyProgress|RepairMeasures)|^TestServerNotice|^TestStreamDispatchCannotBypassCarrierActivation$'], ROOT/'core')
    step('candidate-native-vet', [GO,'vet','./...'], ROOT/'native')
    step('candidate-admin-native-race', [GO,'test','-race','-tags=itomtest','-json','-count=1','-timeout=100s','./...','-run','^TestNativeAdmin'], ROOT/'native')
    # Isolate the complete final performance matrix from analysis and packaging.
    step('candidate-speed', ['python','tools/transport_qualification.py','--go',GO,'--accounts','15','--streams','both','--repeats','3','--uid-history','64','--results',str(WORK/'speed-final')])
    report['complete'] = True
    save()
    print('ALL REV6 INTERNAL GATES COMPLETE; real-provider/phone qualification remains separate', flush=True)
except Exception as error:
    report['stopped_reason'] = str(error)
    save()
    print(str(error), flush=True)
    raise SystemExit(1)
