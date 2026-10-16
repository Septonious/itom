#!/usr/bin/env python3
"""Repeat the field-failure application suite; preserve failures and source identity."""
import argparse
import hashlib
import json
import os
from pathlib import Path
import subprocess
import time

from rework_gate import runtime_fingerprint, summarize_events
from transport_qualification import source_fingerprint

ROOT = Path(__file__).resolve().parents[1]
REQUIRED = (
    'TestRev7RetainedSmallBodiesKeepBothDirectionalOrdinalHeadrooms',
    'TestRev7OrderedAdmissionCancelledBeforePhysicalWorkReleasesFlight',
    'TestRev7FullJournalWaitsForRealCapacityWithoutSelfWake',
    'TestRev7RetainedOutboundCleanupStartsBeforeSmallWindowIsFull',
    'TestRev7ReducedBulkLeaseCannotImpersonateFastGrant',
    'TestRev7RetainedDownloadCannotTakeUploadRefillShare',
    'TestRev7ControlAndReceiveRepairBypassQueuedOptionalLogins',
    'TestRev7RetainedDebtAllowsConsecutiveBulkBodiesWithoutGrantRoundTrips',
    'TestRev7CappedMixedSocketsRepairWithoutLosingOriginalStream',
    'TestRev7CorrelatedControlEOFBetweenTransfersKeepsEveryCarrier',
    'TestRev7AcceptedControlWithLostCompletionRetriesFreshAndKeepsDebt',
    'TestRev7CloseDuringControlRepairCannotPublishOrLeakSocket',
    'TestRev7LostHandshakeReplyDoesNotDelayLastAccountOrFirstOpen',
    'TestRev7MissingIDLENotificationsBoundActiveChatLatency',
    'TestRev6IdleAccountsKeepSocketsAndNextTCPBurstUsable',
    'TestRev6HealthyAccountsActivateWhileOtherLoginsFailAndExtrasStall',
    'TestRev4FieldSparseUIDHistoryCannotStarveNewDATA',
    'TestRev4HTTPPhasesKeepMessagesUsable',
    'TestRev4DelayedBodiesAndReaderRestartsKeepEveryTCPStream',
    'TestRev4RetainedStateMixedTrafficAndNextTransfer',
    'TestRev4InterruptedBulkCannotStrandApplicationAfterRejoin',
    'TestRev4ParallelApplicationStartupOnMobilePath',
    'TestRev4LongBlackoutPreservesInflightTCPAndMessages',
    'TestRev4LogicalOwnerAnswersAfterPhysicalCarrierCancellation',
    'TestRev4OpenUsesSelectedCarrierWithRetainedLogicalActor',
    'TestRev4OldEnvelopeCannotInterruptActivationInControlMailbox',
    'TestRev4AuthenticatedRecoveryCannotSleepPastProviderReturn',
    'TestRev4RepairMeasuresRecoveredPathWithoutRefundingFlight',
    'TestRev4DelayedStreamCannotBlockHealthyStreamPrefix',
    'TestRev4HealthyProgressDoesNotTreatVisibilityTailAsGlobalQueue',
    'TestRev5Historical180SlotsBootstrapWithoutForgivingDebt',
    'TestRev5Historical180SlotsTLSFifteenAccounts',
    'TestRev5PhysicalReceiptsBoundPacedBodiesAndRecover',
    'TestRev5PacedAccountDoesNotShrinkHealthyPoolOrRTT',
    'TestRev5ProviderStateRetryDoesNotWeakenUIDProof',
    'TestRev5PacedBodiesYieldWithoutRestartingHealthyReceiver',
    'TestRev5PerSocketPacingCalibration',
    'TestRev5PacedDownloadKeepsApplicationsAndNextPrefixUsable',
    'TestRev5FastLargeBodyPagesKeepAmortizedFetches',
    'TestRev5ProbePhasePreservesCountClockAndRejectsUnloadedDirection',
    'TestRev5PeerReadSeparatesUnloadedPropagationFromBodyPacing',
    'TestRev5SmallBodiesKeepBatchingOnMobilePropagation',
    'TestRev5KnownPeerBodiesBoundColdAdmissionWithoutBlockingUnknown',
    'TestRev5KnownPeerLeaseExpiryWakesWithoutExternalTraffic',
    'TestRev5AccountDATARecoverySharesAuthenticatedBudget',
    'TestRev5RampAbortAndRestartKeepOriginalChatUsable',
    'TestServerNoticeBroadcastAcrossSessions',
    'TestRev5SplitBulkDoesNotDisplacePrimedShortReader',
)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--go', default='go')
    parser.add_argument('--repeats', type=int, default=3)
    parser.add_argument('--commit-delay-ms', type=int, default=0)
    parser.add_argument('--results', type=Path, required=True)
    args = parser.parse_args()
    if not 1 <= args.repeats <= 5 or not 0 <= args.commit_delay_ms <= 25:
        parser.error('repeats must be 1..5; commit delay must be 0..25 ms')
    env = os.environ.copy()
    env.update(GOMAXPROCS='2', GOWORK='off', GOFLAGS='-mod=readonly', GOTOOLCHAIN='local')
    # The suite specifies each application/fault model itself. Old exploratory
    # performance options must not silently alter its workload or suppress IO.
    for key in list(env):
        if key.startswith(('ITOM_LOAD_', 'ITOM_FIELD_', 'ITOM_REV4_TEST_')):
            del env[key]
    env['ITOM_LOAD_SERVER_COMMIT_US'] = str(args.commit_delay_ms * 1000)
    version = subprocess.run([args.go, 'version'], env=env, capture_output=True, text=True, check=True)
    if 'go1.27.1 ' not in version.stdout:
        parser.error('Go 1.27.1 required')
    out = args.results.resolve() / (time.strftime('%Y%m%dT%H%M%S', time.gmtime()) + '-' + str(os.getpid()))
    out.mkdir(parents=True, exist_ok=False)
    report = {'schema': 1, 'classification': 'FIELD_FAILURE_LABORATORY_REPRODUCTION',
              'runtime_sha256': runtime_fingerprint(), 'source_sha256': source_fingerprint(),
              'go': version.stdout.strip(), 'release': json.loads((ROOT / 'source-info.json').read_text()),
              'real_provider_tested': False, 'android_device_tested': False,
              'tests_required': list(REQUIRED), 'server_added_commit_delay_ms': args.commit_delay_ms,
              'criteria': 'All cases execute and pass; verified application payloads, bounded startup/recovery, no unexpected carrier exits.',
              'runs': []}
    for repeat in range(1, args.repeats + 1):
        before = source_fingerprint()
        log = out / ('repeat-%d.jsonl' % repeat)
        command = [args.go, 'test', '-mod=readonly', '-json', '-count=1', '-timeout=480s']
        if args.commit_delay_ms:
            command += ['-tags=itomtest']
        command += ['./tunnel', '-run', '^(' + '|'.join(REQUIRED) + ')$']
        print('RUN field suite', repeat, flush=True)
        started = time.monotonic()
        with log.open('w') as stream:
            result = subprocess.run(command, cwd=ROOT / 'core', env=env, stdout=stream, stderr=subprocess.STDOUT)
        after = source_fingerprint()
        summary = summarize_events(log, REQUIRED)
        passed = (result.returncode == 0 and before == after == report['source_sha256'] and not summary['failed']
                  and not summary['skipped'] and not summary['missing_required'])
        report['runs'].append({'repeat': repeat, 'exit_code': result.returncode,
                               'elapsed_seconds': round(time.monotonic()-started, 3),
                               'source_before': before, 'source_after': after,
                               'log': log.name, 'log_sha256': hashlib.sha256(log.read_bytes()).hexdigest(),
                               'summary': summary, 'passed': passed})
        report['all_cases_passed'] = all(r['passed'] for r in report['runs'])
        (out / 'results.json').write_text(json.dumps(report, indent=2) + '\n')
        print('RESULT field suite', repeat, 'PASS' if passed else 'FAIL', flush=True)
    print('EVIDENCE', out / 'results.json', flush=True)
    raise SystemExit(0 if report['all_cases_passed'] else 1)


if __name__ == '__main__':
    main()
