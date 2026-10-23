from pathlib import Path
import hashlib,json,os,shutil,subprocess,sys
repo=Path(__file__).resolve().parents[4];source=repo/'archives/1.1.0/source/core';out=Path(sys.argv[1]);out.mkdir(parents=True,exist_ok=True)
def hashes(p):return {str(f.relative_to(p)):hashlib.sha256(f.read_bytes()).hexdigest()for f in sorted(p.rglob('*.go'))if not f.name.endswith('_test.go')}
baseline='6ade0182848652152fd8aa9af7a8078dfda27563'
subprocess.run(['git','diff','--exit-code',baseline,'HEAD','--','archives/1.1.0/source/core'],cwd=repo,check=True)
r={'runtime_baseline':baseline,'recipe_commit':os.environ.get('GITHUB_SHA'),'classification':'routing sensitivity; unchanged quota, proof, windows and deadlines','variants':{}}
for variant in ['current','data_receive','data_both']:
 p=out/variant;shutil.copytree(source,p,ignore=shutil.ignore_patterns('*.test'))
 if variant!='current':
  f=p/'tunnel/lane.go';s=f.read_text();needle='\tif fastBatch && l.dataShards > 0 {\n\t\tfolder = l.sendFolder\n\t}\n';assert s.count(needle)==1;s=s.replace(needle,'');f.write_text(s)
 if variant=='data_both':
  f=p/'tunnel/io.go';s=f.read_text();needle='if fast && m.topologyCaps&capSplitData != 0 && len(raw) <= 64<<10 {';assert s.count(needle)==1;s=s.replace(needle,needle[:-1]+'&& !m.DataMaintenanceIsolated() {');f.write_text(s)
 subprocess.run(['gofmt','-w',str(p/'tunnel/lane.go'),str(p/'tunnel/io.go')],check=True)
 r['variants'][variant]=hashes(p)
base=r['variants']['current'];assert base==hashes(source)
for name,v in r['variants'].items():
 expected={'current':[],'data_receive':['tunnel/lane.go'],'data_both':['tunnel/io.go','tunnel/lane.go']}[name]
 assert sorted(f for f in base if base[f]!=v[f])==expected
(out/'FAST_ROUTE_PROVENANCE.json').write_text(json.dumps(r,indent=2)+'\n')
