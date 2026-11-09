#!/usr/bin/env python3
"""Aggregate measured paired baseline/candidate synthetic application RTT runs.

Every sample comes from the actual TLS echo path inside a shaped fixture.
No simulated IMAP RTT input is reported as a measured application RTT.
"""
import argparse
import json
from pathlib import Path
import statistics
from rtt_report import latency_stats

PHASES=(("idle","IdleMS"),("loaded","LoadedMS"),("after","AfterMS"))
FIELDS=("median_ms","p95_ms","p99_ms")


def aggregate(paths):
    if len(paths)<3:
        raise ValueError("at least three independent paired runs required")
    roots=[Path(p) for p in paths]
    profiles=set()
    version_pairs=set()
    results=[]
    for root in roots:
        manifest=json.loads((root/"manifest.json").read_text())
        profiles.add(manifest.get("rc116_latency_profile"))
        version_pairs.add((
            manifest.get("baseline_version") or ("1.1.5" if manifest.get("baseline_is_mailstream") else "1.0.0-rc2"),
            manifest.get("candidate_version") or "1.1.6-rc1",
        ))
        pair=[]
        for label in ("rc2","candidate"):
            result=json.loads((root/(label+".json")).read_text())
            if result.get("success") is not True or result.get("stage")!="complete":
                raise ValueError(f"{root}/{label}: incomplete test")
            for phase,key in PHASES:
                if len(result.get(key) or [])< (6 if phase=="loaded" else 20):
                    raise ValueError(f"{root}/{label}: insufficient {phase} RTT observations")
            pair.append(result)
        results.append(pair)
    if len(profiles)!=1:
        raise ValueError("cannot combine different network profiles")
    if len(version_pairs)!=1:
        raise ValueError("cannot combine distinct baseline/candidate versions")
    baseline_name,candidate_name=next(iter(version_pairs))
    if baseline_name==candidate_name:
        raise ValueError("baseline and candidate labels must be distinct")
    profile=next(iter(profiles))
    output={"profile":profile,"pairs":len(results),"source":"measured TLS echo RTT through ITOM",
            "baseline_version":baseline_name,"candidate_version":candidate_name,
            "per_phase":{},"throughput_mbps":{},"warnings":[]}
    for phase,key in PHASES:
        item={}
        for i,version in enumerate((baseline_name,candidate_name)):
            runs=[latency_stats(pair[i][key]) for pair in results]
            merged=latency_stats([sample for pair in results for sample in pair[i][key]])
            item[version]={"per_run":runs,"median_of_run_medians_ms":
                           round(statistics.median(x["median_ms"] for x in runs),3),
                           "median_of_run_p95_ms":
                           round(statistics.median(x["p95_ms"] for x in runs),3),
                           "pooled":merged}
        base=item[baseline_name]
        cand=item[candidate_name]
        item["paired_median_diff_ms"]=round(statistics.median(
            latency_stats(pair[1][key])["median_ms"]-latency_stats(pair[0][key])["median_ms"]
            for pair in results),3)
        item["p95_diff_ms"]=round(cand["median_of_run_p95_ms"]-base["median_of_run_p95_ms"],3)
        item["p95_pooled_diff_ms"]=round(cand["pooled"]["p95_ms"]-base["pooled"]["p95_ms"],3)
        output["per_phase"][phase]=item
    for direction in ("upload","download"):
        item={}
        for i,name in enumerate((baseline_name,candidate_name)):
            speeds=[]
            for pair in results:
                for phase in pair[i].get("Phases") or []:
                    if phase.get("name")==direction and isinstance(phase.get(direction),dict):
                        speeds.append(float(phase[direction]["full_phase_mbps"]))
            if len(speeds)!=len(results):
                raise ValueError(f"expected one complete {direction} transfer in each run")
            item[name]={"median":round(statistics.median(speeds),3),
                        "minimum":round(min(speeds),3),"all":speeds}
        output["throughput_mbps"][direction]=item
        # A 200 Mbps absolute floor is meaningful only for the home link.
        # Poor mobile (2/0.5 Mbps) is measured against the released RC1
        # *relative* 75% goodput gate, never against a home-speed minimum.
        if profile not in ("mobile", "mobile-poor") and item[candidate_name]["minimum"]<200:
            output["warnings"].append(f"{direction} high-capacity run fell below hard 200Mbps floor")
    print(f"\n=== AGGREGATED MEASURED RTT: {profile} ({len(results)} paired runs) ===",flush=True)
    for phase in ("idle","loaded","after"):
        item=output["per_phase"][phase]
        a,b=item[baseline_name],item[candidate_name]
        print(f"{phase:>6}: median-of-medians {a['median_of_run_medians_ms']:.3f} -> "
              f"{b['median_of_run_medians_ms']:.3f} ms; "
              f"p95 (median of runs) {a['median_of_run_p95_ms']:.3f} -> "
              f"{b['median_of_run_p95_ms']:.3f} ms ({item['p95_diff_ms']:+.3f}); "
              f"pooled p95 {a['pooled']['p95_ms']:.3f} -> "
              f"{b['pooled']['p95_ms']:.3f} ms ({item['p95_pooled_diff_ms']:+.3f}), "
              f"{b['pooled']['count']} candidate observations",flush=True)
    for direction,item in output["throughput_mbps"].items():
        print(f"{direction:>8}: median throughput {item[baseline_name]['median']:.2f} -> "
              f"{item[candidate_name]['median']:.2f} Mbps; "
              f"candidate minimum {item[candidate_name]['minimum']:.2f} Mbps",flush=True)
    for warning in output["warnings"]:
        print("HARD FLOOR WARNING: "+warning,flush=True)
    if output["warnings"]:
        raise ValueError("high-capacity bandwidth floor violated")
    return output


def main():
    parser=argparse.ArgumentParser(description=__doc__)
    parser.add_argument("paths",nargs="+",type=Path)
    parser.add_argument("--out",type=Path)
    args=parser.parse_args()
    output=aggregate(args.paths)
    if args.out:
        args.out.write_text(json.dumps(output,indent=2)+"\n")
        print("Wrote verified measured RTT aggregation:",args.out,flush=True)

if __name__=="__main__":
    main()
