from pathlib import Path
import subprocess,json,zipfile,hashlib,difflib,tarfile,io
import argparse
p=argparse.ArgumentParser();p.add_argument('--output',type=Path,required=True);args=p.parse_args()
R=Path(__file__).resolve().parents[1]; O=args.output; O.mkdir(parents=True,exist_ok=True)
def git(*a): return subprocess.check_output(['git',*a],cwd=R)
base='v1.0.0-rc2:archives/1.0.0-rc2/source'
refs=[('RC3','v1.0.0-rc3'),('RC4','v1.0.0-rc4')]+[(f'RC{i}','48eb23389403c33f2566db6c86c329c4d398c7d1') for i in range(5,14)]+[('RC10.1','48eb23389403c33f2566db6c86c329c4d398c7d1')]
manifest={'baseline_commit':git('rev-parse','v1.0.0-rc2').decode().strip(),'latest_remote_commit':'48eb23389403c33f2566db6c86c329c4d398c7d1','latest_local_commit':git('rev-parse','48eb23389403c33f2566db6c86c329c4d398c7d1').decode().strip(),'scope':'All archived RC3-RC13 source deltas including RC10.1, exact git binary patches and file inventories; latest workflows/tools delta; historical audit evidence. Original historical binary artifacts remain on immutable tags/branches. No private signing material.','snapshots':[]}
with zipfile.ZipFile(O/'POST_RC2_CHANGES.zip','w',zipfile.ZIP_DEFLATED,compresslevel=9) as z:
 for label,ref in refs:
  path=f'archives/1.0.0-{label.lower()}/source'; tree=f'{ref}:{path}'
  treeid=git('rev-parse',tree).decode().strip(); names=git('diff','--name-only',base,tree).decode().splitlines(); patch=git('diff','--binary','--full-index',base,tree)
  entry={'release':label,'git_ref':ref,'tree':treeid,'files':names,'patch_sha256':hashlib.sha256(patch).hexdigest()};manifest['snapshots'].append(entry)
  z.writestr(f'patches/{label}-over-RC2.patch',patch)
  with tarfile.open(fileobj=io.BytesIO(git('archive',tree))) as t:
   entry['source_sha256']={m.name:hashlib.sha256(t.extractfile(m).read()).hexdigest() for m in t if m.isfile()}
 # Preserve the selected/rejected experiment ledger and every historical archived audit, including raw logs.
 for name in git('ls-tree','-r','--name-only','48eb23389403c33f2566db6c86c329c4d398c7d1','archives').decode().splitlines():
  if '/audit/' in name or name.endswith(('RC2_ONWARD_EXPERIMENT_LEDGER.md','RC2_TO_RC7_AUDIT.md')):
   z.writestr('evidence/'+name,git('show',f'HEAD:{name}'))
 z.writestr('workflow-and-tooling.patch',git('diff','--binary','--full-index','v1.0.0-rc2','48eb23389403c33f2566db6c86c329c4d398c7d1','--','.github','tools'))
 for ref in ['48eb23389403c33f2566db6c86c329c4d398c7d1','v1.0.0-rc3','v1.0.0-rc4']:
  z.writestr('history/'+ref.replace('/','_')+'.tsv',git('log','--all' if ref=='48eb23389403c33f2566db6c86c329c4d398c7d1' else ref,'--format=%H%x09%ad%x09%s','--date=iso-strict'))
 z.writestr('MANIFEST.json',json.dumps(manifest,indent=2))
 z.writestr('RESTORE.txt','Apply any patches/<RC>-over-RC2.patch to a clean extraction of v1.0.0-rc2:archives/1.0.0-rc2/source using git apply --binary. Verify every resulting file against MANIFEST.json. RC3/RC4 are separate tag histories; their deltas are included explicitly. Existing GitHub development branches and archives are retained. This is a code/evidence archive, not a deployment package.\n')
(O/'manifest.json').write_text(json.dumps(manifest,indent=2)+'\n')
print('archive bytes', (O/'POST_RC2_CHANGES.zip').stat().st_size, 'sha256',hashlib.sha256((O/'POST_RC2_CHANGES.zip').read_bytes()).hexdigest())
