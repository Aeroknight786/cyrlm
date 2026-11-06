# Cloud prefill + ship KV cache + local decode  vs  local prefill.
# KV bytes/token = 2 (K,V) * full-attention layers * kv_heads * head_dim * bytes/elem
KV = {  # name: (full_attn_layers, kv_heads, head_dim)
  "gpt-oss-20b (SWA hybrid)":  (12, 8, 64),    # 24 layers, half sliding-window(128)
  "gpt-oss-120b (SWA hybrid)": (18, 8, 64),
  "Qwen3-Next-80B-A3B (linear-attn hybrid)": (12, 2, 256),
  "Qwen3-30B-A3B (MoE, full attn)": (48, 4, 128),
  "Llama-3.1-8B (dense)":      (32, 8, 128),
  "Qwen3-32B (dense)":         (64, 8, 128),
}
def kvb(m, b=2.0): L,h,d = KV[m]; return 2*L*h*d*b
print("KV cache per token (fp16 / q8_0) and at 32k tokens:")
for m in KV:
    print(f"  {m:42s} {kvb(m)/1024:6.0f} KiB / {kvb(m,1.0625)/1024:5.0f} KiB   32k: {kvb(m)*32768/1e9:5.2f} GB / {kvb(m,1.0625)*32768/1e9:5.2f} GB")

LINKS = {"100 Mbps": 100e6, "300 Mbps": 300e6, "1 Gbps": 1e9}
EFF, OVH = 0.9, 0.5            # usable link fraction; RTT+restore+queue seconds
def offload(tokens, m, cloud_tps, link, b):
    xfer = tokens*kvb(m,b)/(LINKS[link]*EFF/8)
    return max(tokens/cloud_tps, xfer) + OVH    # chunked prefill pipelines compute with transfer

# (device, model, tokens, local_tps, cloud_tps, note)  local_tps from bench_data.csv
CASES = [
 ("M4 Pro",       "gpt-oss-20b (SWA hybrid)",  32768, 419.7, 8000, "cold 32k, measured pp32768"),
 ("M3 Max",       "gpt-oss-20b (SWA hybrid)",  32768, 530.5, 8000, "cold 32k, measured pp32768"),
 ("RTX 3090",     "gpt-oss-20b (SWA hybrid)",  32768, 3577,  8000, "cold 32k, measured pp32768"),
 ("RTX 4090",     "gpt-oss-20b (SWA hybrid)",  32768, 5112,  8000, "cold 32k, measured pp32768"),
 ("Strix Halo",   "gpt-oss-120b (SWA hybrid)", 32768, None,  4000, "cold 32k, integrated depth sweep"),
 ("M3 Max",       "Llama-3.1-8B (dense)",       8192, 505.3, 6000, "cold 8k, measured pp8192"),
 ("M4 Pro",       "gpt-oss-20b (SWA hybrid)",   3000, 300,   8000, "per-turn delta @~30k depth (est. rate)"),
 ("RTX 4090",     "gpt-oss-20b (SWA hybrid)",   3000, 4400,  8000, "per-turn delta @~30k depth (est. rate)"),
 ("Strix Halo",   "gpt-oss-120b (SWA hybrid)",  3000, 348.6, 4000, "per-turn delta @32k, measured"),
]
# Strix Halo ROCm gpt-oss-120b pp2048 at depth 0/4k/8k/16k/32k = 1000/824/703/515/349
strix_cold = 4096/912 + 4096/764 + 8192/609 + 16384/432
print(f"\nStrix Halo cold 32k local time (integrated): {strix_cold:.0f} s\n")
print(f"{'device':11s} {'model':26s} {'case':38s} {'local':>7s} | " + " | ".join(f"{l} fp16/q8" for l in LINKS))
for dev, m, n, ltps, ctps, note in CASES:
    local = strix_cold if ltps is None else n/ltps
    cells = []
    for l in LINKS:
        a, b = offload(n, m, ctps, l, 2.0), offload(n, m, ctps, l, 1.0625)
        cells.append(f"{a:6.1f}/{b:5.1f}s")
    print(f"{dev:11s} {m[:26]:26s} {note:38s} {local:6.1f}s | " + " | ".join(cells))

print("\nBreak-even link speed = local prefill tok/s x KV bytes/token (above it, offload wins on transfer):")
for dev, m, n, ltps, ctps, note in CASES[:6]:
    r = (32768/strix_cold) if ltps is None else ltps
    print(f"  {dev:11s} {m[:28]:28s} fp16 {r*kvb(m)*8/1e6:6.0f} Mbps   q8_0 {r*kvb(m,1.0625)*8/1e6:6.0f} Mbps")
