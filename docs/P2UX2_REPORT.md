# P2 UX round 2 report

2026-11-07. Base `b0954db87f3afb168d6aa887502337e467787e41`. Work stays on this clone's current branch; no commits were made.

## Delivered

1. Mac launcher rows use the scenario's follow-up-turn seconds from `predict.Result`, carried through `core.ModelChoice.Reply`. The recipe table, launcher, details, and explanation use the same provenance helper. Ready recipes are predicted with their saved quant, engine, context, KV settings, and calibration; raw short-context samples no longer become Mac launcher headlines. Measurement-backed calibrated figures have no `~`. The existing speed field and non-Mac launcher behavior remain available. Tab refreshes Mac predictions for the selected tool and ignores stale results.
2. Claude Code's default opening prompt is 20,000 tokens. The comment records the bare-install ~12K observation and the owner's 2026-11-07 measurements on Claude Code 2.1.204: 20,200 via `claude -p` with 25 tools/one MCP server; 41K–47K interactively. Other tool defaults are unchanged. Mac bridges save successful requests with no assistant history to `$TRIALM_HOME/opening-prompts.json`, keyed by harness. Engine-reported full prompt tokens take precedence; `tok.Render` supplies the fallback. Only counts are stored, at most ten per tool, with an ordinary even/odd median. Writes use the existing atomic 0600 store writer, a process mutex, and a Mac advisory lock so simultaneous bridges preserve each other's samples. Failed requests, follow-ups, and unlabelled verification bridges do not contribute. Launch enables observation after its own checks. Scenario lookup uses the median on Mac and the default when absent or unreadable.
3. Mac recipes use tool display names, `FIRST REPLY`, `LATER REPLIES`, and `WRITING`, explain the selected tool's opening prompt, and keep published claims in `c`. The table retains its 89-column footprint; four model-column characters move to the longer metric headings so all three fit without truncation. The explanatory paragraph wraps at the terminal width. Context remains in details. The header also has the correct default while ranking is in progress.
4. Mac setup cards show install/start/connect/check in plain words. Header and counts formatting are preserved. `Not touched` names the selected tool's settings, the shell, and other models. `d` retains the complete executable steps, argv, and technical verification logs. Only display helpers were added under setup; planner/executor behavior and plan serialization are unchanged.
5. Opening messages, tool choices, Mac recipe headings, and setup text use display names, including Claude Code, Codex, and Qwen Code. Harness IDs remain in executable details where they are command arguments.

The launcher/table/card changes and measurement use are scoped to Mac. The requested Claude Code default changes scenario-derived numbers on every fixture. Plain and JSON renderers were not edited. Both shared exported download/Ready contracts remain unchanged. No product file outside `internal/ui/tui`, `internal/core`, `internal/harness`, `internal/server`, and setup display text changed; the requested test goldens and this report are the only other paths.

## Files

- `internal/core/models.go`, `models_test.go`: reply figures, saved Ready recipe prediction, common figure provenance, correct opening caption during ranking, and scenario/calibration regression tests.
- `internal/core/launch.go`: enable opening observation after verification.
- `internal/harness/harness.go`, `opening.go`, `opening_lock_darwin.go`, `opening_lock_other.go`, `opening_test.go`, `wiring_test.go`: documented 20K default, bounded count storage/median, default/median/per-tool/non-Mac/corrupt-file/concurrent-request/concurrent-process coverage, and updated default assertion.
- `internal/server/server.go`, `bridge.go`, `opening_test.go`: harness attribution, successful first-request accounting, deferred observation, and HTTP streaming/buffered tests.
- `internal/setup/display.go`: setup overview, unchanged counts formatting, untouched-settings wording, and expanded descriptions.
- `internal/ui/tui/components.go`, `launcher.go`, `root.go`, `views.go`, `setup.go`: the five display fixes, tool refresh, and shared selection rendering within the UI budget.
- `internal/ui/tui/setup_test.go`, `ux2_test.go`: coherent shared Ready/table fixture, all display names, reply equality/provenance, no launcher tok/s, prompt captions, full headings, claims removal, argv only in details, and tool switching/stale-result regression coverage.

## Verification and budgets

