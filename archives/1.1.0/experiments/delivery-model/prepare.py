#!/usr/bin/env python3
"""Build an isolated model candidate beside immutable RC2 and DEV2 controls."""
import argparse,hashlib,json,shutil,subprocess,os
from pathlib import Path
HERE=Path(__file__).resolve().parent
ROOT=HERE.parents[3]
def replace(path,old,new):
    text=path.read_text(); assert text.count(old)==1,(path,old,text.count(old));path.write_text(text.replace(old,new))
def main():
    p=argparse.ArgumentParser();p.add_argument('output',type=Path);a=p.parse_args();out=a.output.resolve()
    subprocess.run(['python3',str(HERE.parent/'prepare_pair.py'),str(out)],check=True)
    shutil.copytree(out/'v110',out/'model')
    tunnel=out/'model/tunnel'
    shutil.copyfile(HERE/'model.go',tunnel/'throughput_model.go')
    shutil.copyfile(HERE/'model_test.go',tunnel/'throughput_model_test.go')
    path=tunnel/'delivery_control.go'
    replace(path,'policy                              deliveryPolicy','model                               throughputModel\n\tpolicy                              deliveryPolicy')
    replace(path,'var age, fastAge time.Duration','var age, fastAge time.Duration\n\tvar minACK time.Duration\n\tfor _, sample := range d.ages[:d.ageCount] { if sample > 0 && (minACK == 0 || sample < minACK) { minACK = sample } }')
    old='d.window = d.policy.next(now, deliveryFeedback{'
    replace(path,old,'d.window = d.model.next(now, modelFeedback{deliveryFeedback: deliveryFeedback{')
    old='FastBaseline: d.fastPathBaseline(), Pressure: now.Before(d.fastPressureUntil)})'
    replace(path,old,'FastBaseline: d.fastPathBaseline(), Pressure: now.Before(d.fastPressureUntil)}, RateAt: d.lastRateAt, RTTAt: now, RTT: minACK})\n\td.policy.mode, d.policy.capacityMbps = d.model.mode, d.model.bandwidth\n\td.policy.delivered, d.policy.lastProgress = d.model.delivered, d.model.lastProgress\n\td.policy.probes = d.model.probes')
    if os.environ.get('ITOM_MODEL_REVISION','1')=='2':
        replace(path,'wake: make(chan struct{}), window: deliveryInitialWindow','model: throughputModel{retainWindow:true}, wake: make(chan struct{}), window: deliveryInitialWindow')
        replace(path,'RTT: minACK})','RTT: minACK, SupplyLimited: d.modelSupplyLimited()})')
    # All variants get byte-identical dynamic mobile fixture code, not a
    # candidate-only workload or a reset of the shared serialization clock.
    for variant in ('rc2','v110','model'):
        t=out/variant/'tunnel'
        shutil.copyfile(HERE/'mobile_trace_test.go',t/'model_mobile_trace_test.go')
        shutil.copyfile(HERE/'transitions_test.go',t/'model_transitions_test.go')
        replace(t/'healthy_endpoints_test.go','p.setMbps(0)','p.setMbps(0)\n\tstartModelMobileTrace(t, p)')
    prov=json.loads((out/'provenance.json').read_text())
    prov['experiment']='useful-delivery model; no production source changed'
    prov['model_revision']=os.environ.get('ITOM_MODEL_REVISION','1')
    prov['model_production']={str(f.relative_to(out/'model')):hashlib.sha256(f.read_bytes()).hexdigest() for f in sorted((out/'model').rglob('*.go')) if not f.name.endswith('_test.go')}
    prov['shared_mobile_trace_sha256']=hashlib.sha256((HERE/'mobile_trace_test.go').read_bytes()).hexdigest()
    (out/'provenance.json').write_text(json.dumps(prov,indent=2)+'\n')
if __name__=='__main__':main()
