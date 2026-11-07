# Speed-model review (gpt-6-astra, 2026-11-07)

*Read-only review requested by the owner: accuracy, scope for improvement, overengineering and non-generalisable tuning. Reviewed at commit 5318eee.*

**Verdict: keep the simple physical model and the new attention term. The calibration system promises more generality than its evidence supports, and several measurement-handling bugs can make predictions worse after calibration.**

The strongest demonstrated improvement is already today’s attention change: for Qwen3.5 9B on the M5, the current uncalibrated formula gives approximately **22.4 tok/s short and 8.3 tok/s at 64K**, versus **16.2 at 64K previously**. That is substantially closer to the reported 21 and 7–9. It does not establish accuracy across other models, machines, or deep prefill.

My top three recommendations, ranked by user impact per line of code:

1. **Fix how real measurements are applied.** Preserve actual depth and prompt interval; normalize against the prediction at that same point. Currently, recording 8.96 tok/s at 64K can produce a subsequent prediction of **6.14 tok/s at 64K**.
2. **Stop unsupported calibration transfer.** A Qwen hybrid MoE probe does not validate every recurrent model, quantization, or depth. Remove the cross-quant composition and architecture-only fallback until they beat a simpler correction on held-out real models.
3. **Evaluate and refit the shipped predictor through one path.** Keep `fit-attn`, repair its observation handling, and extend that approach to the existing coefficients. Stop maintaining separate Python/Go models and manually synchronized accuracy claims.

**The single most valuable next fix is recommendation 1.** It turns measurements already collected from users into reliable evidence. For predictions before download, deep prefill and synthetic-to-real transfer are the largest remaining weaknesses.

I made no edits. Python analysis ran successfully; Go tests and `go run ./tools/fit-attn` could not start because the read-only sandbox blocks Go’s temporary build/cache directories. Current-formula numbers below are analytical Python replays, not executed Go test results.

---

**Accuracy: the headline median hides the difficult cases.**

Replaying the checked-in research evaluator produces the following results. These describe that evaluator, **not a fresh validation of the current Go predictor**. Its depth convention also has a bug discussed below.

| Backend | Decode median absolute error; rows | Prefill median absolute error; rows |
|---|---:|---:|
| Metal | 6.8%; 125 | 5.7%; 127 |
| CUDA | 13.5%; 65 | 21.1%; 72 |
| ROCm | 4.7%; 31 | 69.5%; 32 |
| Vulkan | 15.3%; 41 | 32.5%; 41 |
| SYCL | 10.5%; 6 | 21.9%; 6 |
| CPU | 137.2%; 4 | 29.7%; 7 |
| MLX | 65.6%; 4 | No evaluated rows |
| Overall | 10.1%; 276 | 12.7%; 285 |

The overall mean absolute errors are substantially worse: **19.4% decode and 37.8% prefill**. The source is the leave-one-device-out evaluation in [predict.py:272](research/accuracy/predict.py:272).

The important subdivisions are:

- **Depth:** deep decode has **18.6% median error**, with only **55% within ±25%**. Long/deep prefill has **30.9% median error**, with only **47% within ±25%**.
- **MoE:** decode median error is **13.1%**, versus **9.3% for non-MoE**; prefill is **22.6%**, versus **9.8%**.
- **Hybrid models:** the public hybrid/SSM segment has **31.8% median decode error**, but all ten rows come from the M4 Max. That does not validate Qwen’s different recurrent architecture.
- **Intel Arc:** **41.2% decode / 238.9% prefill** median error. A general backend coefficient is not enough here.
- **Strix Halo:** good decode median, **7.9%**, but **83.1% prefill** in the current evaluator replay. The saved report says **176.2%**, demonstrating that the checked-in report is stale.
- **Largest prefill miss:** Strix Halo/Vulkan/gpt-oss-120B at depth 32K measures **137 tok/s**, versus approximately **1,226** in the research evaluator: **+794%**.

The old llmfit comparison is useful evidence that this approach beats a crude alternative: its saved decode median error is **26.1% versus 10.1%**. It does not validate today’s calibration grid or long-context promises. See [out_primary.txt:3](research/accuracy/out_primary.txt:3).

**The M5 fixture exposes the remaining problem particularly clearly.**

Using the current formula and analytically updating the saved synthetic baselines for the new attention rate:

| M5 Qwen3.5 9B measurement | Measured | Current uncalibrated | Synthetic calibration after rebasing |
|---|---:|---:|---:|
| Decode, short context | 14.4 | 22.4, **+56%** | 19.8, **+38%** |
| Decode, 32K | 9.4 | 12.1, **+29%** | 17.1, **+81%** |
| Prefill, 2K from empty | 368 | 698, **+90%** | 316, **−14%** |
| Prefill, 2K at 32K | 162 | 418, **+158%** | 288, **+78%** |

These are the older build-11960 fixture measurements, distinct from today’s Ready runs. The short-context variation from roughly 14 to 22 is real evidence of machine/run variability, not justification for choosing whichever observation fits best. See [STATUS.md:24](docs/STATUS.md:24).

A useful ablation: keeping this fixture’s **depth-zero correction constant** instead of transferring separate depth corrections gives approximately **10.7 decode and 189 prefill at 32K**, errors of **+14% and +17%**. One model cannot establish a replacement policy, but it certainly does not justify the more elaborate depth transfer.

---

**1. Real-row overrides currently compare different quantities. High priority.**

[real.go:54](internal/calib/real.go:54) groups decode observations into buckets; [calibration.go:162](internal/predict/calibration.go:162) converts the resulting speed into a multiplier at bucket anchors 0/8K/32K.

Consequently, a measurement at 64K is treated as though it were measured at 32K. The model’s depth slowdown is then applied again between 32K and 64K. With today’s M5 formula, **8.96 recorded at 64K becomes 6.14 predicted there**. Measurements at 16K, 32K and 64K can also be mixed into one median.

Prefill is worse: `RealSpeed` ignores depth altogether for prefill. The same short-prompt speed becomes a separate multiplier against the baseline at **every** anchor. This forces approximately the same prefill speed at all three anchors, cancelling the depth penalty the predictor was supposed to preserve.

**Fix:** compute residuals at each observation’s actual interval. A short prefill measurement can supply one overall scale correction; it cannot identify the long-context attention coefficient.

The measurement itself also loses information:

- [ready.go:301](internal/verify/ready.go:301) aims at about 90% of the requested depth, possibly rescales again, but [ready.go:139](internal/verify/ready.go:139) records the requested depth.
- [ready.go:168](internal/verify/ready.go:168) measures prompt tokens divided by wall-clock TTFT. That includes request overhead and first-token latency and can include cache effects; it is not pure engine prefill time.
- [core/setup.go:65](internal/core/setup.go:65) compares that roughly batch-sized measurement with predicted **2K** prefill.
- [real.go:13](internal/calib/real.go:13) does not retain prompt length, actual token counts, KV type, batch settings, flash attention, model-file identity, or token-estimation status. Those distinctions matter when reusing observations.

Preserving those fields is justified bookkeeping, not a new modelling system.

**2. Repetition spread is being promoted into prediction accuracy.**

[real.go:68](internal/calib/real.go:68) allows two identical measurements to produce a **0% band**. The overrides then extrapolate those measurements across depth buckets. Repeatability at one point says nothing about extrapolation error.

Similarly, [calibration.go:224](internal/predict/calibration.go:224) clears `PrefillMeasureOnly` after calibration, even when prefill uses an unmeasured default. “Calibrated” should not imply that every displayed operation has evidence.

Keep measurement spread separate from prediction error. Until real held-out coverage exists, retain a conservative prior for extrapolated quantities.

**3. The synthetic grid checks layer scaling, not transfer to a model class.**

[synthetic.go:29](internal/calib/synthetic.go:29) supplies one geometry per class. Examples include:

- Dense: train on **2 and 6 layers**, check **4**, then extrapolate to 36 or other model sizes.
- Hybrid: train on **4 and 8**, check **12**, then transfer to other recurrent geometries.
- Deep dense attention: one **2-layer** reference.
- Hybrid reference: Qwen3.6 **MoE**, also applied to Qwen3.5 9B **dense hybrid**.

A held-out layer count within one geometry cannot validate changes in width, attention heads, expert routing, quant mixture, or kernel selection.

The classification in [calibration.go:94](internal/predict/calibration.go:94) is especially broad: any recurrent model becomes `hybrid`; sliding-window models share a class irrespective of whether their other computation resembles gpt-oss.

