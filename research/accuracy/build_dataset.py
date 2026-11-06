"""Builds bench_data.csv from measurements transcribed from public sources.

Every number below was copied from the cited page (values are t/s; where a
source gives FA on and off, the better of the two is used per metric unless
noted). Run: python3 -I build_dataset.py <out.csv>
"""
import csv
import sys

from specs import DEV, MODELS, GIB

U4167 = "https://github.com/ggml-org/llama.cpp/discussions/4167"
U15013 = "https://github.com/ggml-org/llama.cpp/discussions/15013"
U10879 = "https://github.com/ggml-org/llama.cpp/discussions/10879"
U15021 = "https://github.com/ggml-org/llama.cpp/discussions/15021"
U23313 = "https://github.com/ggml-org/llama.cpp/discussions/23313"
UXD = "https://github.com/XiongjieDai/GPU-Benchmarks-on-LLM-Inference"
U15396 = "https://github.com/ggml-org/llama.cpp/discussions/15396"
U16578 = "https://github.com/ggml-org/llama.cpp/discussions/16578"
ULHL = "https://github.com/lhl/strix-halo-testing"
U13565 = "https://github.com/ggml-org/llama.cpp/issues/13565"
UIK = "https://github.com/ikawrakow/ik_llama.cpp/discussions/164"
UMLX = "https://github.com/ml-explore/mlx-lm/discussions/1847"
URED = "https://www.reddit.com/r/LocalLLaMA/comments/1nn72ji/"

# file sizes (GiB) as printed by llama-bench / model cards
SIZE = {
    ("llama2-7b", "Q4_0"): 3.56, ("llama2-7b", "Q8_0"): 6.67, ("llama2-7b", "F16"): 12.55,
    ("llama3-8b", "Q4_K_M"): 4.58, ("llama3-8b", "F16"): 14.96,
    ("llama3-8b", "Q4_0"): 4.33, ("llama3-8b", "Q8_0"): 7.95,
    ("llama3-70b", "Q4_K_M"): 39.59, ("llama3-70b", "F16"): 131.42,
    ("gpt-oss-20b", "MXFP4"): 11.27, ("gpt-oss-120b", "MXFP4"): 59.02,
    ("qwen3-30b-a3b", "Q4_K_M"): 16.49, ("qwen3-30b-a3b", "Q8_0"): 30.25,
}

rows = []


def R(device, backend, model, quant, metric, n, depth, tps, url, notes="",
      os_="", eligible="yes", size_gib=None, engine="llama.cpp"):
    if size_gib is None:
        size_gib = SIZE[(model, quant)]
    d = DEV[device]
    m = MODELS[model]
    rows.append(dict(
        device=device, segment=d["segment"], backend=backend, engine=engine,
        model=model, params_total_B=m["total"],
        params_active_B=m.get("active", m["total"]) if m["moe"] else m["total"],
        moe=int(m["moe"]), quant=quant, file_size_GB=round(size_gib * GIB / 1e9, 3),
        metric=metric,
        test=(f"{metric}{n}" + (f"@d{depth}" if depth else "")),
        n_tokens=n, depth=depth, measured_tps=tps,
        spec_bw_GBs=d["bw"], spec_fp16_tflops=round(d["tflops"], 2),
        spec_conf=d["conf"], os=os_, fit_eligible=eligible,
        source_url=url, notes=notes))


