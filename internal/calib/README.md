`trialm calibrate` is optional. It generates deterministic synthetic GGUFs in
`$TRIALM_HOME/cache/calib/`, invokes the installed `llama-bench`, and atomically
saves observations, unmodified engine JSON and fits in `$TRIALM_HOME/calib.json`.
It downloads no model weights and installs no engine. Generator version 3 names
the synthetic files; fitting version 2 names the measurement/fit interpretation.
Old version-3 observations can be refitted without regenerating files. Coverage
lists only family/quants with usable decode and prefill at every depth bucket;
partial fits and raw completion alone do not count as usable calibration.

```
trialm calibrate --plan          # full grid and per-step estimated seconds
trialm calibrate --plan --quick  # two default picks, including held-out checks
trialm calibrate                 # run, or resume finished groups
trialm calibrate --refit         # refit saved rows without engine/GPU access
trialm calibrate --status
```

All modes accept `--json`. Plan and refit need neither hardware detection nor an
installed engine. Refit preserves raw rows, fingerprint, completion and original
wall time; it replaces fits and saves atomically. Loading an older fitting
version rebuilds its fits in memory before predictions can use them. This never
bypasses a changed machine/build fingerprint.

The plan estimates **about 6 min full / 2 min quick** on the owner's M5, using the
saved build-11960 per-step timings. Actual runtime and accuracy still need an
owner-run Metal check. There is **no automatic deadline**. Ctrl-C saves finished
rows; rerunning resumes groups with the same fingerprint. Legacy rows lacking
an explicit load mode and three repetitions do not satisfy the new run grid.
Failed fits stay rejected on resume; use `--refit` to inspect their reasons and
move the old `calib.json` aside for a fresh measurement run.

The optional first-run offer is for bare `trialm`, `detect`, and `recommend`.
Enter runs calibration. Any other key skips immediately. `prefs.json` remembers
the answer while preserving unrelated preferences. Pipes, JSON, CI,
`TRIALM_NO_INPUT`, `--plain`, and `--device` do not prompt. A skipped offer leaves
one reminder on human `recommend` output.

**Grid and measurement**

| Family | Geometry and reference quant | Training layers | Held out | Deep reference layers |
|---|---|---|---|---|
| Hybrid | Qwen3.6 35B-A3B, UD-IQ3_XXS mix | 4, 8 | 12 | 8 |
| Sliding MoE | gpt-oss 20B, MXFP4 | 2, 6 | 4 | 6 |
| Gemma | Gemma 4 26B-A4B, Q4_K_M | 6, 12 | 18 | 6 |
| Dense | Qwen3 8B, Q4_K_M | 2, 6 | 4 | 2 |
| MoE | Qwen3 30B-A3B, Q4_K_M | 2, 6 | 4 | 6 |

Dense also measures IQ3_XXS, IQ4_XS, MXFP4 and Q8_0, each with its own training
pair and held-out probe. Quick covers the first two families **and their checks**.
Each probe runs `-p 512 -n 8 -r 3 -o json -ngl 99 -fa on -lm mlock` and a separate
`-p 1,2,3,4 -n 0 -d 0` verify group. Three repetitions are the smallest odd count
with a majority median. llama-bench's normal warm-up remains enabled.

Only one reference probe per family runs `-d 0,8192,32768`. Every other probe
runs at depth zero. Depth preparation, rather than decode duration, dominated
the old 751-second grid: the old dense six-layer invocations took about a minute
**per quant**. The new grid pays that context preparation cost once per family.
Smaller Gemma/dense deep references save preparation work while preserving a
complete Gemma attention cycle. Depth-zero checks validate layer scaling; the
deep incremental cost has a stated transfer assumption below.

An mlock failure can be fatal or a warning followed by a successful exit. Both
retry the invocation with `-lm mmap`. Only lock failures trigger this retry;
Metal failures and invalid GPU rows do not. The stderr collector remembers lock
warnings even after they leave the bounded log tail. Failed pinned attempts are
discarded before retry. Each saved observation records `load_mode`, repetition
count and spread; fallback notes persist across resume. These are requested
engine load modes, not a claim that the OS pinned every GPU allocation.

