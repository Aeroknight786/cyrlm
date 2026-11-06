"""Published device specs and model geometry used by the predictors.

Bandwidth = spec-sheet peak memory bandwidth (GB/s, 1e9).
TFLOPS    = spec dense FP16 matrix throughput (tensor/XMX/WMMA where present,
            FP16-accumulate rate on NVIDIA; Apple M1-M4 = FP32 vector rate,
            M5 = vector x4 for its GPU neural accelerators; CPUs = FP32 FMA peak).
spec_conf = 'high' when the row names an exact SKU, 'low' when the memory
            config had to be assumed (iGPU rows naming only a CPU family).
"""

GB = 1e9
GIB = 1024 ** 3


def apple_tflops(cores, clk_ghz, mult=1.0):
    return cores * 128 * 2 * clk_ghz / 1000.0 * mult


# key: (segment, vendor_class, bw_GBs, fp16_TFLOPS, spec_conf, note)
DEV = {}


def add(key, seg, bw, tf, conf="high", note=""):
    DEV[key] = dict(segment=seg, bw=bw, tflops=tf, conf=conf, note=note)


# ---- Apple Silicon GPU (cores in key) ----
for chip, bw, cores, clk, seg, mult in [
    ("M1", 68.25, 7, 1.278, "apple_base", 1), ("M1", 68.25, 8, 1.278, "apple_base", 1),
    ("M1 Pro", 200, 14, 1.296, "apple_pro", 1), ("M1 Pro", 200, 16, 1.296, "apple_pro", 1),
    ("M1 Max", 400, 24, 1.296, "apple_max", 1), ("M1 Max", 400, 32, 1.296, "apple_max", 1),
    ("M1 Ultra", 800, 48, 1.296, "apple_ultra", 1), ("M1 Ultra", 800, 64, 1.296, "apple_ultra", 1),
    ("M2", 100, 8, 1.398, "apple_base", 1), ("M2", 100, 10, 1.398, "apple_base", 1),
    ("M2 Pro", 200, 16, 1.398, "apple_pro", 1), ("M2 Pro", 200, 19, 1.398, "apple_pro", 1),
    ("M2 Max", 400, 30, 1.398, "apple_max", 1), ("M2 Max", 400, 38, 1.398, "apple_max", 1),
    ("M2 Ultra", 800, 60, 1.398, "apple_ultra", 1), ("M2 Ultra", 800, 76, 1.398, "apple_ultra", 1),
    ("M3", 100, 10, 1.38, "apple_base", 1),
    ("M3 Pro", 150, 14, 1.38, "apple_pro", 1), ("M3 Pro", 150, 18, 1.38, "apple_pro", 1),
    ("M3 Max", 300, 30, 1.38, "apple_max", 1), ("M3 Max", 400, 40, 1.38, "apple_max", 1),
    ("M3 Ultra", 819, 60, 1.38, "apple_ultra", 1), ("M3 Ultra", 819, 80, 1.38, "apple_ultra", 1),
    ("M4", 120, 10, 1.578, "apple_base", 1),
    ("M4 Pro", 273, 16, 1.578, "apple_pro", 1), ("M4 Pro", 273, 20, 1.578, "apple_pro", 1),
    ("M4 Max", 410, 32, 1.578, "apple_max", 1), ("M4 Max", 546, 40, 1.578, "apple_max", 1),
    # M5: GPU neural accelerators; matmul peak taken as 4x vector (Apple's "4x AI compute" claim).
    ("M5 Pro", 307, 16, 1.6, "apple_pro", 4), ("M5 Pro", 307, 20, 1.6, "apple_pro", 4),
    ("M5 Max", 614, 40, 1.6, "apple_max", 4), ("M5 Ultra", 1228, 80, 1.6, "apple_ultra", 4),
]:
    add(f"Apple {chip} {cores}c", seg, bw, apple_tflops(cores, clk, mult),
        "high" if mult == 1 else "low", "M5 matmul TFLOPS assumed" if mult != 1 else "")

