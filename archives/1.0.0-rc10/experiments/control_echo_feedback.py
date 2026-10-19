#!/usr/bin/env python3
"""LAB ONLY. Bootstrap short-path queue headroom from real unloaded control RPCs.
NOOP is not this multi-command path. Loaded echoes never train this floor.
"""
from pathlib import Path
import sys,re
root=Path(__file__).resolve().parents[3]
core=Path(sys.argv[1]) if len(sys.argv)>1 else root/'archives/1.0.0-rc10/source/core'
p=core/'tunnel/delivery_control.go';s=p.read_text()
a='type deliveryController struct {';assert s.count(a)==1
s=s.replace(a,a+'\n\tunloadedControlEchoMS int64\n\tunloadedControlEchoSamples int')
a='\tif baseline == 0 {\n\t\t//';start=s.index('func (d *deliveryController) fastAgeBudget()');i=s.find(a,start)
assert i!=-1
s=s[:i]+'''\tif d.unloadedControlEchoSamples >= 2 {
\t\t// Two real empty-path authenticated round trips can bootstrap the same
\t\t//100ms queue headroom until unloaded short DATA/ACK observations exist.
\t\treturn time.Duration(d.unloadedControlEchoMS)*time.Millisecond+100*time.Millisecond
\t}
'''+s[i:]
s+='''
func(g *stripeGroup) observeUnloadedControlEcho(elapsed time.Duration){
 if g==nil||elapsed<=0||elapsed>2*time.Second{return}
 g.mu.Lock();defer g.mu.Unlock();d:=&g.delivery;d.init()
 if g.active.Load()!=0||d.bulk!=0||d.fast!=0{return}
 sample:=max(int64(1),elapsed.Milliseconds())
 if d.unloadedControlEchoMS==0||sample<d.unloadedControlEchoMS{d.unloadedControlEchoMS=sample}
 d.unloadedControlEchoSamples=min(16,d.unloadedControlEchoSamples+1)
}
'''
a='type latencyGovernorSnapshot struct {';assert s.count(a)==1
s=s.replace(a,a+'\n\tUnloadedControlEchoMS int64\n\tUnloadedControlEchoSamples int')
a='FastTargetMS: d.fastAgeBudget().Milliseconds(),';assert s.count(a)==1
s=s.replace(a,a+' UnloadedControlEchoMS:d.unloadedControlEchoMS, UnloadedControlEchoSamples:d.unloadedControlEchoSamples,')
p.write_text(s)
p=core/'tunnel/lane.go';s=p.read_text()
a='l.idleRTTAt.Store(now.UnixMilli())';assert s.count(a)==1
s=s.replace(a,a+'\n\t\t\tl.stripe.observeUnloadedControlEcho(now.Sub(l.lastEcho))');p.write_text(s)
(core/'tunnel/rc10_control_echo_feedback_test.go').write_text('''package tunnel
import("testing";"time";"itom.local/core/wire")
func TestRC10ShortHeadroomNeedsTwoRealEmptyPathEchoes(t *testing.T){
 g:=newStripeGroup(wire.ID{1},1);d:=&g.delivery
 g.observeUnloadedControlEcho(260*time.Millisecond)
 if d.fastAgeBudget()!=deliveryFastMaxAge{t.Fatal("one startup sample proved queue baseline")}
 g.observeUnloadedControlEcho(340*time.Millisecond)
 if d.fastAgeBudget()!=360*time.Millisecond{t.Fatal("real unloaded RPC did not initialize headroom",d.fastAgeBudget())}
 g.active.Store(1);g.observeUnloadedControlEcho(time.Millisecond);g.active.Store(0)
 d.bulk=32<<10;g.observeUnloadedControlEcho(time.Millisecond)
 if d.unloadedControlEchoSamples!=2||d.unloadedControlEchoMS!=260||d.bulk!=32<<10||d.delivered!=0{t.Fatal("loaded echo changed baseline or charged debt")}
}
func TestRC10ShortHeadroomPreservesActualHighPropagation(t *testing.T){
 g:=newStripeGroup(wire.ID{1},1)
 g.observeUnloadedControlEcho(1200*time.Millisecond);g.observeUnloadedControlEcho(time.Second)
 if g.delivery.fastAgeBudget()!=1100*time.Millisecond{t.Fatal("fixed guard collapsed a measured high propagation path")}
}
''')