# ---------------- Apple Silicon, llama.cpp Metal, Llama 2 7B (#4167) --------
apple = [
    # chip, cores, F16pp, F16tg, Q8pp, Q8tg, Q4pp, Q4tg
    ("M1", 7, None, None, 108.21, 7.92, 107.81, 14.19),
    ("M1", 8, None, None, 117.25, 7.91, 117.96, 14.15),
    ("M1 Pro", 14, 262.65, 12.75, 235.16, 21.95, 232.55, 35.52),
    ("M1 Pro", 16, 302.14, 12.75, 270.37, 22.34, 266.25, 36.41),
    ("M1 Max", 24, 453.03, 22.55, 405.87, 37.81, 400.26, 54.61),
    ("M1 Max", 32, 599.53, 23.03, 537.37, 40.2, 530.06, 61.19),
    ("M1 Ultra", 48, 875.81, 33.92, 783.45, 55.69, 772.24, 74.93),
    ("M1 Ultra", 64, 1168.89, 37.01, 1042.95, 59.87, 1030.04, 83.73),
    ("M2", 8, None, None, 147.27, 12.18, 145.91, 21.7),
    ("M2", 10, 201.34, 6.72, 181.4, 12.21, 179.57, 21.91),
    ("M2 Pro", 16, 312.65, 12.47, 288.46, 22.7, 294.24, 37.87),
    ("M2 Pro", 19, 384.38, 13.06, 344.5, 23.01, 341.19, 38.86),
    ("M2 Max", 30, 600.46, 24.16, 540.15, 39.97, 537.6, 60.99),
    ("M2 Max", 38, 755.67, 24.65, 677.91, 41.83, 671.31, 65.95),
    ("M2 Ultra", 60, 1128.59, 39.86, 1003.16, 62.14, 1013.81, 88.64),
    ("M2 Ultra", 76, 1401.85, 41.02, 1248.59, 66.64, 1238.48, 94.27),
    ("M3", 10, None, None, 187.52, 12.27, 186.75, 21.34),
    ("M3 Pro", 14, None, None, 272.11, 17.44, 269.49, 30.65),
    ("M3 Pro", 18, 357.45, 9.89, 344.66, 17.53, 341.67, 30.74),
    ("M3 Max", 30, 589.41, 19.54, 566.4, 34.3, 567.59, 56.58),
    ("M3 Max", 40, 779.17, 25.09, 757.64, 42.75, 759.7, 66.31),
    ("M3 Ultra", 60, 1121.8, 42.24, 1085.76, 63.55, 1073.09, 88.4),
    ("M3 Ultra", 80, 1538.34, 39.78, 1487.51, 63.93, 1471.24, 92.14),
    ("M4", 10, 230.18, 7.43, 223.64, 13.54, 221.29, 24.11),
    ("M4 Pro", 16, 381.14, 17.19, 367.13, 30.54, 364.06, 49.64),
    ("M4 Pro", 20, 464.48, 17.18, 449.62, 30.69, 439.78, 50.74),
    ("M4 Max", 32, 736.25, 24.29, 718.56, 43.87, 713.93, 69.95),
    ("M4 Max", 40, 922.83, 31.64, 891.94, 54.05, 885.68, 83.06),
    ("M5 Pro", 16, 1310.78, 21.39, 1302.59, 38.94, 1340.44, 67.59),
    ("M5 Pro", 20, 1588.78, 21.55, 1553.86, 38.92, 1620.64, 66.33),
    ("M5 Max", 40, 3158.49, 37.11, 3143.81, 72.42, 3219.99, 119.92),
    ("M5 Ultra", 80, 5207.14, 73.60, 4910.55, 119.01, 4944.66, 179.10),
]
for chip, c, f16pp, f16tg, q8pp, q8tg, q4pp, q4tg in apple:
    dev = f"Apple {chip} {c}c"
    build = "build 1550 (8e672ef)" if not chip.startswith("M5") else "c1d0e7a (newer build)"
    for q, pp, tg in (("F16", f16pp, f16tg), ("Q8_0", q8pp, q8tg), ("Q4_0", q4pp, q4tg)):
        if pp is None:
            continue
        R(dev, "Metal", "llama2-7b", q, "pp", 512, 0, pp, U4167, build, "macOS")
        R(dev, "Metal", "llama2-7b", q, "tg", 128, 0, tg, U4167, build, "macOS")

# ---------------- NVIDIA CUDA scoreboard, Llama 2 7B Q4_0 (#15013) ----------
cuda = [  # device, pp512 (best of FA on/off), tg128 (best)
    ("RTX 5090", 14970.15, 300.40), ("RTX 4090", 14770.63, 188.96),
    ("RTX 5080", 9487.70, 184.68), ("RTX 5070 Ti", 8419.56, 182.43),
    ("RTX 3090 Ti", 6924.01, 172.26), ("RTX 3090", 5560.06, 161.89),
    ("RTX 5090 Laptop", 7641.89, 158.14), ("RTX 4080 Super", 9439.01, 148.33),
    ("RTX 4080", 9205.93, 143.47), ("RTX 3080", 5569.56, 139.95),
    ("RTX 4070 Ti Super", 7612.32, 132.85), ("RTX 5070", 5783.44, 128.21),
    ("RTX 2080 Ti", 3107.61, 109.17), ("RTX 5060 Ti", 4195.53, 93.46),
    ("RTX 2070 Super", 2293.29, 88.06), ("RTX 3060", 2407.67, 76.92),
    ("RTX 4060 Ti", 3803.45, 64.03), ("RTX 2060 Super", 1563.77, 61.13),
    ("RTX 4050 Laptop", 1725.85, 43.72),
]
for dev, pp, tg in cuda:
    R(dev, "CUDA", "llama2-7b", "Q4_0", "pp", 512, 0, pp, U15013, "scoreboard; best of -fa 0/1")
    R(dev, "CUDA", "llama2-7b", "Q4_0", "tg", 128, 0, tg, U15013, "scoreboard; best of -fa 0/1")

