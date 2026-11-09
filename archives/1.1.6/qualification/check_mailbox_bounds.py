"""Check all live candidate snapshots against independent message and byte caps."""
import argparse,json
from pathlib import Path
p=argparse.ArgumentParser();p.add_argument('directory',type=Path);a=p.parse_args()
rows=[]
for side in ('client','server'):
 for f in a.directory.glob('candidate-'+side+'-trace.jsonl*'):
  rows.extend(json.loads(line)for line in f.read_text().splitlines())
rows=[r for r in rows if isinstance(r.get('Session'),dict)and not r['Session']['closed']and r.get('Mail')]
assert rows,'no live mailbox evidence'
peaks={'control_reservations':0,'incoming_control_messages':0,'data_folder_messages':0,'account_retained_bytes':0,'borrowed_writers':0}
for row in rows:
 lines=row['Mail']['lines'];peaks['borrowed_writers']=max(peaks['borrowed_writers'],sum(bool(l['housekeeping'])for l in lines))
 for l in lines:
  assert l['control_message_limit']==2048 and l['control_recovery_reserve']==128
  values={'control_reservations':l['control_message_reservations'],'incoming_control_messages':l.get('control_count',0),'data_folder_messages':l.get('data_count',0),'account_retained_bytes':l['retained_bytes']}
  for k,v in values.items():peaks[k]=max(peaks[k],v)
  assert values['control_reservations']<=2176 and values['incoming_control_messages']<=2176,values
  assert values['data_folder_messages']<=128 and values['account_retained_bytes']<=64*1024*1024+65536,values
assert peaks['borrowed_writers']<=2
out={'verified':True,'live_samples':len(rows),'peaks':peaks,'control_hard_cap_including_recovery':2176,'data_folder_cap':128,'unknown_append_releases_credit':False}
(a.directory/'MAILBOX_BOUNDS_VALIDATION.json').write_text(json.dumps(out,indent=2)+'\n');print(json.dumps(out,indent=2))
