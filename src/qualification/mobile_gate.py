"""Anti-collapse gate for the fixed 55-second asymmetric bearer cycle.

This is a minimum recovery screen, not a claim of carrier-equivalent speed.
It is independent of the stricter home-network qualification.
"""
from statistics import mean

RECOVERY_MIN_MBPS = {"download": 10.0, "upload": 1.0}


def assess_mobile_cycle(data, worker_exit):
    problems = []
    metrics = {}
    if worker_exit or not data.get("success") or data.get("stage") != "complete":
        problems.append("incomplete native session or application recovery")
    phases = data.get("Phases") or []
    phase = phases[0] if phases and phases[0].get("name") == "duplex" else {}
    if not phase:
        problems.append("missing asymmetric duplex transfer")
    if phase.get("completed_downloads") != 8 or phase.get("completed_uploads") != 8:
        problems.append("not all eight downloads and eight uploads verified")
    for direction, minimum in RECOVERY_MIN_MBPS.items():
        rates = phase.get(direction, {}).get("half_second_mbps", [])
        recovered = rates[80:110]  # t=40–55 s; outage ends at t=30 s
        degraded = rates[40:56]   # t=20–28 s; before outage
        if len(recovered) != 30 or len(degraded) != 16:
            problems.append(f"{direction}: incomplete rate-cycle samples")
            continue
        goodput = mean(recovered)
        low = mean(degraded)
        metrics[direction] = {"degraded_mbps": low, "recovery_mbps": goodput, "minimum_recovery_mbps": minimum}
        if goodput < minimum or goodput <= low:
            problems.append(f"{direction}: recovered goodput below absolute floor or degraded phase")
        if any(sum(recovered[i:i+8]) <= 0 for i in range(len(recovered)-7)):
            problems.append(f"{direction}: four-second post-recovery receiver stall")
    if len(data.get("AfterMS") or []) < 6:
        problems.append("missing six verified post-load echoes")
    return {"problems": problems, "directional": metrics,
            "qualification": "minimum synthetic mobile anti-collapse recovery; not live-carrier qualification"}
