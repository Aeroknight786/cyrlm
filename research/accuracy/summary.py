"""Compact per-segment error table (MdAPE % / share within +-25%) for the report.
Run: python3 -I summary.py bench_data.csv
"""
import os
import sys

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
import predict as P  # noqa: E402

SEGS = [
    ("Apple M1-M4 base/Pro", lambda r: r["segment"] in ("apple_base", "apple_pro") and not r["device"].startswith("Apple M5")),
    ("Apple M1-M4 Max/Ultra", lambda r: r["segment"] in ("apple_max", "apple_ultra") and not r["device"].startswith("Apple M5")),
    ("Apple M5 (new GPU)", lambda r: r["device"].startswith("Apple M5")),
    ("NVIDIA desktop", lambda r: r["segment"] == "nv_desktop"),
    ("NVIDIA laptop", lambda r: r["segment"] == "nv_laptop"),
    ("AMD dGPU (incl. laptop)", lambda r: r["segment"] in ("amd_rdna2", "amd_rdna3", "amd_rdna4", "amd_laptop")),
    ("Strix Halo", lambda r: r["segment"] == "amd_strix_halo"),
    ("Intel Arc dGPU", lambda r: r["segment"] == "intel_arc"),
    ("iGPU (780M/890M/LNL/PTL/MTL)", lambda r: r["segment"] in ("amd_igpu", "intel_igpu")),
    ("CPU-only", lambda r: r["segment"].startswith("cpu")),
    ("NV Spark/Thor (ref)", lambda r: r["segment"] == "nv_unified"),
]


def cell(rows, k):
    e = P.errs([(r.get(k), r["meas"]) for r in rows])
    if e is None:
        return "  -  "
    return f"{e['mdape']:.0f}/{e['w25']:.0f}"


def main(path):
    rows, main_ = P.run(path)
    main_ = [r for r in main_ if not P.seg_of(r).startswith("Hybrid")]
    for metric, keys in (("tg", ("p_spec", "p_global", "p_backend_t0", "p_calib_t0", "llmfit")),
                         ("pp", ("p_spec", "p_global", "p_backend_multi", "p_calib_t0", "llmfit"))):
        M = [r for r in main_ if r["metric"] == metric]
        print(f"\n{metric}: segment | n | spec-ceiling bias | " + " | ".join(keys) + "   (MdAPE%/within25%)")
        for name, f in SEGS + [("ALL", lambda r: True)]:
            S = [r for r in M if f(r)]
            if not S:
                continue
            b = P.errs([(r["p_spec"], r["meas"]) for r in S])["bias"]
            print(f"  {name:30s} | {len(S):3d} | x{b:4.2f} | " + " | ".join(cell(S, k) for k in keys))


if __name__ == "__main__":
    main(sys.argv[1])
