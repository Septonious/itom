#!/usr/bin/env python3
"""LAB ONLY: adapt existing maintenance admission to short-DATA demand.
Already-started operations finish. Urgent control debt restores RC2 admission;
no new gate, timer, quota, ACK, cleanup proof or data writer policy exists.
"""
from pathlib import Path
import argparse
p=argparse.ArgumentParser();p.add_argument('limit',type=int,choices=[1,2]);args=p.parse_args()
r=Path(__file__).resolve().parents[3];d=r/'archives/1.0.0-rc10/source/core/tunnel';p=d/'cleanup_service.go';s=p.read_text()
a='\tif len(g.cleanupActive) >= limit {';assert s.count(a)==1
s=s.replace(a,'''\t// A short exchange needs the same shared access link as BODY proofs.
\t// Retain maintenance progress, and restore full admission when control
\t// debt needs urgent reclamation to preserve descriptor/ACK liveness.
\tif g.delivery.fast>0 {
\t\turgent:=false
\t\tfor _,waiting:=range g.cleanupQueue {if waiting.controlDebt.Load()>=48 {urgent=true;break}}
\t\tif !urgent {limit=min(limit,FAST_LIMIT)}
\t}
'''.replace('FAST_LIMIT',str(args.limit))+a)
p.write_text(s)
(d/'rc10_fast_cleanup_test.go').write_text('''package tunnel
import("fmt";"testing";"itom.local/core/wire")
func TestRC10MaintenanceRespondsToShortDemandWithoutBlockingUrgentControl(t *testing.T){
 g:=newStripeGroup(wire.ID{1},1);var lanes []*lane
 for i:=1;i<=8;i++{l:=stripeTestLane(g,fmt.Sprintf("a%02d",i),true);defer l.cancel();if err:=g.attach(l);err!=nil{t.Fatal(err)};lanes=append(lanes,l)}
 g.delivery.fast=1
 for i:=0;i<FAST_LIMIT;i++{if !g.beginCleanup(lanes[i+2]){t.Fatal("protected maintenance made no progress")}}
 waiting:=lanes[FAST_LIMIT+2]
 if g.beginCleanup(waiting){t.Fatal("short request did not bound new BODY maintenance")}
 waiting.controlDebt.Store(48)
 if !g.beginCleanup(waiting){t.Fatal("short demand blocked urgent control reclamation")}
 if g.delivery.fast!=1{t.Fatal("maintenance fabricated a logical ACK")}
 g.endCleanup(waiting);waiting.controlDebt.Store(0)
 g.delivery.fast=0
 if !g.beginCleanup(waiting){t.Fatal("drained short demand left maintenance throttled")}
 g.endCleanup(waiting)
 for i:=0;i<FAST_LIMIT;i++{g.endCleanup(lanes[i+2])}
 if len(g.cleanupActive)!=0||len(g.cleanupQueue)!=0{t.Fatal("adaptive maintenance leaked ownership")}
}
'''.replace('FAST_LIMIT',str(args.limit)))