# ---------------- Vulkan scoreboard (#10879) ----------------------------------
vk = [
    ("RX 7600", 1353.47, 60.88, ""), ("RX 7600 XT", 1024.38, 56.11, ""),
    ("RX 7800 XT", 2197.05, 124.86, ""), ("RX 7900 GRE", 2336.31, 116.11, ""),
    ("RX 7900 XT", 2941.58, 123.18, ""), ("RX 7900 XTX", 3889.54, 190.92, ""),
    ("RX 9060 XT", 3228.15, 74.01, ""), ("RX 9070", 3164.10, 119.71, ""),
    ("RX 9070 XT", 5603.63, 142.00, ""),
    ("Ryzen AI Max+ 395 (8060S)", 1704.30, 54.38, "row 'Ryzen AI Max+ 300 Series'"),
    ("Arc A750", 1075.94, 43.96, ""), ("Arc A770", 1119.68, 53.07, ""),
    ("Arc B570", 913.95, 50.49, "older Intel Vulkan path"),
    ("Arc B580", 620.94, 72.00, "older Intel Vulkan path (pp later ~1930, see #23313)"),
    ("RTX 3060", 2012.88, 80.59, ""), ("RTX 3090", 5200.68, 171.61, ""),
    ("RTX 4090", 10830.41, 190.10, ""), ("RTX 5090", 11796.38, 273.68, ""),
    ("Apple M3 Ultra 80c", 1116.83, 115.54, "core count not stated; 80c assumed"),
    ("Apple M4 Max 40c", 724.77, 75.02, "core count not stated; 40c assumed"),
    # laptop dGPUs
    ("RTX 5090 Laptop", 6011.18, 154.31, ""), ("RTX 4060 Laptop", 2358.03, 60.01, ""),
    ("RTX 4050 Laptop", 1154.28, 41.89, ""), ("RTX 3070 Laptop", 1832.07, 63.64, ""),
    ("RTX 3060 Laptop", 1059.76, 49.03, ""), ("RX 6800M Laptop", 861.99, 49.06, ""),
    ("RX 6600M Laptop", 605.59, 48.21, ""), ("Arc A770M Laptop", 875.92, 47.69, ""),
    ("GTX 1660 Ti Laptop", 514.34, 57.30, "no tensor cores"),
    # iGPUs (rows name a CPU family only -> memory config assumed)
    ("Radeon 890M (Ryzen AI 300)", 532.59, 23.93, "row 'AMD Ryzen AI 300 Series'"),
    ("Radeon 780M (Ryzen 7000/8000)", 311.02, 21.28, "row 'AMD Ryzen 8000 Series'"),
    ("Radeon 780M (Ryzen 7000/8000)", 312.85, 20.09, "row 'AMD Ryzen 7000 Series'"),
    ("Arc 140V (Lunar Lake)", 867.27, 25.12, "row 'Intel Core Ultra 200 Series'"),
    ("Arc Xe3 12c (Panther Lake)", 1380.07, 24.45, "row 'Intel Core Ultra 300 Series'"),
    ("Arc iGPU (Meteor Lake)", 185.51, 8.21, "row 'Intel Core Ultra 100 Series'"),
]
for dev, pp, tg, note in vk:
    os_ = "macOS" if dev.startswith("Apple") else ""
    R(dev, "Vulkan", "llama2-7b", "Q4_0", "pp", 512, 0, pp, U10879, ("scoreboard; best of -fa 0/1; " + note).strip("; "), os_)
    R(dev, "Vulkan", "llama2-7b", "Q4_0", "tg", 128, 0, tg, U10879, ("scoreboard; best of -fa 0/1; " + note).strip("; "), os_)