All gates pass with Go **1.26.10**, `GOTOOLCHAIN=local`, scratch HOME/USERPROFILE, XDG/config/cache, Claude/Codex config directories, model cache roots, and TRIALM_HOME. The environment wrapper is `/tmp/claude-501/p0b/p2ux2-env`.

- `gofmt -l .`: empty.
- `go vet ./...`: pass.
- `GOOS=windows go vet ./...`: pass.
- `go test -race ./...`: pass, including CLI and TUI golden comparisons.
- `go test ./internal/budgets -run 'TestSourceBudgets|TestStrippedBinaryBudget' -v`: pass.
- Prompt tests cover default and measured-median predictions at a fixed context: the measured opening increases FIRST while the cached later reply remains the same. Bridge tests cover reported counts, fallback counts, failed requests, assistant-history exclusion, and disabled observation during verification, for both buffered and streaming replies. Four concurrent bridge helper processes also preserve all ten samples for each tool.
- Existing CLI fixtures for Codex, detect, and plan remain byte-identical. All changed CLI files are listed below; changes flow from the requested Claude Code scenario update, with no renderer/schema change.

| Budget | Base | Final | Ceiling |
|---|---:|---:|---:|
| Product token-bearing Go lines | 28,571 | 28,758 | 29,000 |
| CLI + UI | 5,496 | 5,500 | 5,500 |
| `cmd/trialm` physical lines | 13 | 13 | 30 |
| Stripped binary bytes | 11,692,914 | 11,725,986 | 14,000,000 |

The binary grew 33,072 bytes. `internal/ui/tui` is 2,157 lines, core 1,396, harness 640, server 930, setup 2,338, and CLI 2,299; every package gate passes. The specific brief and the repository's enforced v0.5 gate use 29,000 total lines; the generic ground-rule 28,500 value is already below the 28,571-line base. UI has no remaining line allowance.

Logs: `/tmp/claude-501/p0b/p2ux2-{gofmt,vet,windows-vet,race,budgets}.log`; exact package counts: `p2ux2-count.txt` and `p2ux2-count-base.txt` in that directory.

## Real-binary PTY frames

These frames came from the final stripped binary in a **100-column, 40-row PTY**, interpreted with a terminal emulator. Command:

```sh
trialm home --device 'Apple M5' --ram 24 --for claude-code --ctx 49152
```

The run used `/tmp/claude-501/p2ux2-pty/{home,trialm}` and a tiny explicitly marked placeholder in the scratch HF snapshot solely to exercise discovery and planning. It was never loaded. The `what-if` label is intentional: sandbox-native RAM detection cannot supply the owner's hardware state. These are real production UI/planner frames with simulated M5 predictions, not new model-performance measurements. The run visited launcher → `f` recipes → Esc → Enter setup → `d` details → `q`. It stopped before approval, fetched no weights or engine asset, executed no engine/harness/admin step, exited 0, restored terminal flags, and used no alternate screen or OSC 11 query.

Raw transcript, driver, and text frames: `/tmp/claude-501/p2ux2-pty/`. Driver log: `/tmp/claude-501/p0b/p2ux2-pty.log`.

### Launcher

```text
╭────────────────────────────────────────────────────────────────────────────╮
│ ▲ triaLM    what-if Apple M5 10c · 24.0 GB · Claude Code ✓                 │
╰────────────────────────────────────────────────────────────────────────────╯

  Your models
  ❯ ◐ Qwen3.5 9B                ~37 s per reply   on disk · not set up
    ○ Qwen3.6 35B-A3B           ~22 s per reply   13.2 GB · not on disk
    ○ Gemma 4 26B-A4B           ~30 s per reply   13.6 GB · not on disk

    ✦ Find something faster or smarter

enter run in Claude Code  t cloud trial  / search  ? help
```

### Recipes