The transfer equation at [calib.go:583](internal/calib/calib.go:583) assumes architecture and quant effects separate multiplicatively. Dense quant ratios are measured at an arbitrary **36 layers** and applied even to the architecture’s deep-context correction. The fallback at line 589 goes further: it applies the architecture correction when the quant ratio is unavailable or rejected.

**Delete that fallback and suspend cross-quant composition.** Preserve direct, explicitly scoped synthetic probes. Do not delete all calibration: the M5 short-prefill example improves from **698 to 316**, against **368 measured**. The cost of withdrawing unsupported transfer is less coverage and sometimes worse point estimates; there is no defensible dataset-wide percentage for that cost.

**4. Rebasing is necessary and mostly correctly conceived, but the regression test bypasses it.**

The new attention term and calibration do **not inherently double count** when calibration is recomputed relative to the current predictor. [calib.go:198](internal/calib/calib.go:198) invalidates old fits, and `Rebaseline` is the right mechanism.

However:

- [calibration_test.go:164](internal/predict/calibration_test.go:164) calls `FitRows` directly on a fixture without the current baseline version.
- [calib.go:266](internal/calib/calib.go:266) stamps the current baseline version without checking whether row baselines were recomputed.
- Analytically replaying this produces **10.2 tok/s at 32K with stale baselines**, versus **17.1 after rebasing**, against **9.4 measured**. The test can therefore show an apparent improvement that production rebasing removes.
- [run.go:69](internal/calib/run.go:69) can reuse old rows and subsequently call `FitRows`. The normal CLI upgrade mitigates this, but correctness should not depend on that earlier call succeeding.
- [calibrate.go:361](internal/calibrationcli/calibrate.go:361) rebases using the currently detected machine without first verifying the saved fingerprint.

Require baseline validity at the fitting boundary. Rebaseline the fixture before evaluating it. Future coefficient changes must invalidate derived fits automatically or reliably bump the version.

**5. Keep `fit-attn`; repair its evidence selection.**

Its basic scope is appropriate: one coefficient per backend, retain the simpler prior when held-out performance does not improve.

My formula replay reproduced:

| Backend | Comparisons / devices | Fitted efficiency | Held-out slowdown error |
|---|---:|---:|---:|
| Metal | 9 / **4** | 0.194 | **16.7%**, versus 22.3% |
| CUDA | 23 / 9 | 0.673 | **5.8%**, versus 4.7%; correctly unchanged |
| ROCm | 4 / 1 | 0.498 | Cannot hold out |
| Vulkan | 4 / 1 | 0.399 | Cannot hold out |

But several details weaken the apparent evidence:

- [main.go:157](tools/fit-attn/main.go:157): F16 comparisons silently disappear because the catalog models have no F16 quant. This loses the useful Q4/F16 cross-check. The nine Metal comparisons are six public Q4 comparisons plus three runs from the owner.
- [main.go:114](tools/fit-attn/main.go:114): public source/run identity is discarded, and duplicate depths overwrite one another. On Strix Halo/ROCm, the stock **17.82 tok/s** deep result disappears behind the later tuned **36.43** result.
- [main.go:117](tools/fit-attn/main.go:117): eligibility, engine and settings are not checked.
- [main.go:57](tools/fit-attn/main.go:57): medians weight comparisons, not devices. The owner supplies one-third of Metal’s comparisons. Removing that device changes the fitted median from **0.194 to 0.133**.
- [main.go:164](tools/fit-attn/main.go:164): slowdown ratios do **not** cancel the weight-time baseline. For `time=A+KV/B`, the ratio still depends on `A`. The test comment claiming otherwise at [predict_test.go:169](internal/predict/predict_test.go:169) is incorrect.

This coefficient is an effective correction within this predictor, not a clean measurement of hardware bandwidth. That is acceptable if stated honestly. Report devices separately from comparisons, preserve run conditions, and report absolute-speed error alongside slowdown error.

**6. The research evaluation and production implementation have drifted.**

[build_dataset.py:209](research/accuracy/build_dataset.py:209) stores mean depth `N/2` for generation-from-empty rows. [predict.py:25](research/accuracy/predict.py:25) adds `N/2` again.

This affects **39 generation rows**. For example, `tg8192@d4096` is evaluated at 8192 instead of its recorded mean depth 4096. Correcting that convention changes overall decode median error from **10.1% to 9.6%**. The aggregate change is small; the inconsistent meaning of depth is still a correctness defect.

