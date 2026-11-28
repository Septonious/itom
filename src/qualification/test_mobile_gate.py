import unittest
from mobile_gate import assess_mobile_cycle


def result(down=20.0, up=2.0):
    def samples(recovered):
        return [1.0]*40 + [0.1]*16 + [0.0]*24 + [recovered]*30
    return {"success": True, "stage": "complete", "AfterMS": [200.0]*6,
            "Phases": [{"name": "duplex", "completed_downloads": 8, "completed_uploads": 8,
                        "download": {"half_second_mbps": samples(down)},
                        "upload": {"half_second_mbps": samples(up)}}]}


class MobileGateTest(unittest.TestCase):
    def test_real_recovery_passes(self):
        self.assertEqual(assess_mobile_cycle(result(), 0)["problems"], [])

    def test_field_collapse_fails_even_if_relative_speed_improves(self):
        self.assertEqual(len(assess_mobile_cycle(result(.20, .23), 0)["problems"]), 2)

    def test_missing_verified_upload_rejected(self):
        d = result()
        d["Phases"][0]["completed_uploads"] = 7
        self.assertTrue(assess_mobile_cycle(d, 0)["problems"])

    def test_missing_post_load_echoes_rejected(self):
        d = result()
        d["AfterMS"] = [200.0]
        self.assertTrue(assess_mobile_cycle(d, 0)["problems"])

    def test_four_second_stall_rejected(self):
        d = result(30, 3)
        d["Phases"][0]["download"]["half_second_mbps"][85:93] = [0.0]*8
        self.assertTrue(any("stall" in p for p in assess_mobile_cycle(d, 0)["problems"]))

    def test_worker_failure_rejected(self):
        self.assertTrue(assess_mobile_cycle(result(), 1)["problems"])
