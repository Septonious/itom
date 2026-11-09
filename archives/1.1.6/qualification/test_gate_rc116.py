"""Synthetic RTT release gates must reject meaningful p95 regressions."""
import json
import tempfile
import unittest
from pathlib import Path
from rtt_report import build_report
from gate_rc116 import gate


class GateTests(unittest.TestCase):
    def scenario(self, root, control_loaded, candidate_loaded, speed=240):
        root=Path(root)
        (root/'manifest.json').write_text(json.dumps({'rc116_latency_profile':'home'}))
        for name, loaded in [('rc2',control_loaded),('candidate',candidate_loaded)]:
            (root/(name+'.json')).write_text(json.dumps({
                'success':True,'stage':'complete','IdleMS':[65]*30,
                'LoadedMS':[loaded]*20,'AfterMS':[66]*30,
                'Phases':[{'name':'upload','upload':{'full_phase_mbps':speed}},
                          {'name':'download','download':{'full_phase_mbps':230}}]}))
        build_report(root)

    def test_good_latency_and_200_floor_passes(self):
        with tempfile.TemporaryDirectory() as tmp:
            self.scenario(tmp,250,270)
            gate(tmp)

    def test_worse_tail_rejected(self):
        with tempfile.TemporaryDirectory() as tmp:
            self.scenario(tmp,250,450)
            with self.assertRaisesRegex(RuntimeError,'regression gate'):
                gate(tmp)

    def test_below_200_floor_rejected(self):
        with tempfile.TemporaryDirectory() as tmp:
            self.scenario(tmp,250,250,speed=190)
            with self.assertRaisesRegex(RuntimeError,'regression gate'):
                gate(tmp)

if __name__ == '__main__':
    unittest.main()