The checked-in prefill report also differs from today’s script:

- Overall median: **13.5% saved / 12.7% replayed**.
- Strix Halo: **176.2% saved / 83.1% replayed**.

Furthermore, “other model” calibration actually excludes only the same **model–quant pair** at [predict.py:332](research/accuracy/predict.py:332). Of 202 decode calibration evaluations, **70 have only other quantizations of the same model available**; prefill has **76 of 211**. Those results should not be advertised as general cross-model transfer.

---

**Constants and special cases that need a weaker claim or stronger evidence.**

| Location | Concern | Honest alternative |
|---|---|---|
| [predict.go:27](internal/predict/predict.go:27) | Main backend efficiencies and overheads have substantial public support. These are broadly reasonable. | Keep; regenerate and evaluate using the shipped feature calculations. |
| [predict.go:45](internal/predict/predict.go:45) | Vulkan attention efficiency is manually raised from fitted **0.03 to 0.08**, reducing predicted attention time by 62.5%. | Evaluate the floor held out; otherwise retain a clearly labelled prior or require measurement. |
| [predict.go:32](internal/predict/predict.go:32), [attn_eff.go:12](internal/predict/attn_eff.go:12) | SYCL/CPU assumptions and MLX’s borrowed Metal coefficients are not equally validated. MLX gets 0.19 despite no MLX depth evidence. | Explicit sparse-data priors; do not imply held-out support for MLX. |
| [specs.go:236](internal/specs/specs.go:236) | M5 assumes **4×** throughput. The same FLOPS value feeds attention, although the assumption concerns matmul acceleration. | Treat matmul and attention assumptions separately; measure before tightening bands. |
| [catalog.go:216](internal/catalog/catalog.go:216) | Qwen’s **0.72** decode factor comes from two devices and scales weights, attention and overhead across backends. | Retain only as a provisional, scoped family observation; test an unseen backend/model before broad application. |
| [predict.go:494](internal/predict/predict.go:494) | CPU efficiencies **0.6/0.85**, plus the CPU table assumptions, lack demonstrated general accuracy here. | Prefer measured machine bandwidth with broad residual uncertainty. |
| [engines.json:97](internal/engine/engines.json:97), [engines.json:193](internal/engine/engines.json:193), [engines.json:278](internal/engine/engines.json:278) | Runtime multipliers **0.95, 0.97, 0.98** are small unsupported adjustments; llama.cpp is already the fitted baseline. | Collapse to 1 absent matched measurements. Their direct effect is only 2–5%. |
| [synthetic.go:139](internal/calib/synthetic.go:139), [calibration.go:112](internal/predict/calibration.go:112) | One UD mixed-quant layout stands in for a quant class; Q4_K_S is treated as Q4_K_M. File labels do not uniquely identify tensor kernels. | Scope to the measured layout; avoid claiming equivalence. |
| [calib.go:304](internal/calib/calib.go:304), [calib.go:399](internal/calib/calib.go:399), [calib.go:592](internal/calib/calib.go:592), [calib.go:666](internal/calib/calib.go:666) | **35% rejection, 15% depth allowance, 60/70% transfer allowance, 10% floor** are policy choices, not calibrated coverage. Compounding them does not create evidence. | Keep simple rejection safeguards; replace unsupported transfer with the prior and describe uncertainty without implied coverage. |
| [predict.go:204](internal/predict/predict.go:204) | Segment bands, calibrated discounts and depth/hybrid additions are hand-set. | Fit displayed coverage from held-out residuals where available; label the rest as assumptions. |
| [predict_test.go:35](internal/predict/predict_test.go:35), [predict_test.go:185](internal/predict/predict_test.go:185) | Tolerances admit known **+65%** prefill error; RTX 3090 permits 20%, M5 30%. These are regression guards, not accuracy gates. | Keep that distinction explicit; evaluate the full dataset separately. |
| [calibration_test.go:220](internal/predict/calibration_test.go:220) | Real-model misses call `Skip`, so deterioration cannot fail validation. | Report expected failures separately and enforce an explicit accuracy/non-regression criterion. |

The M5-specific calibration runtime estimates in [plan.go:33](internal/calib/plan.go:33) are fine as labelled rough scheduling estimates. They do not need sophisticated fitting.

**What I would actually delete or collapse.**

