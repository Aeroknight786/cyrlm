# P2 verify handoff

## Changes

- `internal/verify/ready.go` adds Ready checks, a plain renderer, prediction misses, low-bit tool-call checks, and the spill threshold. `verify.Run` remains available and its streaming `measure` helper is reused.
- `internal/verify/ready_test.go` exercises a fake engine and provides an opt-in CPU llama-server test.
- `internal/calib/real.go` and `real_test.go` add fingerprint-checked recording and retrieval of real measurements. `internal/predict/calibration.go` prefers matching real evidence when assembling calibration factors. The caller must pass the Ready report's speeds to `calib.RecordReal`; recording is not automatic.
- No goldens changed. No files outside the requested ownership were changed other than this requested report.

## Verification

- `gofmt -l .`: empty. `go vet ./...`, `GOOS=windows go vet ./...`, and `go test -race ./...`: passed with scratch HOME and configuration.
- Product source: 25,643 lines, up 262 from 25,381; limit 28,500 and allowance +450. CLI + UI: 5,285/5,500; package limits passed.
- `TRIALM_REAL_ENGINE=1 go test -v ./internal/verify -run TestRealEngine`: passed using a generated Q4_K_M, two-layer synthetic GGUF and `/opt/homebrew/bin/llama-server -ngl 0 -dev none --no-op-offload`. File was deleted. One CPU run measured 67.8 tok/s decode at zero, 421.9 tok/s prompt reading, 0.647 s follow-up TTFT, and 0.0827, 0.0823, 0.0888, 0.0970 s spec requests. These are single-run synthetic figures, not production model predictions. Harness was skipped.

## Remaining work and owner checks

- This implementation is not yet acceptance-complete. The fake-engine test does not cover slow-at-depth spill behavior, missing usage and timings, fingerprint invalidation end-to-end, or the predictor hook. Add these tests before merging.
- The spec requests extend a warm text prefix; the API tokenizer and chat template can change the number of newly processed tokens. The code does not verify that the engine processed exactly 1–4 tokens, so the sweep needs token-count validation or an engine-native path before its values can be treated as spec verify costs.
- Follow-up measures TTFT but does not establish that the engine reused the cache. Depth prompts are approximations, and target depth leaves generation room. Measurement-derived speed bands and exact recipe ubatch behavior need validation on the supported engines.
- Real recording requires an existing compatible synthetic calibration file and is not wired into setup or launch. Real evidence is applied only where the remaining timing buckets have synthetic evidence. Run-to-run spread is used with two or more samples; one sample retains the prior band. Confirm identity strings and fingerprint behavior with the integration owner.
- Run the full journey on actual models and all intended harness routes; this CPU synthetic smoke cannot validate model quality, spilling, or other engines.
