"""Analytical roofline predictor + llmfit re-implementation, evaluated on
bench_data.csv with held-out schemes. Stdlib only.

Run: python3 -I predict.py bench_data.csv
"""
import csv
import math
import os
import statistics as st
import sys
from collections import defaultdict

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from specs import MODELS, kv_bytes_per_token_at, attn_flops_per_token  # noqa: E402

# ----------------------------------------------------------------------------
# Features
# ----------------------------------------------------------------------------


def features(r):
    m = r["model"]
    n, d = int(r["n_tokens"]), int(r["depth"])
    if r["metric"] == "tg":
        depth_avg = d + n / 2.0
        b = float(r["file_size_GB"]) * 1e9 * MODELS[m]["read_frac"] + kv_bytes_per_token_at(m, depth_avg)
        r["bytes_tok"] = b
        r["bytes_tok_nokv"] = float(r["file_size_GB"]) * 1e9 * MODELS[m]["read_frac"]
        r["roof"] = float(r["spec_bw_GBs"]) * 1e9 / b           # tok/s ceiling
        r["roof_nokv"] = float(r["spec_bw_GBs"]) * 1e9 / r["bytes_tok_nokv"]
    else:
        ctx_avg = d + n / 2.0
        f = 2 * MODELS[m]["flops"] * 1e9 + attn_flops_per_token(m, ctx_avg)
        r["flops_tok"] = f
        r["flops_tok_noattn"] = 2 * MODELS[m]["flops"] * 1e9
        r["roof"] = float(r["spec_fp16_tflops"]) * 1e12 / f
        r["roof_noattn"] = float(r["spec_fp16_tflops"]) * 1e12 / r["flops_tok_noattn"]
    r["meas"] = float(r["measured_tps"])
    return r


# ----------------------------------------------------------------------------
# llmfit re-implementation (static reading of llmfit-core/src/fit.rs,
# hardware.rs, models.rs; default CalcConfig)
# ----------------------------------------------------------------------------

LF_BYTES_PP = {"F32": 4.0, "F16": 2.0, "BF16": 2.0, "Q8_0": 1.0, "Q6_K": 0.75, "Q5_K_M": 0.625,
               "Q4_K_M": 0.5, "Q4_0": 0.5, "Q3_K_M": 0.375, "Q2_K": 0.25, "MXFP4": 0.53,
               "mlx-4bit": 0.5, "mlx-8bit": 1.0}
LF_BPP = {"F32": 4.0, "F16": 2.0, "BF16": 2.0, "Q8_0": 1.05, "Q6_K": 0.80, "Q5_K_M": 0.68,
          "Q4_K_M": 0.58, "Q4_0": 0.58, "Q3_K_M": 0.48, "Q2_K": 0.37, "MXFP4": 0.55,
          "mlx-4bit": 0.55, "mlx-8bit": 1.0}
LF_QMULT = {"F32": 0.3, "F16": 0.6, "BF16": 0.6, "Q8_0": 0.8, "Q6_K": 0.95, "Q5_K_M": 1.0,
            "Q4_K_M": 1.15, "Q4_0": 1.15, "Q3_K_M": 1.25, "Q2_K": 1.35, "MXFP4": 1.15,
            "mlx-4bit": 1.15, "mlx-8bit": 0.85}


def lf_quant(q):
    return {"MLX-4bit": "mlx-4bit", "MLX-8bit": "mlx-8bit"}.get(q, q)


