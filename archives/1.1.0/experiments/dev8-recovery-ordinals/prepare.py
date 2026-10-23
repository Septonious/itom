from pathlib import Path
import hashlib,json,os,shutil,subprocess,sys
repo=Path(__file__).resolve().parents[4]
out=Path(sys.argv[1]);out.mkdir(parents=True,exist_ok=True)
source=repo/'archives/1.1.0/source/core'
def hashes(p):return {str(f.relative_to(p)):hashlib.sha256(f.read_bytes()).hexdigest()for f in sorted(p.rglob('*.go'))if not f.name.endswith('_test.go')}
record={'source_commit':os.environ.get('GITHUB_SHA'),'baseline_runtime':'3c09753204a5bc4c06246a51cb4b015d7f3d9b0a','thresholds_relaxed':False,'journal_wallet_pool_wire_production_unchanged':True,'variants':{}}
subprocess.run(['git','diff','--exit-code',record['baseline_runtime'],'HEAD','--','archives/1.1.0/source/core'],cwd=repo,check=True)
for variant in ['current','bulk_slots','split_slots']:
 p=out/variant;shutil.copytree(source,p,ignore=shutil.ignore_patterns('*.test'))
 f=p/'tunnel/grant_recovery.go';s=f.read_text()
 if variant!='current':
  needle='\treturn limit, nil\n'
  assert s.count(needle)==1
  s=s.replace(needle,'\t// Experimental: reserve ordinals for complete maximum-sized BODY copies.\n\tlimit.Messages = min(limit.Messages, max(uint64(1), limit.Bytes/dataBatchMIMEHardCap))\n'+needle)
 if variant=='split_slots':
  assert s.count('available/5')==1
  s=s.replace('available/5','available/2')
 f.write_text(s);subprocess.run(['gofmt','-w',str(f)],check=True)
 record['variants'][variant]=hashes(p)
base=record['variants']['current']
assert base==hashes(source),'current runtime was changed while preparing the comparison'
for name,v in record['variants'].items():
 changed=[f for f in base if v[f]!=base[f]]
 assert changed==([]if name=='current'else['tunnel/grant_recovery.go']),changed
(out/'ORDINAL_PROVENANCE.json').write_text(json.dumps(record,indent=2)+'\n')
