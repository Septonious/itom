#!/usr/bin/env python3
"""LAB ONLY: allocate bounded owned scan pages from existing live-count hints.
Counters change capacity only, never rows, cursor, decode, or proof authority.
"""
from pathlib import Path
import sys
root=Path(__file__).resolve().parents[3]
core=Path(sys.argv[1]) if len(sys.argv)>1 else root/'archives/1.0.0-rc10/source/core'
p=core/'journal/ledger.go';s=p.read_text()
for kind,bucket,counter in [('Attempt','attemptBucket','attempt_count'),('Grant','grantBucket','grant_count')]:
 a=f'out := make([]{kind}, 0, limit)';assert s.count(a)==1;s=s.replace(a,f'out := make([]{kind}, 0)')
 a=f'c := tx.Bucket({bucket}).Cursor()\n\t\tfor k, v := c.Seek(key(after + 1));'
 b=f'''// The existing live counter sizes storage only. A stale/understated
\t\t// hint can grow the slice; malformed metadata falls back to the original
\t\t// bounded capacity. Neither can hide a row or bypass its decode.
\t\tcapacity := limit
\t\tif count, err := number(tx.Bucket(metaBucket), "{counter}"); err == nil {{
\t\t\tcapacity = int(min(uint64(limit), count))
\t\t}}
\t\tout = make([]{kind}, 0, capacity)
\t\tc := tx.Bucket({bucket}).Cursor()
\t\tfor k, v := c.Seek(key(after + 1));'''
 assert s.count(a)==1;s=s.replace(a,b)
p.write_text(s)
(core/'journal/rc10_scan_hint_test.go').write_text('''package journal
import("testing";"path/filepath";"time";"itom.local/core/pool";"itom.local/core/wire";bolt "go.etcd.io/bbolt")
func TestRC10ScanHintCannotHideRowsOrChangeExclusiveCursor(t *testing.T){
 s,err:=Open(filepath.Join(t.TempDir(),"scan.db"),testConfig(1),time.Second);if err!=nil{t.Fatal(err)};defer s.Close();enabled(t,s,"a0","12345678")
 for i:=0;i<8;i++{g,err:=s.Issue(Scope{Account:"a0",User:"12345678",Session:wire.ID{1}},pool.Amount{Bytes:1000,Messages:1});if err!=nil{t.Fatal(err)};if _,err=s.Prepare(g.Ref,"itom-u1-up",wire.ID{byte(i+1)},1000,[]byte{1});err!=nil{t.Fatal(err)}}
 attempts,err:=s.ScanAttempts(0,256);if err!=nil{t.Fatal(err)};grants,err:=s.ScanGrants(0,256);if err!=nil{t.Fatal(err)}
 for _,raw:=range [][]byte{key(0),key(2),key(^uint64(0)),[]byte("bad")} {
  if err=s.write(func(tx *bolt.Tx)error{m:=tx.Bucket(metaBucket);if err:=m.Put([]byte("attempt_count"),raw);err!=nil{return err};return m.Put([]byte("grant_count"),raw)});err!=nil{t.Fatal(err)}
  a,err:=s.ScanAttempts(0,256);if err!=nil||len(a)!=8{t.Fatal("hint hid attempts",len(a),err)};g,err:=s.ScanGrants(0,256);if err!=nil||len(g)!=8{t.Fatal("hint hid grants",len(g),err)}
  a,err=s.ScanAttempts(attempts[3].Ref.ID,256);if err!=nil||len(a)!=4||a[0].Ref.ID!=attempts[4].Ref.ID{t.Fatal("attempt cursor changed",a,err)}
  g,err=s.ScanGrants(grants[3].Ref.ID,256);if err!=nil||len(g)!=4||g[0].Ref.ID!=grants[4].Ref.ID{t.Fatal("grant cursor changed",g,err)}
 }
 if err=s.write(func(tx *bolt.Tx)error{m:=tx.Bucket(metaBucket);if err:=putNumber(m,"attempt_count",8);err!=nil{return err};return putNumber(m,"grant_count",8)});err!=nil{t.Fatal(err)}
}
''')