def lf_bandwidth(name):
    """hardware::gpu_memory_bandwidth_gbps, same match order (subset that matters)."""
    s = name.lower()
    if "laptop" in s or "mobile" in s or "max-q" in s:
        return None
    chain = [("5090", 1792), ("5080", 960), ("5070 ti", 896), ("5070", 672), ("5060 ti", 448),
             ("5060", 256), ("4090", 1008), ("4080 super", 736), ("4080", 717),
             ("4070 ti super", 672), ("4070 ti", 504), ("4070 super", 504), ("4070", 504),
             ("4060 ti", 288), ("4060", 272), ("3090 ti", 1008), ("3090", 936), ("3080 ti", 912),
             ("3080", 760), ("3070 ti", 608), ("3070", 448), ("3060 ti", 448), ("3060", 360),
             ("2080 ti", 616), ("2080 super", 496), ("2080", 448), ("2070 super", 448), ("2070", 448),
             ("2060 super", 448), ("2060", 336), ("1660 ti", 288), ("1660 super", 336),
             ("1660", 192), ("1650 super", 192), ("1650", 128),
             ("h100", 2039), ("a100", 1555), ("l40", 864), ("l4", 300), ("t4", 320),
             ("8060s", 256), ("8050s", 256), ("strix halo", 256), ("ryzen ai max", 256),
             ("9070 xt", 624), ("9070", 488), ("7900 xtx", 960), ("7900 xt", 800), ("7900 gre", 576),
             ("7800 xt", 624), ("7700 xt", 432), ("7600", 288), ("6950 xt", 576), ("6900 xt", 512),
             ("6800 xt", 512), ("6800", 512), ("6700 xt", 384), ("6600 xt", 256), ("6600", 224),
             ("m5 max", 614), ("m5 pro", 307), ("m5", 153.6), ("m4 ultra", 819), ("m4 max", 546),
             ("m4 pro", 273), ("m4", 120), ("m3 ultra", 800), ("m3 max", 400), ("m3 pro", 150),
             ("m3", 100), ("m2 ultra", 800), ("m2 max", 400), ("m2 pro", 200), ("m2", 100),
             ("m1 ultra", 800), ("m1 max", 400), ("m1 pro", 200), ("m1", 68)]
    for k, v in chain:
        if k in s:
            return float(v)
    return None


LF_K = {"CUDA": 220.0, "Metal": 160.0, "MLX": 250.0, "ROCm": 180.0, "Vulkan": 150.0, "SYCL": 100.0}

# MoE architecture metadata as in llmfit's catalog (hf_models.json)
LF_MOE = {
    "gpt-oss-20b": dict(hidden=2880, layers=24, act=4, inter=2880, vocab=201088, nexp=32, heads=64, kv=8, hd=64, fixed_bpp=1.43),
    "gpt-oss-120b": dict(hidden=2880, layers=36, act=4, inter=2880, vocab=201088, nexp=128, heads=64, kv=8, hd=64, fixed_bpp=1.43),
    "qwen3-30b-a3b": dict(hidden=2048, layers=48, act=8, inter=768, vocab=151936, nexp=128, heads=32, kv=4, hd=128, fixed_bpp=3.2),
}
LF_ACTIVE_CATALOG = {"gpt-oss-20b": 3.529, "gpt-oss-120b": 9.310, "qwen3-30b-a3b": 3.3}
VRAM = {"RTX 4080 Super": 16, "RTX 5060 Ti": 16, "RTX 5070 Ti": 16, "RTX 5080": 16, "RTX 3090": 24,
        "RTX 4090": 24, "RTX 5090": 32, "RTX 3060": 12, "RTX 4070": 12}


def llmfit_tg(r):
    m = r["model"]
    q = lf_quant(r["quant"])
    total = MODELS[m]["total"]
    be = r["backend"]
    if be == "CPU":
        k = 90.0 if "M2 Max" in r["device"] else 70.0
        return (k / total) * LF_QMULT.get(q, 1.0) * 1.1 * 0.3
    bw = lf_bandwidth(r["device"])
    if bw is not None:
        if m in LF_MOE:
            a = LF_MOE[m]
            active_ffn = a["layers"] * a["act"] * 3 * a["hidden"] * a["inter"] / 1e9
            attn = a["layers"] * (2 * a["heads"] * a["hd"] * a["hidden"] + 2 * a["kv"] * a["hd"] * a["hidden"]) / 1e9
            fixed = attn + a["layers"] * a["nexp"] * a["hidden"] / 1e9 + 2 * a["vocab"] * a["hidden"] / 1e9
            per_tok = active_ffn * LF_BPP.get(q, 0.58) + fixed * a["fixed_bpp"]
            pressure = 1.0
            vram = VRAM.get(r["device"])
            if vram:
                util = total * LF_BPP.get(q, 0.58) / vram
                if 0.6 <= util <= 1.0:
                    ratio = 1 - a["act"] / a["nexp"]
                    pressure = max(1 - (util - 0.6) * ratio, 0.3)
            return bw / per_tok * pressure
        return bw / (total * LF_BYTES_PP.get(q, 0.5)) * 0.55
    params = LF_ACTIVE_CATALOG.get(m, total)
    k = LF_K.get(be, 150.0)
    return k / params * LF_QMULT.get(q, 1.0) * 1.1


