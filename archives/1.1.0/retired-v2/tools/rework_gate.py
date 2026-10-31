#!/usr/bin/env python3
"""Run fresh local or synthetic performance checks and retain honest JSON evidence.

Local checks are not Android/device/provider qualification. Performance failures
are preserved; an empty test selection or a skipped required test cannot pass.
"""
import argparse
import hashlib
import json
import os
from pathlib import Path
import platform
import subprocess
import sys
import time

ROOT = Path(__file__).resolve().parents[1]


def runtime_fingerprint():
    digest = hashlib.sha256()
    files = []
    for root in (ROOT/'core', ROOT/'native'):
        files.extend(p for p in root.rglob('*') if p.is_file() and p.suffix in ('.go', '.mod', '.sum'))
    for file in sorted(files):
        digest.update(str(file.relative_to(ROOT)).encode()+b'\0'+file.read_bytes()+b'\0')
    return digest.hexdigest()


def summarize_events(path, required=()):
    events = [json.loads(line) for line in path.read_text().splitlines() if line.startswith('{')]
    passed = sorted({e['Test'] for e in events if e.get('Test') and e.get('Action') == 'pass'})
    failed = sorted({e['Test'] for e in events if e.get('Test') and e.get('Action') == 'fail'})
    skipped = sorted({e['Test'] for e in events if e.get('Test') and e.get('Action') == 'skip'})
    missing = sorted(set(required) - set(passed) - set(failed))
    return dict(passed=passed, failed=failed, skipped=skipped, missing_required=missing)


def run_step(name, command, cwd, env, out, required=(), tests=False):
    log = out / (name + '.log')
    begin = time.monotonic()
    print('RUN', name, flush=True)
    with log.open('w') as stream:
        result = subprocess.run(command, cwd=cwd, env=env, stdout=stream, stderr=subprocess.STDOUT)
    item = dict(name=name, command=command, cwd=str(cwd.relative_to(ROOT)), exit_code=result.returncode,
                elapsed_seconds=round(time.monotonic()-begin, 3), log=log.name)
    if tests:
        item.update(summarize_events(log, required))
        item['ok'] = result.returncode == 0 and bool(item['passed']) and not item['missing_required'] and not item['failed']
    else:
        item['ok'] = result.returncode == 0
    print('RESULT', name, 'PASS' if item['ok'] else 'FAIL', flush=True)
    return item