```text
Recipes for Claude Code on this Mac · balanced
First reply reads Claude Code's opening prompt (~20K tokens); later replies reuse it. ~ = estimate,
? = how sure.
MODEL                     FILE · ENGINE              FIRST REPLY LATER REPLIES WRITING
❯ 1 Qwen3.6 35B-A3B       IQ3_XXS 13.2 GB · llamacpp        ~32 s         ~22 s   ~18 t/s
  2 Gemma 4 26B-A4B       IQ4_XS 13.6 GB · llamacpp         ~46 s         ~30 s   ~13 t/s
  3 gpt-oss 20B           MLX-MXFP4 12.1 GB · lmstud…       ~37 s         ~25 s   ~16 t/s
  4 Gemma 4 12B           Q4_K_M 7.12 GB · llamacpp         ~91 s         ~46 s  ~8.9 t/s
  5 Qwen3.5 9B            Q4_K_M 5.68 GB · llamacpp         ~57 s         ~37 s   ~10 t/s
3-bit files: quality caveat
c published claims for similar hardware
Trial: real cloud model · no-logging providers · cap 2.00 USD
calibrate for tighter numbers: trialm calibrate

enter try  s set up  d details  w window  n needs  / search
```

### Setup

```text
╭────────────────────────────────────────────────────────────────────────────╮
│ Set up Qwen3.5 9B for Claude Code                                          │
│ already on disk · 16.7 MB download · 1 approval · no password              │
│ Not touched · your Claude Code settings, your shell, other models          │
│                                                                            │
│ Install llama.cpp b12182 (16.7 MB)                                         │
│ Start the engine                                                           │
│ Connect Claude Code                                                        │
│ Check speed and tools                                                      │
╰────────────────────────────────────────────────────────────────────────────╯

enter go  esc back  d details
```

Qwen3.5 9B is `~37 s per reply` in the launcher and `~37 s` in LATER REPLIES. Qwen3.6 is `~22 s` in both. The uncached FIRST values are now `~57 s` and `~32 s`, respectively. The separate `details.txt` capture confirms that argv and paths are still accessible with `d`.

## Changed CLI goldens

Each path below changes because the Claude Code default opening grows from 12K to 20K. This raises uncached first-reply and session times, changes prompt-reading figures/warnings, and can change automatic recommended context (40,960 → 57,344), deep writing/follow-up timings, memory figures, or the resulting candidate selection. Those are consequences of the scenario input; the existing renderer and JSON field names are unchanged. Codex-specific goldens and plan/detect fixtures do not change.

- `cmd/trialm/testdata/golden/3090-128/predict.json` — FIRST/prefill/session and derived context/memory/scenario fields.
- `cmd/trialm/testdata/golden/3090-128/predict.txt` — FIRST REPLY/session and scenario-derived context, timing, or fit text.
- `cmd/trialm/testdata/golden/3090-128/recommend.json` — FIRST/prefill/session and derived context/memory/scenario fields.
- `cmd/trialm/testdata/golden/3090-128/recommend.txt` — FIRST REPLY/session and scenario-derived context, timing, or fit text.
- `cmd/trialm/testdata/golden/4090-64/predict.json` — FIRST/prefill/session and derived context/memory/scenario fields.
- `cmd/trialm/testdata/golden/4090-64/predict.txt` — FIRST REPLY/session and scenario-derived context, timing, or fit text.
- `cmd/trialm/testdata/golden/4090-64/recommend.json` — FIRST/prefill/session and derived context/memory/scenario fields.
- `cmd/trialm/testdata/golden/4090-64/recommend.txt` — FIRST REPLY/session and scenario-derived context, timing, or fit text.
- `cmd/trialm/testdata/golden/5070-64/predict.json` — FIRST/prefill/session and derived context/memory/scenario fields.
- `cmd/trialm/testdata/golden/5070-64/predict.txt` — FIRST REPLY/session and scenario-derived context, timing, or fit text.
- `cmd/trialm/testdata/golden/5070-64/recommend.json` — FIRST/prefill/session and derived context/memory/scenario fields.
- `cmd/trialm/testdata/golden/5070-64/recommend.txt` — FIRST REPLY/session and scenario-derived context, timing, or fit text.
- `cmd/trialm/testdata/golden/7900xt-64/predict.json` — FIRST/prefill/session and derived context/memory/scenario fields.
- `cmd/trialm/testdata/golden/7900xt-64/predict.txt` — FIRST REPLY/session and scenario-derived context, timing, or fit text.
- `cmd/trialm/testdata/golden/7900xt-64/recommend.json` — FIRST/prefill/session and derived context/memory/scenario fields.
- `cmd/trialm/testdata/golden/7900xt-64/recommend.txt` — FIRST REPLY/session and scenario-derived context, timing, or fit text.
- `cmd/trialm/testdata/golden/cpu-32/predict.json` — FIRST/prefill/session and derived context/memory/scenario fields.
- `cmd/trialm/testdata/golden/cpu-32/predict.txt` — FIRST REPLY/session and scenario-derived context, timing, or fit text.
- `cmd/trialm/testdata/golden/cpu-32/recommend.json` — FIRST/prefill/session and derived context/memory/scenario fields.
- `cmd/trialm/testdata/golden/cpu-32/recommend.txt` — FIRST REPLY/session and scenario-derived context, timing, or fit text.
- `cmd/trialm/testdata/golden/m5-24/predict.json` — FIRST/prefill/session and derived context/memory/scenario fields.
- `cmd/trialm/testdata/golden/m5-24/predict.txt` — FIRST REPLY/session and scenario-derived context, timing, or fit text.
- `cmd/trialm/testdata/golden/m5-24/recommend.json` — FIRST/prefill/session and derived context/memory/scenario fields.
- `cmd/trialm/testdata/golden/m5-24/recommend.txt` — FIRST REPLY/session and scenario-derived context, timing, or fit text.

