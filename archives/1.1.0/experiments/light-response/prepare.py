#!/usr/bin/env python3
"""Isolate a bounded short-response search and fresh bulk startup epochs."""
import argparse,hashlib,json,os,shutil,subprocess
from pathlib import Path
HERE=Path(__file__).resolve().parent
ROOT=HERE.parents[3]
def replace(path,old,new):
 s=path.read_text();assert s.count(old)==1,(path,old,s.count(old));path.write_text(s.replace(old,new))
def main():
 p=argparse.ArgumentParser();p.add_argument('output',type=Path);a=p.parse_args();out=a.output.resolve()
 subprocess.run(['python3',str(HERE.parent/'delivery-model/prepare.py'),str(out)],env=dict(os.environ,ITOM_MODEL_REVISION='2'),check=True)
 shutil.copytree(out/'model',out/'previous')
 t=out/'model/tunnel'
 replace(t/'response_wait.go','type responseWaitPolicy struct{ emptyTurns int }','type responseWaitPolicy struct {\n emptyTurns int\n boundedPoll bool // keep a measured hot search out of extra IDLE command turns\n}')
 replace(t/'response_wait.go','p.emptyTurns = min(p.emptyTurns+1, 3)','if p.boundedPoll { return false }\n p.emptyTurns = min(p.emptyTurns+1, 3)')
 replace(t/'response_wait.go','func (*responseWaitPolicy) poll(direct time.Duration) time.Duration {','func (p *responseWaitPolicy) poll(direct time.Duration) time.Duration {\n if p.boundedPoll && direct > 0 { return min(20*time.Millisecond, max(2*time.Millisecond, direct/4)) }')
 replace(t/'response_wait.go','func (*responseWaitPolicy) enabled(direct time.Duration, isolated bool) bool {','func (p *responseWaitPolicy) enabled(direct time.Duration, isolated bool) bool {\n if p.boundedPoll { return isolated && direct > 0 }')
 replace(t/'io.go','var demand responseWaitPolicy','demand := responseWaitPolicy{boundedPoll: true}')
 if os.environ.get('ITOM_LIGHT_EPOCH_RESET','1')=='1':
  replace(t/'throughput_model.go','m.startup, m.plateau = true, 0','m.startup, m.plateau = true, 0\n m.fullRate = 0\n m.lowRates, m.dropReference = 0, 0')
  replace(t/'throughput_model.go','if rateUtilized && old > 0 && f.RateMbps < .7*old {','if rateUtilized && old > 0 && f.RateMbps < .7*old && (!m.startup || window == deliveryMaxWindow || float64(window) >= old*1e6/8*max(100*time.Millisecond,m.feedbackRTT).Seconds()*2+128*1024) {')
 shutil.copyfile(HERE/'search_test.go',t/'bounded_response_test.go')
 subprocess.run(['gofmt','-w',str(t/'response_wait.go'),str(t/'io.go'),str(t/'throughput_model.go'),str(t/'bounded_response_test.go')],check=True)
 prov=json.loads((out/'provenance.json').read_text())
 prov['experiment']='bounded measured polling only during request hot window; independent DATA/maintenance and IDLE capability required; fresh startup epoch evidence reset, capacity history preserved'
 prov['epoch_reset']=os.environ.get('ITOM_LIGHT_EPOCH_RESET','1')=='1'
 prov['previous_production']=prov['model_production']
 prov['model_production']={str(f.relative_to(out/'model')):hashlib.sha256(f.read_bytes()).hexdigest() for f in sorted((out/'model').rglob('*.go')) if not f.name.endswith('_test.go')}
 prov['production_delta']=[r for r,h in prov['model_production'].items() if prov['previous_production'].get(r)!=h]
 (out/'provenance.json').write_text(json.dumps(prov,indent=2)+'\n')
 print('Candidate production delta:',prov['production_delta'])
if __name__=='__main__':main()
