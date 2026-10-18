#!/usr/bin/env python3
"""LAB ONLY: size owned scan slices from the same bounded read snapshot.
No cache, DB format, cursor, proof, reservation or commit behavior changes.
"""
from pathlib import Path
r=Path(__file__).resolve().parents[3];d=r/'archives/1.0.0-rc10/source/core/journal';p=d/'ledger.go';s=p.read_text()
for kind,bucket in [('Attempt','attemptBucket'),('Grant','grantBucket')]:
 a=f'out := make([]{kind}, 0, limit)';assert s.count(a)==1;s=s.replace(a,f'out := make([]{kind}, 0)')
 a=f'c := tx.Bucket({bucket}).Cursor()\n\t\tfor k, v := c.Seek(key(after + 1));'
 b=f'''c := tx.Bucket({bucket}).Cursor()
\t\t// Both passes share one immutable read transaction and the original
\t\t// 256-row bound. Empty/sparse journals need not allocate256 rows on
\t\t// every health or cleanup scan; full inventories still return owned data.
\t\tcount := 0
\t\tfor k, _ := c.Seek(key(after+1)); k != nil && count < limit; k, _ = c.Next() {{ count++ }}
\t\tout = make([]{kind}, 0, count)
\t\tfor k, v := c.Seek(key(after + 1));'''
 assert s.count(a)==1;s=s.replace(a,b)
p.write_text(s)
