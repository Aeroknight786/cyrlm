These are trimmed coordinator captures from `/tmp/claude-501/p0b/calibdata/`,
Apple M5, llama-bench build 11960, November 7, 2026. All 400 numeric timing rows,
probe identities, check flags, depths and predictor baselines are unchanged.
Duplicate raw JSON (including environment metadata), old fits and notes were
removed. No samples were selected or repaired. Legacy repetitions appear as
separate rows, so refit can reconstruct their exact medians and spread.

The original quiet sliding/MXFP4 depth-zero decode fit had a 118% band; its
hybrid/IQ3_XXS decode fit had a 42,389% band. The contended counterparts were
244% and 140,190%. Quiet still includes severe outliers; regression tests must
not pretend it is clean. See the parent README for the replacement derivation.

`calib-m5-v2.json` and `real-qwen3.5-9b-Q4_K_M.json` are unmodified owner-run
M5/build-11960 inputs from `/tmp/claude-501/p0b/calibdata/` (2026-11-07). The
former took 192 seconds; the latter used Qwen3.5 9B Q4_K_M, mlock, FA on and
three repetitions. See `../README.md` for the external validation result and
its wide bands. These raw files include the source file path and machine
identity needed to audit the benchmark; no weights are included.
