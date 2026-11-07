`trialm calibrate` is optional. It generates deterministic synthetic GGUFs in
`$TRIALM_HOME/cache/calib/`, invokes the installed `llama-bench`, and atomically
saves raw JSON rows and fits in `$TRIALM_HOME/calib.json`. It downloads no model
weights and installs no engine. Generator version 3 is part of every cache name.
A normal run has an eight-minute budget; `--quick` has a two-minute budget.
**These are budgets, not measured completion times.** The restricted M5 session
could not access Metal, so neither completion target has been validated yet.
Unfinished groups resume; every finished engine row survives cancellation.

The optional first-run offer is for bare `trialm`, `detect`, and `recommend`.
Enter runs calibration. Any other key skips immediately. `prefs.json` remembers
the answer while preserving unrelated preferences. Pipes, JSON, CI,
`TRIALM_NO_INPUT`, `--plain`, and `--device` do not prompt. A skipped offer leaves
one reminder on human `recommend` output. The native Windows console path is
compile-checked; the actual single-key offer was exercised on a macOS PTY.

**Grid and scope**

| Probe | Geometry | Quant | Layer counts |
|---|---|---|---|
| Hybrid | Qwen3.6 35B-A3B, hidden 2048, 256 experts / 8 active, vocab 248320 | UD-IQ3_XXS mix: IQ2_S gate/up, IQ3_XXS down, Q6_K attention/shared/head | 4, 8; 12 held out |
| Sliding MoE | gpt-oss 20B, hidden 2880, 32 experts / 4 active, vocab 201088 | MXFP4 experts, Q8_0 attention/embedding/head | 2, 6; 4 held out |
| Sliding MoE | Gemma 4 26B-A4B, hidden 2816, 128 experts / 8 active, vocab 262144 | Q4_K; Q8_0 for rows not divisible by 256; Q6_K head | 6, 12 |
| Dense | Qwen3 8B, hidden 4096, vocab 151936 | Q4_K, IQ3_XXS, IQ4_XS, MXFP4, Q8_0 | 2, 6 |
| MoE | Qwen3 30B-A3B, hidden 2048, 128 experts / 8 active, vocab 151936 | Q4_K | 2, 6 |

Quick runs only the two default-pick training pairs, with the same depth grid.
Full runs prioritize those pairs and their checks, then Gemma and the quant
sweep. Each file is at least 256 MiB from real tensor dimensions, without padding
an otherwise cache-resident toy. Hybrid counts preserve the 3:1 recurrent/full
attention ratio; Gemma counts preserve its 5:1 sliding/full pattern. Full heads
and synthetic SentencePiece vocabularies, scores, token types, byte tokens and
special-token IDs are present. Random streams differ across tensors and chunks.
FP16/BF16 values and quant scales are bounded and finite.

Decode uses `-n 32`, prefill `-p 512`, at `-d 0,8192,32768`, `-r 2`, `-o json`,
`-ngl 99`, and flash attention. A separate `-p 1,2,3,4 -n 0 -d 0` run measures
small-prompt verify costs. llama-bench caches depth preparation within one
invocation. Verify costs describe llama-bench's prompt path: returning logits
for every speculative candidate in a server may add output-head work.

MLA is **not covered** in this version. Gemma's fit is transferable only as an
architecture/quant approximation; it does not measure every Gemma geometry.
The default Qwen probe preserves tensor formats of the selected early layers;
the real UD file additionally uses IQ4_XS down projections in layers 34, 38, 39.
Dense quant probes isolate a kernel and do not reproduce every mixed-quant
recipe sold under the same file label. No fit is a real-model validation result.

**Fit derivation**

For each family class, quant class, depth bucket and operation, fit the existing
roofline's baseline and the measured per-token seconds independently:

```
measured(L) = a + b L
baseline(L) = A + B L
speed multiplier(L) = (A + B L) / (a + b L)
```

`a` captures the full head and launch/fixed cost. `b` captures layer cost. Least
squares uses the two training layer counts and both raw repetitions. Extra layer
counts are held out of the fit and included in its residuals. Negative intercepts,
nonpositive slopes, invalid timings, and one-count fits are rejected.

For observed seconds `y_i`, fitted `yhat_i`, `n` residual samples, `n_train`
training samples, mean training layer count `Lbar`, and training sum of squares
`Sxx = sum((L_i-Lbar)^2)`:

