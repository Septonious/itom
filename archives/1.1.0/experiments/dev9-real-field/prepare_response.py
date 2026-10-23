from pathlib import Path
import hashlib,json,os,shutil,subprocess,sys
repo=Path(__file__).resolve().parents[4];out=Path(sys.argv[1])
subprocess.run(['python3',str(Path(__file__).with_name('prepare.py')),str(out)],check=True)
(out/'v3').mkdir()
p=subprocess.Popen(['git','archive','cf10d3d7da815c98413a58bd6fd7e4f3ce2a9c6c','archives/1.1.0/source/core'],cwd=repo,stdout=subprocess.PIPE)
subprocess.run(['tar','-x','-C',str(out/'v3'),'--strip-components=4'],stdin=p.stdout,check=True);p.stdout.close();assert p.wait()==0
shared=json.loads((out/'DEV9_PROVENANCE.json').read_text())['fixtures']
hashes=lambda p:{str(f.relative_to(p)):hashlib.sha256(f.read_bytes()).hexdigest()for f in sorted(p.rglob('*.go'))if not f.name.endswith('_test.go')}
before=hashes(out/'v3')
for n in shared:shutil.copyfile(out/'model/tunnel'/n,out/'v3/tunnel'/n)
assert hashes(out/'v3')==before
(out/'RESPONSE_PROVENANCE.json').write_text(json.dumps({'source_commit':os.environ.get('GITHUB_SHA'),'v3':'cf10d3d7da815c98413a58bd6fd7e4f3ce2a9c6c','production':{v:hashes(out/v)for v in ['v3','model']},'fixtures':shared,'thresholds_relaxed':False},indent=2)+'\n')
