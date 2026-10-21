#!/usr/bin/env python3
"""Isolate quota-aware cleanup timing; never modify source/ or storage authority."""
from pathlib import Path
import argparse,shutil,json,hashlib
ROOT=Path(__file__).resolve().parents[3]
def digest(root):
 return {str(p.relative_to(root)):hashlib.sha256(p.read_bytes()).hexdigest() for p in root.rglob('*.go') if not p.name.endswith('_test.go')}
def main():
 p=argparse.ArgumentParser();p.add_argument('output',type=Path);a=p.parse_args();out=a.output.resolve();out.mkdir(parents=True,exist_ok=False)
 source=ROOT/'archives/1.1.0/source/core'
 for label in ('control','adaptive'):shutil.copytree(source,out/label,ignore=shutil.ignore_patterns('__pycache__','*.test'))
 core=out/'adaptive';lane=core/'tunnel/lane.go';s=lane.read_text()
 old='l.nextRemoteCleanup = advancePeriodic(l.nextRemoteCleanup, runtimeRemoteCleanupInterval, now)'
 assert s.count(old)==1;s=s.replace(old,'l.nextRemoteCleanup = advancePeriodic(l.nextRemoteCleanup, l.remoteCleanupPeriod(), now)')
 old='''if status.Closed {
		return l.offer()
	}'''
 assert s.count(old)==1
 s=s.replace(old,'''if status.Closed {
        // Fenced permissions retain all spent physical debt. Bring already
        // eligible fast proof work forward; never treat a timer as proof.
        if l.issuedFast[status.Key] {
            deadline := time.Now().Add(25*time.Millisecond)
            if l.nextRemoteCleanup.After(deadline) { l.nextRemoteCleanup=deadline }
        }
		return l.offer()
	}''')
 lane.write_text(s)
 (core/'tunnel/reclamation_policy.go').write_text('''package tunnel
import "time"

// Only the cadence changes. The same allocator, FIFO cleanup admission,
// physical deletion proofs, journal/wallet and directional shares authorize IO.
func (l *lane) remoteCleanupPeriod() time.Duration {
 const normal = runtimeRemoteCleanupInterval
 if !l.side || l.stripe==nil || l.data==nil || l.cipher==nil { return normal }
 u,err:=l.data.UsageByOrigin(l.account.ID,l.user.ID,l.cipher.Context().Session)
 if err!=nil || u.HistoricalLocal.Bytes==0 && u.HistoricalRemote.Bytes==0 && u.HistoricalLocal.Messages==0 && u.HistoricalRemote.Messages==0 { return normal }
 profile,err:=l.data.Limit(l.account.ID,l.user.ID);if err!=nil {return normal}
 _,remote,err:=recoveredDirectionalShares(profile,u);if err!=nil || remote.Bytes<u.HistoricalRemote.Bytes {return normal}
 available:=remote.Bytes-u.HistoricalRemote.Bytes
 if available<=fastLeaseBytes {return 150*time.Millisecond}
 bulk:=min(uint64(remoteBulkLeases*dataLeaseBytes),available-fastLeaseBytes)
 return max(150*time.Millisecond,normal*time.Duration(bulk)/time.Duration(remoteBulkLeases*dataLeaseBytes))
}
''')
 (out/'provenance.json').write_text(json.dumps(dict(control=digest(out/'control'),adaptive=digest(core),classification='experiment',physical_quota_changed=False),indent=2)+'\n')
 print('Isolated cadence experiment; production source untouched')
if __name__=='__main__':main()