```
sigma = sqrt(sum(log(y_i/yhat_i)^2) / (n-2))
leverage(L) = sqrt(1 + 1/n_train + (L-Lbar)^2/Sxx)
band_pct(L) = ceil(100 * (exp(1.15 * sigma * leverage(L)) - 1))
```

1.15 is the central 75% normal-error interval coefficient. The leverage term
widens the interval when extrapolating from shallow probes to a full model.
Two observations alone cannot estimate a band and are not applied. A noisy run
may yield a wider band than the prior; the code does not clamp it to a promised
number. This is a conditional synthetic fit interval, not empirical 75% coverage
on real architectures. Correlated repetitions and unseen architecture effects
remain limitations until the owner-run benchmarks validate transfer.

Where only a family reference quant was measured, compose its speed multiplier
with the dense target/reference quant ratio. Add their log-error radii, avoiding
an assumption of independent fits. Unmeasured quants, depths, MLA, CPU offload,
CPU-only machines, and MLX retain the prior. Changed OS, RAM, chip/GPU or engine
build invalidates the fingerprint. Missing RAM detection prevents making a new
fingerprint. `--device` predictions never load or apply calibration.

For llama.cpp-kernel runtimes, the timing methods apply each depth bucket inside
the existing roofline calculation. The hook recomputes every derived headline
and scenario; JSON carries `kind: calibrated`. Ollama and LM Studio carry a note
that transfer assumes matching engine kernels/settings. MLX remains `simulated`.

**Verification on this M5, 2026-11-07**

Homebrew llama-bench reports build 11960, commit `8642541b2`. The full CLI attempt
used `/tmp/trialm-calib-src/real-run` as `TRIALM_HOME`. Generation plus the first
failed GPU invocation took **0.99 s** (first 4-layer Qwen synthetic file generated
in about 0.68 s); Metal failed to create its command queue. Zero GPU rows/fits
were stored. `--status` reported incomplete. Hardware detection also returned
RAM=0 (`sysctl ... Operation not permitted`); fingerprint creation now rejects
that missing identity field instead of persisting a usable calibration.

Separate CPU-only `llama-bench -dev none -ngl 0 -nopo 1 -p 1 -n 1` smoke tests
successfully loaded and executed all nine representative geometry/quant probes.
The final cached smoke suite took about 10 seconds. These CPU results were never
saved as GPU calibration. Full-size synthetic files, rather than real model
weights, were used throughout.

Actual-machine `trialm predict ... --runtime llamacpp --json` was captured before
and after the failed calibration. Both outputs were identical and `simulated`:

| Model/file | Decode short / deep | Prefill at scenario prompt | Band decode / prefill |
|---|---:|---:|---:|
| qwen3.6:35b-a3b, `--quant IQ3_XXS` | about 49 / 36 tok/s | about 890 tok/s | ±60% / ±70% |
| gpt-oss:20b, MXFP4 | about 40 / 29 tok/s | about 730 tok/s | ±45% / ±55% |

These are prior roofline outputs, **not measured model speeds**. Both memory
checks said `no` because RAM detection was blocked. A calibrated comparison and
a measured 5–8 minute grid remain outstanding. Run in a normal terminal with GPU
and RAM access before interpreting these as a working setup.

`tools/m5-bench.sh` was syntax-checked, **never run**. It asks via `/dev/tty`
before any model download, pins repository revisions, resumes `.part` downloads,
and writes JSON fixtures under `internal/calib/testdata/apple-m5` by default.
It implements the separate real-model grid in PLAN §10, including verify sizes
1,2,3,4,8. No pinned engine downloader was added; P2 owns that installation path.

Geometry and loader references:

- [Qwen3.6 config](https://huggingface.co/Qwen/Qwen3.6-35B-A3B/blob/main/config.json)
- [gpt-oss config](https://huggingface.co/openai/gpt-oss-20b/blob/main/config.json)
- [Gemma 4 config](https://huggingface.co/google/gemma-4-26B-A4B-it/blob/main/config.json)
- [llama.cpp b11960 model loaders](https://github.com/ggml-org/llama.cpp/tree/b11960/src/models)
- [llama-bench depth and timing behavior](https://github.com/ggml-org/llama.cpp/blob/b11960/tools/llama-bench/llama-bench.cpp)

Only HTTP Range **headers** were read to audit the selected Qwen and gpt-oss
GGUF tensor layouts; their weight data was not downloaded.
