#!/usr/bin/env python3
"""LAB ONLY: amortize proof discovery; no refund until the existing ACK/delete proofs.
An existing eligible DATA body starts the read. Fill its bounded FETCH with
other already committed live local bodies; keep those observations cached until
those exact attempts independently become eligible. Eligible hints go first.
"""
from pathlib import Path
import shutil
r=Path(__file__).resolve().parents[3];d=r/'archives/1.0.0-rc10/source/core/tunnel';old=r/'archives/1.0.0-rc9/source/core/tunnel'
p=d/'cleanup.go';s=p.read_text();a='''\t\t\t\tif len(wanted) > 0 {
\t\t\t\t\trows, err := finder.FetchCleanupCandidates'''
b='''\t\t\t\tif len(wanted) > 0 {
\t\t\t\t\t// An eligible row already requires this discovery turn. Cache
\t\t\t\t\t// other live, committed local DATA observations in its bounded
\t\t\t\t\t// FETCH, but do not make them eligible for deletion or refund.
\t\t\t\t\tif kind == cleanupLocalData && l.cipher != nil {
\t\t\t\t\t\tfor _, a := range attempts {
\t\t\t\t\t\t\tif a.Folder == folder && a.Ordinal == 0 && a.State == journal.Committed && a.Ref.Grant.Scope.Session == currentSession && !cachedDigests[a.Digest] {
\t\t\t\t\t\t\t\tif _, present := wanted[a.Digest]; !present { wanted[a.Digest] = false }
\t\t\t\t\t\t\t}
\t\t\t\t\t\t}
\t\t\t\t\t}
\t\t\t\t\trows, err := finder.FetchCleanupCandidates'''
assert s.count(a)==1;s=s.replace(a,b);p.write_text(s)
# RC9's io delta is only the already isolated4MiB cleanup selector extraction.
shutil.copy(old/'io.go',d/'io.go');p=d/'io.go';s=p.read_text()
if '\n\t"sort"' not in s:s=s.replace('import (','import (\n\t"sort"',1)
a='hints := m.controlClient().AppendHints(folder, wanted)';assert s.count(a)==1
s=s.replace(a,'''// Membership requests discovery; true gives eligible bodies priority,
\t// false requests cache prefetch only. Neither value authorizes deletion.
\tincluded := make(map[[32]byte]bool,len(wanted))
\tfor digest := range wanted { included[digest] = true }
\thints := m.controlClient().AppendHints(folder, included)''')
s=s.replace('writer.AppendHints(folder, wanted)','writer.AppendHints(folder, included)')
a='''\t\tfor _, entry := range m.runtime.appendHints {
\t\t\tif entry.user == m.runtimeUser && entry.Folder == folder && wanted[entry.Digest] {'''
b='''\t\tfor _, entry := range m.runtime.appendHints {
\t\t\t_, selected := wanted[entry.Digest]
\t\t\tif entry.user == m.runtimeUser && entry.Folder == folder && selected {'''
assert s.count(a)==1;s=s.replace(a,b)
s=s.replace('cleanupCandidateUIDs(hints)','cleanupCandidateUIDs(hints, wanted)').replace('func cleanupCandidateUIDs(hints []mailbox.AppendHint)', 'func cleanupCandidateUIDs(hints []mailbox.AppendHint, wanted map[[32]byte]bool)')
a='''\tvalidity := hints[0].Validity
\tids := make([]uint32, 0, 32)'''
b='''\t// Prefetch can never displace a currently eligible body from the byte cap.
\tsort.SliceStable(hints,func(i,j int) bool { return wanted[hints[i].Digest] && !wanted[hints[j].Digest] })
\tvalidity := hints[0].Validity
\tids := make([]uint32, 0, 32)'''
assert s.count(a)==1;s=s.replace(a,b);p.write_text(s)
(d/'rc10_cleanup_prefetch_test.go').write_text('''package tunnel
import("bytes";"context";"testing";"itom.local/core/journal";"itom.local/core/mailbox";"itom.local/core/pool";"itom.local/core/servercfg")
type rc10PrefetchMail struct { *memoryMail; calls int; requested map[[32]byte]bool }
func(m *rc10PrefetchMail) FetchCleanupCandidates(ctx context.Context, folder string, wanted map[[32]byte]bool)([]mailbox.Message,error){
 m.calls++;m.requested=make(map[[32]byte]bool,len(wanted));for k,v:=range wanted {m.requested[k]=v}
 return inventory(ctx,m.memoryMail,folder)
}
func TestRC10PrefetchedUnacknowledgedDataCannotRefundEarly(t *testing.T){
 a:=servercfg.Account{ID:"a01",Mail:mailbox.Config{Host:"mail.example"}};u:=servercfg.User{ID:"12345678",Up:"itom-u-up",Down:"itom-u-down"}
 s,err:=ledger(t.TempDir(),"data",a,u,false);if err!=nil{t.Fatal(err)};defer s.Close()
 if err=s.EnableAfterInventory(a.ID,u.ID,s.Run());err!=nil{t.Fatal(err)}
 cipher:=cleanupFixtureEpoch(t);m:=&rc10PrefetchMail{memoryMail:&memoryMail{rows:map[string]map[uint32][]byte{},next:map[string]uint32{}}}
 if err=m.Ensure(context.Background(),u.Down);err!=nil{t.Fatal(err)}
 g,err:=s.Issue(journal.Scope{Account:a.ID,User:u.ID,Session:cipher.Context().Session},pool.Amount{Bytes:2<<20,Messages:8});if err!=nil{t.Fatal(err)}
 var attempts []journal.Attempt
 for i:=0;i<4;i++ {body:=bytes.Repeat([]byte{byte(i+1)},32<<10);raw,err:=mailbox.Encode(body);if err!=nil{t.Fatal(err)}
 at,err:=s.PrepareStarted(g.Ref,u.Down,[16]byte{byte(i+1)},uint64(len(raw)),body);if err!=nil{t.Fatal(err)}
 if err=s.Complete(at.Ref,journal.Accepted);err!=nil{t.Fatal(err)};if err=m.Append(context.Background(),u.Down,raw);err!=nil{t.Fatal(err)};attempts=append(attempts,at)}
 if err=s.CloseGrant(g.Ref);err!=nil{t.Fatal(err)}
 l:=&lane{ctx:context.Background(),account:a,user:u,data:s,mail:m,sendFolder:u.Down,cipher:cipher}
 first:=map[journal.AttemptRef]bool{attempts[0].Ref:true};result:=l.cleanup(cleanupLocalData,first,nil,nil,true,s)
 if result.err!=nil||len(result.local)!=1||m.calls!=1||len(m.requested)!=4{t.Fatal("first bounded observation",result,m.calls,len(m.requested))}
 for i,a:=range attempts{priority,ok:=m.requested[a.Digest];if !ok||priority!=(i==0){t.Fatal("wrong discovery priority",i,priority,ok)}}
 held,err:=s.Held(a.ID,u.ID);if err!=nil||held.Messages!=3{t.Fatal("prefetch refunded unacknowledged debt",held,err)}
 for uid:=uint32(2);uid<=4;uid++{absent,err:=m.Absent(l.ctx,u.Down,1,uid);if err!=nil||absent{t.Fatal("prefetch deleted unacknowledged BODY",uid,err)}}
 later:=map[journal.AttemptRef]bool{};for _,a:=range attempts[1:]{later[a.Ref]=true}
 result=l.cleanup(cleanupLocalData,later,nil,nil,true,s)
 if result.err!=nil||len(result.local)!=3||m.calls!=1{t.Fatal("cached observations did not amortize FETCH",result,m.calls)}
 if err=s.Audit();err!=nil{t.Fatal(err)}
}
func TestRC10CleanupEligibleHintsPrecedePrefetchWithinByteLimit(t *testing.T){
 var hints []mailbox.AppendHint;wanted:=map[[32]byte]bool{}
 for i:=1;i<=12;i++{k:=[32]byte{byte(i)};hints=append(hints,mailbox.AppendHint{Validity:1,UID:uint32(i),MIMEBytes:512<<10,Digest:k});wanted[k]=i>=10}
 validity,ids:=cleanupCandidateUIDs(hints,wanted)
 if validity!=1||len(ids)!=8||ids[0]!=10||ids[1]!=11||ids[2]!=12{t.Fatal("prefetch displaced eligible evidence or byte bound",validity,ids)}
}
''')
