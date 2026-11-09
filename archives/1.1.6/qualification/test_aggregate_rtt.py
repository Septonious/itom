"""Aggregation tests for measured synthetic RTT and per-direction bandwidth."""
import json
import tempfile
import unittest
from pathlib import Path
from aggregate_rtt import aggregate

class AggregateRTTTests(unittest.TestCase):
    def make(self,root,i,profile="home",candidate_speed=260):
        root=Path(root)/str(i)
        root.mkdir()
        (root/"manifest.json").write_text(json.dumps({"rc116_latency_profile":profile}))
        for name,rtt,speed in (("rc2",80,310),("candidate",70,candidate_speed)):
            doc={"success":True,"stage":"complete",
                 "IdleMS":[rtt+j%3 for j in range(30)],
                 "LoadedMS":[250+j%10 for j in range(20)],
                 "AfterMS":[75+j%3 for j in range(30)],
                 "Phases":[{"name":"upload","upload":{"full_phase_mbps":speed}},
                           {"name":"download","download":{"full_phase_mbps":speed}}]}
            (root/(name+".json")).write_text(json.dumps(doc))
        return root

    def test_combined_percentiles_preserve_measured_samples(self):
        with tempfile.TemporaryDirectory() as temp:
            roots=[self.make(temp,i) for i in range(5)]
            result=aggregate(roots)
            self.assertEqual(result["pairs"],5)
            self.assertEqual(result["per_phase"]["idle"]["1.1.6-rc1"]["pooled"]["count"],150)
            self.assertEqual(result["per_phase"]["idle"]["p95_diff_ms"],-10)
            self.assertEqual(result["throughput_mbps"]["upload"]["1.1.6-rc1"]["minimum"],260)

    def test_any_home_run_below_200_rejected(self):
        with tempfile.TemporaryDirectory() as temp:
            roots=[self.make(temp,i,candidate_speed=190 if i==3 else 250) for i in range(5)]
            with self.assertRaisesRegex(ValueError,"floor violated"):
                aggregate(roots)

if __name__=="__main__":
    unittest.main()