def main():
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument('--go', default='go')
    p.add_argument('--suite', choices=('local', 'performance'), default='local')
    p.add_argument('--results', type=Path, default=ROOT/'reports'/'rework')
    p.add_argument('--provider-mbps', type=int, default=600)
    p.add_argument('--repeats', type=int, default=3)
    p.add_argument('--http-rtt-ms', type=int, default=65)
    a = p.parse_args()
    if not 1 <= a.repeats <= 3 or not 5 <= a.provider_mbps <= 2000 or not 0 <= a.http_rtt_ms <= 1000:
        p.error('repeats=1..3, provider-mbps=5..2000 required')
    if a.suite=='performance':
        return subprocess.call([sys.executable,str(ROOT/'tools/transport_qualification.py'),'--go',a.go,'--results',str(a.results),'--provider-mbps',str(a.provider_mbps),'--repeats',str(a.repeats)])
    go = str(Path(a.go).resolve()) if '/' in a.go or '\\' in a.go else a.go
    env = os.environ.copy()
    env.update(GOTOOLCHAIN='local', GOWORK='off', GOFLAGS='-mod=readonly', ITOM_UNSTABLE_SURVIVAL='1', GOMAXPROCS='4')
    version = subprocess.run([go, 'version'], env=env, text=True, capture_output=True)
    if version.returncode or 'go1.27.1 ' not in version.stdout:
        p.error('verified Go 1.27.1 is required: '+version.stdout.strip())
    # New directory per execution; previous results cannot masquerade as this run.
    out = a.results.resolve()/time.strftime('%Y%m%dT%H%M%S', time.gmtime())
    out.mkdir(parents=True, exist_ok=False)
    report = dict(suite=a.suite, source_info=json.loads((ROOT/'source-info.json').read_text()),
                  go=version.stdout.strip(), platform=platform.platform(), gomaxprocs=4, runtime_source_sha256=runtime_fingerprint(),
                  real_provider='NOT_RUN', android_device='NOT_RUN',
                  scenario=dict(acceptance_mbps=50, max_rtt_ms_strict=1000, provider_mbps=a.provider_mbps, http_rtt_ms=a.http_rtt_ms, repeats=a.repeats, flows_per_direction=24, measured_seconds=30, warmup_seconds=10), steps=[])
    def step(*args, **kwargs):
        report['steps'].append(run_step(*args, env=env, out=out, **kwargs))
        (out/'results.json').write_text(json.dumps(report, indent=2)+'\n')
    if a.suite == 'local':
        step('source', [sys.executable, 'tools/check_sources.py'], ROOT)
        step('core', [go, 'test', '-json', '-p=2', '-count=1', '-timeout=12m', './...'], ROOT/'core', tests=True,
             required=('TestLatencyFirstPolicySpeedtestAndPostLoadRecovery', 'TestSharedAccountAdmissionUnderSpeedtestLoad', 'TestApplicationByteClockExcludesWarmupAndLateBytes'))
        step('vet-core', [go, 'vet', './...'], ROOT/'core')
        step('native', [go, 'test', '-json', '-p=2', '-count=1', '-timeout=8m', './...'], ROOT/'native', tests=True)
        step('native-imap-bridge', [go, 'test', '-json', '-tags=itomtest', '-count=1', '-timeout=100s', './...', '-run', '^TestNativeIMAPTCPBridgeSingleMultiConsecutive$'], ROOT/'native', tests=True, required=('TestNativeIMAPTCPBridgeSingleMultiConsecutive',))
        step('vet-native', [go, 'vet', './...'], ROOT/'native')
        step('imap-fork', [go, 'test', '-json', '-p=2', '-count=1', '-timeout=8m', './...'], ROOT/'core/third_party/go-imap', tests=True)
        step('race-durable', [go, 'test', '-json', '-race', '-p=2', '-count=1', '-timeout=8m', './datapath', './flow', './journal', './mailbox'], ROOT/'core', tests=True)
        step('race-transport', [go, 'test', '-json', '-race', '-count=3', '-timeout=8m', './tunnel', '-run', '^Test(Preparation|ReceiveBatch|LatencyFirst|Policy|CapacityBroadcast|SharedAccountAdmission|ControlAdvisory|ApplicationByteClock|Delivery|AccountWork|RealIMAPProvider|DATAWriterCapacity|Field68InvalidDataProof|Field77)'], ROOT/'core', tests=True,
             required=('TestPreparationSharesPhysicalCapacityAndCancellation', 'TestCapacityBroadcastWakesAllSpeedtestWaiters', 'TestSharedAccountAdmissionUnderSpeedtestLoad'))
        binary = out/'ITOM_REWORK1_linux_amd64'
        step('build-linux', [go, 'build', '-trimpath', '-buildvcs=false', '-o', str(binary), './cmd/itom'], ROOT/'core')
        if binary.is_file():
            report['binary_sha256'] = hashlib.sha256(binary.read_bytes()).hexdigest()
            step('binary-identity', [str(binary), 'version'], ROOT)
    report['runtime_source_unchanged'] = report['runtime_source_sha256'] == runtime_fingerprint()
    report['local_checks_passed'] = all(s['ok'] for s in report['steps']) and report['runtime_source_unchanged']
    report['production_qualified'] = False
    (out/'results.json').write_text(json.dumps(report, indent=2)+'\n')
    print('REPORT', out/'results.json', flush=True)
    return 0 if report['local_checks_passed'] else 1


if __name__ == '__main__':
    raise SystemExit(main())