Every `(probe, depth, operation)` contributes **one median seconds/token**.
Unmodified `samples_ns` remain in its raw JSON. Refit also accepts the legacy
one-row-per-repetition format, grouping those repetitions before fitting.
Median absolute deviation estimates repetition spread:

```
y_i = median(repetition seconds/token)
s_i = 1.4826 * median(abs(repetition - y_i))
```

A lone slow repetition cannot move the majority median. Two saved repetitions
have no majority; their spread is retained, not discarded as an outlier. One
repetition alone does not support a usable fit. These statistics cannot identify
systematic contention affecting every repetition equally.

Each synthetic file is at least 256 MiB from real tensor dimensions, without
padding a cache-resident toy. Full output heads and synthetic vocabularies,
scores, token types, byte tokens and special-token IDs are retained. Hybrid
counts preserve the 3:1 recurrent/full attention ratio; Gemma counts preserve
its 5:1 sliding/full pattern. Random streams differ across tensors and chunks;
FP16/BF16 values and quant scales are bounded and finite.

MLA and MLX are not covered. Gemma measures the 26B-A4B geometry, not every Gemma
shape. The default Qwen probe preserves the early-layer tensor formats; the real
UD file additionally uses IQ4_XS down projections in layers 34, 38, 39. Dense
quant probes isolate kernels rather than every mixed-quant recipe sold under a
file label. Verify rows measure the prompt path; serving all speculative logits
can add output-head work. None of these fits validates real-model performance.

**Runtime estimate**

`plan.go` keeps a transparent, intentionally approximate per-step model. For a
main invocation with `L` layers and synthetic file size `W` bytes:

```
zero_work = 1 + 0.35 L                 # process, warm-up, 512-token prefill, n=8
cold_generation = 0.5 + W / 2e9        # inferred synthetic writer throughput
extra_depth = 1.15 (legacy - (1 + 0.4 L))  # only for the five deep references
main_seconds = ceil(1.15 (zero_work + cold_generation + extra_depth))
verify_seconds = ceil(1.15 (0.5 + 0.04 L))
```

`legacy` is the observed old full-depth invocation: hybrid/8 ≈18 s, sliding/6
≈42 s, Gemma/6 ≈36 s, dense/2 ≈18 s, MoE/6 ≈41 s. Depth state is cached by
llama-bench, so the estimate does not multiply preparation time by repetition
count. The extra allowances account for more state restores and process/load
variation. The coefficients for zero work and generation are estimates, not
separately measured regressions. Allow about 25% variation; mmap fallback or
contention can cost more. The current cold plan totals about **380 s / 120 s**
(54 / 12 invocations). Resume ETA sums unfinished steps and does not count
skipped steps as elapsed benchmark work. `--plan` always shows the full cold grid.

**Fit and band derivation**

For each family, quant, depth bucket and operation, fit measured medians and the
existing roofline baseline independently, holding checks out of both fits:

```
measured(L) = a + b L
baseline(L) = A + B L
speed_multiplier(L) = baseline(L) / measured(L)
```

`a` captures full-head and launch cost; `b` captures layer cost. Use least squares
on the two training medians. A negative unconstrained intercept uses the physical
nonnegative boundary `a=0, b=sum(L_i*y_i)/sum(L_i²)`; prefill batching sometimes
puts the unconstrained optimum below zero. Nonpositive slopes, invalid timings
and one-count fits cannot be applied. Every accepted fit needs an independent
held-out layer check. Any check with `abs(y_check / measured(L_check) - 1) > 35%`
rejects that fit, even if its repetition spread would explain the error. Failed
checks cannot be bypassed by quant/depth composition.