def llmfit_pp(r):
    """Only produced when the user configures gpu_compute_tflops_fp16; here we
    hand it the same spec TFLOPS our roofline uses. CPU-only -> None."""
    if r["backend"] == "CPU":
        return None
    m = r["model"]
    params = LF_ACTIVE_CATALOG.get(m, MODELS[m]["total"]) * 1e9
    return float(r["spec_fp16_tflops"]) * 1e12 * 0.35 / (2 * params)


# ----------------------------------------------------------------------------
# Error metrics
# ----------------------------------------------------------------------------


def errs(pairs):
    """pairs: list of (pred, meas). Returns dict of metrics on |pred/meas-1|."""
    pairs = [(p, m) for p, m in pairs if p is not None and p > 0]
    if not pairs:
        return None
    ape = [abs(p / m - 1) for p, m in pairs]
    lr = [math.log(p / m) for p, m in pairs]
    return dict(n=len(pairs), mape=100 * st.mean(ape), mdape=100 * st.median(ape),
                w15=100 * sum(a <= .15 for a in ape) / len(ape),
                w25=100 * sum(a <= .25 for a in ape) / len(ape),
                w40=100 * sum(a <= .40 for a in ape) / len(ape),
                bias=math.exp(st.median(lr)))


def fmt(e):
    if e is None:
        return "   -"
    return (f"n={e['n']:3d} MAPE={e['mape']:6.1f} MdAPE={e['mdape']:5.1f} "
            f"<=15%:{e['w15']:5.1f} <=25%:{e['w25']:5.1f} <=40%:{e['w40']:5.1f} med(pred/meas)={e['bias']:.2f}")


# ----------------------------------------------------------------------------
# Fitting (held-out)
# ----------------------------------------------------------------------------


def gmean_ratio(rs):
    return math.exp(st.median([math.log(x["meas"] / x["roof"]) for x in rs]))


def fit_eff_t0(rs, key_x):
    """Weighted LS for t = x/e + t0 (x = roof time per token at eff 1), w = 1/t^2.
    Falls back to eff-only if fit is degenerate. Returns (e, t0)."""
    xs = [1.0 / x[key_x] for x in rs]     # seconds/token at 100% efficiency
    ys = [1.0 / x["meas"] for x in rs]
    ws = [1.0 / (y * y) for y in ys]
    W = sum(ws)
    mx = sum(w * x for w, x in zip(ws, xs)) / W
    my = sum(w * y for w, y in zip(ws, ys)) / W
    sxx = sum(w * (x - mx) ** 2 for w, x in zip(ws, xs))
    sxy = sum(w * (x - mx) * (y - my) for w, x, y in zip(ws, xs, ys))
    if sxx <= 0:
        return None
    slope = sxy / sxx
    t0 = my - slope * mx
    if slope <= 0 or t0 < 0:
        # constrain t0 >= 0: eff-only fit
        e = gmean_ratio(rs) if key_x == "roof" else None
        return (e, 0.0) if e else None
    return (1.0 / slope, t0)


def group_key(r):
    return r["backend"]


def solve(A, b):
    """Gaussian elimination for small dense systems."""
    n = len(b)
    M = [row[:] + [bb] for row, bb in zip(A, b)]
    for i in range(n):
        piv = max(range(i, n), key=lambda k: abs(M[k][i]))
        if abs(M[piv][i]) < 1e-30:
            return None
        M[i], M[piv] = M[piv], M[i]
        for k in range(n):
            if k != i:
                f = M[k][i] / M[i][i]
                for j in range(i, n + 1):
                    M[k][j] -= f * M[i][j]
    return [M[i][n] / M[i][i] for i in range(n)]


