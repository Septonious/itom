#!/usr/bin/env python3
"""Extract only the known routing-data assets from the checksum-verified RC20 APK."""
import hashlib, json, pathlib, sys, zipfile

apk, manifest, out=map(pathlib.Path,sys.argv[1:])
expected=[line.split()[0] for line in manifest.read_text().splitlines()
          if line.split() and line.split()[-1].lstrip("*").endswith(apk.name)]
assert len(expected)==1 and hashlib.sha256(apk.read_bytes()).hexdigest()==expected[0]
out.mkdir(parents=True,exist_ok=True)
with zipfile.ZipFile(apk) as z:
    for name in ("geosite.dat","geoip.dat","ads-trackers.txt","malware.txt"):
        path="assets/routing-data/"+name
        assert z.namelist().count(path)==1,path
        info=z.getinfo(path)
        assert 0<info.file_size<=64*1024*1024,path
        data=z.read(info)
        (out/name).write_bytes(data)
        print(name,len(data),hashlib.sha256(data).hexdigest())
    raw=z.read("assets/routing-data/manifest.json")
    assert len(raw)<=65536
    meta=json.loads(raw)
    for name in ("geosite.dat","geoip.dat","ads-trackers.txt","malware.txt"):
        data=(out/name).read_bytes()
        assert meta["datasets"][name]["size"]==len(data)
        assert meta["datasets"][name]["sha256"]==hashlib.sha256(data).hexdigest()
    (out/"manifest.json").write_bytes(raw)