# ---- NVIDIA desktop (bw GB/s, FP16 tensor dense w/ FP16 acc) ----
for name, bw, tf, seg in [
    ("RTX 2060 Super", 448, 57.4, "nv_desktop"), ("RTX 2070 Super", 448, 72.5, "nv_desktop"),
    ("RTX 2080 Ti", 616, 107.6, "nv_desktop"),
    ("RTX 3060", 360, 51.2, "nv_desktop"), ("RTX 3070", 448, 81.3, "nv_desktop"),
    ("RTX 3080", 760, 119.1, "nv_desktop"), ("RTX 3080 Ti", 912, 136.4, "nv_desktop"),
    ("RTX 3090", 936, 142.3, "nv_desktop"), ("RTX 3090 Ti", 1008, 160.0, "nv_desktop"),
    ("RTX 4060 Ti", 288, 88.3, "nv_desktop"), ("RTX 4070", 504, 116.8, "nv_desktop"),
    ("RTX 4070 Super", 504, 141.9, "nv_desktop"),
    ("RTX 4070 Ti", 504, 160.4, "nv_desktop"), ("RTX 4070 Ti Super", 672, 176.4, "nv_desktop"),
    ("RTX 4080", 717, 194.9, "nv_desktop"), ("RTX 4080 Super", 736, 208.9, "nv_desktop"),
    ("RTX 4090", 1008, 330.3, "nv_desktop"),
    ("RTX 5060 Ti", 448, 94.8, "nv_desktop"), ("RTX 5070", 672, 123.5, "nv_desktop"),
    ("RTX 5070 Ti", 896, 175.8, "nv_desktop"), ("RTX 5080", 960, 225.1, "nv_desktop"),
    ("RTX 5090", 1792, 419.0, "nv_desktop"),
    ("T4", 320, 65.0, "nv_datacenter"),
]:
    add(name, seg, bw, tf)

# ---- NVIDIA laptop (bandwidth of the reference memory config; TGP unknown) ----
for name, bw, tf in [
    ("RTX 5090 Laptop", 896, 228.0), ("RTX 4060 Laptop", 256, 58.3),
    ("RTX 4050 Laptop", 192, 48.5), ("RTX 3070 Laptop", 448, 64.0),
    ("RTX 3060 Laptop", 336, 52.0), ("GTX 1660 Ti Laptop", 288, 9.1),
]:
    add(name, "nv_laptop", bw, tf, "high", "TGP/clock not reported")

# ---- NVIDIA unified (out of target list; used for depth analysis) ----
add("DGX Spark (GB10)", "nv_unified", 273, 62.5, "low")
add("Jetson Thor", "nv_unified", 273, 62.5, "low")

# ---- AMD discrete ----
for name, bw, tf, seg in [
    ("RX 6800 XT", 512, 41.5, "amd_rdna2"), ("RX 6900 XT", 512, 46.1, "amd_rdna2"),
    ("RX 7600", 288, 43.5, "amd_rdna3"), ("RX 7600 XT", 288, 45.1, "amd_rdna3"),
    ("RX 7800 XT", 624, 74.6, "amd_rdna3"), ("RX 7900 GRE", 576, 91.9, "amd_rdna3"),
    ("RX 7900 XT", 800, 103.2, "amd_rdna3"), ("RX 7900 XTX", 960, 122.8, "amd_rdna3"),
    ("RX 9060 XT", 320, 102.6, "amd_rdna4"), ("RX 9070", 640, 144.5, "amd_rdna4"),
    ("RX 9070 XT", 640, 194.7, "amd_rdna4"), ("Radeon AI PRO R9700", 640, 191.3, "amd_rdna4"),
    ("RX 6800M Laptop", 384, 23.6, "amd_laptop"), ("RX 6600M Laptop", 224, 17.7, "amd_laptop"),
]:
    add(name, seg, bw, tf)

# ---- AMD APU ----
add("Ryzen AI Max+ 395 (8060S)", "amd_strix_halo", 256, 59.4)
add("Radeon 890M (Ryzen AI 300)", "amd_igpu", 120, 23.8, "low", "LPDDR5X-7500 assumed")
add("Radeon 880M", "amd_igpu", 120, 17.8, "low", "LPDDR5X-7500 assumed")
add("Radeon 780M (Ryzen 7000/8000)", "amd_igpu", 89.6, 16.6, "low", "DDR5-5600 dual-channel assumed")

# ---- Intel ----
for name, bw, tf in [
    ("Arc A750", 512, 117.0), ("Arc A770", 560, 137.6), ("Arc B570", 380, 101.5),
    ("Arc B580", 456, 116.5), ("Arc Pro B60", 456, 98.5), ("Arc A770M Laptop", 512, 105.0),
]:
    add(name, "intel_arc", bw, tf)
add("Arc 140V (Lunar Lake)", "intel_igpu", 136.5, 33.5, "low", "LPDDR5X-8533 assumed")
add("Arc Xe3 12c (Panther Lake)", "intel_igpu", 153.6, 60.0, "low", "LPDDR5X-9600 assumed")
add("Arc iGPU (Meteor Lake)", "intel_igpu", 119.5, 9.0, "low", "LPDDR5X-7467 assumed")

