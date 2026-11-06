"""Report startup, rolling speed and cleanup occupancy for short real TLS runs."""
import argparse,json,statistics
from pathlib import Path

p=argparse.ArgumentParser();p.add_argument('directory',type=Path);p.add_argument('--require-goal',action='store_true');p.add_argument('--require-sustained',action='store_true');a=p.parse_args()
d=json.loads((a.directory/'candidate.json').read_text())
assert d['success'] and d['stage']=='complete' and all(x['success']for x in d['Phases'])
assert d['diagnostics']['Mail']['failed_accounts']==0 and not d['diagnostics']['Session']['closed']
assert d['diagnostics']['Client']['ActiveLines']==d['diagnostics']['Client']['TotalLines']==30
assert d['Provider']['connection_rejections']==0
phases=[]
for x in d['Phases']:
 v=x[x['name']];active=v['half_second_mbps'][:int(x['send_seconds']*2)]
 one=[statistics.mean(active[i:i+2])for i in range(len(active)-1)]
 ramp=next(((i+2)*.5 for i,r in enumerate(one)if r>=100),None)
 rolling=[statistics.mean(active[i:i+4])for i in range(4,len(active)-3)]
 phases.append({'direction':x['name'],'full_phase_mbps':v['full_phase_mbps'],'active_interval_mbps':statistics.mean(active),'first_two_seconds_mbps':statistics.mean(active[:4]),'ramp_rolling_one_second_100mbps_at_seconds':ramp,'minimum_rolling_two_second_mbps_after_two_seconds':min(rolling)if rolling else None,'active_half_second_mbps':active,'maximum_receiver_gap_ms':v['max_receiver_gap_after_ramp_ms'],'tail_drain_seconds':max(0,x['elapsed_seconds']-x['send_seconds'])})
peak=0
for f in a.directory.glob('candidate-server-trace.jsonl*'):
 for line in f.read_text().splitlines():
  row=json.loads(line)
  if row.get('Mail'):
   assert row['Mail']['failed_accounts']==0 and not row['Mail']['reclamation']['paused']
   peak=max(peak,sum(bool(x['housekeeping'])for x in row['Mail']['lines']))
result={'all_transfers_completed':True,'max_idle_post_ms':max(d['IdleMS']+d['AfterMS']),'max_loaded_ms':max(d['LoadedMS']),'peak_borrowed_writers':peak,'phases':phases}
result['full_phase_goal_met']=all(x['full_phase_mbps']>=100 for x in phases)
result['two_second_ramp_goal_met']=all(x['ramp_rolling_one_second_100mbps_at_seconds']is not None and x['ramp_rolling_one_second_100mbps_at_seconds']<=2 for x in phases)
result['rolling_two_second_goal_met']=all(x['minimum_rolling_two_second_mbps_after_two_seconds']is not None and x['minimum_rolling_two_second_mbps_after_two_seconds']>=100 for x in phases)
(a.directory/'SHORT_RUN_VALIDATION.json').write_text(json.dumps(result,indent=2)+'\n')
print(json.dumps(result,indent=2))
if a.require_goal or a.require_sustained:
 assert result['max_idle_post_ms']<=300 and result['max_loaded_ms']<=2000
 assert peak<=2
 assert result['full_phase_goal_met'] and result['rolling_two_second_goal_met']
if a.require_goal:assert result['two_second_ramp_goal_met']
