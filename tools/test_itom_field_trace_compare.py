"""Synthetic tests for the offline ITOM field-trace analyzer."""
import io
import json
import tempfile
import unittest
import zipfile
from pathlib import Path

from itom_field_trace_compare import Analysis, alias, collect, compare, percentile


class TraceAnalyzerTests(unittest.TestCase):
    def test_percentile_empty_and_nearest_rank(self):
        self.assertIsNone(percentile([], 95))
        self.assertEqual(percentile([100, 200, 300, 400], 95), 400)

    def test_client_mail_not_double_counted_and_stale_not_failure(self):
        a = Analysis()
        line = {"AccountID": "account-a", "RTTMillis": 203,
                "RTTFresh": True, "IMAPLatencyMillis": 8,
                "ActiveLines": 3, "TotalLines": 3}
        a.ingest({"Event": "client_runtime", "SampledAtMS": 1000,
                  "Client": {"RTTMillis": 146, "Lines": [line]},
                  "Mail": {"Lines": [{"Account": "account-a", "rtt_ms": 203}]}}, "client")
        a.ingest({"Event": "client_runtime", "SampledAtMS": 3000,
                  "Client": {"RTTMillis": 146, "Lines": [
                      dict(line, RTTMillis=432, RTTFresh=False)]}}, "client")
        r = a.report()
        acct = r["accounts"][alias("account-a")]
        self.assertEqual(acct["observations"], 2)
        self.assertEqual(acct["fresh_observations"], 1)
        self.assertEqual(acct["stale_observations"], 1)
        self.assertEqual(acct["fresh_delivery_rtt_ms"]["p50"], 203)
        self.assertEqual(acct["imap_command_latency_ms"]["p50"], 8)
        self.assertEqual(r["displayed_global_rtt_ms_not_independent_probes"]["observations"], 2)
        self.assertEqual(r["explicit_probe_snapshot_rtt_ms"]["observations"], 0)

    def test_server_snapshot_and_recovery_metrics(self):
        a = Analysis()
        a.ingest({"Event": "runtime", "SampledAtMS": 2000,
                  "Mail": {"Lines": [
                      {"account": "account-b", "rtt_ms": 196, "rtt_fresh": True,
                       "imap_latency_ms": 8, "queued_mime_bytes": 65536,
                       "writer_recovering": True, "writer_reconnects": 2,
                       "control_backpressure": True, "peer_verified": False,
                       "ready": True}
                  ]}}, "server")
        r = a.report()["accounts"][alias("account-b")]
        self.assertEqual(r["fresh_delivery_rtt_ms"]["p50"], 196)
        self.assertEqual(r["queued_mime_bytes"]["p95"], 65536)
        self.assertEqual(r["writer_recovering_observations"], 1)
        self.assertEqual(r["control_backpressure_observations"], 1)
        self.assertEqual(r["peer_unverified_observations"], 1)
        self.assertEqual(r["max_writer_reconnects"], 2)

    def test_zip_capture_and_invalid_line(self):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "phone.zip"
            with zipfile.ZipFile(path, "w") as archive:
                archive.writestr("transport-trace.jsonl",
                    json.dumps({"Event": "client_runtime", "SampledAtMS": 1000,
                                "Client": {"Lines": [{"AccountID": "test",
                                    "RTTMillis": 150, "RTTFresh": True}]}})
                    + "\ninvalid-json\n")
                archive.writestr("diagnostics.json", json.dumps({
                    "transport": {"Probe": {"RTTMillis": 146, "Failure": ""},
                                  "SampledAtMS": 2000}}))
            r = collect([path], [])
            self.assertEqual(r["invalid_json_records"], 1)
            self.assertEqual(r["record_counts"]["client"], 2)
            self.assertEqual(r["accounts"][alias("test")]["fresh_observations"], 1)
            self.assertEqual(r["explicit_probe_snapshot_rtt_ms"]["p50"], 146)

    def test_delta_matched_by_private_alias(self):
        before = Analysis()
        after = Analysis()
        before.ingest({"Client": {"Lines": [{"AccountID": "id", "RTTMillis": 220,
                        "RTTFresh": True}]}}, "client")
        after.ingest({"Client": {"Lines": [{"AccountID": "id", "RTTMillis": 180,
                        "RTTFresh": True}]}}, "client")
        diff = compare(before.report(), after.report())
        self.assertEqual(diff[alias("id")]["fresh_delivery_rtt_ms_p50_delta"], -40)


if __name__ == "__main__":
    unittest.main()
