# P2 speed-model fixes

2026-11-07. Base `dc56861ece236ff6b9e6c479f2f56b12e725e918`. All measurements and gates below use Go 1.26.10 on macOS arm64 with scratch HOME, XDG, GOPATH, module/build caches and TRIALM_HOME.

## 1. Measurements retain their intervals

`verify.Ready` now keys decode speeds by the reported prompt count and also retains individual samples. Each sample carries its mode, actual depth, processed prompt count, generated count, estimation flag, speed and measured TTFT. The reading check displays the processed prompt count and TTFT. Known cached tokens become the starting depth of the prefill interval.

`calib.RecordReal` stores each sample separately in the existing row format, including depth, prompt, generation count and the new estimation flag. Setup passes those samples through. `RealFactor` evaluates each observation against the uncalibrated predictor at its own depth or prompt interval. It selects the most recent matching interval and takes the median residual of repetitions at that exact depth and prompt length. Different intervals remain separate. Prediction uses the resulting single scale for every depth of that operation. This preserves the physical depth slowdown.

The Go replay uses Apple M5 24 GB, Qwen3.5 9B Q4_K_M, 65,536 context, two decode recordings of 8.96 tok/s at 65,536 and two 512-token prefill recordings of 368 tok/s from empty.

| Quantity | Uncalibrated Go | Before fix | After fix |
|---|---:|---:|---:|
| Decode at 65,536, tok/s | 8.299319 | 6.139450 | 8.960000 |
| Prefill over [0, 512), tok/s | 709.336641 | 368.000000 | 368.000000 |
| Prefill over [32,768, 33,280), tok/s | 422.203286 | 368.000000 | 219.036774 |

Focused tests cover a conflicting shorter-depth recording, distinct decode depths with a common normalized correction and a prefill interval [7,168, 10,241) crossing a depth bucket. Storage round-trip tests check every requested metadata field and exact model matching. Ready tests cover reported counts, estimated counts and cached prompt offsets. The model launcher reads the first decode sample and also accepts historical journals with a zero-depth map entry.

Compatibility decisions deserve attention. Historical rows retain their saved depth because their actual original count cannot be reconstructed. Historical prefill rows with no prompt length use the previous 512-token interval assumption. Calls through the older `RealMeasurement.Decode` and `Prefill` fields remain supported and their newly saved rows flag counts as estimated. Legacy map inputs are ordered by depth for deterministic interval selection. Exported downloader and Ready signatures remain intact.

## 2. Repetitions retain extrapolation uncertainty

Real corrections retain at least the operation's uncalibrated band. The shared headline band covers extrapolation across depths, so the floor also applies when a headline happens to coincide with a measurement. Spread among normalized residuals can widen that floor.

In the Go replay above, equal prefill runs previously produced a 0% prefill band. The new band is 70%, matching its uncalibrated prior. The decode headline band is 60%. A separate test repeats each operation independently and verifies that its bands remain at least the uncalibrated 60% / 70%. With decode-only evidence and `PrefillMeasureOnly` initially true, the old Go path cleared it to false. The fixed path keeps it true. Measured prefill evidence can clear it.

## 3. First reply includes its complete answer

First-reply time now sums prefill and every generated token of the first answer. Cold-first-reply and session totals include that complete turn. Session follow-ups use the exact per-token loop already used for the typical follow-up headline.

A constant-speed Go fixture uses 100 base tokens, 10 new tokens per turn, 301 output tokens, two turns, 100 tok/s decode, 50 tok/s prefill and a 3-second cold load.

| Quantity | Before, seconds | After, seconds |
|---|---:|---:|
| First reply | 2.01 | 5.01 |
| Cold first reply | 5.01 | 8.01 |
| Session | 8.71 | 11.22 |
| Typical follow-up | 3.21 | 3.21 |
| Session follow-up generation alone | 3.50 | 3.01 |

The old session loop charged 350 tokens for the 301-token answer. The fixed loop charges 301. The focused test also covers 0, 1, 49, 50, 51 and 300 tokens.

The Qwen/M5 Go replay uses the default scenario with 301 output tokens and two turns. Warm first reply changes from 18.992502 to 36.601402 seconds, cold first reply from 21.628606 to 39.237506, and session from 47.659861 to 62.250871. The typical follow-up stays 36.925009 seconds.

## 4. Speculative calibration sweep is opt-in

