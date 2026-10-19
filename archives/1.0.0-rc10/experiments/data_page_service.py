#!/usr/bin/env python3
"""LAB ONLY. Learn1/2/4 BODY pages from full actual service, not a4Mbps safety rate.
Preserves streaming, cursors, unknown writes, absolute/progress guards and RC2 flight.
"""
from pathlib import Path
import sys
root=Path(__file__).resolve().parents[3]
core=Path(sys.argv[1]) if len(sys.argv)>1 else root/'archives/1.0.0-rc10/source/core'
d=core/'tunnel';p=d/'io.go';s=p.read_text()
a='dataReadLimit uint32 // command mutex; large-BODY evidence only, reset on replacement';assert s.count(a)==1
s=s.replace(a,'dataReadPage dataReadPagePolicy // command mutex; actual full BODY service only');p.write_text(s)
p=d/'topology.go';s=p.read_text()
a='m.dataReadLimit = 0';assert s.count(a)==1;s=s.replace(a,'m.dataReadPage = dataReadPagePolicy{}')
a='''limit := m.dataReadLimit
\tif limit == 0 {
\t\tlimit = 1
\t}''';assert s.count(a)==1;s=s.replace(a,'limit := m.dataReadPage.size()')
a='var bodyBytes int';assert s.count(a)==1;s=s.replace(a,a+'\n\tvar bodyRows int')
a='bodyBytes += len(row.MIME)';assert s.count(a)==2;s=s.replace(a,a+'\n\t\t\t\tbodyRows++',1)
a=s.index('\t// Empty polls and tiny messages cannot prove');b=s.index('\n\tif err != nil {',a)
s=s[:a]+'''\tif err==nil {
\t\tselection,_:=reader.SelectionCost(folder)
\t\tm.dataReadPage.observe(limit,bodyRows,bodyBytes,time.Since(started)-callbackWait,selection)
\t}
'''+s[b:];p.write_text(s)
(d/'rc10_data_page_policy.go').write_text('''package tunnel
import("time")
// Four max-sized bodies being safe under30s is not evidence that they preserve
// short RPC latency on a shared link. Probe one step at a time from two actually
// full, fast commands; shrink on real serialization above this reader's SELECT
// floor. The first complete BODY is indivisible. Callback/journal wait is excluded
// by the caller, while TCP/IMAP service and the entire transfer remain included.
type dataReadPagePolicy struct{limit uint32;fast int;floor time.Duration}
func(p *dataReadPagePolicy)size()uint32{if p.limit==0{p.limit=1};return p.limit}
func(p *dataReadPagePolicy)observe(requested uint32,rows,bytes int,elapsed,selection time.Duration){
 if rows==0||bytes<32<<10||elapsed<=0{return};p.size()
 if requested!=p.limit{return}
 if selection>0&&selection<=2*time.Second&&(p.floor==0||selection<p.floor){p.floor=selection}
 budget:=max(250*time.Millisecond,2*p.floor+100*time.Millisecond)
 if elapsed>budget {p.limit=max(uint32(1),p.limit/2);p.fast=0;return}
 if rows>=int(requested){p.fast++;if p.fast>=2{p.limit=min(uint32(4),2*p.limit);p.fast=0}}else{p.fast=0}
}
''')
(d/'rc10_data_page_policy_test.go').write_text('''package tunnel
import("testing";"time")
func TestRC10DataPagesNeedFullSizedFastService(t *testing.T){
 var p dataReadPagePolicy
 for i:=0;i<20;i++{p.observe(p.size(),1,1024,time.Millisecond,80*time.Millisecond)}
 if p.size()!=1{t.Fatal("tiny control mail proved bulk capacity")}
 for i:=0;i<2;i++{p.observe(p.size(),1,32<<10,180*time.Millisecond,80*time.Millisecond)}
 if p.size()!=2{t.Fatal("full BODY service did not probe next page",p.size())}
 for i:=0;i<10;i++{p.observe(p.size(),1,1<<20,180*time.Millisecond,80*time.Millisecond)}
 if p.size()!=2{t.Fatal("one BODY proved a two BODY command")}
 for i:=0;i<2;i++{p.observe(p.size(),2,1<<20,200*time.Millisecond,80*time.Millisecond)}
 if p.size()!=4{t.Fatal("full fast commands did not recover original page capacity")}
 p.observe(4,4,1<<20,time.Second,80*time.Millisecond)
 if p.size()!=2{t.Fatal("slow but >4Mbps read failed to contract",p.size())}
 p.observe(2,2,512<<10,time.Second,80*time.Millisecond)
 if p.size()!=1{t.Fatal("continued slow service failed to reach one BODY")}
 for i:=0;i<4;i++{n:=p.size();p.observe(n,int(n),1<<20,200*time.Millisecond,80*time.Millisecond)}
 if p.size()!=4{t.Fatal("recovered read path remained permanently capped")}
}
func TestRC10DataPagesPreservePropagationAndRejectStaleSamples(t *testing.T){
 var p dataReadPagePolicy
 for i:=0;i<4;i++{n:=p.size();p.observe(n,int(n),512<<10,550*time.Millisecond,300*time.Millisecond)}
 if p.size()!=4{t.Fatal("true propagation masqueraded as queue pressure")}
 p.observe(4,4,512<<10,2*time.Second,300*time.Millisecond)
 if p.size()!=2{t.Fatal("extra serialization did not reduce page")}
 p.observe(4,4,512<<10,time.Millisecond,300*time.Millisecond)
 if p.size()!=2{t.Fatal("old observation grew a new page generation")}
}
''')
