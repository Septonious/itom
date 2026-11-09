#!/usr/bin/env python3
"""Report provider APPEND body bytes per verified useful application byte.

Provider counts include MIME envelopes, receipt/control frames, retransmits and
session setup. They exclude TCP/TLS/IP headers. This is a measured fixture
accounting ratio, NOT a direct physical radio byte counter.
"""
import argparse
import json
import statistics
from pathlib import Path


def measure(path):
    source=json.loads(Path(path).read_text())
    provider=source.get("Provider")
    if not isinstance(provider,dict) or "appended_body_bytes" not in provider:
        raise ValueError(f"{path}: provider appended byte counter is absent")
    mime_bytes=int(provider["appended_body_bytes"])
    app_bytes=0
    for phase in source.get("Phases") or []:
        for direction in ("upload","download"):
            record=phase.get(direction)
            if not isinstance(record,dict):
                continue
            app_bytes+=int(record.get("verified_bytes",0))
    if mime_bytes <= 0 or app_bytes <= 0:
        raise ValueError(f"{path}: cannot measure positive bytes: mime={mime_bytes} app={app_bytes}")
    return {"provider_appended_mime_bytes":mime_bytes,
            "verified_application_bytes":app_bytes,
            "mime_bytes_per_verified_app_byte":round(mime_bytes/app_bytes,5),
            "useful_app_fraction":round(app_bytes/mime_bytes,6)}


def report(paths):
    if len(paths)<2:
        raise ValueError("need at least one baseline/candidate pair")
    combined={}
    for directory in map(Path,paths):
        for label in ("rc2","candidate"):
            entry=measure(directory/(label+".json"))
            combined.setdefault(label,[]).append(entry)
    values={}
    for label,numbers in combined.items():
        ratios=[x["mime_bytes_per_verified_app_byte"] for x in numbers]
        values[label]={"runs":len(numbers),"median_mime_bytes_per_app_byte":round(statistics.median(ratios),5),
                       "all_runs":numbers}
    if len(combined["rc2"])!=len(combined["candidate"]):
        raise ValueError("unpaired data")
    baseline=values["rc2"]["median_mime_bytes_per_app_byte"]
    candidate=values["candidate"]["median_mime_bytes_per_app_byte"]
    values["mime_byte_reduction_percent"]=round(100*(1-candidate/baseline),3)
    print("MEASURED PROVIDER APPEND MIME-BODY OVERHEAD (includes protocol/control/retry bytes, excludes IP/TLS framing)")
    print(f"1.1.5: {baseline:.5f} MIME bytes / verified app byte")
    print(f"1.1.6: {candidate:.5f} MIME bytes / verified app byte")
    print(f"MIME byte reduction at identical application work: {values['mime_byte_reduction_percent']:+.3f}%")
    return values


if __name__=="__main__":
    parser=argparse.ArgumentParser()
    parser.add_argument("folders",nargs="+",type=Path)
    parser.add_argument("--out",type=Path)
    a=parser.parse_args()
    values=report(a.folders)
    if a.out:
        a.out.write_text(json.dumps(values,indent=2)+"\n")
