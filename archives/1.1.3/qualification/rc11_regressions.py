"""Reproduce RC10 slow-receipt limitation and verify upload rate protection."""
import argparse,json,subprocess
from pathlib import Path
p=argparse.ArgumentParser();p.add_argument('--reference',type=Path,required=True);p.add_argument('--out',type=Path,required=True);a=p.parse_args()
source=Path(__file__).resolve().parents[1]/'source';a.out.mkdir(parents=True,exist_ok=False)
body=(source/'mailstream/delivery_slow_receipt_test.go').read_text().replace('w := deliveryWindow{adaptive: true}','var w deliveryWindow')
(a.reference/'mailstream/delivery_slow_receipt_test.go').write_text(body)
results={}
for label,root,pattern,expected in [
 ('rc10_slow_receipts',a.reference,'^TestSaturatedUploadWithSlowBulkReceipts$','bulk stayed self-limited'),
 ('candidate_slow_receipts_and_compression',source,'^Test(SaturatedUploadWithSlowBulkReceipts|CompressedBulkReceiptsKeepSlowLinkFlightBounded|BulkBandwidthRestartsAfterFastLightProbes)$',None)]:
 r=subprocess.run(['go','test','-json','-count=1','-timeout=20s','-run',pattern,'.'],cwd=root/'mailstream',stdout=subprocess.PIPE,stderr=subprocess.STDOUT,text=True,timeout=180)
 (a.out/(label+'.jsonl')).write_text(r.stdout)
 if expected:assert r.returncode!=0 and expected in r.stdout,r.stdout[-2000:]
 else:assert r.returncode==0,r.stdout[-4000:]
 results[label]={'verified':True,'exit_status':r.returncode,'expected_reference_failure':bool(expected)}
(a.out/'REGRESSION_RESULTS.json').write_text(json.dumps(results,indent=2)+'\n');print(json.dumps(results,indent=2))
