#!/usr/bin/env python3
"""LAB after adaptive_data_cleanup. Learn FETCH's actual empty-command floor.
SELECT cost alone is not a FETCH baseline (the delayed provider makes this
distinction explicit). Empty replies train overhead only, never body capacity.
Unknown baseline retains original scheduler capacity, not a speculative cap.
"""
from pathlib import Path
import sys
root=Path(__file__).resolve().parents[3]
core=Path(sys.argv[1]) if len(sys.argv)>1 else root/'archives/1.0.0-rc10/source/core'
d=core/'tunnel';p=d/'io.go';s=p.read_text()
a='providerRTT        atomic.Int64';assert s.count(a)==1
s=s.replace(a,a+'\n\temptyFetchNS atomic.Int64')
a='reader, rows, next, err := m.readPage(ctx, f, cur, verify, limit, callback)';assert s.count(a)==1
s=s.replace(a,a+'\n\t\tif err==nil && len(rows)==0 {m.noteEmptyFetch(time.Since(readStarted))}')
p.write_text(s)
p=d/'topology.go';s=p.read_text()
a='\tif err != nil {\n\t\tm.retireDataReader(reader, folder)';assert s.count(a)==2
s=s.replace(a,'\tif err==nil && bodyBytes==0 {m.noteEmptyFetch(time.Since(started)-callbackWait)}\n'+a,1);p.write_text(s)
p=d/'cleanup_service.go';s=p.read_text()
a='g.cleanupData[l] = kind == cleanupLocalData'
if a not in s:a='g.cleanupData[l]=kind==cleanupLocalData'
assert s.count(a)==1
s=s.replace(a,a+'''\n\t// Without a real fixed FETCH observation, preserve original capacity.
\tif m:=physicalMail(l.mail);m==nil || m.emptyFetchNS.Load()<=0 {g.cleanupData[l]=false}''');p.write_text(s)
p=d/'cleanup.go';s=p.read_text()
import re
a=re.search(r'readElapsed,\s*readSelection\s+time.Duration',s).group()
s=s.replace(a,'readElapsed,readSelection,readFetch time.Duration')
a='r.readElapsed, r.readSelection)'
if a not in s:a='r.readElapsed,r.readSelection)'
assert s.count(a)==1;s=s.replace(a,a[:-1]+',r.readFetch)')
a='"selection_ms": r.readSelection.Milliseconds()'
if a not in s:a='"selection_ms":r.readSelection.Milliseconds()'
assert s.count(a)==1;s=s.replace(a,a+',"empty_fetch_ms":r.readFetch.Milliseconds()');p.write_text(s)
p=d/'rc10_cleanup_read_policy.go'
original=p.read_text()
def edit(a,b):
 global original
 assert a in original,a
 original=original.replace(a,b)
edit('elapsed,selection time.Duration','elapsed,selection,emptyFetch time.Duration') if 'elapsed,selection time.Duration' in original else edit('elapsed, selection time.Duration','elapsed, selection, emptyFetch time.Duration')
edit('selection>2*time.Second','selection>2*time.Second||emptyFetch<=0||emptyFetch>2*time.Second') if 'selection>2*time.Second' in original else edit('selection > 2*time.Second','selection > 2*time.Second || emptyFetch<=0 || emptyFetch>2*time.Second')
if 'floor==0||selection<floor' in original:
 edit('floor==0||selection<floor','floor==0||selection+emptyFetch<floor');edit('floor=selection','floor=selection+emptyFetch');edit('fixed:=2*floor','fixed:=floor')
else:
 edit('floor == 0 || selection < floor','floor == 0 || selection+emptyFetch < floor');edit('floor = selection','floor = selection+emptyFetch');edit('fixed := 2 * floor','fixed := floor')
edit('r.readSelection=cost','r.readSelection=cost;r.readFetch=time.Duration(m.emptyFetchNS.Load())') if 'r.readSelection=cost' in original else edit('r.readSelection = cost','r.readSelection = cost;r.readFetch=time.Duration(m.emptyFetchNS.Load())')
edit('elapsed,selection,demand)','elapsed,selection,emptyFetch,demand)') if 'elapsed,selection,demand)' in original else edit('elapsed, selection, demand)','elapsed, selection, emptyFetch, demand)')
a='g.cleanupRead.observe(l.account.ID,bytes,elapsed,selection,emptyFetch,demand)'
if a not in original:a='g.cleanupRead.observe(l.account.ID, bytes, elapsed, selection, emptyFetch, demand)'
assert original.count(a)==1
original=original.replace(a,'''// A just-completed full read also demonstrates demand when all DATA
 // slots were in use, even if the FIFO handed off before its actor ran.
 activeData:=0;for active:=range g.cleanupActive{if g.cleanupData[active]{activeData++}}
 demand=demand||activeData+1>=g.cleanupRead.capacity()
 '''+a)
original+='''
// A tagged empty FETCH is fixed command service, not body/delivery evidence.
// Minima cannot be inflated by later loaded replies. No extra command is sent.
func(m *laneMail) noteEmptyFetch(elapsed time.Duration){
 if elapsed<=0||elapsed>2*time.Second{return}
 for {old:=m.emptyFetchNS.Load();if old>0&&old<=int64(elapsed){return};if m.emptyFetchNS.CompareAndSwap(old,int64(elapsed)){return}}
}
'''
p.write_text(original)
p=d/'rc10_cleanup_read_policy_test.go';s=p.read_text()
s=re.sub(r'(80\s*\*\s*time.Millisecond|time.Millisecond),\s*(true|false)\)',r'\1,80*time.Millisecond,\2)',s)
a='l := stripeTestLane(g, fmt.Sprintf("a%02d", i), true)'
if a in s:s=s.replace(a,a+'\n m:=&laneMail{baseData:true};m.noteEmptyFetch(80*time.Millisecond);l.mail=m')
else:
 a='l:=stripeTestLane(g,fmt.Sprintf("a%02d",i),true)';assert a in s;s=s.replace(a,a+';m:=&laneMail{baseData:true};m.noteEmptyFetch(80*time.Millisecond);l.mail=m')
s+='''
func TestRC10EmptyFetchFloorIsSeparateFromSelectAndCapacity(t *testing.T){
 var p cleanupReadPolicy
 for i:=0;i<12;i++{p.observe(fmt.Sprintf("a%02d",i%2),512<<10,170*time.Millisecond,time.Millisecond,80*time.Millisecond,true)}
 if p.capacity()!=8{t.Fatal("cheap SELECT hid80ms fixed FETCH service",p.capacity())}
 m:=&laneMail{};m.noteEmptyFetch(80*time.Millisecond);m.noteEmptyFetch(time.Second)
 if time.Duration(m.emptyFetchNS.Load())!=80*time.Millisecond{t.Fatal("loaded empty FETCH inflated fixed floor")}
 m.noteEmptyFetch(0)
 if p.capacity()!=8{t.Fatal("empty metadata invented body service capacity")}
}
'''
p.write_text(s)
