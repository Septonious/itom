from pathlib import Path
import hashlib,json,os,shutil,subprocess,sys
repo=Path(__file__).resolve().parents[4];source=repo/'archives/1.1.0/source/core';out=Path(sys.argv[1]);out.mkdir(parents=True,exist_ok=True)
def hashes(p):return {str(f.relative_to(p)):hashlib.sha256(f.read_bytes()).hexdigest()for f in sorted(p.rglob('*.go'))if not f.name.endswith('_test.go')}
baseline='6ade0182848652152fd8aa9af7a8078dfda27563'
subprocess.run(['git','diff','--exit-code',baseline,'HEAD','--','archives/1.1.0/source/core'],cwd=repo,check=True)
r={'runtime_baseline':baseline,'recipe_commit':os.environ.get('GITHUB_SHA'),'classification':'routing sensitivity; unchanged quota, proof, windows and deadlines','variants':{}}
for variant in ['current','primed_data','primed_quiet','primed_quiet_slots']:
 p=out/variant;shutil.copytree(source,p,ignore=shutil.ignore_patterns('*.test'))
 if variant!='current':
  f=p/'tunnel/lane.go';s=f.read_text();needle='\tif fastBatch && l.dataShards > 0 {\n\t\tfolder = l.sendFolder\n\t}\n';assert s.count(needle)==1;s=s.replace(needle,'');f.write_text(s)
 if variant!='current':
  f=p/'tunnel/io.go';s=f.read_text();needle='if fast && m.topologyCaps&capSplitData != 0 && len(raw) <= 64<<10 {';assert s.count(needle)==1;s=s.replace(needle,needle[:-1]+'&& !m.DataMaintenanceIsolated() {');f.write_text(s)
 if variant!='current':
  f=p/'tunnel/io.go';s=f.read_text();s=s.replace('dataReader    *mailbox.Client','dataReader    *mailbox.Client\n\tdataReaderWake chan struct{}',1)
  needle='\tm.readerHotRequestAt.Store(time.Now().UnixNano())'
  assert s.count(needle)==1
  s=s.replace(needle,needle+'\n\tif m.dataReaderWake != nil { select { case m.dataReaderWake <- struct{}{}: default: } }',1);f.write_text(s)
  f=p/'tunnel/topology.go';s=f.read_text();needle='\tm.dataReader = reader\n';assert s.count(needle)==1;s=s.replace(needle,needle+'\tm.dataReaderWake = make(chan struct{}, 1)\n',1)
  needle='func (m *laneMail) waitData(ctx context.Context, folder string, budget time.Duration) (used, notified bool, err error) {'
  assert s.count(needle)==1;s=s.replace(needle,needle+'\n\tif time.Now().UnixNano() < m.readerHotUntil.Load() { return false, false, nil }',1)
  needle='reader.IdleWaitWakeAhead(ctx, folder, budget, nil)';assert s.count(needle)==1;s=s.replace(needle,'reader.IdleWaitWakeAhead(ctx, folder, budget, m.dataReaderWake)',1);f.write_text(s)
 if variant in ['primed_quiet','primed_quiet_slots']:
  f=p/'tunnel/reader_reserve.go';s=f.read_text();needle='scores[l] = g.carrierLatencyLocked(l)';assert s.count(needle)==1;s=s.replace(needle,'scores[l] = 0 // experiment: matching stable account order at both endpoints');f.write_text(s)
 if variant=='primed_quiet_slots':
  f=p/'tunnel/grant_recovery.go';s=f.read_text();needle='\treturn limit, nil\n';assert s.count(needle)==1;s=s.replace(needle,'\tlimit.Messages = min(limit.Messages, max(uint64(1), limit.Bytes/dataBatchMIMEHardCap))\n'+needle).replace('available/5','available/2');f.write_text(s)
 changed=[f for f in (p/'tunnel').glob('*.go')if f.read_bytes()!=(source/'tunnel'/f.name).read_bytes()]
 if changed:subprocess.run(['gofmt','-w',*[str(f)for f in changed]],check=True)
 r['variants'][variant]=hashes(p)
base=r['variants']['current'];assert base==hashes(source)
for name,v in r['variants'].items():
 expected={'current':[],'primed_data':['tunnel/io.go','tunnel/lane.go','tunnel/topology.go'],'primed_quiet':['tunnel/io.go','tunnel/lane.go','tunnel/reader_reserve.go','tunnel/topology.go'],'primed_quiet_slots':['tunnel/grant_recovery.go','tunnel/io.go','tunnel/lane.go','tunnel/reader_reserve.go','tunnel/topology.go']}[name]
 assert sorted(f for f in base if base[f]!=v[f])==expected
(out/'FAST_ROUTE_PROVENANCE.json').write_text(json.dumps(r,indent=2)+'\n')
