#!/usr/bin/env python3
"""Identical application worker and independent shaped provider for RC2 and v3.

All credentials and data are generated local fixtures. No provider credentials
or live servers are read. Each core keeps its own pinned IMAP dependency.
"""
import argparse
import hashlib
import json
import os
import platform
import re
from pathlib import Path
import shutil
import subprocess
import tempfile
import time
from rtt_report import build_report

ROOT = Path(__file__).resolve().parent


def fingerprint(core):
    files = [p for p in core.rglob('*') if p.is_file() and
             (p.suffix == '.go' and not p.name.endswith('_test.go') or p.name in ('go.mod', 'go.sum'))]
    digest = hashlib.sha256()
    for p in sorted(files):
        digest.update(p.relative_to(core).as_posix().encode())
        digest.update(p.read_bytes())
    return digest.hexdigest()


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--go', default=shutil.which('go'))
    parser.add_argument('--out', type=Path, required=True)
    parser.add_argument('--baseline', type=Path, default=ROOT.parent.parent/'1.0.0-rc2/source/core')
    parser.add_argument('--baseline-is-mailstream',action='store_true',help='Compare against exact 1.1.5 source with matching native/mailstream adapter')
    parser.add_argument('--candidate', type=Path, default=ROOT.parent/'source/core')
    parser.add_argument('--upload-fetch-delay-ms',type=int,default=0)
    parser.add_argument('--download-fetch-delay-ms',type=int,default=0)
    parser.add_argument('--large-data-fetch-delay-only',action='store_true')
    parser.add_argument('--max-expunge-mib',type=int,default=0)
    parser.add_argument('--oversized-expunge-delay-ms',type=int,default=0)
    parser.add_argument('--history-folders',type=int,default=0)
    parser.add_argument('--account-metadata-delay-ms',type=int,default=0)
    parser.add_argument('--idle-observe',default='0s')
    parser.add_argument('--prefill-control', type=int, default=0)
    parser.add_argument('--early-store-expunge', action='store_true')
    parser.add_argument('--bulk-publish-delay-ms', type=int, default=0)
    parser.add_argument('--client-upload-mbps',type=int,default=0)
    parser.add_argument('--client-download-mbps',type=int,default=0)
    parser.add_argument('--existing-create-stall-ms',type=int,default=0)
    parser.add_argument('--startup-select-refusals',type=int,default=0)
    parser.add_argument('--fresh-control-accounts',type=int,default=0)
    parser.add_argument('--missing-control-delay-ms',type=int,default=0)
    parser.add_argument('--unavailable-control-accounts',type=int,default=0)
    parser.add_argument('--minimum-ready-accounts',type=int,default=0)
    parser.add_argument('--recovered-client-bytes',default='')
    parser.add_argument('--physical-recovered-history',action='store_true')
    parser.add_argument('--download-first',action='store_true')
    parser.add_argument('--recovered-client-mib',type=int,default=0)
    parser.add_argument('--mutation-row-cost-ms',type=int,default=0)
    parser.add_argument('--append-service-ms',type=int,default=0)
    parser.add_argument('--reject-selected-status',action='store_true')
    parser.add_argument('--frontend',choices=('direct','native-tun'),default='direct')
    parser.add_argument('--mobile-latency-tails',action='store_true')
    parser.add_argument('--mobile-latency',action='store_true')
    parser.add_argument('--latency-profile',choices=('historical','home','home-icmp','mobile'),default='historical')
    parser.add_argument('--client-rate-dip',action='store_true')
    parser.add_argument('--client-mbps', type=int, default=500)
    parser.add_argument('--cancel-download-after', default='0s')
    parser.add_argument('--accounts', type=int, default=10)
    parser.add_argument('--duration', default='12s')
    parser.add_argument('--cycles', type=int, default=1)
    parser.add_argument('--streams', type=int, default=1)
    parser.add_argument('--upload-streams', type=int, default=0)
    parser.add_argument('--timed-speedtest', action='store_true')
    parser.add_argument('--duplex', action='store_true')
    parser.add_argument('--zero-latency', action='store_true')
    parser.add_argument('--worst-latency', action='store_true')
    parser.add_argument('--only', choices=('rc2', 'candidate'))
    parser.add_argument('--order', choices=('baseline-first', 'candidate-first'), default='baseline-first')
    parser.add_argument('--idle-probes', type=int, default=20)
    parser.add_argument('--post-probes', type=int, default=12)
    args = parser.parse_args()
    if args.frontend!='direct' and (args.only!='candidate' or args.recovered_client_bytes or args.recovered_client_mib):parser.error('native frontend fixture requires candidate without seeded debt')
    if args.mobile_latency and (args.zero_latency or args.worst_latency):parser.error('mobile latency is an independent variable model')
    if args.latency_profile!='historical' and (args.zero_latency or args.worst_latency or args.mobile_latency or args.mobile_latency_tails):parser.error('home/mobile profiles cannot be mixed with legacy or fixed latency modes')
    if args.client_rate_dip and (args.client_mbps!=100 or args.client_upload_mbps or args.client_download_mbps):parser.error('rate dip requires shared 100 Mbps link')
    if not 0<=args.max_expunge_mib<=64: parser.error('expunge budget must be 0..64 MiB')
    if args.recovered_client_bytes and (args.recovered_client_mib or not re.fullmatch(r'[0-9]+(?:,[0-9]+)*',args.recovered_client_bytes) or len(args.recovered_client_bytes.split(','))!=args.accounts or any(int(v)>64<<20 for v in args.recovered_client_bytes.split(','))): parser.error('invalid per-account recovered debt')
    if args.physical_recovered_history and (not args.recovered_client_bytes or args.history_folders<1):parser.error('physical history requires byte vector and history folders')
    if not 0<=args.recovered_client_mib<=64: parser.error('recovered debt must be 0..64 MiB per account')
    if not 0<=args.mutation_row_cost_ms<=20: parser.error('mutation row cost must be 0..20 ms')
    if not 0<=args.append_service_ms<=1000: parser.error("append service must be 0..1000 ms")
    if args.client_upload_mbps<0 or args.client_download_mbps<0 or (args.client_upload_mbps==0)!=(args.client_download_mbps==0) or not 0<=args.existing_create_stall_ms<=30000 or not 0<=args.startup_select_refusals<=8:
        parser.error('invalid directional rate or startup fault')
    if not re.fullmatch(r"[0-9]+(?:\.[0-9]+)?s",args.idle_observe) or float(args.idle_observe[:-1])>120:
        parser.error('idle observation must be 0..120 seconds')
    if not 0<=args.upload_fetch_delay_ms<=1000 or not 0<=args.download_fetch_delay_ms<=1000:
        parser.error('upload/download fetch delays must be 0..1000 milliseconds')
    if not 0<=args.history_folders<=1000 or not 0<=args.account_metadata_delay_ms<=1000:
        parser.error('history folders and metadata delay must be 0..1000')
    if not 0 <= args.prefill_control <= 1000 or not 0 <= args.bulk_publish_delay_ms <= 1000 or args.client_mbps <= 0 or not re.fullmatch(r"[0-9]+(?:\.[0-9]+)?s", args.cancel_download_after):
        parser.error('client rate must be positive; cancellation must be a duration in seconds')
    if not re.fullmatch(r"[0-9]+(?:\.[0-9]+)?s", args.duration) or float(args.duration[:-1]) <= 0:
        parser.error('duration must be positive seconds, e.g. 12s or 0.5s')
    if not 0 <= args.upload_streams <= 48 or args.timed_speedtest and (args.cancel_download_after=='0s' or args.duplex or not args.download_first):
        parser.error('timed speedtest requires download-first cancellation and independent valid upload stream count')
    if not 1 <= args.accounts <= 20 or not 1 <= args.streams <= 48:
        parser.error('accounts must be 1..20 and streams 1..48')
    if not 0<=args.fresh_control_accounts<=args.accounts or not 0<=args.missing_control_delay_ms<=30000 or not 0<=args.unavailable_control_accounts<args.accounts or not 0<=args.minimum_ready_accounts<=args.accounts:
        parser.error('invalid startup fixture count or delay')
    if args.unavailable_control_accounts and args.minimum_ready_accounts != args.accounts-args.unavailable_control_accounts:
        parser.error('degraded fixture requires its explicit healthy account count')
    if not 1 <= args.cycles <= 5:
        parser.error('cycles must be 1..5')
    if not 1 <= args.idle_probes <= 100 or not 1 <= args.post_probes <= 100:
        parser.error('probe counts must be 1..100')
    if args.zero_latency and args.worst_latency:
        parser.error('select only one latency model')
    if not args.go:
        parser.error('pinned Go 1.27.1 required')
    env = dict(os.environ, GOTOOLCHAIN='local', GOWORK='off', GOFLAGS='-buildvcs=false')
    env['PATH'] = str(Path(args.go).resolve().parent)+os.pathsep+env.get('PATH', '')
    version = subprocess.check_output([args.go, 'version'], env=env, text=True).strip()
    if not version.startswith('go version go1.27.1 '):
        parser.error('unexpected Go version: '+version)
    output = args.out.resolve()
    output.mkdir(parents=True, exist_ok=False)
    sources = {'rc2': args.baseline.resolve(), 'candidate': args.candidate.resolve()}
    manifest = {'upload_streams':args.upload_streams or args.streams,'timed_speedtest':args.timed_speedtest,'frontend':args.frontend,'field_effective_client_latency_ms':[30,50,180,70,35,250,80,45]if args.mobile_latency_tails else None,'mobile_imap_latency_ms':[30,55,100,70]if args.mobile_latency else None,'rc116_client_imap_latency_ms':{'home':[15,20,30,18,27,22],'mobile':[30,45,85,150,70,120,50,95],'home-icmp':[3]}.get(args.latency_profile),'observed_home_icmp_reference_ms':3 if args.latency_profile in ('home','home-icmp') else None,'rc116_latency_profile':args.latency_profile,'application_probe_timeout_seconds':30 if args.client_rate_dip else 3,'client_rate_dip':{'high_mbps':100,'low_mbps':1,'after_seconds':10,'duration_seconds':15}if args.client_rate_dip else None,'recovered_client_bytes':args.recovered_client_bytes,'physical_recovered_history':args.physical_recovered_history,'download_first':args.download_first,'fresh_control_accounts':args.fresh_control_accounts,'missing_control_delay_ms':args.missing_control_delay_ms,'unavailable_control_accounts':args.unavailable_control_accounts,'minimum_ready_accounts':args.minimum_ready_accounts,'max_expunge_mib':args.max_expunge_mib,'recovered_client_mib':args.recovered_client_mib,'mutation_row_cost_ms':args.mutation_row_cost_ms,'append_service_ms':args.append_service_ms,'reject_selected_status':args.reject_selected_status,'client_upload_mbps':args.client_upload_mbps,'client_download_mbps':args.client_download_mbps,'existing_create_stall_ms':args.existing_create_stall_ms,'startup_select_refusals':args.startup_select_refusals,'upload_fetch_delay_ms':args.upload_fetch_delay_ms,'download_fetch_delay_ms':args.download_fetch_delay_ms,'large_data_fetch_delay_only':args.large_data_fetch_delay_only,'history_folders':args.history_folders,'account_metadata_delay_ms':args.account_metadata_delay_ms,'idle_observe':args.idle_observe,'prefill_control': args.prefill_control, 'early_store_expunge': args.early_store_expunge, 'bulk_publish_delay_ms': args.bulk_publish_delay_ms, 'go': version, 'accounts': args.accounts, 'duration': args.duration, 'cycles': args.cycles,
                'oversized_expunge_delay_ms':args.oversized_expunge_delay_ms,
                'streams': args.streams, 'duplex': args.duplex, 'order': args.order,
                'cancel_download_after': args.cancel_download_after,
                'idle_probes': args.idle_probes, 'post_probes': args.post_probes, 'variable_latency': not args.zero_latency and not args.worst_latency,
                'latency_model': 'zero' if args.zero_latency else 'constant-worst' if args.worst_latency else 'independent-variable',
                'rtt_step_ms': {'client': 500, 'server': 700},
                'host': {'machine': platform.machine(), 'kernel': platform.release(), 'cpu_max': Path('/sys/fs/cgroup/cpu.max').read_text().strip() if Path('/sys/fs/cgroup/cpu.max').exists() else None},
                'core_sha256': {label: fingerprint(core) for label, core in sources.items()},
                'application_worker_sha256': hashlib.sha256((ROOT/'worker/main.go').read_bytes()).hexdigest(),
                'provider_sha256': fingerprint(ROOT/'provider'),
                'harness_sha256': fingerprint(ROOT),
                'runner_sha256': hashlib.sha256(Path(__file__).read_bytes()).hexdigest(),
                'mailstream_sha256': fingerprint(sources['candidate'].parent/'mailstream'),'baseline_mailstream_sha256':fingerprint(sources['rc2'].parent/'mailstream') if args.baseline_is_mailstream else None,
                'native_sha256':fingerprint(sources['candidate'].parent/'native'),
                'provider_shared_mbps': {'client_both_directions': args.client_mbps, 'server_both_directions': 1000},
                'field_performance_qualified': False, 'attempts': []}
    with tempfile.TemporaryDirectory(prefix='itom-speed-') as temp:
        work = Path(temp)
        with (output/'build.log').open('w') as build_log:
            subprocess.run([args.go, 'build', '-trimpath', '-o', str(work/'provider'), './provider'],
                           cwd=ROOT, env=env, stdout=build_log, stderr=subprocess.STDOUT, check=True)
            for label, core in (list(sources.items()) if args.order == 'baseline-first' else reversed(list(sources.items()))):
                if args.only and args.only != label:
                    continue
                build_dir = work/label
                build_dir.mkdir()
                shutil.copy2(ROOT/'worker/main.go', build_dir/'main.go')
                if label == 'candidate' or args.baseline_is_mailstream:
                    shutil.copy2(ROOT/'worker/adapter.go', build_dir/'adapter.go')
                    shutil.copy2(ROOT/'worker/native.go',build_dir/'native.go')
                else:
                    # Frozen RC2's public admin hook accepts only its reserved
                    # management host. Its existing itomtest entry point changes
                    # only local exit-address permission; provider dial defaults
                    # to the real verified-TLS mailbox adapter.
                    (build_dir/'adapter.go').write_text('''package main
import (
 "context"
 "itom.local/core/servercfg"
 "itom.local/core/tunnel"
)
func startPerformanceClient(ctx context.Context,p servercfg.Profile,dir,frontend string)(performanceClient,error){return tunnel.StartClientWithOptions(ctx,p,dir,nil,tunnel.ClientOptions{Diagnostics:true})}
func serveFixture(ctx context.Context,cfg servercfg.Config,dir,origin,echo string)error {
 return tunnel.ServeIntegrationFixture(ctx,cfg,dir,nil,nil)
}
''')
                module = ('module itom.local/performanceworker\n\ngo 1.27.1\n\n'
                          'require itom.local/core v0.0.0\n'
                          f'replace itom.local/mobile => {json.dumps(str(core.parent/"native"))}\n'
                          f'replace itom.local/core => {json.dumps(str(core))}\n'
                          f'replace github.com/emersion/go-imap/v2 => {json.dumps(str(core/"third_party/go-imap"))}\n'
                          f'replace itom.local/mailstream => {json.dumps(str(core.parent/"mailstream"))}\n')
                if label=='candidate' or args.baseline_is_mailstream:module+='require itom.local/mobile v0.0.0\nrequire gvisor.dev/gvisor v0.0.0-20260701204157-69c2d17aea96\n'
                (build_dir/'go.mod').write_text(module)
                subprocess.run([args.go, 'mod', 'tidy'], cwd=build_dir, env=env,
                               stdout=build_log, stderr=subprocess.STDOUT, check=True)
                tags = ['-tags=itomtest'] if label == 'rc2' else []
                subprocess.run([args.go, 'build', *tags, '-trimpath', '-o', str(work/(label+'-worker')), '.'],
                               cwd=build_dir, env=env, stdout=build_log, stderr=subprocess.STDOUT, check=True)
        try:
            for label, core in (list(sources.items()) if args.order == 'baseline-first' else reversed(list(sources.items()))):
                if args.only and args.only != label:
                    continue
                private = work/(label+'-provider')
                private.mkdir()
                provider_log = (output/(label+'-provider.log')).open('w')
                provider = subprocess.Popen([str(work/'provider'), '-dir', str(private), '-accounts', str(args.accounts),
                                             '-mobile-latency-tails='+str(args.mobile_latency_tails).lower(),'-mobile-latency='+str(args.mobile_latency).lower(),'-client-latency-profile',args.latency_profile,'-client-rate-dip='+str(args.client_rate_dip).lower(),'-history-bytes',args.recovered_client_bytes if args.physical_recovered_history else '', '-fresh-control-accounts',str(args.fresh_control_accounts),'-missing-control-delay',f'{args.missing_control_delay_ms}ms','-unavailable-control-accounts',str(args.unavailable_control_accounts),'-max-expunge-mib',str(args.max_expunge_mib),'-oversized-expunge-delay',f'{args.oversized_expunge_delay_ms}ms','-mutation-row-cost',f'{args.mutation_row_cost_ms}ms','-append-service',f'{args.append_service_ms}ms','-reject-selected-status='+str(args.reject_selected_status).lower(),'-client-upload-mbps',str(args.client_upload_mbps),'-client-download-mbps',str(args.client_download_mbps),'-existing-create-stall',f'{args.existing_create_stall_ms}ms','-startup-select-refusals',str(args.startup_select_refusals),'-upload-fetch-delay',f'{args.upload_fetch_delay_ms}ms','-download-fetch-delay',f'{args.download_fetch_delay_ms}ms','-large-data-fetch-delay-only='+str(args.large_data_fetch_delay_only).lower(),'-history-folders',str(args.history_folders),'-account-metadata-delay',f'{args.account_metadata_delay_ms}ms','-client-mbps', str(args.client_mbps), '-bulk-publish-delay', f'{args.bulk_publish_delay_ms}ms', '-prefill-control', str(args.prefill_control), '-early-store-expunge='+str(args.early_store_expunge).lower(), '-variable='+str(not args.zero_latency).lower(), '-worst='+str(args.worst_latency).lower()], env=env,
                                            stdout=provider_log, stderr=subprocess.STDOUT)
                try:
                    for _ in range(200):
                        if (private/'provider.json').exists():
                            break
                        if provider.poll() is not None:
                            raise RuntimeError('provider startup failed')
                        time.sleep(.025)
                    info = json.loads((private/'provider.json').read_text())
                    child_env = dict(env, SSL_CERT_FILE=info['root'])
                    command = [str(work/(label+'-worker')), '-provider', str(private/'provider.json'),
                               '-out', str(output/(label+'.json')), '-state', str(work/(label+'-state')),
                               '-label', label,'-frontend',args.frontend,'-recovered-client-bytes',args.recovered_client_bytes,'-download-first='+str(args.download_first).lower(),'-minimum-ready-accounts',str(args.minimum_ready_accounts),'-recovered-client-mib',str(args.recovered_client_mib), '-duration', args.duration, '-streams', str(args.streams), '-upload-streams',str(args.upload_streams),'-timed-speedtest='+str(args.timed_speedtest).lower(), '-cycles', str(args.cycles),
                               '-cancel-download-after', args.cancel_download_after,
                               '-idle-observe',args.idle_observe,'-probe-timeout','30s'if args.client_rate_dip else '3s','-idle-probes', str(args.idle_probes), '-post-probes', str(args.post_probes),
                               '-cpu', str(output/(label+'.pprof'))]
                    if args.duplex:
                        command.append('-duplex')
                    started = time.monotonic()
                    with (output/(label+'-worker.log')).open('w') as log:
                        try:
                            result = subprocess.run(command, env=child_env, stdout=log, stderr=subprocess.STDOUT, timeout=float(args.idle_observe.removesuffix('s'))+max(150,2*float(args.duration.removesuffix('s'))*args.cycles+120)+(60+2*float(args.duration.removesuffix('s')) if args.recovered_client_mib or args.recovered_client_bytes else 0))
                            status = result.returncode
                        except subprocess.TimeoutExpired:
                            status = 'timeout'
                    manifest['attempts'].append({'version': label, 'exit_status': status,
                                                 'elapsed_seconds': time.monotonic()-started})
                    print(label, 'status:', status, 'result:', output/(label+'.json'), flush=True)
                finally:
                    provider.terminate()
                    try:
                        provider.wait(timeout=5)
                    except subprocess.TimeoutExpired:
                        provider.kill(); provider.wait()
                    provider_log.close()
        finally:
            manifest['core_unchanged'] = {label: fingerprint(core) == manifest['core_sha256'][label]
                                          for label, core in sources.items()}
            manifest['native_unchanged']=fingerprint(sources['candidate'].parent/'native')==manifest['native_sha256']
            manifest['mailstream_unchanged']=fingerprint(sources['candidate'].parent/'mailstream')==manifest['mailstream_sha256']
            if args.baseline_is_mailstream:
                manifest['baseline_mailstream_unchanged']=fingerprint(sources['rc2'].parent/'mailstream')==manifest['baseline_mailstream_sha256']
            (output/'manifest.json').write_text(json.dumps(manifest, indent=2)+'\n')
    # Every synthetic performance invocation prints measured echo RTT for both
    # versions, even if a fixture failed; no silent throughput-only reports.
    build_report(output)
    for label in sources:
        p = output/(label+'.json')
        if p.exists():
            result = json.loads(p.read_text())
            print(label, 'success:', result['success'], 'stage:', result['stage'])
            for phase in result.get('Phases') or []:
                print(' ', phase['name'], {k: round(phase[k]['full_phase_mbps'], 2) for k in ('upload', 'download')})


if __name__ == '__main__':
    main()
