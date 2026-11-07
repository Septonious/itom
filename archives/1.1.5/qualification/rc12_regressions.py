"""Replay blocked existing-folder CREATE and temporary account loss on RC11."""
import argparse
import json
import re
import subprocess
from pathlib import Path

p = argparse.ArgumentParser()
p.add_argument('--reference', type=Path, required=True)
p.add_argument('--out', type=Path, required=True)
a = p.parse_args()
source = Path(__file__).resolve().parents[1] / 'source'
a.out.mkdir(parents=True, exist_ok=False)
body = (source/'mailstream/startup_field_test.go').read_text()
# Startup attempt counters are new diagnostics, not part of either scenario.
# Keep the same provider faults, HTTPS requests and full attachment assertion.
body = re.sub(r'\n\tfor _, link := range \[\]\*IMAPLink\{client, server\} \{.*?\n\trequest\(\)', '\n\trequest()', body, flags=re.S)
(a.reference/'mailstream/startup_field_test.go').write_text(body)
results = {}
for label, root, expected in [('rc11_existing_folders', a.reference, 'existing folders did not start'),
                              ('rc11_temporary_account', a.reference, 'accounts never rejoined'),
                              ('candidate_account_recovery', source, None)]:
    pattern = '^Test(ExistingControlFoldersStartWithoutRedundantCreate|UnavailableAccountJoinsRunningHTTPSWithoutVPNRestart)$'
    if label == 'rc11_existing_folders': pattern = '^TestExistingControlFoldersStartWithoutRedundantCreate$'
    if label == 'rc11_temporary_account': pattern = '^TestUnavailableAccountJoinsRunningHTTPSWithoutVPNRestart$'
    r = subprocess.run(['go', 'test', '-json', '-count=1', '-timeout=35s', '-run', pattern, '.'],
                       cwd=root/'mailstream', stdout=subprocess.PIPE, stderr=subprocess.STDOUT, text=True, timeout=180)
    (a.out/(label+'.jsonl')).write_text(r.stdout)
    if expected: assert r.returncode != 0 and expected in r.stdout, r.stdout[-4000:]
    else: assert r.returncode == 0, r.stdout[-4000:]
    results[label] = {'verified': True, 'exit_status': r.returncode, 'expected_reference_failure': bool(expected)}
(a.out/'REGRESSION_RESULTS.json').write_text(json.dumps(results, indent=2)+'\n')
print(json.dumps(results, indent=2))
