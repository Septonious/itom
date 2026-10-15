from pathlib import Path
import json
w=Path('/workspace/work/rev5');d=json.loads((w/'candidate-validation.json').read_text())
print('source',d['source_sha256'],'complete',d['complete'],'passed gates',[s['name'] for s in d['steps'] if s['passed']])
print((w/'validation-progress.log').read_text().strip())
for root in ['field-repeated','field-commit-delay']:
 reports=sorted((w/root).glob('*/results.json'))
 matching=[]
 for p in reports:
  x=json.loads(p.read_text())
  if x['source_sha256']==d['source_sha256']:matching.append((p,x))
 if matching:
  p,x=matching[-1];print(root,'completed',[(r['repeat'],r['passed'],r['elapsed_seconds']) for r in x['runs']])
 dirs=sorted(p for p in (w/root).glob('*') if p.stat().st_mtime >= (w/'validation-cycle7.pid').stat().st_mtime)
 if dirs:
  p=dirs[-1];logs=sorted(p.glob('repeat-*.jsonl'))
  if logs:
   passed=0;failed=[];current=None
   for raw in logs[-1].open():
    try:e=json.loads(raw)
    except ValueError:continue
    t=e.get('Test','')
    if e.get('Action')=='run':current=t
    if e.get('Action')=='pass' and t and '/' not in t:passed+=1
    if e.get('Action')=='fail':failed.append(t)
   print(root,logs[-1].name,'passed cases',passed,'failures',failed,'current',current)