# ---------------- ROCm/HIP scoreboard (#15021) --------------------------------
rocm = [
    ("RX 6800 XT", 1447.07, 83.92), ("RX 6900 XT", 1948.31, 88.49),
    ("RX 7600 XT", 1199.16, 48.58), ("RX 7800 XT", 2304.63, 100.94),
    ("RX 7900 GRE", 1598.79, 97.53), ("RX 7900 XT", 3261.75, 116.15),
    ("RX 7900 XTX", 3874.25, 170.12), ("RX 9060 XT", 1479.27, 67.58),
    ("RX 9070", 2452.68, 115.32), ("RX 9070 XT", 5055.19, 101.27),
    ("Radeon AI PRO R9700", 4773.07, 97.98),
    ("Ryzen AI Max+ 395 (8060S)", 1003.53, 50.01),
    ("Radeon 880M", 213.31, 16.16),
]
for dev, pp, tg in rocm:
    R(dev, "ROCm", "llama2-7b", "Q4_0", "pp", 512, 0, pp, U15021, "scoreboard; best of -fa 0/1", "Linux")
    R(dev, "ROCm", "llama2-7b", "Q4_0", "tg", 128, 0, tg, U15021, "scoreboard; best of -fa 0/1", "Linux")
# second Strix Halo ROCm row in same scoreboard (labelled 'Radeon 8060S'): pp 4x lower -> config/build noise
R("Ryzen AI Max+ 395 (8060S)", "ROCm", "llama2-7b", "Q4_0", "pp", 512, 0, 366.08, U15021, "row 'Radeon 8060S' (likely no hipBLASLt)", "Linux")
R("Ryzen AI Max+ 395 (8060S)", "ROCm", "llama2-7b", "Q4_0", "tg", 128, 0, 48.97, U15021, "row 'Radeon 8060S'", "Linux")

# ---------------- SYCL scoreboard (#23313) ------------------------------------
sycl = [
    ("Arc A770", 937.24, 67.09), ("Arc B580", 2063.52, 88.13),
    ("Arc B570", 1355.95, 76.72), ("Arc Pro B60", 1869.16, 84.44),
    ("Arc Xe3 12c (Panther Lake)", 652.38, 19.20),
    ("Arc 140V (Lunar Lake)", 535.62, 25.26),
]
for dev, pp, tg in sycl:
    R(dev, "SYCL", "llama2-7b", "Q4_0", "pp", 512, 0, pp, U23313, "best of FA/precision variants", "Linux")
    R(dev, "SYCL", "llama2-7b", "Q4_0", "tg", 128, 0, tg, U23313, "best of FA/precision variants", "Linux")

# ---------------- XiongjieDai: Llama 3 8B / 70B with length sweep -------------
# tgN generates N tokens from empty context -> mean depth N/2; ppN from empty.
xd = [  # device, backend, model, quant, tg512, tg8192, pp512, pp8192
    ("RTX 3070", "CUDA", "llama3-8b", "Q4_K_M", 72.79, 61.64, 2402.51, 1419.97),
    ("RTX 3080", "CUDA", "llama3-8b", "Q4_K_M", 109.57, 89.90, 3728.86, 2232.21),
    ("RTX 3080 Ti", "CUDA", "llama3-8b", "Q4_K_M", 110.60, 88.63, 3690.30, 2381.52),
    ("RTX 4070 Ti", "CUDA", "llama3-8b", "Q4_K_M", 83.50, 73.46, 3936.29, 2019.71),
    ("RTX 4080", "CUDA", "llama3-8b", "Q4_K_M", 108.15, 93.71, 5389.74, 2882.03),
    ("RTX 4080", "CUDA", "llama3-8b", "F16", 40.58, None, 7246.97, None),
    ("RTX 3090", "CUDA", "llama3-8b", "Q4_K_M", 115.42, 87.49, 4030.40, 2527.40),
    ("RTX 3090", "CUDA", "llama3-8b", "F16", 47.40, 42.62, 4444.65, 2667.14),
    ("RTX 4090", "CUDA", "llama3-8b", "Q4_K_M", 130.58, 110.66, 7138.99, 4039.68),
    ("RTX 4090", "CUDA", "llama3-8b", "F16", 54.84, 50.88, 9382.00, 4744.18),
    ("Apple M1 Max 32c", "Metal", "llama3-8b", "Q4_K_M", 35.73, 26.84, 408.23, 302.92),
    ("Apple M1 Max 32c", "Metal", "llama3-8b", "F16", 18.75, 15.03, 517.34, 351.46),
    ("Apple M1 Max 32c", "Metal", "llama3-70b", "Q4_K_M", 4.34, 3.71, 34.96, 30.97),
    ("Apple M2 Ultra 76c", "Metal", "llama3-8b", "Q4_K_M", 78.81, 54.13, 994.04, 913.55),
    ("Apple M2 Ultra 76c", "Metal", "llama3-8b", "F16", 36.90, 30.68, 1175.40, 1103.44),
    ("Apple M2 Ultra 76c", "Metal", "llama3-70b", "Q4_K_M", 12.48, 9.34, 118.79, 108.57),
    ("Apple M2 Ultra 76c", "Metal", "llama3-70b", "F16", 4.76, 4.23, 147.58, 135.15),
    ("Apple M3 Max 40c", "Metal", "llama3-8b", "Q4_K_M", 48.97, 36.12, 693.32, 505.32),
    ("Apple M3 Max 40c", "Metal", "llama3-8b", "F16", 22.04, 18.74, 769.84, 515.15),
    ("Apple M3 Max 40c", "Metal", "llama3-70b", "Q4_K_M", 7.65, 5.60, 70.19, 61.96),
]
for dev, be, model, q, tg512, tg8192, pp512, pp8192 in xd:
    os_ = "macOS" if be == "Metal" else "Ubuntu 22.04"
    note = "May 2024 snapshot; tgN = N tokens from empty ctx (mean depth N/2)"
    R(dev, be, model, q, "tg", 512, 256, tg512, UXD, note, os_)
    if tg8192:
        R(dev, be, model, q, "tg", 8192, 4096, tg8192, UXD, note, os_)
    R(dev, be, model, q, "pp", 512, 0, pp512, UXD, note, os_)
    if pp8192:
        R(dev, be, model, q, "pp", 8192, 0, pp8192, UXD, note, os_)

