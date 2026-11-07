"""Recheck the raw matched-source transport evidence used for final 1.1.2 publication."""
from pathlib import Path
import json,subprocess,sys

def validate(root,repo):
 sys.path.insert(0,str(repo/'archives/1.1.2/qualification'))
 from compare import fingerprint
 def read(p):return json.loads(p.read_text())
 models={}
 folders={'mobile_rate_dip':'hosted-recovery/mobile-rate-dip','physical_recovery':'hosted-recovery/physical-debt-repeat','mixed_startup':'hosted-mixed/mixed-startup','degraded_accounts':'hosted-degraded/degraded-accounts','ordinary_1':'hosted-throughput/throughput-1','ordinary_4':'hosted-throughput/throughput-4','ordinary_32':'hosted-throughput/throughput-32',
 'retained_control':'hosted-field/retained-history','account_metadata_interference':'hosted-field/account-interference',
 'delayed_upload_fetch':'hosted-upload/upload-delayed','corporate_50_35':'hosted-corporate/corporate-candidate','home_append_service':'hosted-home/home-service',
 'repeated_cleanup':'hosted-repeated/repeated-cleanup','recovered_client':'hosted-recovered/recovered-client','short_1':'hosted-short/short-1','short_16':'hosted-short/short-16',
 'mobile_95_80':'hosted-mobile/mobile-candidate','mobile_fast_small_setup':'hosted-mobile/mobile-fast-setup-candidate','paged_collection':'hosted-paged/paged-collection','repeat_field':'hosted-repeat/repeat-candidate'}
 weak=list((root/'hosted-safety').rglob('candidate.json'));assert len(weak)==1
 folders['weak_7mbps']=str(weak[0].parent.relative_to(root))
 for label,folder in folders.items():
  path=root/folder;d,m=read(path/'candidate.json'),read(path/'manifest.json')
  assert m['mailstream_sha256']==fingerprint(repo/'archives/1.1.2/source/mailstream'),label
  assert m['core_sha256']['candidate']==fingerprint(repo/'archives/1.1.2/source/core'),label
  assert m['mailstream_unchanged'] and m['core_unchanged']['candidate']
  assert d['success'] and d['stage']=='complete' and all(p['success']for p in d['Phases']),label
  diag=d['diagnostics'];assert not diag['Session']['closed'] and diag['Mail']['failed_accounts']==0,label
  assert diag['Client']['TotalLines']==30 and diag['Client']['ActiveLines']==(15 if label=='degraded_accounts' else 30),label
  assert d['Provider']['connection_rejections']==0,label
  assert max(d['IdleMS']+d['AfterMS'])<=300 and (label=='mobile_rate_dip' or max(d['LoadedMS'])<=2000),label
  lines=diag['Client']['Lines'];assert len(lines)==10 and len({l['AccountID']for l in lines})==10
  assert all(l['RateReady'] and l['UploadBytes']>0 and l['DownloadBytes']>0 for l in lines if label!='degraded_accounts' or int(l['AccountID'][1:])>5),label
  if label in ('ordinary_1','ordinary_4','account_metadata_interference','home_append_service','repeat_field','mixed_startup'):assert all(p[p['name']]['full_phase_mbps']>=120 for p in d['Phases']),label
  models[label]={'folder':folder,'targets':d['targets'],'phases':[{'direction':p['name'],'mbps':p[p['name']]['full_phase_mbps']}for p in d['Phases']],
   'max_idle_post_ms':max(d['IdleMS']+d['AfterMS']),'max_loaded_ms':max(d['LoadedMS'])}
 q=repo/'archives/1.1.2/qualification'
 for script,folder,args in [('check_rate_dip.py','hosted-recovery/mobile-rate-dip',[]),('check_physical_recovery.py','hosted-recovery/physical-debt-repeat',[]),('check_mixed_startup.py','hosted-mixed/mixed-startup',[]),('check_degraded.py','hosted-degraded/degraded-accounts',[]),('check_repeated.py','hosted-repeated/repeated-cleanup',['--require-goal']),('check_recovered.py','hosted-recovered/recovered-client',[]),
 ('short_runs.py','hosted-short/short-1',['--require-sustained']),('short_runs.py','hosted-short/short-16',['--require-sustained']),
 ('check_mobile.py','hosted-mobile/mobile-candidate',['--reference',str(root/'hosted-mobile/mobile-reference')]),
 ('check_mobile.py','hosted-mobile/mobile-fast-setup-candidate',['--reference',str(root/'hosted-mobile/mobile-fast-setup-reference')]),
 ('check_paged_collection.py','hosted-paged/paged-collection',[]),('check_final_repeat.py','hosted-repeat/repeat-candidate',['--reference',str(root/'hosted-repeat/repeat-reference')])]:
  subprocess.run([sys.executable,str(q/script),str(root/folder),*args],check=True,stdout=subprocess.DEVNULL)
 for folder in folders.values():
  lines=read(root/folder/'candidate.json')['diagnostics']['Mail']['lines']
  assert lines and all(l['control_message_limit']==2048 and l['control_recovery_reserve']==128 for l in lines)
  subprocess.run([sys.executable,str(q/'check_mailbox_bounds.py'),str(root/folder)],check=True,stdout=subprocess.DEVNULL)
 audit=read(root/'hosted-audit/TRANSPORT_RESTORE.json')
 assert audit['verified'] and audit['baseline_commit']=='6ccdb6d2a408dcddadd0ea26d1cc840c6acb350a' and audit['controller_and_mailbox_bounds_identical'] and audit['native_runtime_byte_identical'] and audit['service_lifecycle_preserves_uncollected_debt']
 regressions={}
 for folder in ('hosted-recovery/recovery-regressions','hosted-recovery/lifecycle-regressions','hosted-mixed/startup-regressions','hosted-field/regression-evidence','hosted-field/feedback-evidence','hosted-upload/upload-regressions','hosted-corporate/corporate-regressions',
 'hosted-home/home-regressions','hosted-repeated/repeated-regressions','hosted-short/short-regressions','hosted-mobile/mobile-regressions','hosted-paged/collection-regressions'):
  d=read(root/folder/'REGRESSION_RESULTS.json');assert d and all(v['verified']for v in d.values()),folder;regressions[folder]=d
 lifecycle=regressions['hosted-recovery/lifecycle-regressions'];assert set(lifecycle)=={'v111','candidate'}
 assert lifecycle['v111']['exit_status']==1 and lifecycle['v111']['expected_reference_failure'] and lifecycle['candidate']['exit_status']==0 and not lifecycle['candidate']['expected_reference_failure']
 expected={version+'_'+case for version in ('v111','candidate')for case in ('quarantine','https','proactive','idle_lease','historical_page')}
 recovery=regressions['hosted-recovery/recovery-regressions'];assert set(recovery)==expected
 for name,result in recovery.items():assert result['exit_status']==(1 if name.startswith('v111_')else 0) and result['expected_reference_failure']==name.startswith('v111_')
 events=[json.loads(line)for line in (root/'hosted-recovery/recovery-races/events.jsonl').read_text().splitlines()]
 passed={e.get('Test')for e in events if e.get('Action')=='pass'}
 assert {'TestServerCleansOwnedMailAfterAbruptClientPowerLoss','TestInheritedDataDebtRequestsRecoveryWithBulkHeadroom','TestRecoveryResolvesControlQuarantineAboveFrozenStartupFence','TestHTTPSRecoversAfterLiveControlFailureAndNewStoragePressure','TestRecoveryPreferenceDoesNotReserveAnIdleMutationLease','TestHistoricalDataUsesBoundedHealthyPagesAndConservativeRetries'}<=passed
 assert any('client mutations=0' in e.get('Output','')for e in events)
 return models,regressions,read(root/'hosted-repeat/repeat-candidate/FINAL_REPEAT_VALIDATION.json')