# ---- CPU-only (bw = SoC/DRAM spec; tflops = FP32 FMA peak of the threads used) ----
add("CPU M2 Max (8P)", "cpu_apple", 400, 0.95, "high", "SoC bandwidth; CPU cluster cannot reach it")
add("CPU Ryzen AI Max+ 395", "cpu_x86_lpddr5x", 256, 4.6)
add("CPU Ryzen 9 7950X", "cpu_x86_ddr5", 96, 2.56, "low", "DDR5-6000 assumed")
add("CPU Ryzen 7 7840HS", "cpu_x86_ddr5", 89.6, 1.0, "low", "DDR5-5600 SO-DIMM assumed")
add("CPU Ryzen 7 8745H", "cpu_x86_ddr5", 89.6, 1.0, "low", "DDR5-5600 assumed")
add("CPU Ryzen 7 9800X3D", "cpu_x86_ddr5", 96, 1.5, "low", "DDR5-6000 assumed")
add("CPU Core Ultra 7 155H", "cpu_x86_lpddr5x", 119.5, 0.8, "low", "LPDDR5X-7467 assumed")

# ---- Model geometry ----
# read_frac: fraction of file bytes read per decoded token (MoE active set,
#            excludes input-embedding table). flops_params: params doing
#            matmuls per token (2*P FLOPs). kv_*: for KV-read and attention terms.
MODELS = {
    # name: total_B, read_frac, flops_params_B, layers_full, layers_swa, swa_window, kv_heads, head_dim, q_heads
    "llama2-7b": dict(total=6.74, read_frac=0.981, flops=6.61, full=32, swa=0, win=0, kvh=32, hd=128, qh=32, moe=False),
    "llama3-8b": dict(total=8.03, read_frac=0.935, flops=7.50, full=32, swa=0, win=0, kvh=8, hd=128, qh=32, moe=False),
    "llama3-70b": dict(total=70.55, read_frac=0.985, flops=69.5, full=80, swa=0, win=0, kvh=8, hd=128, qh=64, moe=False),
    "gpt-oss-20b": dict(total=20.91, read_frac=2.57 / 12.10, flops=3.61, full=12, swa=12, win=128, kvh=8, hd=64, qh=64, moe=True, active=3.61),
    "gpt-oss-120b": dict(total=116.83, read_frac=3.53 / 63.37, flops=5.12, full=18, swa=18, win=128, kvh=8, hd=64, qh=64, moe=True, active=5.12),
    "qwen3-30b-a3b": dict(total=30.53, read_frac=3.04 / 30.53, flops=3.04, full=48, swa=0, win=0, kvh=4, hd=128, qh=32, moe=True, active=3.3),
    # hybrid SSM/attention models (decode treated as dense, depth ~0)
    "falcon-h1-1.5b": dict(total=1.55, read_frac=0.95, flops=1.4, full=0, swa=0, win=0, kvh=0, hd=0, qh=0, moe=False),
    "falcon-h1-3b": dict(total=3.1, read_frac=0.95, flops=2.9, full=0, swa=0, win=0, kvh=0, hd=0, qh=0, moe=False),
    "falcon-h1-7b": dict(total=7.6, read_frac=0.95, flops=7.2, full=0, swa=0, win=0, kvh=0, hd=0, qh=0, moe=False),
    "granite-4.0-h-350m": dict(total=0.34, read_frac=0.85, flops=0.3, full=0, swa=0, win=0, kvh=0, hd=0, qh=0, moe=False),
    "granite-4.0-h-1b": dict(total=1.5, read_frac=0.9, flops=1.3, full=0, swa=0, win=0, kvh=0, hd=0, qh=0, moe=False),
    "nemotron-3-nano-4b": dict(total=3.97, read_frac=0.9, flops=3.6, full=0, swa=0, win=0, kvh=0, hd=0, qh=0, moe=False),
}


def kv_bytes_per_token_at(model, depth, kv_bytes=2.0):
    """Bytes of KV cache read to decode one token at a given context depth."""
    m = MODELS[model]
    per_tok_layer = 2 * m["kvh"] * m["hd"] * kv_bytes
    return per_tok_layer * (m["full"] * depth + m["swa"] * min(depth, m["win"]))


def attn_flops_per_token(model, ctx):
    """Attention score+value FLOPs for one token attending to ctx tokens."""
    m = MODELS[model]
    per_layer = 4 * m["qh"] * m["hd"]
    return per_layer * (m["full"] * ctx + m["swa"] * min(ctx, m["win"]))
