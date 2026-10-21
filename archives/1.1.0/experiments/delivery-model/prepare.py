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
    shutil.copyfile(HERE/'runtime.go',tunnel/'throughput_model_runtime.go')
    shutil.copyfile(HERE/'runtime_test.go',tunnel/'throughput_model_runtime_test.go')
    path=tunnel/'delivery_control.go'
    replace(path,'policy                              deliveryPolicy','modelEnabled                        bool\n\tmodel                               throughputModel\n\tpolicy                              deliveryPolicy')
    replace(path,'var age, fastAge time.Duration','var age, fastAge time.Duration\n\tvar minACK time.Duration\n\tfor _, sample := range d.ages[:d.ageCount] { if sample > 0 && (minACK == 0 || sample < minACK) { minACK = sample } }')
    text=path.read_text();start=text.index('d.window = d.policy.next(');end=text.index('\n\tif d.policy.mode ==',start);legacy=text[start:end]
    old='d.window = d.policy.next(now, deliveryFeedback{'
    replace(path,old,'d.window = d.model.next(now, modelFeedback{deliveryFeedback: deliveryFeedback{')
    old='FastBaseline: d.fastPathBaseline(), Pressure: now.Before(d.fastPressureUntil)})'
    replace(path,old,'FastBaseline: d.fastPathBaseline(), Pressure: now.Before(d.fastPressureUntil)}, RateAt: d.lastRateAt, RTTAt: now, RTT: minACK})\n\td.policy.mode, d.policy.capacityMbps = d.model.mode, d.model.bandwidth\n\td.policy.delivered, d.policy.lastProgress = d.model.delivered, d.model.lastProgress\n\td.policy.probes = d.model.probes')
    if os.environ.get('ITOM_MODEL_REVISION','1')=='2':
        replace(path,'RTT: minACK})','RTT: minACK, SupplyLimited: d.modelSupplyLimited()})')
    else:
        replace(tunnel/'throughput_model_runtime.go','const modelRetainsWindow = true','const modelRetainsWindow = false')
    text=path.read_text();start=text.index('d.window = d.model.next(');end=text.index('\n\tif d.policy.mode ==',start)
    path.write_text(text[:start]+'if d.modelEnabled {\n\t'+text[start:end]+'\n\t} else {\n\t'+legacy+'\n\t}'+text[end:])
    stripe=tunnel/'stripe.go'
    replace(stripe,'type stripeHub struct {','type stripeHub struct {\n\tmodelEnabled bool')
    replace(stripe,'g := newStripeGroup(logical, generation)','g := newStripeGroup(logical, generation)\n\th.configureModel(g)')
    replace(stripe,'g = newStripeGroup(l.logicalSession, l.generation)','g = newStripeGroup(l.logicalSession, l.generation)\n\t\th.configureModel(g)')
    pool=tunnel/'pool.go'
    replace(pool,'stripe: newStripeGroup(logicalSession, generation)','stripe: newModelStripeGroup(logicalSession, generation)')
    replace(pool,'c.stripe = newStripeGroup(logical, generation)','c.stripe = newModelStripeGroup(logical, generation)')
    replace(pool,'hubs[u.ID] = newStripeHub()','hubs[u.ID] = newModelStripeHub()')
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
