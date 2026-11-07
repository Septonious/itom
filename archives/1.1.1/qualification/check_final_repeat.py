"""Three full MULTI runs across the field's large-EXPUNGE timeout boundary."""
import argparse,json
from pathlib import Path
p=argparse.ArgumentParser();p.add_argument('directory',type=Path);p.add_argument('--reference',type=Path,required=True);a=p.parse_args()
def read(p):return json.loads(p.read_text())
d,ref=read(a.directory/'candidate.json'),read(a.reference/'candidate.json')
m,old=read(a.directory/'manifest.json'),read(a.reference/'manifest.json')
for key in ('duration','cycles','streams','max_expunge_mib','oversized_expunge_delay_ms','append_service_ms','mutation_row_cost_ms'):
 assert m[key]==old[key],key
assert (m['duration'],m['cycles'],m['streams'],m['max_expunge_mib'],m['oversized_expunge_delay_ms'])==('20s',3,16,16,9000)
assert ref['Provider']['commands'].get('DELAYED_OVERSIZED_EXPUNGE',0)>0
assert ref['Provider']['commands']['MAX_EXPUNGE_BYTES']>16<<20
assert d['success'] and d['stage']=='complete' and len(d['Phases'])==6,d.get('error')
assert all(p['success']for p in d['Phases'])
assert d['diagnostics']['Client']['ActiveLines']==d['diagnostics']['Client']['TotalLines']==30
assert d['diagnostics']['Mail']['failed_accounts']==0 and not d['diagnostics']['Session']['closed']
assert not d['diagnostics']['Mail']['reclamation']['paused']
assert d['Provider']['connection_rejections']==0
commands=d['Provider']['commands']
assert commands.get('DELAYED_OVERSIZED_EXPUNGE',0)==commands.get('REJECTED_OVERSIZED_EXPUNGE',0)==0
assert 4<<20 < commands['MAX_ACCEPTED_EXPUNGE_BYTES']<=16<<20
assert d['diagnostics']['Mail']['retired_segments']>0
assert max(d['IdleMS']+d['AfterMS'])<=300 and max(d['LoadedMS'])<=2000
phases={direction:[p[direction]['full_phase_mbps']for p in d['Phases']if p['name']==direction]for direction in ('upload','download')}
assert all(len(v)==3 and min(v)>=100 and min(v[1:])>=.9*v[0] for v in phases.values()),phases
rows=[]
for file in a.directory.glob('candidate-server-trace.jsonl*'):rows.extend(json.loads(line)for line in file.read_text().splitlines())
rows=[r for r in rows if isinstance(r.get('Session'),dict) and not r['Session']['closed']]
assert rows and all(not r['Mail']['reclamation']['paused'] and r['Mail']['failed_accounts']==0 for r in rows)
assert max(sum(bool(l['housekeeping'])for l in r['Mail']['lines'])for r in rows)<=2
assert max(r['Mail']['retired_segments']for r in rows)>0
result={'verified':True,'model':'three 20-second 16-stream MULTI runs, 60-ms APPEND service, 9-second refusal above 16 MiB EXPUNGE','phase_mbps':phases,'reference_success':ref['success'],'reference_oversized_delays':ref['Provider']['commands']['DELAYED_OVERSIZED_EXPUNGE'],'max_candidate_expunge_bytes':commands['MAX_ACCEPTED_EXPUNGE_BYTES'],'max_idle_post_ms':max(d['IdleMS']+d['AfterMS']),'max_loaded_ms':max(d['LoadedMS']),'field_qualified':False}
(a.directory/'FINAL_REPEAT_VALIDATION.json').write_text(json.dumps(result,indent=2)+'\n');print(json.dumps(result,indent=2))
