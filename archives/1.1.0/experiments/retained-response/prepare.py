#!/usr/bin/env python3
"""Short-response candidate plus quota-headroom-scaled remote cleanup timing."""
from pathlib import Path
import argparse,hashlib,json,os,shutil,subprocess
HERE=Path(__file__).resolve().parent
def main():
 p=argparse.ArgumentParser();p.add_argument('output',type=Path);a=p.parse_args();out=a.output.resolve()
 subprocess.run(['python3',str(HERE.parent/'light-response/prepare.py'),str(out)],check=True)
 for variant in ('rc2','model','previous','v110'):
  shutil.copyfile(HERE/'directional_test.go',out/variant/'tunnel/directional_capacity_test.go')
 t=out/'model/tunnel';lane=t/'lane.go';s=lane.read_text()
 old='l.nextRemoteCleanup = advancePeriodic(l.nextRemoteCleanup, runtimeRemoteCleanupInterval, now)';assert s.count(old)==1
 lane.write_text(s.replace(old,'l.nextRemoteCleanup = advancePeriodic(l.nextRemoteCleanup, l.remoteCleanupPeriod(), now)'))
 floor=int(os.environ.get('ITOM_RETAINED_GC_FLOOR_MS','900'));assert 900<=floor<=1500
 policy='''package tunnel
import "time"

// Historical debt is not released or discounted here. Existing authenticated
// observation/deletion proofs and FIFO maintenance admission still govern IO.
// Scale only the next eligible remote-cleanup turn when the recovered inbound
// budget is smaller than a fresh profile. Fresh/unknown profiles keep RC2 cadence.
func (l *lane) remoteCleanupPeriod() time.Duration {
 const normal = runtimeRemoteCleanupInterval
 const floor = FLOOR_MS*time.Millisecond
 if !l.side || l.stripe==nil || l.data==nil || l.cipher==nil { return normal }
 u,err:=l.data.UsageByOrigin(l.account.ID,l.user.ID,l.cipher.Context().Session)
 if err!=nil || u.HistoricalLocal.Bytes==0 && u.HistoricalRemote.Bytes==0 && u.HistoricalLocal.Messages==0 && u.HistoricalRemote.Messages==0 { return normal }
 profile,err:=l.data.Limit(l.account.ID,l.user.ID);if err!=nil {return normal}
 _,remote,err:=recoveredDirectionalShares(profile,u);if err!=nil || remote.Bytes<u.HistoricalRemote.Bytes {return normal}
 available:=remote.Bytes-u.HistoricalRemote.Bytes
 if available<=fastLeaseBytes {return floor}
 bulk:=min(uint64(remoteBulkLeases*dataLeaseBytes),available-fastLeaseBytes)
 return max(floor,normal*time.Duration(bulk)/time.Duration(remoteBulkLeases*dataLeaseBytes))
}
'''.replace('FLOOR_MS',str(floor))
 (t/'reclamation_policy.go').write_text(policy)
 shutil.copyfile(HERE/'quota_test.go',t/'reclamation_quota_test.go')
 (t/'reclamation_policy_test.go').write_text('''package tunnel
import "testing"
func TestRetainedCadenceMissingAuthorityKeepsOriginalTiming(t *testing.T) {
 for _, l:=range []*lane{{},{side:true}} {
  if l.remoteCleanupPeriod()!=runtimeRemoteCleanupInterval {t.Fatal("unknown capacity accelerated cleanup")}
 }
}
''')
 subprocess.run(['gofmt','-w',str(lane),str(t/'reclamation_policy.go'),str(t/'reclamation_policy_test.go')],check=True)
 prov=json.loads((out/'provenance.json').read_text());prov['light_response_production']=prov['model_production']
 prov['model_production']={str(f.relative_to(out/'model')):hashlib.sha256(f.read_bytes()).hexdigest() for f in sorted((out/'model').rglob('*.go')) if not f.name.endswith('_test.go')}
 prov['experiment']+='; quota-headroom-scaled remote cleanup scheduling only'
 prov['remote_cleanup_floor_ms']=floor;prov['physical_quota_changed']=False
 prov['production_delta']=[r for r,h in prov['model_production'].items() if prov['previous_production'].get(r)!=h]
 (out/'provenance.json').write_text(json.dumps(prov,indent=2)+'\n')
 print('Candidate production delta:',prov['production_delta'])
if __name__=='__main__':main()