`trialm calibrate --spec` enables the existing `-p 1,2,3,4 -n 0` steps. `TRIALM_CALIBRATE_SPEC=1` enables the same option, and an explicit `--spec=false` overrides it. Other environment values leave the default off. `RunOptions.Spec` carries the choice into execution. Plan output, start and step events, resume ETA and the launcher's offer estimate use the selected grid.

These are outputs of the existing Go scheduling estimator using the owner's M5 basis. They are estimated durations for fresh runs.

| Plan | Before steps | Before estimate | Default steps now | Default estimate now | With `--spec` |
|---|---:|---:|---:|---:|---:|
| Full | 54 | 377 s | 27 | 347 s, 5 min 47 s | 54 steps, 377 s |
| Quick | 12 | 116 s | 6 | 109 s, 1 min 49 s | 12 steps, 116 s |

The full estimated saving is 30 seconds and quick saves 7 seconds. Halving invocations chiefly removes short steps. No scheduling coefficient was refitted.

Focused tests check both flags, environment handling, exact counts and estimates, unchanged regular argv, CPU argv, event totals and resume ETA. Fake-engine tests exercise mlock and mmap with and without spec. A saved 44-row spec calibration resumes through the new default grid with all rows retained, zero remaining ETA and no new engine calls. Saved verify fits still populate `Timing.VerifySeconds`. Existing calibration fixture tests pass.

## UI glyph fix

NO_COLOR now retains Unicode boxes, wordmark and glyphs while dropping colour. ASCII selection comes from TERM=dumb, explicit ASCII configuration and the existing native-console detector. The focused UI test checks Unicode boxes and the wordmark without escape sequences, plus dumb-terminal ASCII boxes. Existing native Windows-console detection tests pass.

## Files and scope

The main implementation files are `internal/verify/ready.go`, `internal/calib/real.go`, `internal/predict/calibration.go`, `internal/predict/predict.go`, `internal/calib/plan.go`, `internal/calib/run.go`, `internal/calibrationcli/calibrate.go` and `internal/ui/tui/theme.go`.

Small wiring changes outside those primary files are required in `internal/calib/calib.go` for the row estimation flag, `internal/core/setup.go` for sample persistence, `internal/core/models.go` for actual-depth journal reads, and `internal/ui/tui/launcher.go` for the selected plan estimate.

Tests added or updated are `internal/predict/p2fix_test.go`, `internal/calib/real_test.go`, `internal/calib/refit_test.go`, `internal/calib/runner_test.go`, `internal/calibrationcli/calibrate_test.go`, `internal/core/models_test.go`, `internal/verify/ready_test.go` and `internal/ui/tui/root_test.go`.

The required deadcode command identified unused production helpers. A repository-wide reference search confirmed the selected deletions had no production callers. Budget cleanup removes `responses.IsCustomTool`, `core.Session.Status`, `window.Generate`, `gguf.MergeSplits`, `gguf.File.buildIndex` and `gguf.ParseGGMLType`. The Responses test checks the custom marker directly in the schema. That cleanup touches `internal/api/responses/responses.go` and `internal/api/responses/responses_test.go`. Tests that only exercised the deleted GGUF helpers were removed. Window tests now call the active `generate` implementation directly. GGUF comments and its split-part warning were adjusted to remove the deleted API name. These edits touch `internal/ui/window/window.go`, `internal/ui/window/window_test.go`, and `internal/gguf/{gguf,split,summary,types,parse_test,summary_test}.go`. The unread timestamp field in `internal/ui/tui/calibration.go` was also removed.

`FactorEvidence` was checked byte-for-byte against the base and is identical. Fitted constants remain identical. Work follows the four requested review items and the requested glyph and budget cleanup.

## Verification and budgets

| Gate | Result |
|---|---|
| `gofmt -l .` | Empty |
| `go vet ./...` | Pass |
| `GOOS=windows go vet ./...` | Pass |
| `go test ./...` | Pass |
| `go test -race ./...` | Pass, final rerun after the test updates |
| `go test ./internal/budgets -v` | Pass |
| `go run golang.org/x/tools/cmd/deadcode@v0.38.0 ./cmd/trialm` | Completed before and after cleanup |
| `TRIALM_REAL_ENGINE=1 go test ./internal/verify -run '^TestRealEngine$' -v -count=1` | Pass |

| Budget | Before | After | Limit |
|---|---:|---:|---:|
| Product Go source lines | 28,545 | 28,494 | 28,500 |
| CLI and UI source lines | 5,501 | 5,500 | 5,500 |
| cmd/trialm physical lines | 13 | 13 | 30 |
| Native stripped binary, bytes | 11,659,362 | 11,692,850 | 14,000,000 |