# ---------------- gpt-oss (#15396): MoE, long prompts ------------------------
go = [  # device, backend, model, pp2048, pp32768 (or pp16384 if 32k missing), tg128, note
    ("Apple M3 Ultra 80c", "Metal", "gpt-oss-20b", 2816.47, (32768, 1351.67), 115.52, ""),
    ("Apple M2 Ultra 76c", "Metal", "gpt-oss-20b", 2191.13, (32768, 1218.99), 116.08, ""),
    ("Apple M2 Ultra 76c", "Metal", "gpt-oss-120b", 1244.57, (32768, 752.31), 79.68, ""),
    ("Apple M4 Max 32c", "Metal", "gpt-oss-20b", 1277.42, (32768, 568.13), 92.36, "36GB model => 32c"),
    ("Apple M1 Max 32c", "Metal", "gpt-oss-20b", 994.75, (32768, 497.65), 75.15, "64GB; 32c assumed"),
    ("Apple M1 Pro 16c", "Metal", "gpt-oss-20b", 515.76, (16384, 361.29), 45.68, "32GB; 16c assumed"),
    ("Apple M3 Max 40c", "Metal", "gpt-oss-20b", 1347.72, (32768, 530.52), 64.26, "128GB => 40c"),
    ("Apple M4 Pro 20c", "Metal", "gpt-oss-20b", 700.78, (32768, 419.74), 63.34, "64GB; 20c assumed"),
    ("RTX 3090", "CUDA", "gpt-oss-20b", 5170.56, (32768, 3577.10), 161.77, ""),
    ("RTX 4090", "CUDA", "gpt-oss-20b", 8022.33, (32768, 5112.35), 221.95, ""),
    ("RTX 4080 Super", "CUDA", "gpt-oss-20b", 8170.95, (32768, 6739.51), 186.51, ""),
    ("RTX 5060 Ti", "CUDA", "gpt-oss-20b", 3839.21, (32768, 3078.06), 111.51, ""),
    ("RTX 5070 Ti", "CUDA", "gpt-oss-20b", 6339.76, (32768, 4547.18), 189.45, ""),
    ("RTX 5080", "CUDA", "gpt-oss-20b", 7476.55, (32768, 5531.03), 204.85, ""),
    ("RTX 5090", "CUDA", "gpt-oss-20b", 9848.38, (32768, 6290.76), 282.51, ""),
]
for dev, be, model, pp2k, (nlong, pplong), tg, note in go:
    os_ = "macOS" if be == "Metal" else ""
    R(dev, be, model, "MXFP4", "pp", 2048, 0, pp2k, U15396, note, os_)
    R(dev, be, model, "MXFP4", "pp", nlong, 0, pplong, U15396, note, os_)
    R(dev, be, model, "MXFP4", "tg", 128, 0, tg, U15396, note, os_)