## Changed TUI goldens

Every changed file is listed below. Coverage retains 60/80/120 columns and both color modes; no golden was removed.

- `internal/ui/tui/testdata/details-120-colorfalse.golden` — The shared Ready fixture is calibrated; reply time keeps its calibration provenance and has no estimate marker.
- `internal/ui/tui/testdata/details-120-colortrue.golden` — The shared Ready fixture is calibrated; reply time keeps its calibration provenance and has no estimate marker.
- `internal/ui/tui/testdata/details-60-colorfalse.golden` — The shared Ready fixture is calibrated; reply time keeps its calibration provenance and has no estimate marker.
- `internal/ui/tui/testdata/details-60-colortrue.golden` — The shared Ready fixture is calibrated; reply time keeps its calibration provenance and has no estimate marker.
- `internal/ui/tui/testdata/details-80-colorfalse.golden` — The shared Ready fixture is calibrated; reply time keeps its calibration provenance and has no estimate marker.
- `internal/ui/tui/testdata/details-80-colortrue.golden` — The shared Ready fixture is calibrated; reply time keeps its calibration provenance and has no estimate marker.
- `internal/ui/tui/testdata/error-120-colorfalse.golden` — Launcher rows now show scenario reply times, with the Ready row sharing the table result.
- `internal/ui/tui/testdata/error-120-colortrue.golden` — Launcher rows now show scenario reply times, with the Ready row sharing the table result.
- `internal/ui/tui/testdata/error-60-colorfalse.golden` — Launcher rows now show scenario reply times, with the Ready row sharing the table result.
- `internal/ui/tui/testdata/error-60-colortrue.golden` — Launcher rows now show scenario reply times, with the Ready row sharing the table result.
- `internal/ui/tui/testdata/error-80-colorfalse.golden` — Launcher rows now show scenario reply times, with the Ready row sharing the table result.
- `internal/ui/tui/testdata/error-80-colortrue.golden` — Launcher rows now show scenario reply times, with the Ready row sharing the table result.
- `internal/ui/tui/testdata/error-details-120-colorfalse.golden` — Launcher rows now show scenario reply times, with the Ready row sharing the table result.
- `internal/ui/tui/testdata/error-details-120-colortrue.golden` — Launcher rows now show scenario reply times, with the Ready row sharing the table result.
- `internal/ui/tui/testdata/error-details-60-colorfalse.golden` — Launcher rows now show scenario reply times, with the Ready row sharing the table result.
- `internal/ui/tui/testdata/error-details-60-colortrue.golden` — Launcher rows now show scenario reply times, with the Ready row sharing the table result.
- `internal/ui/tui/testdata/error-details-80-colorfalse.golden` — Launcher rows now show scenario reply times, with the Ready row sharing the table result.
- `internal/ui/tui/testdata/error-details-80-colortrue.golden` — Launcher rows now show scenario reply times, with the Ready row sharing the table result.
- `internal/ui/tui/testdata/launch-120-colorfalse.golden` — The opening message uses the tool display name (Codex).
- `internal/ui/tui/testdata/launch-120-colortrue.golden` — The opening message uses the tool display name (Codex).
- `internal/ui/tui/testdata/launch-60-colorfalse.golden` — The opening message uses the tool display name (Codex).
- `internal/ui/tui/testdata/launch-60-colortrue.golden` — The opening message uses the tool display name (Codex).
- `internal/ui/tui/testdata/launch-80-colorfalse.golden` — The opening message uses the tool display name (Codex).
- `internal/ui/tui/testdata/launch-80-colortrue.golden` — The opening message uses the tool display name (Codex).
- `internal/ui/tui/testdata/launcher-120-colorfalse.golden` — Scenario seconds per reply replace short-context tok/s; calibrated Ready values omit ~.
- `internal/ui/tui/testdata/launcher-120-colortrue.golden` — Scenario seconds per reply replace short-context tok/s; calibrated Ready values omit ~.
- `internal/ui/tui/testdata/launcher-60-colorfalse.golden` — Scenario seconds per reply replace short-context tok/s; calibrated Ready values omit ~.
- `internal/ui/tui/testdata/launcher-60-colortrue.golden` — Scenario seconds per reply replace short-context tok/s; calibrated Ready values omit ~.
- `internal/ui/tui/testdata/launcher-80-colorfalse.golden` — Scenario seconds per reply replace short-context tok/s; calibrated Ready values omit ~.
- `internal/ui/tui/testdata/launcher-80-colortrue.golden` — Scenario seconds per reply replace short-context tok/s; calibrated Ready values omit ~.
- `internal/ui/tui/testdata/launcher-calibrating-120-colorfalse.golden` — The model rows under calibration use scenario seconds per reply.
- `internal/ui/tui/testdata/launcher-calibrating-120-colortrue.golden` — The model rows under calibration use scenario seconds per reply.
- `internal/ui/tui/testdata/launcher-calibrating-60-colorfalse.golden` — The model rows under calibration use scenario seconds per reply.
- `internal/ui/tui/testdata/launcher-calibrating-60-colortrue.golden` — The model rows under calibration use scenario seconds per reply.
- `internal/ui/tui/testdata/launcher-calibrating-80-colorfalse.golden` — The model rows under calibration use scenario seconds per reply.
- `internal/ui/tui/testdata/launcher-calibrating-80-colortrue.golden` — The model rows under calibration use scenario seconds per reply.
- `internal/ui/tui/testdata/launcher-offer-120-colorfalse.golden` — The model rows under the calibration offer use scenario seconds per reply.
- `internal/ui/tui/testdata/launcher-offer-120-colortrue.golden` — The model rows under the calibration offer use scenario seconds per reply.
- `internal/ui/tui/testdata/launcher-offer-60-colorfalse.golden` — The model rows under the calibration offer use scenario seconds per reply.
- `internal/ui/tui/testdata/launcher-offer-60-colortrue.golden` — The model rows under the calibration offer use scenario seconds per reply.
- `internal/ui/tui/testdata/launcher-offer-80-colorfalse.golden` — The model rows under the calibration offer use scenario seconds per reply.
- `internal/ui/tui/testdata/launcher-offer-80-colortrue.golden` — The model rows under the calibration offer use scenario seconds per reply.
- `internal/ui/tui/testdata/needs-120-colorfalse.golden` — Tool choices use display names; selection margins use the shared cursor renderer.
- `internal/ui/tui/testdata/needs-120-colortrue.golden` — Tool choices use display names; selection margins use the shared cursor renderer.
- `internal/ui/tui/testdata/needs-60-colorfalse.golden` — Tool choices use display names; selection margins use the shared cursor renderer.
- `internal/ui/tui/testdata/needs-60-colortrue.golden` — Tool choices use display names; selection margins use the shared cursor renderer.
- `internal/ui/tui/testdata/needs-80-colorfalse.golden` — Tool choices use display names; selection margins use the shared cursor renderer.
- `internal/ui/tui/testdata/needs-80-colortrue.golden` — Tool choices use display names; selection margins use the shared cursor renderer.
- `internal/ui/tui/testdata/recipes-120-colorfalse.golden` — Tool name and Mac heading, opening-prompt explanation, renamed/reordered columns, calibrated Ready result, and one claims-gallery hint.
- `internal/ui/tui/testdata/recipes-120-colortrue.golden` — Tool name and Mac heading, opening-prompt explanation, renamed/reordered columns, calibrated Ready result, and one claims-gallery hint.
- `internal/ui/tui/testdata/recipes-60-colorfalse.golden` — Tool name and Mac heading, opening-prompt explanation, renamed/reordered columns, calibrated Ready result, and one claims-gallery hint.
- `internal/ui/tui/testdata/recipes-60-colortrue.golden` — Tool name and Mac heading, opening-prompt explanation, renamed/reordered columns, calibrated Ready result, and one claims-gallery hint.
- `internal/ui/tui/testdata/recipes-80-colorfalse.golden` — Tool name and Mac heading, opening-prompt explanation, renamed/reordered columns, calibrated Ready result, and one claims-gallery hint.
- `internal/ui/tui/testdata/recipes-80-colortrue.golden` — Tool name and Mac heading, opening-prompt explanation, renamed/reordered columns, calibrated Ready result, and one claims-gallery hint.
- `internal/ui/tui/testdata/setup-120-colorfalse.golden` — Four short setup steps, pinned asset size, and plain-language untouched settings.
- `internal/ui/tui/testdata/setup-120-colortrue.golden` — Four short setup steps, pinned asset size, and plain-language untouched settings.
- `internal/ui/tui/testdata/setup-60-colorfalse.golden` — Four short setup steps, pinned asset size, and plain-language untouched settings.
- `internal/ui/tui/testdata/setup-60-colortrue.golden` — Four short setup steps, pinned asset size, and plain-language untouched settings.
- `internal/ui/tui/testdata/setup-80-colorfalse.golden` — Four short setup steps, pinned asset size, and plain-language untouched settings.
- `internal/ui/tui/testdata/setup-80-colortrue.golden` — Four short setup steps, pinned asset size, and plain-language untouched settings.
- `internal/ui/tui/testdata/setup-details-120-colorfalse.golden` — Keep the full step list while using the Mac untouched-settings wording and explicit install title.
- `internal/ui/tui/testdata/setup-details-120-colortrue.golden` — Keep the full step list while using the Mac untouched-settings wording and explicit install title.
- `internal/ui/tui/testdata/setup-details-60-colorfalse.golden` — Keep the full step list while using the Mac untouched-settings wording and explicit install title.
- `internal/ui/tui/testdata/setup-details-60-colortrue.golden` — Keep the full step list while using the Mac untouched-settings wording and explicit install title.
- `internal/ui/tui/testdata/setup-details-80-colorfalse.golden` — Keep the full step list while using the Mac untouched-settings wording and explicit install title.
- `internal/ui/tui/testdata/setup-details-80-colortrue.golden` — Keep the full step list while using the Mac untouched-settings wording and explicit install title.
- `internal/ui/tui/testdata/setup-disk-120-colorfalse.golden` — Already-on-disk card with four short steps and plain-language untouched settings.
- `internal/ui/tui/testdata/setup-disk-120-colortrue.golden` — Already-on-disk card with four short steps and plain-language untouched settings.
- `internal/ui/tui/testdata/setup-disk-60-colorfalse.golden` — Already-on-disk card with four short steps and plain-language untouched settings.
- `internal/ui/tui/testdata/setup-disk-60-colortrue.golden` — Already-on-disk card with four short steps and plain-language untouched settings.
- `internal/ui/tui/testdata/setup-disk-80-colorfalse.golden` — Already-on-disk card with four short steps and plain-language untouched settings.
- `internal/ui/tui/testdata/setup-disk-80-colortrue.golden` — Already-on-disk card with four short steps and plain-language untouched settings.
- `internal/ui/tui/testdata/setup-paused-120-colorfalse.golden` — Retain pause/resume status beneath the short Mac setup overview.
- `internal/ui/tui/testdata/setup-paused-120-colortrue.golden` — Retain pause/resume status beneath the short Mac setup overview.
- `internal/ui/tui/testdata/setup-paused-60-colorfalse.golden` — Retain pause/resume status beneath the short Mac setup overview.
- `internal/ui/tui/testdata/setup-paused-60-colortrue.golden` — Retain pause/resume status beneath the short Mac setup overview.
- `internal/ui/tui/testdata/setup-paused-80-colorfalse.golden` — Retain pause/resume status beneath the short Mac setup overview.
- `internal/ui/tui/testdata/setup-paused-80-colortrue.golden` — Retain pause/resume status beneath the short Mac setup overview.
- `internal/ui/tui/testdata/setup-progress-120-colorfalse.golden` — Retain download progress beneath the short Mac setup overview.
- `internal/ui/tui/testdata/setup-progress-120-colortrue.golden` — Retain download progress beneath the short Mac setup overview.
- `internal/ui/tui/testdata/setup-progress-60-colorfalse.golden` — Retain download progress beneath the short Mac setup overview.
- `internal/ui/tui/testdata/setup-progress-60-colortrue.golden` — Retain download progress beneath the short Mac setup overview.
- `internal/ui/tui/testdata/setup-progress-80-colorfalse.golden` — Retain download progress beneath the short Mac setup overview.
- `internal/ui/tui/testdata/setup-progress-80-colortrue.golden` — Retain download progress beneath the short Mac setup overview.
- `internal/ui/tui/testdata/why-120-colorfalse.golden` — The shared Ready fixture is calibrated; the displayed writing figure keeps its calibration provenance.
- `internal/ui/tui/testdata/why-120-colortrue.golden` — The shared Ready fixture is calibrated; the displayed writing figure keeps its calibration provenance.
- `internal/ui/tui/testdata/why-60-colorfalse.golden` — The shared Ready fixture is calibrated; the displayed writing figure keeps its calibration provenance.
- `internal/ui/tui/testdata/why-60-colortrue.golden` — The shared Ready fixture is calibrated; the displayed writing figure keeps its calibration provenance.
- `internal/ui/tui/testdata/why-80-colorfalse.golden` — The shared Ready fixture is calibrated; the displayed writing figure keeps its calibration provenance.
- `internal/ui/tui/testdata/why-80-colortrue.golden` — The shared Ready fixture is calibrated; the displayed writing figure keeps its calibration provenance.