- **Delete default speculative verify sweeps.** [plan.go:43](internal/calib/plan.go:43) doubles the grid from 27 to 54 invocations. The fixture contains **35 verify fits out of 83**. `VerifySeconds` and `SpecVerifySecs` are exposed/stored but have no current prediction consumer. Removing them costs **zero current decode/prefill prediction accuracy**. Make them optional when a supported speculative feature actually uses them.
- **Keep short and target-depth decode verification.** That directly catches the failure motivating this review. The intermediate 8K point can be optional diagnostics; it should not substitute for the target.
- **Collapse cross-quant transfer and its compounded bands.** A **±126% decode / ±154% prefill** envelope cannot support a useful accuracy promise. Keeping raw observations and rejected-fit reasons is worthwhile; maintaining several layers of arithmetic around an unidentified transfer is not.
- **Keep the physical attention term and small fitter.** The observed benefit justifies their complexity.
- **Keep raw rows, fingerprints, repetitions and an independent layer check.** These are useful foundations. Narrow the scope of what they establish.
- **Do not implement PLAN §5’s additional cache-hit, overlap, acceptance and speculative priors yet.** The plan itself describes two-point curves and one-device examples. Preserve these as measured recipe claims until held-out evidence supports general prediction.

**Other correctness risks affecting whole agent turns.**

At [predict.go:325](internal/predict/predict.go:325), `FirstReply` includes only one decoded token. That is a first-token time. The session starts with this value and therefore omits the rest of the first answer—**299 tokens for the default scenario**.

At [predict.go:329](internal/predict/predict.go:329), follow-up depth is capped at the context limit before adding new input and output, so it can simulate beyond available context. At line 353, generation rounds up to 50-token chunks: a 301-token answer is charged as 350. Compaction simply halves history and charges no rebuilding cost. These are illustrative scenario assumptions, not validated whole-session timing.

Settings also have incomplete timing consequences: [predict.go:480](internal/predict/predict.go:480) uses flash attention for memory estimation, while timing ignores whether it is enabled; KV quantization reduces byte counts without a measured kernel-cost correction. Synthetic calibration always uses its own fixed settings. Avoid transferring it across materially different configurations.

Finally, uncertainty uses inconsistent frames. [calib.go:642](internal/calib/calib.go:642) derives sensitivity in seconds, while [check.go:182](internal/calib/check.go:182) applies a symmetric percentage interval to speed. A ±25% time interval corresponds to approximately **−20%/+33% speed**, not ±25%. Choose and document one error definition before claiming coverage.

**Scalability needs less machinery, with fewer sources of truth.**

Today, adding public rows does not automatically improve shipped predictions. `fit-attn -write` regenerates one table; decode/prefill tables, runtime adjustments, family rules and bands still require hand edits. Local real measurements help only the exact model identifiers and presently suffer the framing defects above. Synthetic calibration reruns can reuse completed rows rather than obtain fresh evidence.

The minimal durable structure is:

1. **One observation format:** exact model geometry/file, engine/backend/build, relevant settings, starting depth, processed/generated token counts, time and run identity.
2. **One predictor implementation used for fitting and evaluation:** extend the existing small Go fitter; generate existing coefficient tables and a reproducible error report.
3. **Grouped validation:** hold out devices for population coefficients and real models/geometries for transfer claims. Do not count multiple runs as additional devices.
4. **Simple local corrections:** fit only the quantities the observations identify; otherwise retain the population prior. Keep exact measurements available without pretending they establish cross-model accuracy.

No larger learning system is justified yet. The immediate work is measurement correctness, narrower transfer, and reproducible evaluation.
---

# Second opinion: can long-context prefill be predicted more reliably? (Fable, 2026-11-07)

# Can long-context prefill be predicted more accurately? (2026-11-07, read-only)

Method: replayed the shipped Go prefill formula in Python on the 285 eligible pp rows, then a leave-one-device-out (LODO) refit of the same dense/MoE/attention form, plus within-device long/short slopes (which cancel level error and isolate the attention coefficient). Scripts: (scratch) pp_replay.py, pp_lodo.py. Decode at depth not re-examined; fit-attn's held-out rule already governs it.

## 1. Verdict