The package ceilings also pass. The remaining deadcode findings are `gguf.Parse` and `gguf.SplitPartNames`.

The CPU engine test generates its own 1.2 GB, two-layer Q4_K_M synthetic GGUF and removes it afterwards. It runs Homebrew llama-server with `-ngl 0`, `-dev none` and `--no-op-offload`. The final run reported short decode at 117 prompt tokens and target decode at 870 tokens for a requested depth of 512. Both generated 32 tokens and had exact engine counts. Reading processed 418 new tokens after 21 cached tokens, generated 16 tokens and measured TTFT at 1.894101417 seconds, giving 220.685121 tok/s. These timings exercise measurement plumbing under CPU test conditions.

The sandbox cannot detect RAM for `TestRecordedFingerprintInvalidation`, so that existing integration test skips with its explicit RAM-detection message. Row persistence and normalization are covered independently. GPU calibration duration and GPU accuracy were not measured. Model weights were not downloaded and no admin action ran.

Go evidence logs are under `/tmp/claude-501/p0b/` with prefixes `p2fix-before`, `p2fix-after`, `p2fix-focused-final`, `p2fix-real-engine`, `p2fix-resume`, `p2fix-budgets`, `p2fix-vet`, `p2fix-race-verified` and `p2fix-deadcode`.

## Review accuracy

All four targeted findings reproduce in Go. The reported 6.14 tok/s re-anchoring failure is 6.139450051880081 in Go. The prefill flattening, repetition-derived zero band, first-answer omission and 301-to-350 rounding also reproduce. I found no incorrect claim among the four requested findings. The review's 90% prompt target is an estimate in triaLM token units. The CPU test demonstrates that engine tokenization can put the actual depth above the requested value. The review's unrelated research-evaluator percentages were outside this fix scope.

## Patch handoff

The ordered series is `/tmp/claude-501/p0b/p2fix-1.patch` and `/tmp/claude-501/p0b/p2fix-2.patch`, each with its matching one-line `.txt` summary. The first milestone contains the four fixes, tests, goldens, glyph fix and initial cleanup. The second keeps distinct real intervals out of a shared median, adds conflicting-interval coverage, removes the unused Responses helper and updates this report. The patches include new files. All required gates passed before each patch was written. Applying the ordered series with `git apply --whitespace=nowarn` to a fresh `git archive HEAD` export reproduces the complete working tree byte-for-byte, including Git executable modes and new files. Replay evidence is `/tmp/claude-501/p0b/p2fix-replay.txt`.

The changed TUI snapshots preserve renderer padding, including existing trailing spaces. The whitespace mode specified for patch replay accepts that snapshot content.

## Owner checks

1. Run `TestRecordedFingerprintInvalidation` on a host where hardware detection reads RAM, then run a Ready setup into a scratch store and inspect its saved real-row metadata.
2. Run full and quick calibration on the M5 GPU to compare actual elapsed time with the labelled 347-second and 109-second estimates. Use `--spec` to exercise the preserved optional grid.
3. Inspect NO_COLOR rendering on Windows Terminal, native conhost and the usual macOS terminal. The automated UI checks cover glyph selection and snapshots.

## Changed goldens

There are 29 CLI goldens and 85 TUI goldens. Every path is listed below. Reason T means complete first-answer and exact session-generation timing. Reason G means Unicode retained under NO_COLOR. Reason P means the selected-grid estimate replaces the fixed calibration-time text. A recursive comparison of every changed CLI JSON golden confirms that its changed scalar fields are only `cold_first_reply_s` and `session_s`.