def wls(X, y):
    """min sum w (y - X c)^2, w = 1/y^2, no intercept."""
    k = len(X[0])
    A = [[sum(x[i] * x[j] / (yy * yy) for x, yy in zip(X, y)) for j in range(k)] for i in range(k)]
    b = [sum(x[i] * yy / (yy * yy) for x, yy in zip(X, y)) for i in range(k)]
    return solve(A, b)


def pp_terms(r):
    """Seconds/token at 100% of spec FLOPS: [dense matmul, moe matmul, attention]."""
    F = float(r["spec_fp16_tflops"]) * 1e12
    mat = r["flops_tok_noattn"] / F
    att = (r["flops_tok"] - r["flops_tok_noattn"]) / F
    return [0.0 if r["moe"] == "1" else mat, mat if r["moe"] == "1" else 0.0, att]


def fit_pp(train):
    """Per-backend coefficients c (time = sum c_i * term_i); c_i = 1/eff_i."""
    X = [pp_terms(r) for r in train]
    y = [1.0 / r["meas"] for r in train]
    used = [i for i in range(3) if any(x[i] > 0 for x in X)]
    if not used:
        return None
    Xs = [[x[i] for i in used] for x in X]
    c = wls(Xs, y)
    if c is None or any(ci <= 0 for ci in c):
        # fallback: single coefficient on total time
        tot = [[sum(x)] for x in X]
        c1 = wls(tot, y)
        return None if (c1 is None or c1[0] <= 0) else ("single", c1[0])
    full = [0.0, 0.0, 0.0]
    for i, ci in zip(used, c):
        full[i] = ci
    return ("multi", full)


def pred_pp(r, fit, glob):
    if fit is None:
        fit = glob
    kind, c = fit
    t = pp_terms(r)
    if kind == "single":
        return 1.0 / (c * sum(t))
    cc = list(c)
    gk, gc = glob
    for i in range(3):
        if t[i] > 0 and cc[i] <= 0:
            cc[i] = gc[i] if gk == "multi" and gc[i] > 0 else (gc if gk == "single" else 1.0)
    return 1.0 / sum(ci * ti for ci, ti in zip(cc, t))


