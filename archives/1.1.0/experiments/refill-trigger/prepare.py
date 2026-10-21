#!/usr/bin/env python3
"""Request existing bounded cleanup on authenticated bulk permission starvation."""
from pathlib import Path
import argparse,hashlib,json,os,re,shutil,subprocess
HERE=Path(__file__).resolve().parent

def main():
 p=argparse.ArgumentParser();p.add_argument('output',type=Path);a=p.parse_args();out=a.output.resolve()
 subprocess.run(['python3',str(HERE.parent/'duplex-reclamation/prepare.py'),str(out)],check=True)
 mode=os.environ.get('ITOM_REFILL_TRIGGER','empty');assert mode in ('empty','low')
 t=out/'model/tunnel';p=t/'lane.go';s=p.read_text()
 old='wasUsable := l.issued[m.Key]';assert s.count(old)==1
 s=s.replace(old,old+'\n wasBulk := wasUsable && !l.issuedFast[m.Key]')
 old='''		return l.publishCleanup(status)
	case wire.CleanupStatus:''';assert s.count(old)==1
 s=s.replace(old,'''  if err := l.publishCleanup(status); err != nil { return err }
  if wasBulk && status.Closed { l.requestRefillCleanup(time.Now()) }
  return nil
 case wire.CleanupStatus:''')
 s,count=re.subn(r'(\tcleanupQueuedKind\s+cleanupKind)',r'\1\n refillCleanupRequested bool // actor-owned proof-service request',s);assert count==1
 old='''			if l.cleanupQueued {
				l.startDueCleanup(time.Now())
			}''';assert s.count(old)==1
 s=s.replace(old,'''   if l.cleanupQueued || l.refillCleanupRequested {
    l.refillCleanupRequested = false
    l.startDueCleanup(time.Now())
   }''')
 s=s.replace('Only a retained FIFO request may use this wake; periodic due work','FIFO admission or an authenticated bulk-refill request may use this wake; periodic due work')
 p.write_text(s)
 threshold='bulkActive == 0' if mode=='empty' else 'bulkActive < remoteBulkLeases'
 (t/'refill_trigger.go').write_text('''package tunnel
import "time"

// A fenced lease is not physical free space. If the normal offer pass cannot
// restore usable inbound bulk permission, request the existing bounded proof
// service rather than waiting out a periodic timer. No IO/proof/quota is skipped.
// Local-pressure arbitration, queued FIFO admission and gcBusy remain unchanged.
func (l *lane) requestRefillCleanup(now time.Time) {
 if !l.side || l.stripe==nil || !l.stripe.delivery.modelEnabled || l.data==nil || l.cipher==nil { return }
 if l.remoteCleanupPeriod()>=runtimeRemoteCleanupInterval { return }
 _,bulkActive:=l.issuedGrantCounts()
 if !(THRESHOLD) {return}
 if l.control!=nil {
  held,err:=l.control.Held(l.account.ID,l.user.ID)
  // A control-starved offer needs local control proof, not extra remote work.
  if err!=nil || held.Messages>=64 {return}
 }
 if l.nextRemoteCleanup.After(now) {l.nextRemoteCleanup=now}
 l.refillCleanupRequested=true
 select {case l.cleanupWake<-struct{}{}:default:}
}
'''.replace('THRESHOLD',threshold))
 shutil.copyfile(HERE/'refill_trigger_test.go',t/'refill_trigger_test.go')
 subprocess.run(['gofmt','-w',str(p),str(t/'refill_trigger.go'),str(t/'refill_trigger_test.go')],check=True)
 prov=json.loads((out/'provenance.json').read_text());prov['duplex_reclamation_production']=prov['model_production']
 prov['model_production']={str(f.relative_to(out/'model')):hashlib.sha256(f.read_bytes()).hexdigest() for f in sorted((out/'model').rglob('*.go')) if not f.name.endswith('_test.go')}
 prov['refill_trigger']=mode;prov['experiment']+='; request existing proof service on authenticated fenced bulk permission shortfall'
 prov['production_delta']=[r for r,h in prov['model_production'].items() if prov['previous_production'].get(r)!=h]
 (out/'provenance.json').write_text(json.dumps(prov,indent=2)+'\n')
if __name__=='__main__':main()
