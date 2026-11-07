"""Preserve the accepted RC18 transport; report RTT rather than promise a target."""
import argparse,json,math,statistics
from pathlib import Path
p=argparse.ArgumentParser();p.add_argument('directory',type=Path);a=p.parse_args()
def read(p):return json.loads(p.read_text())
def metrics(d):
 v=d['LoadedMS']
 return {'median_ms':statistics.median(v),'p95_ms':sorted(v)[int(.95*(len(v)-1))],
         'median_adjacent_delta_ms':statistics.median(abs(x-y)for x,y in zip(v,v[1:])),
         'idle_median_ms':statistics.median(d['IdleMS']),'post_median_ms':statistics.median(d['AfterMS'])}
pairs=[]
for n in range(1,4):
 dirs=[a.directory/f'{role}-{n}'for role in ('reference','candidate')]
 old,new=[read(d/'candidate.json')for d in dirs]
 om,nm=[read(d/'manifest.json')for d in dirs]
 for k in ('client_upload_mbps','client_download_mbps','append_service_ms','duration','cycles','streams','variable_latency','upload_fetch_delay_ms','download_fetch_delay_ms'):
  assert om[k]==nm[k],k
 for d in (old,new):
  assert d['success'] and d['stage']=='complete' and len(d['Phases'])==4,d.get('error')
  assert all(x['success']for x in d['Phases'])
  assert d['diagnostics']['Client']['ActiveLines']==d['diagnostics']['Client']['TotalLines']==30
  assert d['diagnostics']['Mail']['failed_accounts']==0 and not d['diagnostics']['Session']['closed']
  assert d['Provider']['connection_rejections']==0
  assert max(d['IdleMS']+d['AfterMS'])<=300 and max(d['LoadedMS'])<=2000
 lines=new['diagnostics']['Client']['Lines']
 assert len({l['AccountID']for l in lines})==10
 assert all(l['RateReady'] and l['UpBps']>=0 and l['DownBps']>=0 for l in lines)
 assert all(l['UploadBytes']>0 and l['DownloadBytes']>0 for l in lines)
 assert new['diagnostics']['Client']['TransportStreams']==new['diagnostics']['Session']['streams']
 phases=[]
 for x,y in zip(new['Phases'],old['Phases']):
  direction=x['name'];assert direction==y['name']
  speed,prior=x[direction]['full_phase_mbps'],y[direction]['full_phase_mbps']
  phases.append({'direction':direction,'mbps':speed,'reference_mbps':prior,'ratio':speed/prior})
 pairs.append({'reference':metrics(old),'candidate':metrics(new),'phases':phases,'candidate_core':nm['core_sha256']['candidate'],'candidate_mailstream':nm['mailstream_sha256']})
 assert pairs[0]['candidate_core']==pairs[-1]['candidate_core'] and pairs[0]['candidate_mailstream']==pairs[-1]['candidate_mailstream']
ratios={direction:math.exp(statistics.mean(math.log(p['ratio'])for pair in pairs for p in pair['phases']if p['direction']==direction))for direction in ('upload','download')}
# This is a diagnostic/polling change, not a new congestion algorithm. Retain
# the original absolute gates and require the paired mean to preserve payload.
assert all(r>=.9 for r in ratios.values()),ratios
result={'verified':True,'reference':'immutable field-accepted RC18','comparisons_predeclared':3,'pairs':pairs,'geometric_payload_ratios':ratios,'latency_statistics_are_models_not_field_promises':True,'sub_100ms_verified':False}
(a.directory/'RC19_VALIDATION.json').write_text(json.dumps(result,indent=2)+'\n');print(json.dumps(result,indent=2))
