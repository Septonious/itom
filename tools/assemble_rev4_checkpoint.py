from pathlib import Path
import hashlib,json,sys,zipfile
root=Path(__file__).resolve().parents[1]
x=json.loads((root/'archives/rev4-checkpoint/manifest.json').read_text())
assert x['release_ready'] is False and x['classification']=='UNSTABLE_CHECKPOINT'
out=Path(sys.argv[1]);out.mkdir(parents=True,exist_ok=False)
p=out/x['archive']['name']
with p.open('xb') as dest:
 for part in x['parts']:
  f=root/part['path'];b=f.read_bytes()
  assert len(b)==part['size'] and hashlib.sha256(b).hexdigest()==part['sha256']
  dest.write(b)
assert p.stat().st_size==x['archive']['size'] and hashlib.sha256(p.read_bytes()).hexdigest()==x['archive']['sha256']
with zipfile.ZipFile(p) as z:
 assert z.testzip() is None
 status=json.loads(z.read('STATUS.json'))
 assert status['release_ready'] is False and status['classification']=='UNSTABLE_CHECKPOINT'
 for line in z.read('SHA256SUMS').decode().splitlines():
  expected,name=line.split('  ',1)
  assert hashlib.sha256(z.read(name)).hexdigest()==expected
(out/(p.name+'.sha256')).write_text(x['archive']['sha256']+'  '+p.name+'\n')
print('Verified unstable checkpoint; no release published')
