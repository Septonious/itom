#!/usr/bin/env python3
"""Keep RC2 periodic cadence; retain only event-driven proof service on shortage."""
from pathlib import Path
import argparse,hashlib,json,os,subprocess
HERE=Path(__file__).resolve().parent

def main():
 p=argparse.ArgumentParser();p.add_argument('output',type=Path);a=p.parse_args();out=a.output.resolve()
 env=dict(os.environ,ITOM_REFILL_TRIGGER='empty',ITOM_RETAINED_GC_FLOOR_MS='1100',ITOM_LOCAL_CLEANUP_DIVISOR='16')
 subprocess.run(['python3',str(HERE.parent/'refill-trigger/prepare.py'),str(out)],env=env,check=True)
 t=out/'model/tunnel';p=t/'lane.go';s=p.read_text()
 old='l.nextRemoteCleanup = advancePeriodic(l.nextRemoteCleanup, l.remoteCleanupPeriod(), now)';assert s.count(old)==1
 p.write_text(s.replace(old,'l.nextRemoteCleanup = advancePeriodic(l.nextRemoteCleanup, runtimeRemoteCleanupInterval, now)'))
 # The existing headroom-derived period remains an advisory pressure classifier
 # for requestRefillCleanup; it does not schedule periodic work in this variant.
 subprocess.run(['gofmt','-w',str(p)],check=True)
 prov=json.loads((out/'provenance.json').read_text());prov['periodic_acceleration_production']=prov['model_production']
 prov['model_production']={str(f.relative_to(out/'model')):hashlib.sha256(f.read_bytes()).hexdigest() for f in sorted((out/'model').rglob('*.go')) if not f.name.endswith('_test.go')}
 prov['periodic_remote_cleanup_ms']=1500
 prov['headroom_hint_used_as_periodic_timer']=False
 prov['experiment']+='; restore original1500ms periodic cadence, headroom hint used only as eligibility classifier'
 prov['production_delta']=[r for r,h in prov['model_production'].items() if prov['previous_production'].get(r)!=h]
 (out/'provenance.json').write_text(json.dumps(prov,indent=2)+'\n')
if __name__=='__main__':main()
