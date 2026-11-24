import unittest

from home_gate import BURSTS, IDS, REVISION, check


def control():
    # Boundary tests for the release decision only. The runner supplies socket data.
    phases = []
    for direction in ("download", "upload"):
        phases.append({"name": direction, "streams": 8, "success": True, "elapsed_seconds": 15,
                       "send_seconds": 15, "loaded_ms": [200],
                       direction: {"verified_bytes": 400_000_000, "half_second_mbps": [215]*30}})
    light = []
    for rate, streams in BURSTS.items():
        for name, bps, n in [(f"burst_{rate}k", rate*1000, streams), (f"idle_after_{rate}k", 0, 0)]:
            light.append(dict(name=name, streams=n, offered_bps=bps, elapsed_s=3,
                              verified_echo_bytes=bps*3//8, application_ms=[111]))
    stages = ["idle", "download", "upload", "post_load", *(p["name"] for p in light)]
    return {
        "success": True, "stage": "complete", "configured_accounts": 15, "granted_accounts": IDS,
        "Provider": {"connection_rejections": 0, "peak": dict.fromkeys(IDS, 8),
                     "successful_logins": dict.fromkeys(IDS, 8)},
        "diagnostics": {"Revision": REVISION, "QualificationAppTCP": {"segments_sent": 2, "segments_received": 2},
                        "Mail": {"lines": [dict(account=i, ready=True, data=True, peer_verified=True,
                                                UploadBytes=90000, DownloadBytes=90000) for i in IDS]}},
        "IdleMS": [100], "AfterMS": [100], "Phases": phases, "light": light,
        "account_samples": [dict(phase=s, account=i, rtt_ms=110) for s in stages for i in IDS],
    }


class HomeGateTests(unittest.TestCase):
    def test_exact_latency_limit(self):
        self.assertEqual(check(control(), 15, 1), [])
        r = control()
        r["Phases"][1]["loaded_ms"].append(200.001)
        self.assertTrue(check(r, 15, 1))

    def test_pinned_and_censored_tail(self):
        for change in ({"rtt_ms": 300}, {"error": "deadline"}, {"rtt_ms": float("nan")}):
            r = control()
            r["account_samples"][-1].update(change)
            self.assertTrue(check(r, 15, 1))
        r = control()
        r["account_samples"].pop()
        self.assertTrue(check(r, 15, 1))

    def test_throughput_window_and_grant(self):
        r = control()
        r["Phases"][0]["download"]["half_second_mbps"][10:] = [200]*20
        self.assertTrue(check(r, 15, 1))
        r = control()
        r["Provider"]["peak"]["a09"] = 1
        self.assertTrue(check(r, 15, 1))

    def test_complete_trace_and_stream_churn(self):
        r = control()
        r["light"][0]["verified_echo_bytes"] -= 1
        self.assertTrue(check(r, 15, 1))
        r = control()
        r["light"][0]["application_ms"] = []
        self.assertTrue(check(r, 15, 1))


if __name__ == "__main__":
    unittest.main()
