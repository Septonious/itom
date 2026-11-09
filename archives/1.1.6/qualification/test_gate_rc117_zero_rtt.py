"""Unit tests for the fail-closed released-RC1 comparison gates."""
import json
from pathlib import Path
import tempfile
import unittest

from gate_rc117_zero_rtt import check


def example_report(rtt_increase=0.0, throughput_ratio=1.0):
    def row(delta):
        return {
            "count": 40,
            "minimum_ms": 10.0 + delta,
            "median_ms": 20.0 + delta,
            "p95_ms": 40.0 + delta,
            "p99_ms": 50.0 + delta,
            "maximum_ms": 60.0 + delta,
        }
    def entry(delta, ratio):
        return {
            "completed": True,
            "rtt": {phase: row(delta) for phase in ("idle", "loaded", "after")},
            "throughput_mbps": {"download": 400.0 * ratio, "upload": 300.0 * ratio},
        }
    return {"results": [entry(0, 1), entry(rtt_increase, throughput_ratio)]}


class ZeroRTTGateTest(unittest.TestCase):
    def run_gate(self, *, increase=0.0, ratio=1.0):
        with tempfile.TemporaryDirectory() as directory:
            folders = []
            for number in range(3):
                folder = Path(directory) / str(number)
                folder.mkdir()
                (folder / "SYNTHETIC_RTT_REPORT.json").write_text(
                    json.dumps(example_report(increase, ratio))
                )
                folders.append(folder)
            check(folders, "unit-test")

    def test_equal_rtt_and_75_percent_throughput_accepted(self):
        self.run_gate(ratio=0.75)

    def test_any_positive_rtt_rejected(self):
        with self.assertRaisesRegex(RuntimeError, "zero-positive-RTT"):
            self.run_gate(increase=0.001)

    def test_more_than_25_percent_speed_loss_rejected(self):
        with self.assertRaisesRegex(RuntimeError, "75%-throughput"):
            self.run_gate(ratio=0.749)

    def test_requires_three_trials(self):
        with self.assertRaisesRegex(ValueError, "three independent"):
            check([], "unit-test")


if __name__ == "__main__":
    unittest.main()
