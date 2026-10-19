#!/usr/bin/env python3
"""LAB ONLY: adapt existing DATA-cleanup FIFO parallelism to complete read service.
Keeps original RC2 controller, quota/proofs, polling and DATA writers unchanged.
Control/presence correctness work retains its original scheduler authority.
"""
from pathlib import Path
import sys
root=Path(__file__).resolve().parents[3]
core=Path(sys.argv[1]) if len(sys.argv)>1 else root/'archives/1.0.0-rc10/source/core'
d=core/'tunnel'
p=d/'stripe.go';s=p.read_text();a='cleanupActive      map[*lane]bool';assert s.count(a)==1
s=s.replace(a,a+'\n\tcleanupData map[*lane]bool\n\tcleanupRead cleanupReadPolicy');p.write_text(s)
p=d/'cleanup_service.go';s=p.read_text()
a='func (g *stripeGroup) beginCleanup(l *lane) bool {';assert s.count(a)==1
s=s.replace(a,a+'\n\treturn g.beginCleanupClass(l,cleanupRemote)\n}\nfunc(g *stripeGroup) beginCleanupClass(l *lane,kind cleanupKind)bool{')
a='\tif g.cleanupActive[l] {\n\t\treturn true\n\t}';assert s.count(a)==1
s=s.replace(a,a+'\n\tif g.cleanupData==nil {g.cleanupData=map[*lane]bool{}}\n\tg.cleanupData[l]=kind==cleanupLocalData')
a='\t\t\ti--';assert s.count(a)==2
s=s.replace(a,'\t\t\tdelete(g.cleanupData,waiting)\n'+a,1)
a='\tquiet := g.interactiveReadersLocked()';assert s.count(a)==1
s=s.replace(a,'''\tdataActive:=0
\tfor active:=range g.cleanupActive {if g.cleanupData[active] {dataActive++}}
\tdataLimit:=min(limit,g.cleanupRead.capacity())
'''+a)
a='\tfor i, waiting := range g.cleanupQueue {';assert s.count(a)==1
s=s.replace(a,a+'\n\t\tif g.cleanupData[waiting] && dataActive>=dataLimit {continue}')
a='func (g *stripeGroup) removeCleanupLocked(l *lane) {';assert s.count(a)==1
s=s.replace(a,a+'\n\tdelete(g.cleanupData,l)');p.write_text(s)
p=d/'cleanup.go';s=p.read_text()
a='type cleanupResult struct {';assert s.count(a)==1
s=s.replace(a,a+'\n\treadBytes uint64\n\treadElapsed,readSelection time.Duration')
a='l.stripe.beginCleanup(l)';assert s.count(a)==1;s=s.replace(a,'l.stripe.beginCleanupClass(l,kind)')
a='rows, err := finder.FetchCleanupCandidates(l.ctx, folder, wanted)';assert s.count(a)==1
s=s.replace(a,'readStarted:=time.Now()\n\t\t\t\t\t'+a+'\n\t\t\t\t\tif err==nil {l.recordCleanupRead(&r,folder,rows,time.Since(readStarted))}')
a='rows, next, err := l.mail.Page(l.ctx, folder, l.outCursor)';assert s.count(a)==1
s=s.replace(a,'readStarted:=time.Now()\n\t\t\t'+a+'\n\t\t\tif err==nil {l.recordCleanupRead(&r,folder,rows,time.Since(readStarted))}')
a='func (l *lane) finishCleanup(r cleanupResult) error {';assert s.count(a)==1
s=s.replace(a,a+'''\n\tif l.stripe!=nil && r.kind==cleanupLocalData && r.readBytes>0 {
\t\tslots:=l.stripe.observeCleanupRead(l,r.readBytes,r.readElapsed,r.readSelection)
\t\tif slots>0 && l.diag!=nil && l.diag.tracer!=nil {
\t\t\tdetails,_:=json.Marshal(map[string]any{"data_slots":slots,"read_bytes":r.readBytes,"read_ms":r.readElapsed.Milliseconds(),"selection_ms":r.readSelection.Milliseconds()})
\t\t\tl.diag.trace(traceEvent{Event:"gc_read_feedback",Details:details})
\t\t}
\t}''')
p.write_text(s)
(d/'rc10_cleanup_read_policy.go').write_text('''package tunnel
import("time";"itom.local/core/mailbox")
// This is scheduling of the existing FIFO, not logical delivery credit or a
// second physical gate. In-progress commands finish; at least one DATA cleaner
// and original independent control reclamation always retain service.
type cleanupReadPolicy struct{slots int;fastAccount,slowAccount string;floors map[string]time.Duration}
func(p *cleanupReadPolicy) capacity()int{if p.slots==0{p.slots=2};return p.slots}
func(p *cleanupReadPolicy) observe(account string,bytes uint64,elapsed,selection time.Duration,demand bool){
 if account==""||bytes<32<<10||elapsed<=0||selection<=0||selection>2*time.Second{return}
 p.capacity();if p.floors==nil{p.floors=map[string]time.Duration{}}
 floor:=p.floors[account];if floor==0||selection<floor{floor=selection;p.floors[account]=floor}
 fixed:=2*floor // each account's own SELECT then exact complete BODY FETCH
 if elapsed>fixed+200*time.Millisecond{
  p.fastAccount=""
  if p.slowAccount==""{p.slowAccount=account}else if p.slowAccount!=account{p.slots=max(1,p.slots/2);p.slowAccount=""}
  return
 }
 p.slowAccount=""
 if demand&&elapsed<=fixed+100*time.Millisecond{
  if p.fastAccount==""{p.fastAccount=account}else if p.fastAccount!=account{p.slots=min(8,p.slots+1);p.fastAccount=""}
 }else{p.fastAccount=""}
}
func(l *lane) recordCleanupRead(r *cleanupResult,folder string,rows []mailbox.Message,elapsed time.Duration){
 if r.kind!=cleanupLocalData{return};var bytes uint64;for _,row:=range rows{bytes+=uint64(len(row.MIME))}
 if bytes<=r.readBytes{return}
 m:=physicalMail(l.mail);if m==nil{return};cost,ok:=m.cleanupClient().SelectionCost(folder);if !ok{return}
 r.readBytes=bytes;r.readElapsed=elapsed;r.readSelection=cost
}
func(g *stripeGroup) observeCleanupRead(l *lane,bytes uint64,elapsed,selection time.Duration)int{
 g.mu.Lock();if g.lanes[l.account.ID]!=l||l.ctx.Err()!=nil{g.mu.Unlock();return 0};demand:=false
 for account:=range g.cleanupRead.floors{if g.lanes[account]==nil{delete(g.cleanupRead.floors,account)}}
 if g.lanes[g.cleanupRead.fastAccount]==nil{g.cleanupRead.fastAccount=""}
 if g.lanes[g.cleanupRead.slowAccount]==nil{g.cleanupRead.slowAccount=""}
 for _,waiting:=range g.cleanupQueue{if g.cleanupData[waiting]{demand=true;break}}
 g.cleanupRead.observe(l.account.ID,bytes,elapsed,selection,demand)
 g.wakeCleanupLocked(g.cleanupChoiceLocked());slots:=g.cleanupRead.capacity();g.mu.Unlock();g.signal();return slots
}
''')
(d/'rc10_cleanup_read_policy_test.go').write_text('''package tunnel
import("testing";"time";"fmt";"itom.local/core/wire")
func TestRC10DataCleanupProbesAndContractsFromRealService(t *testing.T){
 var p cleanupReadPolicy
 for i:=0;i<12;i++{p.observe(fmt.Sprintf("a%02d",i%2),512<<10,200*time.Millisecond,80*time.Millisecond,true)}
 if p.capacity()!=8{t.Fatal("fast useful reads did not discover original parallelism",p.capacity())}
 for i:=0;i<10;i++{p.observe("one-bad-account",512<<10,time.Second,80*time.Millisecond,true)}
 if p.capacity()!=8{t.Fatal("one bad account shrank the healthy pool")}
 for i:=0;i<6;i++{p.observe(fmt.Sprintf("a%02d",i%2),512<<10,time.Second,80*time.Millisecond,true)}
 if p.capacity()!=1{t.Fatal("slow proof serialization did not contract",p.capacity())}
 p.observe("a01",0,time.Second,time.Millisecond,true)
 if p.capacity()!=1{t.Fatal("empty/unknown read trained capacity")}
 for i:=0;i<20;i++{p.observe(fmt.Sprintf("a%02d",i%2),512<<10,200*time.Millisecond,80*time.Millisecond,false)}
 if p.capacity()!=1{t.Fatal("demand-limited reads proved parallel capacity")}
 for i:=0;i<14;i++{p.observe(fmt.Sprintf("a%02d",i%2),512<<10,200*time.Millisecond,80*time.Millisecond,true)}
 if p.capacity()!=8{t.Fatal("recovered path stayed permanently capped")}
}
func TestRC10DataCleanupLimitPreservesControlAndFIFO(t *testing.T){
 g:=newStripeGroup(wire.ID{1},1);var lanes []*lane
 for i:=1;i<=8;i++{l:=stripeTestLane(g,fmt.Sprintf("a%02d",i),true);defer l.cancel();if err:=g.attach(l);err!=nil{t.Fatal(err)};lanes=append(lanes,l)}
 if !g.beginCleanupClass(lanes[2],cleanupLocalData)||!g.beginCleanupClass(lanes[3],cleanupLocalData)||g.beginCleanupClass(lanes[4],cleanupLocalData){t.Fatal("cold DATA cleanup limit not enforced")}
 if !g.beginCleanupClass(lanes[5],cleanupLocalControl){t.Fatal("DATA policy blocked control journal reclamation")}
 g.endCleanup(lanes[2])
 if g.beginCleanupClass(lanes[2],cleanupLocalData)||!g.beginCleanupClass(lanes[4],cleanupLocalData){t.Fatal("adaptive cap broke DATA FIFO")}
 for _,l:=range lanes{g.endCleanup(l)}
 if len(g.cleanupActive)!=0||len(g.cleanupQueue)!=0||len(g.cleanupData)!=0||g.delivery.delivered!=0{t.Fatal("scheduler leaked ownership or invented credit")}
}
''')