# ---------------- Depth sweeps: tg32@dN, pp2048@dN (#16578, lhl) --------------
depths = [0, 4096, 8192, 16384, 32768]
sweeps = [
    ("Ryzen AI Max+ 395 (8060S)", "ROCm", "gpt-oss-120b", "MXFP4", U16578, "GMKtec Evo X2",
     [999.59, 824.37, 703.42, 514.89, 348.59], [47.49, 44.23, 42.52, 39.71, 35.39]),
    ("Ryzen AI Max+ 395 (8060S)", "Vulkan", "gpt-oss-120b", "MXFP4", ULHL, "AMDVLK",
     [729.10, 562.15, 424.50, 249.68, 137.08], [50.05, 46.11, 43.15, 38.46, 31.54]),
    ("Ryzen AI Max+ 395 (8060S)", "ROCm", "gpt-oss-120b", "MXFP4", ULHL, "ROCm rocWMMA (stock)",
     [1006.65, 790.45, 603.83, 405.53, 223.82], [46.56, 38.25, 32.65, 25.50, 17.82]),
    ("Ryzen AI Max+ 395 (8060S)", "ROCm", "gpt-oss-120b", "MXFP4", ULHL, "ROCm rocWMMA (author-tuned)",
     [977.22, 878.54, 743.36, 587.25, 407.87], [48.97, 45.42, 43.55, 40.91, 36.43]),
    ("DGX Spark (GB10)", "CUDA", "gpt-oss-120b", "MXFP4", ULHL, "",
     [1689.47, 1733.41, 1705.93, 1514.78, 1221.23], [52.87, 51.02, 48.46, 44.78, 38.76]),
    ("Jetson Thor", "CUDA", "gpt-oss-20b", "MXFP4", U16578, "",
     [2008.85, 1862.13, 1740.90, 1446.75, 1193.93], [60.85, 55.03, 53.58, 52.49, 48.33]),
    ("Jetson Thor", "CUDA", "gpt-oss-120b", "MXFP4", U16578, "",
     [967.20, 932.85, 892.28, 827.57, 677.70], [42.00, 38.81, 39.22, 37.77, 34.02]),
    ("Jetson Thor", "CUDA", "qwen3-30b-a3b", "Q8_0", U16578, "Qwen3-Coder-30B-A3B",
     [1654.25, 1410.87, 1228.69, 985.39, 686.45], [44.26, 39.46, 36.88, 33.55, 26.92]),
]
for dev, be, model, q, url, note, pps, tgs in sweeps:
    for dpt, pp, tg in zip(depths, pps, tgs):
        R(dev, be, model, q, "pp", 2048, dpt, pp, url, ("depth sweep; " + note).strip("; "), "Linux")
        R(dev, be, model, q, "tg", 32, dpt, tg, url, ("depth sweep; " + note).strip("; "), "Linux")

# ---------------- Strix Halo backend comparison (#13565) ----------------------
R("Ryzen AI Max+ 395 (8060S)", "ROCm", "llama2-7b", "Q4_0", "pp", 512, 0, 348.62, U13565, "HIP default rocBLAS", "Linux")
R("Ryzen AI Max+ 395 (8060S)", "ROCm", "llama2-7b", "Q4_0", "pp", 512, 0, 882.81, U13565, "HIP + ROCBLAS_USE_HIPBLASLT=1", "Linux")
R("Ryzen AI Max+ 395 (8060S)", "ROCm", "llama2-7b", "Q4_0", "tg", 128, 0, 48.70, U13565, "HIP", "Linux")
R("Ryzen AI Max+ 395 (8060S)", "Vulkan", "llama2-7b", "Q4_0", "pp", 512, 0, 881.38, U13565, "", "Linux")
R("Ryzen AI Max+ 395 (8060S)", "Vulkan", "llama2-7b", "Q4_0", "tg", 128, 0, 52.82, U13565, "", "Linux")
R("Ryzen AI Max+ 395 (8060S)", "ROCm", "qwen3-30b-a3b", "Q4_K_M", "pp", 512, 0, 590.40, U13565, "hipBLASLt", "Linux")
R("Ryzen AI Max+ 395 (8060S)", "ROCm", "qwen3-30b-a3b", "Q4_K_M", "tg", 128, 0, 57.02, U13565, "", "Linux")
R("CPU Ryzen AI Max+ 395", "CPU", "llama2-7b", "Q4_0", "pp", 512, 0, 304.42, U13565, "CPU backend, 16 Zen5 cores", "Linux")
R("CPU Ryzen AI Max+ 395", "CPU", "llama2-7b", "Q4_0", "tg", 128, 0, 28.65, U13565, "CPU backend", "Linux")