def run(path):
    rows = [features(r) for r in csv.DictReader(open(path))]
    main = [r for r in rows if r["fit_eligible"] == "yes"]
    for r in main:
        r["llmfit"] = llmfit_tg(r) if r["metric"] == "tg" else llmfit_pp(r)

    for metric in ("tg", "pp"):
        M = [r for r in main if r["metric"] == metric]
        devices = sorted(set(r["device"] for r in M))
        allglobal = [r for r in M if r["segment"] not in ("nv_unified",) and r["engine"] == "llama.cpp"]
        for r in M:
            r["p_spec"] = r["roof"]                       # eff = 1 (ceiling)
            r["p_spec_simple"] = r["roof_nokv"] if metric == "tg" else r["roof_noattn"]
        # LODO fits
        for dev in devices:
            test = [r for r in M if r["device"] == dev]
            train = [r for r in M if r["device"] != dev]
            tr_glob = [r for r in allglobal if r["device"] != dev]
            e_glob = gmean_ratio(tr_glob)
            for r in test:
                r["p_global"] = r["roof"] * e_glob
                same = [x for x in train if group_key(x) == group_key(r) and x["engine"] == r["engine"]]
                if len({x["device"] for x in same}) >= 3:
                    e_be = gmean_ratio(same)
                    r["p_backend"] = r["roof"] * e_be
                    ft = fit_eff_t0(same, "roof")
                    if ft and ft[0]:
                        e, t0 = ft
                        r["p_backend_t0"] = 1.0 / (1.0 / (r["roof"] * e) + t0)
                    else:
                        r["p_backend_t0"] = r["p_backend"]
                else:
                    r["p_backend"] = r["p_global"]
                    r["p_backend_t0"] = r["p_global"]
        # prefill: per-backend multi-term (dense matmul / MoE matmul / attention) LODO
        if metric == "pp":
            for dev in devices:
                test = [r for r in M if r["device"] == dev]
                train = [r for r in M if r["device"] != dev and r["engine"] == "llama.cpp"]
                glob = fit_pp(train)
                for r in test:
                    same = [x for x in train if x["backend"] == r["backend"]]
                    f = fit_pp(same) if len({x["device"] for x in same}) >= 3 else None
                    r["p_backend_multi"] = pred_pp(r, f, glob)
                    # calibration: scale backend-multi model by device ratio from probe rows
                    probe = [x for x in M if x["device"] == r["device"] and x["backend"] == r["backend"]
                             and x["engine"] == r["engine"] and (x["model"], x["quant"]) != (r["model"], r["quant"])]
                    if probe:
                        ratios = [x["meas"] / pred_pp(x, f, glob) for x in probe]
                        r["p_calib_t0"] = r["p_backend_multi"] * math.exp(st.median([math.log(v) for v in ratios]))
                    else:
                        r["p_calib_t0"] = None
        else:
            for dev in devices:
                test = [r for r in M if r["device"] == dev]
                train = [r for r in M if r["device"] != dev]
                for r in test:
                    same = [x for x in train if group_key(x) == group_key(r) and x["engine"] == r["engine"]]
                    ft = fit_eff_t0(same, "roof") if len({x["device"] for x in same}) >= 3 else None
                    t0 = ft[1] if ft and ft[0] else 0.0
                    probe = [x for x in M if x["device"] == r["device"] and x["backend"] == r["backend"]
                             and x["engine"] == r["engine"] and (x["model"], x["quant"]) != (r["model"], r["quant"])]
                    if probe:
                        # device efficiency from probe, keeping the population per-token overhead t0
                        es = []
                        for x in probe:
                            tt = 1.0 / x["meas"] - t0
                            es.append(math.log((1.0 / x["roof"]) / tt) if tt > 0 else math.log(1.0))
                        e = math.exp(st.median(es))
                        r["p_calib_t0"] = 1.0 / (1.0 / (r["roof"] * e) + t0)
                    else:
                        r["p_calib_t0"] = None
        # on-device calibration: probe = other (model, quant) rows on same device+backend+engine
        for r in M:
            probe = [x for x in M if x["device"] == r["device"] and x["backend"] == r["backend"]
                     and x["engine"] == r["engine"] and (x["model"], x["quant"]) != (r["model"], r["quant"])]
            if probe:
                r["p_calib"] = r["roof"] * gmean_ratio(probe)
            else:
                r["p_calib"] = None
            # calibration with the *same* model+quant at a different context (depth probe)
            probe2 = [x for x in M if x["device"] == r["device"] and x["backend"] == r["backend"]
                      and (x["model"], x["quant"]) == (r["model"], r["quant"]) and x is not r
                      and x["test"] != r["test"]]
            r["p_calib_same"] = r["roof"] * gmean_ratio(probe2) if probe2 else None
    return rows, main


SEG_GROUP = {
    "apple_base": "Apple base", "apple_pro": "Apple Pro", "apple_max": "Apple Max",
    "apple_ultra": "Apple Ultra", "nv_desktop": "NVIDIA desktop", "nv_laptop": "NVIDIA laptop",
    "nv_datacenter": "NVIDIA dc", "amd_rdna2": "AMD dGPU", "amd_rdna3": "AMD dGPU", "amd_rdna4": "AMD dGPU",
    "amd_laptop": "AMD laptop dGPU", "amd_strix_halo": "Strix Halo", "amd_igpu": "iGPU (AMD/Intel)",
    "intel_igpu": "iGPU (AMD/Intel)", "intel_arc": "Intel Arc dGPU", "cpu_apple": "CPU-only",
    "cpu_x86_ddr5": "CPU-only", "cpu_x86_lpddr5x": "CPU-only", "nv_unified": "NV unified (Spark/Thor)",
}


def seg_of(r):
    if r["engine"] == "mlx-lm" or r["model"].startswith(("falcon", "granite", "nemotron")):
        return "Hybrid-SSM (M4 Max)"
    if r["device"].startswith("Apple M5"):
        return "Apple M5 (new arch)"
    return SEG_GROUP.get(r["segment"], r["segment"])


