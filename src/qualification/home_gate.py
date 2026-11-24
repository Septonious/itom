"""Check real TLS/TUN application and same-account round trips from a home run."""
import math
from collections import Counter, defaultdict

IDS = [f"a{i:02}" for i in range(1, 9)]
BURSTS = {100: 1, 500: 4, 2000: 8, 5000: 4}
REVISION = "itom-1-5-1-rc1-dev"


def check(result, seconds, light_cycles):
    problems = []

    def require(ok, message):
        if not ok:
            problems.append(message)

    def rtts(values, where, quiet=False):
        if not values or any(not math.isfinite(x) or x <= 0 for x in values):
            problems.append(f"{where} missing or invalid RTT")
            return
        values = sorted(values)
        require(values[-1] <= 200, f"{where} max {values[-1]:.3f} ms > 200")
        p95 = values[math.ceil(.95 * len(values)) - 1]
        if quiet:
            require(p95 <= 150, f"{where} p95 {p95:.3f} ms > 150")

    require(result.get("success") is True and result.get("stage") == "complete",
            "worker failed: " + str(result.get("error", result.get("stage"))))
    require(result.get("configured_accounts") == 15 and result.get("granted_accounts") == IDS,
            "expected the fixed eight-account grant out of fifteen provisioned mailboxes")
    provider = result.get("Provider") or {}
    require(provider.get("connection_rejections") == 0, "provider refused a login or did not report admission")
    require(provider.get("peak") == dict.fromkeys(IDS, 8), "physical account/socket limits or coverage")
    require(provider.get("successful_logins") == dict.fromkeys(IDS, 8), "extra logins or a missing role")
    diag = result.get("diagnostics") or {}
    require(diag.get("Revision") == REVISION, "unexpected transport revision")
    tcp = diag.get("QualificationAppTCP", {})
    require(tcp.get("segments_sent", 0) > 0 and tcp.get("segments_received", 0) > 0,
            "no native TUN application TCP evidence")
    mail = diag.get("Mail", {})
    payload = {row["account"] for row in mail.get("lines", [])
               if row.get("ready") and row.get("data") and row.get("peer_verified")
               and min(row.get("UploadBytes", 0), row.get("DownloadBytes", 0)) > 65536}
    require(payload == set(IDS), "eight verified bidirectional physical accounts required")
    rtts(result.get("IdleMS"), "idle app", True)
    rtts(result.get("AfterMS"), "post-load app", True)
    phases = result.get("Phases") or []
    require([p["name"] for p in phases] == ["download", "upload"], "missing or reordered speedtest phase")
    for p in phases:
        direction = p["name"]
        if direction not in ("download", "upload"):
            continue
        samples = p[direction]
        require(p.get("success") is True and p["streams"] == 8 and p["send_seconds"] == seconds,
                f"{direction} incomplete transfer")
        elapsed, size = p["elapsed_seconds"], samples["verified_bytes"]
        require(math.isfinite(elapsed) and elapsed >= seconds, f"{direction} invalid phase duration")
        rate = 8 * size / (max(elapsed, .000001) * 1e6)
        require(math.isfinite(rate) and rate > 200, f"{direction} verified goodput {rate:.2f} Mbps")
        rates = samples["half_second_mbps"]
        n = seconds * 2
        if len(rates) < n or any(not math.isfinite(x) or x < 0 for x in rates):
            problems.append(f"{direction} incomplete or invalid receiver buckets")
        else:
            floor = min(sum(rates[i:i+20])/20 for i in range(10, n - 19))
            require(floor > 200, f"{direction} weakest post-ramp 10s {floor:.2f} Mbps")
        rtts(p.get("loaded_ms"), f"{direction} app")
    light = result.get("light") or []
    expected = {}
    for rate, streams in BURSTS.items():
        expected[f"burst_{rate}k"] = (rate * 1000, streams)
        expected[f"idle_after_{rate}k"] = (0, 0)
    require(Counter(p["name"] for p in light) == Counter({k: light_cycles for k in expected}),
            "incomplete burst/idle cycles")
    for p in light:
        phase = p["name"]
        if phase not in expected:
            continue
        rate, streams = expected[phase]
        require(p["offered_bps"] == rate and p["streams"] == streams
                and p["verified_echo_bytes"] == rate * 3 // 8, phase + " incomplete echoed payload")
        require(math.isfinite(p["elapsed_s"]) and p["elapsed_s"] >= 3, phase + " incomplete interval")
        rtts(p["application_ms"], phase + " app", True)
    samples = defaultdict(list)
    errors = 0
    for s in result.get("account_samples", []):
        if s.get("error"):
            errors += 1
        samples[s["phase"], s["account"]].append(s["rtt_ms"])
    require(errors == 0, f"{errors} account probe errors")
    stages = ["idle", "download", "upload", "post_load", *expected] if light_cycles else ["idle", "download", "upload", "post_load"]
    for stage in stages:
        for who in IDS:
            rtts(samples[stage, who], f"{stage} {who}", stage not in ("download", "upload"))
    return problems