## Limits and owner checks

- This run verifies request accounting with HTTP fixtures, calibration propagation with recorded-sample prediction fixtures, and the real binary's UI/planner in a PTY. It does not remeasure real Qwen latency, run Claude Code against a real model, or exercise Metal; no model weights were downloaded. A Windows vet pass is not a Windows runtime test.
- The median reflects the last ten successful first requests attributed to that harness, including independent first requests/subagents; it does not distinguish headless versus interactive conversations. A long initial user task contributes to that request's prompt count, as requested. Without engine usage, the fallback remains a token estimate. Count snapshots are atomic; a Mac file lock serializes writes from separate bridge processes.
- Owner: launch an existing Ready Qwen setup in Claude Code with the normal 25-tool/MCP configuration. Start several fresh conversations, then check `opening-prompts.json` and the recipe opening caption. Compare FIRST/LATER with the actual opening/follow-up experience. Check that Ready launcher/table values agree and have no `~` when the calibration fingerprint matches. Tab between tools to check their different reply estimates; open `d` to inspect commands. A stale or unavailable calibration correctly leaves an estimate marker.

## Patch handoff

One completed milestone: `/tmp/claude-501/p0b/p2ux2-1.patch`, with a one-line `p2ux2-1.txt`. It includes all new files and this report. Apply with `git apply --whitespace=nowarn`; intentional golden padding is preserved. All required gates passed before writing the patch. The series was replayed on a fresh `git archive HEAD` export at `/tmp/claude-501/p0b/p2ux2-replay`; every file byte and executable mode matched the final working tree. Replay verification is in `/tmp/claude-501/p0b/p2ux2-replay.log`.
