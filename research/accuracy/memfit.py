"""Memory-fit comparison: llama.cpp-style accounting vs llmfit's formula,
plus macOS wired-limit check. Stdlib only. Run: python3 -I memfit.py
"""
GiB = 1024 ** 3

# name: (file_GiB Q4_K_M, total_B, layers_full, layers_swa, swa_win, kv_heads, head_dim, q_heads, vocab, llmfit_has_metadata)
M = {
    "Llama-3.1-8B": (4.58, 8.03, 32, 0, 0, 8, 128, 32, 128256, False),
    "Qwen3-14B": (8.38, 14.77, 40, 0, 0, 8, 128, 40, 151936, True),
    "Qwen3-30B-A3B": (17.28, 30.53, 48, 0, 0, 4, 128, 32, 151936, True),
    "Qwen2.5-32B": (18.49, 32.76, 64, 0, 0, 8, 128, 40, 152064, True),
    "gpt-oss-20b(MXFP4)": (11.27, 20.91, 12, 12, 128, 8, 64, 64, 201088, True),
    "gpt-oss-120b(MXFP4)": (59.02, 116.83, 18, 18, 128, 8, 64, 64, 201088, True),
    "Llama-3.3-70B": (39.6, 70.55, 80, 0, 0, 8, 128, 64, 128256, False),
}
LF_BPP_Q4KM = 0.58
LF_BPP_MXFP4 = 0.55


def kv_gib(m, ctx, bpe=2.0, swa_full=False):
    _, _, full, swa, win, kvh, hd, *_ = m
    per = 2 * kvh * hd * bpe
    return per * (full * ctx + swa * (ctx if swa_full else min(ctx, win + 512))) / GiB


def compute_buf_gib(m, ctx, fa=True, ubatch=512):
    """Rough llama.cpp compute-buffer size: logits/output + activations; without
    flash attention the KQ score tensor is n_ctx x n_ubatch x n_head x fp32."""
    q_heads, vocab = m[7], m[8]
    base = (ubatch * vocab * 4 + ubatch * 4096 * 4 * 8) / GiB
    if fa:
        return base
    return base + ctx * ubatch * q_heads * 4 / GiB


def llmfit_mem(name, m, ctx):
    bpp = LF_BPP_MXFP4 if "MXFP4" in name else LF_BPP_Q4KM
    w = m[1] * bpp  # GB, llmfit mixes GB/GiB
    if m[9]:
        kv = 2 * m[5] * m[6] * ctx * 2 * m[2] / GiB  # full-attention layers only
    else:
        kv = 0.000008 * m[1] * ctx  # coarse fallback when catalog lacks metadata
    return w + kv + 0.5


def wired_cap(ram):
    return ram * (2 / 3 if ram <= 36 else 0.75)


print(f"{'model':22s} {'ctx':>6s} {'weights':>7s} {'KVf16':>6s} {'KVq8':>5s} {'cbufFA':>6s} {'cbufNoFA':>8s} {'llama.cpp~':>10s} {'llmfit':>7s} {'diff':>6s}")
for name, m in M.items():
    for ctx in (8192, 32768, 65536):
        w = m[0]
        kv = kv_gib(m, ctx)
        kv8 = kv_gib(m, ctx, 1.0625)
        cb = compute_buf_gib(m, ctx, True)
        cbn = compute_buf_gib(m, ctx, False)
        real = w + kv + cb + 0.4  # + backend context/scratch
        lf = llmfit_mem(name, m, ctx)
        print(f"{name:22s} {ctx:6d} {w:7.1f} {kv:6.1f} {kv8:5.1f} {cb:6.2f} {cbn:8.2f} {real:10.1f} {lf:7.1f} {100*(lf/real-1):5.0f}%")

print("\nmacOS default GPU wired cap (approx. 2/3 RAM <=36GB, 3/4 above) vs llmfit unified pool (0.98 x RAM):")
for ram in (16, 24, 32, 36, 48, 64, 128):
    print(f"  {ram:4d} GB RAM: wired cap ~{wired_cap(ram):5.1f} GB, llmfit accepts up to {0.98*ram:5.1f} GB")
print("\nfalse-fit examples (32k ctx, FA on, f16 KV):")
for name, ram in (("Qwen3-14B", 16), ("Qwen3-30B-A3B", 24), ("Qwen2.5-32B", 32), ("gpt-oss-120b(MXFP4)", 64)):
    m = M[name]
    need = m[0] + kv_gib(m, 32768) + compute_buf_gib(m, 32768) + 0.4
    print(f"  {name} on {ram}GB Mac: needs ~{need:.1f} GiB; wired cap ~{wired_cap(ram):.1f}; llmfit pool {0.98*ram:.1f} -> llmfit says {'fits' if llmfit_mem(name, m, 32768) <= 0.98*ram else 'no'}")