def report(rows, main):
    preds = [("spec ceiling (eff=1)", "p_spec"), ("global eff (LODO)", "p_global"),
             ("per-backend eff (LODO)", "p_backend"), ("per-backend eff+t0 (LODO)", "p_backend_t0"),
             ("pp: per-backend multi-term (LODO)", "p_backend_multi"),
             ("on-device calib (other model)", "p_calib"),
             ("on-device calib + pop. t0/terms", "p_calib_t0"), ("llmfit formula", "llmfit")]
    for metric in ("tg", "pp"):
        M = [r for r in main if r["metric"] == metric]
        print(f"\n================ {metric.upper()}  ({len(M)} rows) ================")
        for name, k in preds:
            print(f"{name:32s} ALL  {fmt(errs([(r.get(k), r['meas']) for r in M]))}")
        segs = sorted(set(seg_of(r) for r in M))
        for name, k in preds:
            print(f"\n-- {name}")
            for s in segs:
                S = [r for r in M if seg_of(r) == s]
                print(f"   {s:26s} {fmt(errs([(r.get(k), r['meas']) for r in S]))}")
        print("\n-- by backend (per-backend eff+t0 LODO / llmfit)")
        for b in sorted(set(r["backend"] for r in M)):
            S = [r for r in M if r["backend"] == b]
            print(f"   {b:8s} t0: {fmt(errs([(r.get('p_backend_t0'), r['meas']) for r in S]))}")
            print(f"   {'':8s} lf: {fmt(errs([(r.get('llmfit'), r['meas']) for r in S]))}")
        print("\n-- conditions (per-backend eff+t0 LODO | calib | llmfit)")
        conds = [("depth>=4096 (decode at depth)" if metric == "tg" else "prompt>=8192 or depth>0",
                  (lambda r: int(r["depth"]) + int(r["n_tokens"]) / 2 >= 4096) if metric == "tg"
                  else (lambda r: int(r["n_tokens"]) >= 8192 or int(r["depth"]) > 0)),
                 ("MoE", lambda r: r["moe"] == "1"),
                 ("dense", lambda r: r["moe"] == "0"),
                 ("model >=30GB", lambda r: float(r["file_size_GB"]) >= 30),
                 ("spec_conf=low", lambda r: r["spec_conf"] == "low")]
        for cname, f in conds:
            S = [r for r in M if f(r)]
            for name, k in (("t0", "p_backend_t0"), ("multi", "p_backend_multi"), ("calib", "p_calib"), ("calt0", "p_calib_t0"), ("llmfit", "llmfit")):
                print(f"   {cname:30s} {name:6s} {fmt(errs([(r.get(k), r['meas']) for r in S]))}")

    # Effective efficiencies per backend (all data) for the report
    print("\n================ fitted efficiencies (all eligible llama.cpp rows) ================")
    for metric in ("tg", "pp"):
        for b in sorted(set(r["backend"] for r in main)):
            S = [r for r in main if r["metric"] == metric and r["backend"] == b]
            if len({r['device'] for r in S}) < 2:
                continue
            ratios = sorted(r["meas"] / r["roof"] for r in S)
            ft = fit_eff_t0(S, "roof")
            q = lambda p: ratios[int(p * (len(ratios) - 1))]
            t0s = f"eff={ft[0]:.2f} t0={ft[1]*1000:.2f}ms" if ft and ft[0] else "-"
            print(f"  {metric} {b:7s} n={len(S):3d} eff(meas/roof) p10={q(.1):.2f} med={q(.5):.2f} p90={q(.9):.2f}  | LS {t0s}")


