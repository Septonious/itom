import json,statistics,collections,datetime
from pathlib import Path
W=Path('/workspace/work/rev5')
def utc(s): return datetime.datetime.fromisoformat(s.replace('Z','+00:00')).timestamp()
def stats(vals):
 v=sorted(vals)
 return {'n':len(v),'median':round(statistics.median(v),6) if v else None,'p95':round(v[min(len(v)-1,int(.95*len(v)))],6) if v else None,'max':round(max(v),6) if v else None}
server=[];seen=set()
for p in (W/'field/server').glob('transport-trace.jsonl*'):
 for line in p.open():
  try:e=json.loads(line)
  except:continue
  k=(e.get('run'),e.get('seq'))
  if k in seen:continue
  seen.add(k);server.append(e)
server.sort(key=lambda e:utc(e['time']))
client=[]
for line in Path('/workspace/work/rev4-network-comparison/combined.jsonl').open():
 x=json.loads(line)
 if 'mobile2' in x['captures']:client.append(x['event'])

def join(tx,rx):
 sent=collections.defaultdict(list)
 for e in tx:
  if e['event']=='packet_tx_attempt' and e.get('sha256'):sent[(e.get('account'),e['sha256'])].append(utc(e['time']))
 result=[]
 for e in rx:
  if e['event']!='packet_rx' or not e.get('sha256'):continue
  times=sent.get((e.get('account'),e['sha256']),[]);before=[t for t in times if t<=utc(e['time'])]
  if before:result.append({'account':e.get('account'),'rx_utc':e['time'],'bytes':e.get('bytes',0),'first_attempt_to_rx_s':utc(e['time'])-min(before),'last_attempt_to_rx_s':utc(e['time'])-max(before),'prior_attempts':len(before)})
 return result
result={'server_unique_events':len(server),'client_mobile2_unique_events':len(client),'server_interval_utc':[server[0]['time'],server[-1]['time']],'directions':{},'server_lifecycle':[],'a05_health':[]}
for name,tx,rx in [('download',server,client),('upload',client,server)]:
 matches=join(tx,rx)
 result['directions'][name]={'matched_packets':len(matches),'bulk_at_least_32KiB':stats([r['last_attempt_to_rx_s'] for r in matches if r['bytes']>=32768]),'small_packets':stats([r['last_attempt_to_rx_s'] for r in matches if r['bytes']<32768]),'a05_matches':sum(r['account']=='a05' for r in matches)}
for e in server:
 if e['event']=='lifecycle' and any(x in e.get('name','') for x in ('event=imap_error','event=data_reader_repair','event=descriptor_gap_repair')):result['server_lifecycle'].append({k:e[k] for k in ('time','account','name') if k in e})
 if e['event']=='lifecycle' and e.get('account')=='a05' and 'journal_profile' in e.get('name',''):result.setdefault('a05_profile',[]).append(e['name'])
 if e['event']=='health' and e.get('account')=='a05':
  h=e['details']['Line'];result['a05_health'].append({'time_utc':e['time'],**{k:h.get(k) for k in ('DataMessages','DataBytes','IssuedGrants','AvailableGrants','Stage','DataAttempts','DataAppendCount','DataFetchMessages','Reconnects','LastError')}})
(W/'field-correlation.json').write_text(json.dumps(result,indent=2)+'\n')
print(json.dumps(result,indent=2))