# ---------------- CPU-only, mainline llama.cpp numbers from ik_llama #164 ------
for q, pp, tg in (("F16", 28.96, 7.89), ("Q8_0", 54.54, 14.04), ("Q4_0", 114.63, 23.89)):
    size = {"F16": 14.96, "Q8_0": 7.95, "Q4_0": 4.33}[q]
    R("CPU M2 Max (8P)", "CPU", "llama3-8b", q, "pp", 512, 0, pp, UIK, "mainline build 4384; PP 8 thr", "macOS", size_gib=size)
    R("CPU M2 Max (8P)", "CPU", "llama3-8b", q, "tg", 128, 0, tg, UIK, "mainline build 4384; TG 4 thr", "macOS", size_gib=size)
for q, pp in (("F16", 78.58), ("Q8_0", 147.92), ("Q4_0", 153.52)):
    size = {"F16": 14.96, "Q8_0": 7.95, "Q4_0": 4.33}[q]
    R("CPU Ryzen 9 7950X", "CPU", "llama3-8b", q, "pp", 512, 0, pp, UIK, "mainline build 4384; 16 thr; BF16 row", "", size_gib=size)

# ---------------- MLX vs llama.cpp on M4 Max 40c (mlx-lm #1847) ---------------
mlx = [  # model, mlx_tg, mlx_size_GB, lcpp_tg, lcpp_size_GB
    ("falcon-h1-1.5b", 300.0, 0.88, 148.6, 0.90),
    ("falcon-h1-3b", None, None, 86.1, 1.76 * GIB / 1e9),
    ("falcon-h1-7b", None, None, 52.9, 4.28 * GIB / 1e9),
    ("granite-4.0-h-350m", 521.7, 0.20, 282.1, 0.21),
    ("granite-4.0-h-1b", 275.8, 0.83, 144.0, 0.86),
    ("nemotron-3-nano-4b", 176.8, 2.24, 88.4, 2.84),
]
for model, mtg, msz, ltg, lsz in mlx:
    if mtg:
        R("Apple M4 Max 40c", "MLX", model, "MLX-4bit", "tg", 256, 0, mtg, UMLX,
          "mlx_lm generate 256 tok; hybrid SSM", "macOS", size_gib=msz * 1e9 / GIB, engine="mlx-lm")
    R("Apple M4 Max 40c", "Metal", model, "Q4_K_M", "tg", 256, 0, ltg, UMLX,
      "llama-bench tg256 b8680; hybrid SSM", "macOS", size_gib=lsz * 1e9 / GIB)

# ---------------- Partial / expert offload (not fit) --------------------------
R("RTX 3060", "CUDA", "gpt-oss-20b", "MXFP4", "tg", 128, 0, 30.64, U15396,
  "12GB card, some expert tensors -ot to CPU; host RAM unknown", "", "offload")
R("RTX 3060", "CUDA", "gpt-oss-20b", "MXFP4", "pp", 2048, 0, 2229.95, U15396,
  "12GB card, some expert tensors -ot to CPU", "", "offload")
R("RTX 4070", "CUDA", "gpt-oss-120b", "MXFP4", "tg", 128, 0, 10.0, URED,
  "~10 t/s; i5-12600K, 64GB DDR5, --n-cpu-moe 31, 24k ctx (approximate, user report)", "", "offload")
R("RTX 4070", "CUDA", "gpt-oss-120b", "MXFP4", "pp", 512, 0, 191.0, URED,
  "~191 t/s prompt; same setup (approximate)", "", "offload")

if __name__ == "__main__":
    out = sys.argv[1]
    with open(out, "w", newline="") as f:
        w = csv.DictWriter(f, fieldnames=list(rows[0].keys()))
        w.writeheader()
        w.writerows(rows)
    print(f"wrote {len(rows)} rows to {out}")