Not with current data. Where you have held-out evidence (Metal; CUDA desktop on current builds) long-context prefill is already close to short-context accuracy and no refit beats the shipped constants, while the 31% headline comes from one Strix Halo box, nine 2024 CUDA rows that look like flash-attention-off, and spec-level errors on DGX Spark/Thor — none of which a population constant can fix. The caveat: the CUDA evidence is 7 rows of gpt-oss-20b from one thread, so "fine" is not established for dense 30B+ models, where attention is ~63% of predicted time at 32K vs 42% for gpt-oss-20b.

## 2. Held-out numbers (LODO, Go form)

- Long/deep, all 66 rows: 31.1% median (the review's 30.9% was right). By stratum: Metal 2025 builds 7.3% (7 devices); CUDA desktop 2025 18.7% (7 devices, 7 rows, all gpt-oss-20b pp32768); CUDA 2024 rows 47%, every one over-predicted 1.4–2.2x; nv_unified ~20% but Spark 0.7x and Thor 1.4x at every depth (level, not slope); ROCm 58% and Vulkan 420% because every depth row is the same Strix Halo device, so held out there is no attention evidence at all.
- In-sample replay of the shipped constants gives the same picture (CUDA desktop 2025 20.8%, Metal 4–12%), so the constants themselves are not off.
- Slopes: implied eff_attn per device is Metal 0.19–0.40 (shipped 0.279), CUDA 2025 0.13–0.57 (shipped 0.313), CUDA 2024 0.05–0.10 on all 7 cards — the uniform signature of non-flash attention (f32 scores, memory-bound). triaLM launches llama.cpp with FA on.
- Refits: excluding 2024 rows from training moves CUDA desktop 18.7 -> 17.4%, Metal 7.3 -> 7.1%; fitting eff_attn from slopes alone is 13% vs 11% (CUDA), 12% vs 11% (Metal). Nothing to gain.

## 3. Changes worth making (ranked)

1. Record Ready and calibration prefill measurements properly (review rec 1): actual depth and prompt length, engine prompt_ms not wall TTFT, FA/batch/KV type. Every Ready run then becomes a (device, model, depth, prefill) row; today that evidence is lost. It is the only route that fixes Strix Halo, Arc, ROCm, M5 and nv_unified, which are driver and level effects. Cost: bookkeeping. Risk: none to generalisation; it is measurement.
2. Verify the XD (May 2024) command line; if FA was off, exclude those CUDA rows from CUDA prefill fits and reported error, and label eff_attn "FA on". This is scoping, not an accuracy gain (~1 point held out), but it stops the error claim describing a regime triaLM never launches. The Metal XD rows are consistent with 2025 and should stay.
3. M5: hold. The fixture (Qwen3.5-9B Q4_K_M, pp2048 at d0/8K/32K = 368/220/162) implies attention at 0.16–0.32 of the vector rate, i.e. the 4x accelerator factor is wrong for attention, but 1x over-corrects at 32K (0.28 vs 0.44 measured) and the short-context level is also 0.36 of 4x vs 0.66 on the public M5 Pro F16 row. One device, one hybrid, busy machine: measure before touching specs.go; the ±30% band is honest.

## 4. What not to do

- Refit the attention constants per backend; held out it does not help.
- Add per-segment constants for Strix Halo, nv_unified or Arc (one device each, 2–4x driver swing). Measure-only is correct.
- Add a bandwidth-bound attention term: with FA and ubatch 512 the KV read is ~40x cheaper than the FLOPs on a 4090; the 2024 rows only look memory-bound because FA was off.
- Tune the Vulkan 0.08 floor on four rows from one box (it makes them 1.6x optimistic, but that is one device).

## 5. Cheapest data that would unlock it

1. CUDA desktop, current build, FA on: `llama-bench -m <qwen3-8b Q4_K_M> -m <qwen3-32b Q4_K_M> -p 2048 -d 0,8192,32768 -n 32` on three or more cards (3090/4090/5090 class plus a 5060 Ti). Dense full-attention long-context evidence CUDA has none of. ~10 min per card.
2. The owner's M5 with a dense 8B at Q4_K_M and Q8_0, same sweep: separates "accelerators skip Q4_K_M" from "hybrid layers are slow".
3. One RDNA3/4 dGPU on Vulkan and ROCm, same sweep: a second device for both backends.
4. triaLM's own runs can supply all of this: calibration already sweeps 0/8K/32K but on synthetic GGUFs; Ready measures the real model. With item 3.1 fixed and an opt-in upload, the dataset grows on real user hardware.
