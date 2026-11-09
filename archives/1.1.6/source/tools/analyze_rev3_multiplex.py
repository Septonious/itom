#!/usr/bin/env python3
"""Export numeric field evidence; never export identities or plaintext payloads."""
import argparse
from collections import Counter
import hashlib,json
from pathlib import Path
from analyze_field_transport import analyze,read_events,quantiles

def main():
 p=argparse.ArgumentParser(description=__doc__)
 p.add_argument('--client',type=Path,required=True);p.add_argument('--server',type=Path,required=True);p.add_argument('--output',type=Path,required=True)
 a=p.parse_args();report=analyze(a.client,a.server)
 report['classification']='REV2_FIELD_MULTIPLEX_DIAGNOSTIC';report['schema']=2
 for side,directory in [('client',a.client),('server',a.server)]:
  rows=read_events(directory);out=report['sides'][side]
  controls=[e['details']for e in rows if e['event']=='control_batch_append']
  out['control_records_per_message']=quantiles([d['records']for d in controls])
  out['single_status_messages']=sum(d.get('kinds')=={'STATUS':1}for d in controls)
  out['single_mail_receipt_messages']=sum(d.get('kinds')=={'MAIL_RECEIPT':1}for d in controls)
  out['event_codes']=dict(Counter(e['event']+'/'+e['code']for e in rows if e.get('code')not in (None,'','OK')))
  active=[e['details']['Line']['LatencyGovernor']for e in rows if e['event']=='health'and e['details']['Line']['LatencyGovernor']['DownloadBacklogBytes']]
  out['active_bulk_window_bytes']=quantiles([g['DownloadWindowBytes']for g in active]);out['active_bulk_age_ms']=quantiles([g['ObservedRTTMS']for g in active])
  out['copy_outcomes']=dict(Counter(e.get('code','')+'/'+(e.get('details')or{}).get('stage','')for e in rows if e['event']=='application_copy_end'))
 report['limitations']=[
  'Rotating tails do not cover every test. Screenshots contain no mode/time markers; user reports identify SINGLE improvement and MULTI failures.',
  'No matched account-count field experiment is present.',
  'Physical encrypted message bytes are not useful application Mbps.',
  'Cross-host clocks are not calibrated; matched one-way timings are diagnostic only.',
  'Rev2 captures independent DATA FETCH, but bulk C2S frame ingress was not sampled with a counter; Rev3 adds stream frontiers and envelope delivery metadata.',
  'Socket read RESET/CLOSED can originate from app shutdown. Server carrier timeouts at the end follow the client trace stopping; they do not establish a transport crash.'
 ]
 a.output.parent.mkdir(parents=True,exist_ok=True);a.output.write_text(json.dumps(report,indent=2)+'\n')
 print('Numeric report written; accounts:',report['account_count'],'field_qualified=false')
if __name__=='__main__':main()
