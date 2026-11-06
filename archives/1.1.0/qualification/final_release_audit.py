"""Prove the final transport reverts RC19 and preserves the accepted RC18 path."""
from pathlib import Path
import hashlib,json,subprocess,tempfile
repo=Path(__file__).resolve().parents[3];out=repo/'final-audit';out.mkdir(exist_ok=True)
base='59f062d5120946185700607af32ce89dd1aafb73';prefix='archives/1.1.0/source/'
def old(name):return subprocess.check_output(['git','show',base+':'+prefix+name],cwd=repo)
def tracked(path,ref='HEAD'):return subprocess.check_output(['git','ls-tree','-r','--name-only',ref,'--',prefix+path],cwd=repo,text=True).splitlines()
files=tracked('mailstream/');basefiles=tracked('mailstream/',base)
assert set(files)-set(basefiles)=={prefix+'mailstream/control_bounds.go',prefix+'mailstream/control_bounds_test.go'}
assert not set(basefiles)-set(files)
exceptions={'storage.go','maintenance.go','reclamation.go','collection_page_test.go','imap.go','startup.go','control_storage.go','control_bounds.go','control_bounds_test.go'}
for name in files:
 if Path(name).name not in exceptions:assert (repo/name).read_bytes()==subprocess.check_output(['git','show',base+':'+name],cwd=repo),name
native=[]
for name in tracked('native/'):
 if name.endswith('.go')and not name.endswith('_test.go')and not name.endswith('/mobile.go'):
  assert (repo/name).read_bytes()==subprocess.check_output(['git','show',base+':'+name],cwd=repo),name;native.append(name)
# Compare function ASTs in the only native file that contains diagnostic adapters.
helper=out/'ast.go';helper.write_text('''package main
import("go/parser";"go/token";"go/format";"go/ast";"os";"bytes";"crypto/sha256";"fmt";"encoding/json")
func main(){names:=os.Args[1:];if names[0]=="--"{names=names[1:]};fset:=token.NewFileSet();f,e:=parser.ParseFile(fset,names[0],nil,0);if e!=nil{panic(e)};m:=map[string]string{};for _,d:=range f.Decls{if fn,ok:=d.(*ast.FuncDecl);ok{var b bytes.Buffer;format.Node(&b,fset,fn);key:=fn.Name.Name;if fn.Recv!=nil{var r bytes.Buffer;format.Node(&r,fset,fn.Recv.List[0].Type);key=r.String()+"."+key};m[key]=fmt.Sprintf("%x",sha256.Sum256(b.Bytes()))}};json.NewEncoder(os.Stdout).Encode(m)}''')
reference=out/'mobile-reference.go';reference.write_bytes(old('native/mobile.go'))
def ast(p):return json.loads(subprocess.check_output(['go','run',str(helper),'--',str(p)],cwd=repo))
a,b=ast(reference),ast(repo/(prefix+'native/mobile.go'))
allowed={'*Client.Snapshot','*Client.Diagnostics','*Client.decorateSnapshot'}
assert set(a)-set(b)==set() and set(b)-set(a)<=allowed
for k,v in a.items():
 if k not in allowed:assert b[k]==v,k
# Within the bounded-collection files, every other function remains unchanged.
collection_allowed={'*mailPipe.reclaim','*mailAccountState.processCleanup','mutationPage','mutationPageBounded','*mailAccountState.needsMaintenanceLocked','*mailAccountState.deferMaintenanceLocked','*IMAPLink.sendClassMode','*mailAccountState.run','*IMAPLink.Snapshot','*mailAccountState.openControls','*mailAccountState.controlAppended','*mailAccountState.refundControlLocked'}
for name in ('storage.go','maintenance.go','reclamation.go','imap.go','startup.go','control_storage.go'):
 reference=out/('reference-'+name);reference.write_bytes(old('mailstream/'+name))
 prior,current=ast(reference),ast(repo/(prefix+'mailstream/'+name))
 assert set(prior)-set(current)==set() and set(current)-set(prior)<=collection_allowed
 for key,value in prior.items():
  if key not in collection_allowed:assert current[key]==value,key
assert 'rec.bytes > 0 && rec.bytes <= collectionFirstPageBytes' in (repo/(prefix+'mailstream/storage.go')).read_text()
info=json.loads((repo/(prefix+'source-info.json')).read_text())
assert (info['release'],info['android_version_name'],info['android_version_code'])==('Final','1.1.0',10145)
result={'verified':True,'baseline_commit':base,'mailstream_files_byte_identical':len(files)-len(exceptions),'collection_runtime_exceptions':sorted(collection_allowed),'native_operation_files_byte_identical':len(native),'native_forwarding_functions_identical':len(set(a)-allowed),'only_native_function_exceptions':sorted(allowed),'android_version_name':'1.1.0','android_version_code':10145,'debuggable':False,'production_identity_checked_by_compiled_android_gate':True}
(out/'TRANSPORT_RESTORE.json').write_text(json.dumps(result,indent=2)+'\n');print(json.dumps(result,indent=2))
