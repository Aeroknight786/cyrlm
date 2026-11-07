# P2 verify handoff

## Changes

- `internal/verify/ready.go`, `ready_test.go`, and `verify.go` implement Ready checks, spill detection, estimated token labels, follow-up cache status, and a spec sweep that records costs only when engine timings confirm cache reuse and exactly 1–4 newly processed tokens. An unconfirmed sweep is inconclusive and does not fail Ready.
- `internal/calib/real.go` creates a fingerprinted store when none exists and records matching real-model measurements. `internal/predict/calibration.go` applies exact model, quant and runtime evidence even without synthetic fits; unmeasured buckets retain their default prediction and band. Two or more samples use measured spread; one uses the stated default band. The caller must still pass Ready speeds to `calib.RecordReal`.
- `internal/upstream/upstream.go` and `internal/llm/types.go` are the smallest changes outside verify/calib/predict ownership: they preserve whether streamed usage or llama timings actually reported cached tokens. No goldens changed. No exported shared-contract signatures changed.

## Verification

- Fake-engine tests cover spill below 50%, absent usage and timings, estimated labels, cache reused/not reused/unknown, confirmed sweep counts, and inconclusive sweeps. Predictor tests cover matching real-only evidence, its measured band, and model/quant/engine isolation. A fingerprint integration test records a real row and changes the stored fingerprint, but is skipped in this sandbox because `sysctl hw.memsize` is denied; it needs a normal host to exercise.
- `TRIALM_REAL_ENGINE=1 go test -v ./internal/verify -run '^TestRealEngine$'` passed once with generated Q4_K_M two-layer synthetic GGUF and Homebrew llama-server on CPU (`-ngl 0 -dev none --no-op-offload`). The temporary model was deleted. One run measured about 60 tok/s decode, 540 tok/s prompt reading, and 0.50 s follow-up TTFT. The spec sweep was inconclusive: the server processed 37 new tokens for the first attempted extension, not one. These single-run synthetic figures do not predict production performance or model quality.
- `gofmt -l .`, `go vet ./...`, `GOOS=windows go vet ./...`, and `go test -race ./...` passed with scratch HOME and configuration. Product source 25,707/28,500, net +326 from the pre-round baseline of 25,381, within +450 across both rounds. CLI + UI 5,285/5,500; package budgets pass.

## Remaining gaps and owner checks

- Run the fingerprint integration test on a normal host where hardware detection can read RAM. Confirm identity strings and wire `RecordReal` into setup or launch; recording is not automatic.
- The real-engine sweep correctly returned inconclusive; a successful 1–4 token sweep has only been tested with a fake. Verify engine-specific tokenization and caching on supported engines. Follow-up cache evidence is limited to counts the engine reports.
- Depth prompts are approximations and target depth leaves generation room. Validate measurement-derived bands and recipe ubatch behavior on supported engines. Run the full journey on actual models and all intended harness routes; the CPU synthetic smoke does not establish model quality, spilling, or other engines.
