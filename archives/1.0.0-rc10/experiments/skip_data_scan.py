#!/usr/bin/env python3
"""LAB ONLY: avoid DATA maintenance with provably no eligible local work.
Current unacknowledged DATA stays charged. Old local sessions still scan.
No physical observation, deletion, refund or recovery requirement changes.
"""
from pathlib import Path
r=Path(__file__).resolve().parents[3];d=r/'archives/1.0.0-rc10/source/core/tunnel';p=d/'cleanup.go';s=p.read_text()
a='''\tif !l.cleanupQueued && l.deferOwnerCleanup(kind) {'''
b='''\t// Avoid speculative full-BODY discovery while every local attempt still
\t// belongs to the live unacknowledged session. This is decided before taking
\t// a cleaner or starting a worker, so pressure cannot spin empty GC workers.
\tif kind==cleanupLocalData && l.cipher!=nil && l.data!=nil {
\t\tstore:=l.data.StoreID()
\t\tl.mu.Lock()
\t\teligible:=false
\t\tfor ref:=range l.obsolete {if ref.Grant.Store==store {eligible=true;break}}
\t\tl.mu.Unlock()
\t\tif !eligible {
\t\t\tusage,err:=l.data.UsageByOrigin(l.account.ID,l.user.ID,l.cipher.Context().Session)
\t\t\tif err==nil && usage.HistoricalLocal.Bytes==0 && usage.HistoricalLocal.Messages==0 {
\t\t\t\treturn false
\t\t\t}
\t\t}
\t}
'''+a
assert s.count(a)==1;s=s.replace(a,b);p.write_text(s)
(d/'rc10_cleanup_admission_test.go').write_text('''package tunnel
import("bytes";"context";"testing";"time";"itom.local/core/journal";"itom.local/core/mailbox";"itom.local/core/pool";"itom.local/core/servercfg";"itom.local/core/wire")
func TestRC10NoEligibleCurrentDataDoesNotStartSpeculativeCleaner(t *testing.T){
 for _,historical:=range []bool{false,true}{t.Run(map[bool]string{false:"current",true:"historical"}[historical],func(t *testing.T){
 a:=servercfg.Account{ID:"a01",Mail:mailbox.Config{Host:"mail.example"}};u:=servercfg.User{ID:"12345678",Up:"itom-u-up",Down:"itom-u-down"}
 s,err:=ledger(t.TempDir(),"data",a,u,false);if err!=nil{t.Fatal(err)};defer s.Close()
 if err=s.EnableAfterInventory(a.ID,u.ID,s.Run());err!=nil{t.Fatal(err)}
 cipher:=cleanupFixtureEpoch(t);session:=cipher.Context().Session;if historical{session=wire.ID{99}}
 g,err:=s.Issue(journal.Scope{Account:a.ID,User:u.ID,Session:session},pool.Amount{Bytes:128<<10,Messages:4});if err!=nil{t.Fatal(err)}
 body:=bytes.Repeat([]byte{7},32<<10);raw,err:=mailbox.Encode(body);if err!=nil{t.Fatal(err)}
 at,err:=s.PrepareStarted(g.Ref,u.Down,[16]byte{1},uint64(len(raw)),body);if err!=nil{t.Fatal(err)}
 if err=s.Complete(at.Ref,journal.Accepted);err!=nil{t.Fatal(err)};if err=s.CloseGrant(g.Ref);err!=nil{t.Fatal(err)}
 m:=&memoryMail{rows:map[string]map[uint32][]byte{},next:map[string]uint32{}};if err=m.Ensure(context.Background(),u.Down);err!=nil{t.Fatal(err)}
 ctx,cancel:=context.WithCancel(context.Background());defer cancel()
 l:=&lane{ctx:ctx,account:a,user:u,data:s,mail:m,sendFolder:u.Down,cipher:cipher,obsolete:map[journal.AttemptRef]bool{},gcDone:make(chan cleanupResult,1)}
 started:=l.startCleanup(cleanupLocalData)
 if historical {
  if !started{t.Fatal("historical local debt lost its cleanup path")}
  select{case <-l.gcDone:case <-time.After(time.Second):t.Fatal("historical scan did not finish")}
  l.workers.Wait()
 }else{
  if started{cancel();l.workers.Wait();t.Fatal("unacknowledged current DATA started speculative cleanup")}
  if l.gcBusy||l.gcActive.Load()||l.cleanupWanted.Load(){t.Fatal("empty cleanup retained maintenance ownership")}
 }
 held,err:=s.Held(a.ID,u.ID);if err!=nil||held.Messages!=1{t.Fatal("skipped or unobserved scan refunded physical debt",held,err)}
 })}
}
''')

# The hidden-audit regression must request actual eligible work; an empty live
# journal now correctly starts no cleaner at all. Keep its no-refund assertions.
p=d/'cleanup_service_test.go';s=p.read_text()
if '"itom.local/core/pool"' not in s:s=s.replace('"itom.local/core/journal"','"itom.local/core/journal"\n "itom.local/core/pool"',1)
start=s.index('func TestCleanupNeverStartsHiddenReceiveAudit(');end=s.index('\nfunc ',start+1);chunk=s[start:end]
a='\tl.gcDone = make(chan cleanupResult, 1)'
b='''\tif err=store.EnableAfterInventory(l.account.ID,l.user.ID,store.Run());err!=nil{t.Fatal(err)}
 grant,err:=store.Issue(journal.Scope{Account:l.account.ID,User:l.user.ID,Session:l.cipher.Context().Session},pool.Amount{Bytes:64<<10,Messages:2});if err!=nil{t.Fatal(err)}
 body:=[]byte("eligible local proof absent from provider")
 raw,err:=mailbox.Encode(body);if err!=nil{t.Fatal(err)}
 attempt,err:=store.PrepareStarted(grant.Ref,l.sendFolder,[16]byte{1},uint64(len(raw)),body);if err!=nil{t.Fatal(err)}
 if err=store.Complete(attempt.Ref,journal.Accepted);err!=nil{t.Fatal(err)}
 if err=store.CloseGrant(grant.Ref);err!=nil{t.Fatal(err)}
 l.obsolete=map[journal.AttemptRef]bool{attempt.Ref:true}
'''+a
assert chunk.count(a)==1;chunk=chunk.replace(a,b);s=s[:start]+chunk+s[end:];p.write_text(s)
