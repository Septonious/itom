from pathlib import Path
import hashlib,json,os,shutil,subprocess,sys
repo=Path(__file__).resolve().parents[4];out=Path(sys.argv[1]);out.mkdir(parents=True,exist_ok=True)
subprocess.run(['python3',str(repo/'archives/1.1.0/experiments/field-feedback/prepare.py'),str(out)],check=True)
(out/'dev8').mkdir()
archive=subprocess.Popen(['git','archive','6ade0182848652152fd8aa9af7a8078dfda27563','archives/1.1.0/source/core'],cwd=repo,stdout=subprocess.PIPE)
subprocess.run(['tar','-x','-C',str(out/'dev8'),'--strip-components=4'],stdin=archive.stdout,check=True);archive.stdout.close();assert archive.wait()==0
hashes=lambda p:{str(f.relative_to(p)):hashlib.sha256(f.read_bytes()).hexdigest()for f in sorted(p.rglob('*.go'))if not f.name.endswith('_test.go')}
variants=['rc2','dev8','model'];before={v:hashes(out/v)for v in variants}
shared=['imap_test.go','field_command_service_test.go','field_sequence_itomtest_test.go','dev7_field_throughput_test.go','field_idle_itomtest_test.go','dev8_startup_itomtest_test.go','supervisor_fixture_test.go','dev9_field_itomtest_test.go']
for v in ['rc2','dev8']:
 for n in shared:shutil.copyfile(out/'model/tunnel'/n,out/v/'tunnel'/n)
assert before=={v:hashes(out/v)for v in variants}
(out/'DEV9_PROVENANCE.json').write_text(json.dumps({'source_commit':os.environ.get('GITHUB_SHA'),'dev8_runtime':'6ade0182848652152fd8aa9af7a8078dfda27563','rc2_runtime':'v1.0.0-rc2','production':before,'fixtures':{n:hashlib.sha256((out/'model/tunnel'/n).read_bytes()).hexdigest()for n in shared},'classification':'captured-capacity/topology sensitivity; not exact provider or device replay','thresholds_relaxed':False},indent=2)+'\n')
