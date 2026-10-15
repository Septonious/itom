"""Save a resumable development checkpoint without user captures or build secrets."""
from pathlib import Path
import datetime
import hashlib
import json
import re
import shutil
import sys

work = Path('/workspace/work/rev5')
source = work / 'source/itom'
repo = work / 'publication'
archive = repo / 'archives/rev5'
checkpoint = archive / 'checkpoint'
sys.path.insert(0, str(source / 'tools'))
from transport_qualification import source_fingerprint

now = datetime.datetime.now(datetime.timezone.utc).isoformat()
identity = source_fingerprint()
status = dict(
    schema=1, classification='UNSTABLE_DEVELOPMENT_CHECKPOINT',
    release_ready=False, all_internal_gates_passed=False,
    created_utc=now, branch='itom-0.9.2-rev5',
    source_sha256=identity, active_validation=False,
    latest_focused_ramp_cases=6, latest_focused_ramp_passed=6,
    latest_focused_ramp_max_chat_rtt_us=770812,
    field_cases_required=32, full_final_qualification='NOT_COMPLETE',
    implemented=['transport repairs and field-derived tests',
                 'global and per-client messages', 'Minecraft-style message formatting'],
    pending=['fresh full transport qualification after the latest repairs',
             'darker, higher-contrast Android UI polish',
             'server-authorized admin tab and IMAP management RPC',
             'durable 60-second connection-loss rollback for reversible admin actions',
             'final full qualification, APK and Linux builds, Actions release'],
    constraints=dict(speed_min_mbps=90, loaded_rtt_strictly_below_ms=1100,
                     duplex_probes_per_case=30, failed_runs_may_not_be_waived=True,
                     irreversible_operations_cli_only=True),
    mobile_fatal_cause_confirmed=False, live_provider_tested=False,
    private_captures_included=False, signing_keys_included=False,
    stale_build_outputs_included=False,
    latest_failed_pipeline='cycle7: ramp chat exceeded 1100ms before the two latest fixes',
    historical_results_are_not_qualification_for_current_source=True,
)
(work/'STATUS.json').write_text(json.dumps(status, indent=2)+'\n')
handoff = work/'CURRENT_HANDOFF.md'
prefix = f'''# Network-recovery checkpoint — {now}

This block supersedes all older active-state and "not committed" statements below.
User explicitly requested immediate GitHub commit/push of all intermediary changes because of network problems.
Current source fingerprint: `{identity}`. **UNSTABLE; not a release; all internal gates have NOT passed.**
No validator is active. The six latest ramp cases in `ramp-after-split.jsonl` all passed (max chat RTT 770812us).
The full cycle7 run failed before the latest chat-window and split-affinity repairs; its evidence remains historical.
Fresh qualification is required. Required field cases:32. Android's last build passed77 domain/6 app tests but predates the latest transport repairs; do not release it.
Messages and colors are implemented. UI polish and authenticated admin RPC/60-second rollback remain unimplemented.
User confirmed irreversible operations must remain CLI-only. Complete transport first, then UI/admin, then all final gates.
All release limits remain90Mbps useful rates, every loaded RTT<1100ms,30/30 duplex probes with zero misses/timeouts in all6 SINGLE/MULTI runs.
Checkpoint is in `archives/rev5/checkpoint`, source in `archives/rev5/source`; repository root workflow skips publication when no release manifest exists.
Resume drivers are under checkpoint/drivers; they currently assume `/workspace/work/rev5` and the prior build-cache/toolchain paths. Restore editable source there and use `CURRENT_HANDOFF.md` for details.
Raw uploaded captures, private signing keys, toolchains, build caches and stale binaries/APK are deliberately excluded. Uploaded file references remain in the handoff for authorized re-download.
Update hardcoded31-case release-note text to32 (or the final test count) before final packaging. Do not treat archived aggregate JSON as current qualification.

'''
if not handoff.read_text().startswith('# Network-recovery checkpoint'):
    handoff.write_text(prefix+handoff.read_text())

excluded = {'.git','.gradle','.kotlin','__pycache__','build','dist','libs','reports','.idea'}
def ignore(directory, names):
    return [n for n in names if n in excluded or n=='local.properties'
            or n.endswith(('.pyc','.pyo','.test','.jks','.keystore'))]
mirror = archive/'source'
if mirror.exists():
    shutil.rmtree(mirror)
shutil.copytree(source, mirror, ignore=ignore)
checkpoint.mkdir(parents=True, exist_ok=True)
for name in ('STATUS.json','CURRENT_HANDOFF.md','field-correlation.json'):
    shutil.copy2(work/name, checkpoint/name)
drivers = checkpoint/'drivers'
drivers.mkdir(exist_ok=True)
for path in work.glob('*.py'):
    shutil.copy2(path, drivers/path.name)

def filtered_copy(path, destination):
    # Some Java tools echo proxy/trust-store options. Preserve useful build
    # evidence while dropping environment, credential and private-key lines.
    text = path.read_text(errors='replace')
    secret_line = re.compile(r'JAVA_TOOL_OPTIONS|(?:https?://)[^\s/@]+:[^\s/@]+@|(?:password|token|secret)\s*[=:]\s*[^\s,}]+',re.I)
    rows = [line for line in text.splitlines() if not secret_line.search(line)]
    text = '\n'.join(rows)+'\n'
    text = re.sub(r'-----BEGIN [^-]*PRIVATE KEY-----.*?-----END [^-]*PRIVATE KEY-----',
                  '[private key removed]', text, flags=re.S)
    destination.parent.mkdir(parents=True,exist_ok=True)
    destination.write_text(text)

evidence = checkpoint/'evidence'
for path in list(work.glob('*.log'))+list(work.glob('*.jsonl')):
    filtered_copy(path, evidence/path.name)
for path in (work/'candidate-validation.json',):
    filtered_copy(path,evidence/path.name)
for directory in work.iterdir():
    if not directory.is_dir() or not (directory.name.startswith('validation-cycle') or directory.name in ('field-repeated','field-commit-delay','speed-final')):
        continue
    for path in directory.rglob('*'):
        if path.is_file() and path.suffix in ('.json','.jsonl','.log','.md','.txt'):
            filtered_copy(path, evidence/path.relative_to(work))

files = {str(p.relative_to(mirror)):hashlib.sha256(p.read_bytes()).hexdigest()
         for p in sorted(mirror.rglob('*')) if p.is_file()}
(checkpoint/'SOURCE_FILES_SHA256.json').write_text(json.dumps(files,indent=2)+'\n')
(checkpoint/'README.md').write_text('''# Rev5 unstable development checkpoint

This is a recovery snapshot requested during session network trouble. No Rev5 release is authorized by this snapshot: full current-source qualification is incomplete.

- `../source/` contains the exact editable source, tests and build tools.
- `STATUS.json` records implemented work, pending work and acceptance limits.
- `CURRENT_HANDOFF.md` contains the detailed resume instructions and historical diagnoses. Its newest block overrides older status statements.
- `drivers/` preserves workspace validation and packaging scripts. Paths and toolchain/cache configuration must be adapted when restoring to another machine.
- `evidence/` preserves intermediate passes and failures. These are historical development results, not proof that the current source passed every release gate.
- `SOURCE_FILES_SHA256.json` hashes every saved source file.

Private user captures, signing keys, caches and stale APK/binaries are excluded. Irreversible mailbox purge and uninstall remain CLI-only in the requested future admin interface.
''')
print(json.dumps(dict(source_sha256=identity,files=len(files),checkpoint=str(checkpoint))))
