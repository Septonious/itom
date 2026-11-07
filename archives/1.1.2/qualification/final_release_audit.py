"""Prove the recovery fix preserves the accepted1.1.1 data path and hard bounds."""
from pathlib import Path
import subprocess,json
repo=Path(__file__).resolve().parents[3];root=repo/'archives/1.1.2/source';out=repo/'final-audit';out.mkdir(exist_ok=True)
base='6ccdb6d2a408dcddadd0ea26d1cc840c6acb350a';prefix='archives/1.1.1/source/'
def old(n):return subprocess.check_output(['git','show',base+':'+prefix+n],cwd=repo)
def runtime(p):return p.name in ('go.mod','go.sum')or p.suffix=='.go'and not p.name.endswith('_test.go')
prior={n.removeprefix(prefix)for n in subprocess.check_output(['git','ls-tree','-r','--name-only',base,'--',prefix],cwd=repo,text=True).splitlines()if n.removeprefix(prefix).split('/')[0]in('core','mailstream','native')and runtime(Path(n))}
current={p.relative_to(root).as_posix()for d in ('core','mailstream','native')for p in(root/d).rglob('*')if p.is_file()and runtime(p)};assert prior==current
identity={'core/cmd/itom/main.go','core/tunnel/api.go','core/manager/trace.go'}
needle='if recovering && !idle {\n\t\t// Finish the frozen pre-session scope before chasing live control tails.'
replacement='''if recovering && !idle && retry == "" {
		// Resolve a quarantined live prefix before freezing historical work.
		// Its current authenticated watermark covers the failed attempt; using
		// an already-covered startup fence can skip the retry forever and also
		// prevent the historical LIST which would release recovered capacity.
		// Finish the frozen pre-session scope before chasing live control tails.'''
for n in sorted(prior):
 a,b=old(n),(root/n).read_bytes()
 if n=='core/cmd/itom/main.go':
  a=a.replace(b'itom-1-1-1-final',b'itom-1-1-2-final').replace(b'ResetCapacityState',b'VerifyCapacityState').replace(b'PrepareCapacityPolicy',b'VerifyCapacityState').replace(b'resetErr',b'verifyErr');assert a==b,n
 elif n in identity:assert b==a.replace(b'itom-1-1-1-final',b'itom-1-1-2-final'),n
 elif n=='core/manager/manager.go':
  a=a.replace(b'ResetCapacityState',b'VerifyCapacityState').replace(b'out io.Writer, reset ...func() error',b'out io.Writer, verifyStopped ...func() error').replace(b'len(reset) > 0',b'len(verifyStopped) > 0').replace(b'reset[0]()',b'verifyStopped[0]()');assert a==b,n
 elif n=='core/manager/accounts.go':
  a=a.replace(b'ResetCapacityState',b'VerifyCapacityState').replace(b'\t' + b'fmt.Fprintln(u.Out, "Local server capacity counters reset.")\n',b'').replace(b'ITOM configuration is preserved. Local capacity counters are reset for this purge attempt. Required folders are recreated at startup.',b'ITOM configuration is preserved. Required folders are recreated at startup.').replace(b'ITOM configuration is preserved; local capacity counters are reset.',b'ITOM configuration is preserved.');assert a==b,n
 elif n=='core/manager/capacity.go':
  a=a.decode();a=a[:a.index('// An update performed by an older manager')].rstrip()+'\n';a=a.replace('\n\t"encoding/binary"','').replace('ResetCapacityState','VerifyCapacityState')
  before='// VerifyCapacityState is called only after service shutdown, or before the\n// first start of the explicit-reset policy. It never edits profiles, logins,\n// server identity, provider messages, or the retired transport\'s databases.\n// bbolt\'s exclusive lock prevents a reset from racing a live transport.'
  after='// VerifyCapacityState checks stopped transport ledgers without changing debt.\n// A stop, update, failed purge or first start is not proof of mailbox deletion.\n// Only server-owned collection may refund physical retention. The database lock\n// prevents verification from racing a live transport.'
  assert before in a;a=a.replace(before,after).replace('bolt.Options{Timeout: time.Second}','bolt.Options{Timeout: time.Second, ReadOnly: true}').replace('db.Update(', 'db.View(').replace('\t\t\t\tvar keys [][]byte\n','').replace('\t\t\t\t\t\tkeys = append(keys, bytes.Clone(k))\n','')
  start=a.index('\t\t\t\tfor _, key := range keys {');end=a.index('\t\t\t\treturn nil\n',start);a=a[:start]+a[end:];assert a.encode()==b,n
 elif n=='mailstream/maintenance.go':
  a=a.decode();assert a.count(needle)==1;a=a.replace(needle,replacement)
  before='owned, deferred, fence, removed, e := mutationPage(rows, target.UID, messageLimits...)'
  after='// Closed historical DATA slots have no live producer. An explicitly\n\t// healthy maintenance turn can use the same 16 MiB first-page ceiling as\n\t// current DATA, while unknown/large folders and failed accounts retain\n\t// their conservative 4 MiB and 32-message retry policy.\n\tpageBytes := int64(collectionPageBytes)\n\tif whole && selected.NumMessages <= segmentAppends && len(messageLimits) > 0 && messageLimits[0] > collectionRetryMessages {\n\t\tpageBytes = collectionFirstPageBytes\n\t}\n\towned, deferred, fence, removed, e := mutationPageBounded(rows, target.UID, pageBytes, messageLimits...)'
  assert a.count(before)==1;assert b.decode()==a.replace(before,after),n
 elif n=='mailstream/control_storage.go':
  a=a.decode();a=a.replace('// A quarter of the finite physical budget remains available when historical\n// recovery is requested. Deferral never changes the persisted debt.','// Substantial inherited debt requests proactive recovery before bulk exhausts\n// the account. Deferral never changes the persisted debt or hard byte cap.')
  before_condition='func (a *mailAccountState) storagePressureLocked() bool {\n\tif a.spent+a.queuedBytes < prototypePhysicalBudget*3/4 {'
  new='func (a *mailAccountState) storagePressureLocked() bool {\n\t// Reconcile substantial inherited DATA debt while the other three quarters\n\t// of the account can still carry traffic. Waiting until 75% occupancy makes\n\t// a short first upload exhaust accounts before historical work can finish.\n\t// Small crash reservations still avoid an unnecessary namespace sweep.\n\tif a.recovered >= prototypePhysicalBudget/4 {\n\t\treturn true\n\t}\n\tif a.spent+a.queuedBytes < prototypePhysicalBudget*3/4 {'
  assert a.count(before_condition)==1;assert b.decode()==a.replace(before_condition,new),n
 elif n=='mailstream/reclamation_coordinator.go':
  before='\t\t\t// A bounded preference lets one old reservation become reusable sooner.\n\t\t\t// If its actor is busy, expiry permits ordinary FIFO work to continue.\n\t\t\tif chosen < 0 {\n\t\t\t\treturn false\n\t\t\t}'
  after='\t\t\t// Prefer a recovery owner only among actors actually requesting work.\n\t\t\t// Its returned writer and its 500 ms scheduling delay must not reserve an\n\t\t\t// idle global lease while other accounts can release physical capacity.\n\t\t\tif chosen < 0 {\n\t\t\t\tchosen = 0\n\t\t\t}'
  a=a.decode();assert a.count(before)==1;assert b.decode()==a.replace(before,after),n
 else:assert a==b,n
