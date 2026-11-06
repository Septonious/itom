"""Recheck the raw matched-source transport evidence used for RC19 publication."""
from pathlib import Path
import json,subprocess,sys

def validate(root,repo):
 sys.path.insert(0,str(repo/'archives/1.1.0/qualification'))
 from compare import fingerprint
 def read(p):return json.loads(p.read_text())
 models={}
 folders={'ordinary_1':'hosted-throughput/throughput-1','ordinary_4':'hosted-throughput/throughput-4','ordinary_32':'hosted-throughput/throughput-32',
 'retained_control':'hosted-field/retained-history','account_metadata_interference':'hosted-field/account-interference',
 'delayed_upload_fetch':'hosted-upload/upload-delayed','corporate_50_35':'hosted-corporate/corporate-candidate','home_append_service':'hosted-home/home-service',
 'repeated_cleanup':'hosted-repeated/repeated-cleanup','recovered_client':'hosted-recovered/recovered-client','short_1':'hosted-short/short-1','short_16':'hosted-short/short-16',
 'mobile_95_80':'hosted-mobile/mobile-candidate','mobile_fast_small_setup':'hosted-mobile/mobile-fast-setup-candidate','paged_collection':'hosted-paged/paged-collection'}
 weak=list((root/'hosted-safety').rglob('candidate.json'));assert len(weak)==1
 folders['weak_7mbps']=str(weak[0].parent.relative_to(root))
 for label,folder in folders.items():
  path=root/folder;d,m=read(path/'candidate.json'),read(path/'manifest.json')
  assert m['mailstream_sha256']==fingerprint(repo/'archives/1.1.0/source/mailstream'),label
  assert m['core_sha256']['candidate']==fingerprint(repo/'archives/1.1.0/source/core'),label
  assert m['mailstream_unchanged'] and m['core_unchanged']['candidate']
  assert d['success'] and d['stage']=='complete' and all(p['success']for p in d['Phases']),label
  diag=d['diagnostics'];assert not diag['Session']['closed'] and diag['Mail']['failed_accounts']==0,label
  assert diag['Client']['ActiveLines']==diag['Client']['TotalLines']==30,label
  assert d['Provider']['connection_rejections']==0,label
  assert max(d['IdleMS']+d['AfterMS'])<=300 and max(d['LoadedMS'])<=2000,label
  lines=diag['Client']['Lines'];assert len(lines)==10 and len({l['AccountID']for l in lines})==10
  assert all(l['RateReady'] and l['UploadBytes']>0 and l['DownloadBytes']>0 for l in lines),label
  if label in ('ordinary_1','ordinary_4','account_metadata_interference','home_append_service'):assert d['targets']['full_phase_100mbps'],label
  models[label]={'folder':folder,'targets':d['targets'],'phases':[{'direction':p['name'],'mbps':p[p['name']]['full_phase_mbps']}for p in d['Phases']],
   'max_idle_post_ms':max(d['IdleMS']+d['AfterMS']),'max_loaded_ms':max(d['LoadedMS'])}
 q=repo/'archives/1.1.0/qualification'
 for script,folder,args in [('check_repeated.py','hosted-repeated/repeated-cleanup',['--require-goal']),('check_recovered.py','hosted-recovered/recovered-client',[]),
 ('short_runs.py','hosted-short/short-1',['--require-sustained']),('short_runs.py','hosted-short/short-16',['--require-sustained']),
 ('check_mobile.py','hosted-mobile/mobile-candidate',['--reference',str(root/'hosted-mobile/mobile-reference')]),
 ('check_mobile.py','hosted-mobile/mobile-fast-setup-candidate',['--reference',str(root/'hosted-mobile/mobile-fast-setup-reference')]),
 ('check_paged_collection.py','hosted-paged/paged-collection',[]),('check_rc19.py','hosted-baseline',[])]:
  subprocess.run([sys.executable,str(q/script),str(root/folder),*args],check=True,stdout=subprocess.DEVNULL)
 for n in range(1,4):
  m=read(root/f'hosted-baseline/candidate-{n}/manifest.json')
  assert m['core_sha256']['candidate']==fingerprint(repo/'archives/1.1.0/source/core') and m['mailstream_sha256']==fingerprint(repo/'archives/1.1.0/source/mailstream')
 regressions={}
 for folder in ('hosted-field/regression-evidence','hosted-field/feedback-evidence','hosted-upload/upload-regressions','hosted-corporate/corporate-regressions',
 'hosted-home/home-regressions','hosted-repeated/repeated-regressions','hosted-short/short-regressions','hosted-mobile/mobile-regressions','hosted-paged/collection-regressions'):
  d=read(root/folder/'REGRESSION_RESULTS.json');assert d and all(v['verified']for v in d.values()),folder;regressions[folder]=d
 return models,regressions,read(root/'hosted-baseline/RC19_VALIDATION.json')
