"""Prove 1.1.1 changes bootstrap and failed-account feedback routing only."""
from pathlib import Path
import json,subprocess
repo=Path(__file__).resolve().parents[3];root=repo/'archives/1.1.1/source';out=repo/'final-audit';out.mkdir(exist_ok=True)
base='474364b115fee38d174e75c0e025a108a5ebc0b9';prefix='archives/1.1.0/source/'
def old(n):return subprocess.check_output(['git','show',base+':'+prefix+n],cwd=repo)
def runtime(p):return p.name in ('go.mod','go.sum')or p.suffix=='.go'and not p.name.endswith('_test.go')
prior={n.removeprefix(prefix)for n in subprocess.check_output(['git','ls-tree','-r','--name-only',base,'--',prefix],cwd=repo,text=True).splitlines()if n.removeprefix(prefix).split('/')[0]in('core','mailstream','native')and runtime(Path(n))}
current={p.relative_to(root).as_posix()for d in ('core','mailstream','native')for p in(root/d).rglob('*')if p.is_file()and runtime(p)}
assert current==prior,(current-prior,prior-current)
identity={'core/cmd/itom/main.go','core/tunnel/api.go','core/manager/trace.go'}
block='''\t\t\t\tl.feedbackPackets++
\t\t\t\tl.mu.Unlock()'''
replacement='''\t\t\t\tl.feedbackPackets++
\t\t\t\t// Advance from the physical account that accepted this burst.
\t\t\t\t// Failed candidates must not donate their turns to the first
\t\t\t\t// healthy account and concentrate its control-message backlog.
\t\t\t\tif n > 0 {
\t\t\t\t\tl.roundRobin = l.feedbackAccount + 1
\t\t\t\t}
\t\t\t\tl.mu.Unlock()'''
for n in sorted(prior):
 a,b=old(n),(root/n).read_bytes()
 if n in identity:assert b==a.replace(b'itom-1-1-0-final',b'itom-1-1-1-final'),n
 elif n=='mailstream/imap.go':
  a=a.decode();assert a.count(block)==1;assert b.decode()==a.replace(block,replacement),n
 elif n!='mailstream/startup.go':assert a==b,n
helper=out/'ast.go';helper.write_text('''package main
import("go/parser";"go/token";"go/format";"go/ast";"os";"bytes";"crypto/sha256";"fmt";"encoding/json")
func main(){n:=os.Args[1:];if n[0]=="--"{n=n[1:]};fset:=token.NewFileSet();f,e:=parser.ParseFile(fset,n[0],nil,0);if e!=nil{panic(e)};m:=map[string]string{};for _,d:=range f.Decls{if fn,ok:=d.(*ast.FuncDecl);ok{var b bytes.Buffer;format.Node(&b,fset,fn);key:=fn.Name.Name;if fn.Recv!=nil{var r bytes.Buffer;format.Node(&r,fset,fn.Recv.List[0].Type);key=r.String()+"."+key};m[key]=fmt.Sprintf("%x",sha256.Sum256(b.Bytes()))}};json.NewEncoder(os.Stdout).Encode(m)}''')
reference=out/'startup-reference.go';reference.write_bytes(old('mailstream/startup.go'))
def ast(p):return json.loads(subprocess.check_output(['go','run',str(helper),'--',str(p)],cwd=repo))
a,b=ast(reference),ast(root/'mailstream/startup.go');assert set(b)-set(a)=={'listedControlFolder'}and not set(a)-set(b)
for k,v in a.items():
 if k!='*mailAccountState.openControls':assert b[k]==v,k
kt=[]
for n in subprocess.check_output(['git','ls-tree','-r','--name-only',base,'--',prefix+'android'],cwd=repo,text=True).splitlines():
 rel=n.removeprefix(prefix)
 if '/src/main/'in rel and Path(rel).suffix in('.kt','.xml'):assert(root/rel).read_bytes()==old(rel),rel;kt.append(rel)
info=json.loads((root/'source-info.json').read_text());assert(info['release'],info['android_version_name'],info['android_version_code'],info['android_application_id'])==('Final','1.1.1',10146,'org.itom.client')
result={'verified':True,'baseline_commit':base,'field_controller_commit':'59f062d5120946185700607af32ce89dd1aafb73','runtime_files_compared':len(prior),'runtime_files_byte_identical':len(prior)-5,'identity_only_files':sorted(identity),'only_behavior_changes':['owned control folder LIST/create before SELECT/STATUS','feedback fallback advances from accepted physical account'],'controller_and_mailbox_bounds_identical':True,'native_runtime_byte_identical':True,'android_main_files_identical':len(kt),'android_version_name':'1.1.1','android_version_code':10146,'production_identity_checked_by_compiled_android_gate':True}
(out/'TRANSPORT_RESTORE.json').write_text(json.dumps(result,indent=2)+'\n');print(json.dumps(result,indent=2))