The old formula exponentiated an RMS log residual multiplied by shallow-layer
regression leverage. It counted correlated repetitions as independent evidence
and turned large check errors into bands of thousands of percent. The replacement
is a **sensitivity envelope in seconds**, with no claim of 75% confidence coverage.
For target layer count `L`, training layer mean `Lbar`, and
`Sxx=sum((L_i-Lbar)²)`:

```
w_i(L) = 1/n_train + (L-Lbar)(L_i-Lbar)/Sxx
training_noise(L) = sum(abs(w_i(L)) * s_i)
check_error_j = abs(y_check_j - measured(L_check_j))
check_envelope(L) = max_j((check_error_j + s_check_j) * max(1,L/L_check_j))
band_pct(L) = ceil(100 * (0.10 +
                (training_noise(L) + check_envelope(L)) / measured(L)))
```

On the nonnegative intercept boundary, use the larger absolute weight from this
affine formula and the origin fit, `L*L_i/sum(L_i²)`. The absolute weights
propagate uncertain training medians through
interpolation/extrapolation without assuming independence. The check discrepancy
is conservatively allowed to be a fixed-cost error **or** a layer-cost error:
use the larger of factor 1 and `L/L_check`. Thus extrapolation is accounted for
in seconds, normalized by the full model's own cost. The 10 percentage-point
floor acknowledges sparse synthetic checks and unvalidated real-model transfer;
it is a policy allowance, not an empirical confidence level. Bands have no upper
clamp. A clean 5.2/7.15/9.1 ms hybrid series with roughly 1% repetition spread
supports about 15–20%; a failed check remains rejected.

For deep context, measure a paired incremental cost on the family reference:

```
delta_layer(d) = (median_reference(d) - median_reference(0)) / L_reference
measured_q(L,d) = measured_q(L,0) + L * delta_layer(d)
```

Apply the same operation to baseline times. Keep the depth-zero held-out check
and training noise, and add to the band numerator:

```
L/L_reference * (spread_reference(d) + spread_reference(0) +
                 0.15 * abs(median_reference(d) - median_reference(0)))
```

Context overhead transfers additively across quants of the same family, with
15% of the incremental penalty as an extra allowance for unmeasured layer/quant
interactions. This is **not a held-out validation at depth**. Persist `depth_source`
and the extra per-layer error so the assumption is reviewable. A negative measured
depth penalty is considered noise and is not used as a speedup. Explicit deep
measurements/checks, if present in old rows, take precedence over this transfer.

For other unmeasured architecture/quants, compose the architecture reference
multiplier with the dense target/reference ratio. Add log-error radii conservatively;
these composed bands are usually wider. Unmeasured kernels, CPU offload, CPU-only
machines and `--device` predictions keep the prior. Prediction integration requires
both decode and prefill coverage at every timing depth bucket; a partial or rejected
fit cannot silently mix corrected timing with an incorrect `calibrated` label.
Changed OS, RAM, chip/GPU or engine build invalidates the fingerprint.

**Offline regression evidence**

`testdata/calib-quiet.json` and `calib-contended.json` retain all 400 source timing
rows and baselines from the coordinator's `/tmp/claude-501/p0b/calibdata/` files.
Only duplicate engine/environment JSON and obsolete fits/notes were trimmed.
The quiet file is not clean enough to promise 10–30% everywhere: its hybrid decode
check has 11.6 and 30.2 ms repetitions against a roughly 9.1 ms prediction. That
check is rejected. Its sliding depth-zero decode multiplier is about 1.1, with a
band around 60% instead of the old 118%; deep noise/check failures remain visible.
Legacy dense, MoE and Gemma rows lack independent checks and stay rejected until
the new grid supplies them. The contended fixture cannot claim a narrow band on
the failed/noisy default-pick groups. Clean synthetic regression and fake-engine
runs exercise the complete new grid, produce 10–30% bands, and verify median
storage, mlock-warning fallback, depth transfer, cancellation and resume.

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
a measured new-grid completion time remain outstanding. Run in a normal terminal with GPU
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
