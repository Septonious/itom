from pathlib import Path
import hashlib,json,os,shutil,subprocess,sys
repo=Path(__file__).resolve().parents[4];out=Path(sys.argv[1])
subprocess.run(['python3',str(Path(__file__).with_name('prepare.py')),str(out)],check=True)
(out/'v6').mkdir()
p=subprocess.Popen(['git','archive','01c278b383dfe61466996b1a5aaae1484651d178','archives/1.1.0/source/core'],cwd=repo,stdout=subprocess.PIPE)
subprocess.run(['tar','-x','-C',str(out/'v6'),'--strip-components=4'],stdin=p.stdout,check=True);p.stdout.close();assert p.wait()==0
shared=json.loads((out/'DEV9_PROVENANCE.json').read_text())['fixtures']
hashes=lambda p:{str(f.relative_to(p)):hashlib.sha256(f.read_bytes()).hexdigest()for f in sorted(p.rglob('*.go'))if not f.name.endswith('_test.go')}
before=hashes(out/'v6')
for n in shared:shutil.copyfile(out/'model/tunnel'/n,out/'v6/tunnel'/n)
assert hashes(out/'v6')==before
(out/'SYMMETRIC_PROVENANCE.json').write_text(json.dumps({'source_commit':os.environ.get('GITHUB_SHA'),'v6':'01c278b383dfe61466996b1a5aaae1484651d178','production':{v:hashes(out/v)for v in ['v6','model']},'fixtures':shared,'thresholds_relaxed':False},indent=2)+'\n')