kt=[]
for n in subprocess.check_output(['git','ls-tree','-r','--name-only',base,'--',prefix+'android'],cwd=repo,text=True).splitlines():
 rel=n.removeprefix(prefix)
 if '/src/main/'in rel and Path(rel).suffix in('.kt','.xml'):assert(root/rel).read_bytes()==old(rel),rel;kt.append(rel)
info=json.loads((root/'source-info.json').read_text());assert(info['release'],info['android_version_name'],info['android_version_code'],info['android_application_id'])==('Final','1.1.2',10147,'org.itom.client')
r={'verified':True,'baseline_commit':base,'field_controller_commit':'59f062d5120946185700607af32ce89dd1aafb73','runtime_files_compared':len(prior),'runtime_files_byte_identical':len(prior)-9,'identity_only_files':sorted(identity-{'core/cmd/itom/main.go'}),'only_behavior_changes':['service lifecycle validates retention ledgers read-only; only audited server collection refunds debt','use existing16MiB first-page ceiling for explicitly healthy closed historical DATA slots; retain4MiB and32-message retries','never reserve an idle mutation lease for a nonrequesting recovery owner','request proactive recovery for inherited debt of at least one quarter of the existing budget','resolve quarantined control prefixes using current authenticated watermarks before freezing historical recovery'],'service_lifecycle_preserves_uncollected_debt':True,'controller_and_mailbox_bounds_identical':True,'cleanup_commands_and_byte_ceiling_identical':True,'historical_healthy_first_page_uses_existing_data_ceiling':True,'native_runtime_byte_identical':True,'android_main_files_identical':len(kt),'android_version_name':'1.1.2','android_version_code':10147}
(out/'TRANSPORT_RESTORE.json').write_text(json.dumps(r,indent=2)+'\n');print(json.dumps(r,indent=2))