| Golden | Reason |
|---|---|
| `cmd/trialm/testdata/golden/3090-128/predict.json` | T |
| `cmd/trialm/testdata/golden/3090-128/predict.txt` | T |
| `cmd/trialm/testdata/golden/3090-128/recommend-codex.json` | T |
| `cmd/trialm/testdata/golden/3090-128/recommend.json` | T |
| `cmd/trialm/testdata/golden/3090-128/recommend.txt` | T |
| `cmd/trialm/testdata/golden/4090-64/predict.json` | T |
| `cmd/trialm/testdata/golden/4090-64/predict.txt` | T |
| `cmd/trialm/testdata/golden/4090-64/recommend-codex.json` | T |
| `cmd/trialm/testdata/golden/4090-64/recommend.json` | T |
| `cmd/trialm/testdata/golden/4090-64/recommend.txt` | T |
| `cmd/trialm/testdata/golden/5070-64/predict.json` | T |
| `cmd/trialm/testdata/golden/5070-64/predict.txt` | T |
| `cmd/trialm/testdata/golden/5070-64/recommend-codex.json` | T |
| `cmd/trialm/testdata/golden/5070-64/recommend.json` | T |
| `cmd/trialm/testdata/golden/5070-64/recommend.txt` | T |
| `cmd/trialm/testdata/golden/7900xt-64/predict.json` | T |
| `cmd/trialm/testdata/golden/7900xt-64/predict.txt` | T |
| `cmd/trialm/testdata/golden/7900xt-64/recommend-codex.json` | T |
| `cmd/trialm/testdata/golden/7900xt-64/recommend.json` | T |
| `cmd/trialm/testdata/golden/7900xt-64/recommend.txt` | T |
| `cmd/trialm/testdata/golden/cpu-32/predict.json` | T |
| `cmd/trialm/testdata/golden/cpu-32/predict.txt` | T |
| `cmd/trialm/testdata/golden/cpu-32/recommend-codex.json` | T |
| `cmd/trialm/testdata/golden/cpu-32/recommend.json` | T |
| `cmd/trialm/testdata/golden/cpu-32/recommend.txt` | T |
| `cmd/trialm/testdata/golden/m5-24/predict.json` | T |
| `cmd/trialm/testdata/golden/m5-24/recommend-codex.json` | T |
| `cmd/trialm/testdata/golden/m5-24/recommend.json` | T |
| `cmd/trialm/testdata/golden/m5-24/recommend.txt` | T |
| `internal/ui/tui/testdata/chat-120-colorfalse.golden` | G |
| `internal/ui/tui/testdata/chat-60-colorfalse.golden` | G |
| `internal/ui/tui/testdata/chat-80-colorfalse.golden` | G |
| `internal/ui/tui/testdata/claim-120-colorfalse.golden` | G |
| `internal/ui/tui/testdata/claim-60-colorfalse.golden` | G |
| `internal/ui/tui/testdata/claim-80-colorfalse.golden` | G |
| `internal/ui/tui/testdata/details-120-colorfalse.golden` | G |
| `internal/ui/tui/testdata/details-60-colorfalse.golden` | G |
| `internal/ui/tui/testdata/details-80-colorfalse.golden` | G |
| `internal/ui/tui/testdata/detecting-120-colorfalse.golden` | G |
| `internal/ui/tui/testdata/detecting-60-colorfalse.golden` | G |
| `internal/ui/tui/testdata/detecting-80-colorfalse.golden` | G |
| `internal/ui/tui/testdata/error-120-colorfalse.golden` | G |
| `internal/ui/tui/testdata/error-60-colorfalse.golden` | G |
| `internal/ui/tui/testdata/error-80-colorfalse.golden` | G |
| `internal/ui/tui/testdata/error-details-120-colorfalse.golden` | G |
| `internal/ui/tui/testdata/error-details-60-colorfalse.golden` | G |
| `internal/ui/tui/testdata/error-details-80-colorfalse.golden` | G |
| `internal/ui/tui/testdata/gallery-120-colorfalse.golden` | G |
| `internal/ui/tui/testdata/gallery-60-colorfalse.golden` | G |
| `internal/ui/tui/testdata/gallery-80-colorfalse.golden` | G |
| `internal/ui/tui/testdata/help-120-colorfalse.golden` | G |
| `internal/ui/tui/testdata/help-60-colorfalse.golden` | G |
| `internal/ui/tui/testdata/help-80-colorfalse.golden` | G |
| `internal/ui/tui/testdata/key-120-colorfalse.golden` | G |
| `internal/ui/tui/testdata/key-60-colorfalse.golden` | G |
| `internal/ui/tui/testdata/key-80-colorfalse.golden` | G |
| `internal/ui/tui/testdata/launch-120-colorfalse.golden` | G |
| `internal/ui/tui/testdata/launch-60-colorfalse.golden` | G |
| `internal/ui/tui/testdata/launch-80-colorfalse.golden` | G |
| `internal/ui/tui/testdata/launcher-120-colorfalse.golden` | G |
| `internal/ui/tui/testdata/launcher-60-colorfalse.golden` | G |
| `internal/ui/tui/testdata/launcher-80-colorfalse.golden` | G |
| `internal/ui/tui/testdata/launcher-calibrating-120-colorfalse.golden` | G |
| `internal/ui/tui/testdata/launcher-calibrating-60-colorfalse.golden` | G |
| `internal/ui/tui/testdata/launcher-calibrating-80-colorfalse.golden` | G |
| `internal/ui/tui/testdata/launcher-offer-120-colorfalse.golden` | G, P |
| `internal/ui/tui/testdata/launcher-offer-120-colortrue.golden` | P |
| `internal/ui/tui/testdata/launcher-offer-60-colorfalse.golden` | G, P |
| `internal/ui/tui/testdata/launcher-offer-60-colortrue.golden` | P |
| `internal/ui/tui/testdata/launcher-offer-80-colorfalse.golden` | G, P |
| `internal/ui/tui/testdata/launcher-offer-80-colortrue.golden` | P |
| `internal/ui/tui/testdata/needs-120-colorfalse.golden` | G |
| `internal/ui/tui/testdata/needs-60-colorfalse.golden` | G |
| `internal/ui/tui/testdata/needs-80-colorfalse.golden` | G |
| `internal/ui/tui/testdata/paste-120-colorfalse.golden` | G |
| `internal/ui/tui/testdata/paste-60-colorfalse.golden` | G |
| `internal/ui/tui/testdata/paste-80-colorfalse.golden` | G |
| `internal/ui/tui/testdata/ready-120-colorfalse.golden` | G |
| `internal/ui/tui/testdata/ready-60-colorfalse.golden` | G |
| `internal/ui/tui/testdata/ready-80-colorfalse.golden` | G |
| `internal/ui/tui/testdata/ready-details-120-colorfalse.golden` | G |
| `internal/ui/tui/testdata/ready-details-60-colorfalse.golden` | G |
| `internal/ui/tui/testdata/ready-details-80-colorfalse.golden` | G |
| `internal/ui/tui/testdata/ready-sparkle-120-colorfalse.golden` | G |
| `internal/ui/tui/testdata/ready-sparkle-60-colorfalse.golden` | G |
| `internal/ui/tui/testdata/ready-sparkle-80-colorfalse.golden` | G |
| `internal/ui/tui/testdata/recipes-120-colorfalse.golden` | G, T |
| `internal/ui/tui/testdata/recipes-120-colortrue.golden` | T |
| `internal/ui/tui/testdata/recipes-60-colorfalse.golden` | G, T |
| `internal/ui/tui/testdata/recipes-80-colorfalse.golden` | G, T |
| `internal/ui/tui/testdata/setup-120-colorfalse.golden` | G |
| `internal/ui/tui/testdata/setup-60-colorfalse.golden` | G |
| `internal/ui/tui/testdata/setup-80-colorfalse.golden` | G |
| `internal/ui/tui/testdata/setup-details-120-colorfalse.golden` | G |
| `internal/ui/tui/testdata/setup-details-60-colorfalse.golden` | G |
| `internal/ui/tui/testdata/setup-details-80-colorfalse.golden` | G |
| `internal/ui/tui/testdata/setup-disk-120-colorfalse.golden` | G |
| `internal/ui/tui/testdata/setup-disk-60-colorfalse.golden` | G |
| `internal/ui/tui/testdata/setup-disk-80-colorfalse.golden` | G |
| `internal/ui/tui/testdata/setup-paused-120-colorfalse.golden` | G |
| `internal/ui/tui/testdata/setup-paused-60-colorfalse.golden` | G |
| `internal/ui/tui/testdata/setup-paused-80-colorfalse.golden` | G |
| `internal/ui/tui/testdata/setup-progress-120-colorfalse.golden` | G |
| `internal/ui/tui/testdata/setup-progress-60-colorfalse.golden` | G |
| `internal/ui/tui/testdata/setup-progress-80-colorfalse.golden` | G |
| `internal/ui/tui/testdata/summary-120-colorfalse.golden` | G |
| `internal/ui/tui/testdata/summary-60-colorfalse.golden` | G |
| `internal/ui/tui/testdata/summary-80-colorfalse.golden` | G |
| `internal/ui/tui/testdata/trial-120-colorfalse.golden` | G |
| `internal/ui/tui/testdata/trial-60-colorfalse.golden` | G |
| `internal/ui/tui/testdata/trial-80-colorfalse.golden` | G |
| `internal/ui/tui/testdata/why-120-colorfalse.golden` | G |
| `internal/ui/tui/testdata/why-60-colorfalse.golden` | G |
| `internal/ui/tui/testdata/why-80-colorfalse.golden` | G |
