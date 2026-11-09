"""Test the synthetic RTT reporting and percentile semantics without networking."""
import json
import tempfile
import unittest
from pathlib import Path
from rtt_report import latency_stats, build_report


class SyntheticRTTReporting(unittest.TestCase):
    def test_percentiles_are_measured_nearest_rank(self):
        samples = list(range(1, 101))
        s = latency_stats(samples)
        self.assertEqual((s["count"], s["median_ms"], s["p95_ms"], s["p99_ms"]),
                         (100, 50.5, 95, 99))
        self.assertEqual(latency_stats([])["p95_ms"], None)

    def test_real_echo_arrays_exported_in_all_phases(self):
        with tempfile.TemporaryDirectory() as temp:
            root = Path(temp)
            (root / "manifest.json").write_text(json.dumps({
                "rc116_latency_profile": "home-icmp",
                "rc116_client_imap_latency_ms": [3],
                "observed_home_icmp_reference_ms": 3}))
            for label in ("rc2", "candidate"):
                (root / (label + ".json")).write_text(json.dumps({
                    "success": True, "stage": "complete",
                    "IdleMS": [11.25, 17, 20], "LoadedMS": [90, 110],
                    "AfterMS": [13, 15],
                    "Phases": [
                        {"name": "upload", "upload": {"full_phase_mbps": 250}},
                        {"name": "download", "download": {"full_phase_mbps": 270}}]}))
            records = build_report(root)
            self.assertEqual(records[1]["rtt"]["idle"]["p95_ms"], 20)
            self.assertEqual(records[1]["rtt"]["loaded"]["median_ms"], 100)
            self.assertEqual(records[1]["throughput_mbps"]["upload"], 250)
            self.assertIn("20", (root / "SYNTHETIC_RTT_REPORT.csv").read_text())
            self.assertIn("3", (root / "SYNTHETIC_RTT_REPORT.json").read_text())


if __name__ == "__main__":
    unittest.main()
