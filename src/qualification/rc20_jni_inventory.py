#!/usr/bin/env python3
"""Read actual method descriptors from RC20's DEX tables, not JNI symbol guesses."""
import hashlib, pathlib, struct, sys, zipfile

apk=pathlib.Path(sys.argv[1])
manifest=pathlib.Path(sys.argv[2])
expected=[line.split()[0] for line in manifest.read_text().splitlines()
          if line.split() and line.split()[-1].lstrip("*").endswith(apk.name)]
assert len(expected)==1 and hashlib.sha256(apk.read_bytes()).hexdigest()==expected[0], "APK checksum"
wanted={"checkRouting","validateRoutingData","validateRoutingPolicy","replaceTun","rebindNetwork","setNetworkType"}
found={n:set() for n in wanted}
by_lower={n.lower():n for n in wanted}
bridge_candidates=set()
with zipfile.ZipFile(apk) as z:
    for name in z.namelist():
        if not name.startswith("classes") or not name.endswith(".dex"): continue
        b=z.read(name)
        assert b[:4]==b"dex\n"
        def u32(off): return struct.unpack_from("<I",b,off)[0]
        def u16(off): return struct.unpack_from("<H",b,off)[0]
        ns,so,nt,to,np,po=struct.unpack_from("<6I",b,56)
        nm,mo=struct.unpack_from("<2I",b,88)
        def string(i):
            pos=u32(so+4*i)
            while b[pos]&128: pos+=1
            pos+=1
            end=b.index(0,pos)
            return b[pos:end].decode("utf-8",errors="replace")
        def typ(i):return string(u32(to+4*i))
        def proto(i):
            off=po+12*i
            result=typ(u32(off+4)); params=u32(off+8)
            args=[] if params==0 else [typ(u16(params+4+2*j)) for j in range(u32(params))]
            return "("+ "".join(args)+")"+result
        for i in range(nm):
            off=mo+8*i; cls=typ(u16(off)); method=string(u32(off+4))
            if method.lower() in by_lower:
                found[by_lower[method.lower()]].add(cls+"->"+method+proto(u16(off+2)))
            if any(k in method.lower() for k in ("routing","rebind","replacetun","networktype")):
                bridge_candidates.add(cls+"->"+method+proto(u16(off+2)))
for method, signatures in sorted(found.items()):
    if not signatures: print("MISSING",method)
    for signature in sorted(signatures): print(signature)
if not all(found.values()):
    print("Candidate descriptors:")
    for signature in sorted(bridge_candidates): print(signature)
assert all(found.values()),"Incomplete RC20 JNI inventory"