def depth_report(rows):
    print("\n================ context-depth: decode slowdown tg(d)/tg(0) ================")
    by = defaultdict(list)
    for r in rows:
        if "depth sweep" in r["notes"]:
            by[(r["device"], r["backend"], r["model"], r["notes"])].append(r)
    for k, rs in by.items():
        tg = sorted([r for r in rs if r["metric"] == "tg"], key=lambda r: int(r["depth"]))
        pp = sorted([r for r in rs if r["metric"] == "pp"], key=lambda r: int(r["depth"]))
        t0, tL = tg[0], tg[-1]
        meas_ratio = tL["meas"] / t0["meas"]
        pred_ratio = tL["roof"] / t0["roof"]
        p0, pL = pp[0], pp[-1]
        pm = pL["meas"] / p0["meas"]
        pr = pL["roof"] / p0["roof"]
        print(f"  {k[0][:26]:26s} {k[1]:6s} {k[2]:14s} {k[3][13:40]:27s} tg d32k/d0 meas={meas_ratio:.2f} kv-model={pred_ratio:.2f} | pp2048@d32k/d0 meas={pm:.2f} attn-model={pr:.2f}")
    print("\n  long-prompt prefill ratio pp(N_long)/pp(2048) (gpt-oss, #15396) and XD pp8192/pp512, tg8192/tg512")
    by2 = defaultdict(dict)
    for r in rows:
        if r["source_url"].endswith("15396") and r["metric"] == "pp" and r["fit_eligible"] == "yes":
            by2[(r["device"], r["model"])][int(r["n_tokens"])] = r
        if r["source_url"].endswith("Inference"):
            by2[(r["device"], r["model"] + "/" + r["quant"])][(r["metric"], int(r["n_tokens"]))] = r
    for k, d in by2.items():
        if 2048 in d:
            nl = max(x for x in d if isinstance(x, int))
            a, b = d[2048], d[nl]
            print(f"  {k[0][:22]:22s} {k[1]:14s} pp{nl}/pp2048 meas={b['meas']/a['meas']:.2f} attn-model={b['roof']/a['roof']:.2f}")
        elif ("pp", 512) in d and ("pp", 8192) in d:
            a, b = d[("pp", 512)], d[("pp", 8192)]
            s = f"  {k[0][:22]:22s} {k[1]:20s} pp8192/pp512 meas={b['meas']/a['meas']:.2f} attn-model={b['roof']/a['roof']:.2f}"
            if ("tg", 512) in d and ("tg", 8192) in d:
                c, e = d[("tg", 512)], d[("tg", 8192)]
                s += f" | tg8192/tg512 meas={e['meas']/c['meas']:.2f} kv-model={e['roof']/c['roof']:.2f}"
            print(s)


def worst(main, k="p_backend_t0", n=12):
    print(f"\n================ worst rows for {k} ================")
    for metric in ("tg", "pp"):
        M = [r for r in main if r["metric"] == metric and r.get(k)]
        M.sort(key=lambda r: -abs(math.log(r[k] / r["meas"])))
        for r in M[:n]:
            print(f"  {metric} {r['device'][:28]:28s} {r['backend']:6s} {r['model']:14s} {r['quant']:8s} {r['test']:12s} meas={r['meas']:8.1f} pred={r[k]:8.1f} llmfit={(r.get('llmfit') or 0):8.1f}  {r['notes'][:50]}")


def offload_report(rows):
    print("\n================ partial / expert offload rows (not fitted) ================")
    for r in rows:
        if r["fit_eligible"] != "yes":
            print(f"  {r['device']} {r['model']} {r['test']} meas={r['measured_tps']} :: {r['notes']}")
    # RTX 4070 + DDR5 gpt-oss-120b, --n-cpu-moe 31 of 36 layers on CPU
    nonexp = (0.955 + 0.58) * 1.0625e9
    exp_layer = 3.58 * 0.53125e9 / 36
    gpu_b = nonexp + 5 * exp_layer
    cpu_b = 31 * exp_layer
    for e_cpu in (0.5, 0.7):
        t = gpu_b / (0.75 * 504e9) + cpu_b / (e_cpu * 96e9)
        print(f"  two-tier roofline RTX4070+DDR5-6000 (cpu eff {e_cpu}): {1/t:.1f} tok/s vs ~10 measured")


if __name__ == "__main__":
    rows, main = run(sys.argv[1])
    report(rows, main)
    depth_report(rows)
    worst(main)
    worst(main, "llmfit")
    offload_report(rows)
