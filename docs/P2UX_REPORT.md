# P2 UX handoff

Implemented on this clone's current branch without commits. Apply `/tmp/claude-501/p0b/p2ux-1.patch` and `p2ux-2.patch` in order with `git apply --whitespace=nowarn`. Their `.txt` files describe the milestones. The series was replayed on a fresh `git archive HEAD` export and its complete file and mode manifest matched the final working tree.

## Delivered

- Bare interactive `trialm` starts at the launcher. Detection is inline and asynchronous. The four status lines settle into the machine header and model list. OpenRouter sign-in appears when the selected cloud trial needs it.
- `core.Session.Models` orders saved Ready journals first, complete known cached models next and two remaining recommendations last. Ready speeds come from `Report.DecodeTPS[0]`. Predicted writing speeds always have `~` in launcher and Ready cards. The launcher shows one cached choice per model, with ready setups taking precedence.
- Discovery reads HF snapshots and blob links, `HF_HOME` / `HF_HUB_CACHE`, both standard LM Studio cache layouts, Ollama library and HF manifests including quant-qualified tags, and old `*-cyrlm.sh` launchers including non-default-quant names. It checks complete GGUF shard sets and MLX weight directories. It uses catalog repository and quant data and `download.Candidates`. Discovery never sources a launcher or hashes multi-GB weights. Known local files use the existing `LocalModel` planner input and are read in place.
- Enter launches the selected Ready journal. Enter on a cached or recommended model opens a single setup approval, then automatically hands the TTY to the selected tool after Ready. The launcher path shows three celebration frames over about 450 ms. Reduced-motion terminals show a static Ready card for about 150 ms. Enter can open immediately. Setup reached from the recipe/trial flow retains the explicit Ready action.
- Calibration starts focused above Your models on the first visit. Enter runs the existing job inline. `s` saves `calibration_offer=skipped` and selects the model list. Progress, ETA and cancellation use the existing child job. Preference writes retain the decision.
- Find and `f` open the recipe table directly. `n` opens needs there. `/` searches, Tab cycles installed tool IDs, `t` starts the selected cloud trial and `?` opens the full key reference.
- Rounded cards, a lipgloss v2 blended wordmark, the six semantic roles and a selected-row background implement Bold. Waiting work uses bubbles spinners. Download and calibration progress use the animated bubbles progress model. `d` expands setup steps/logs and raw errors. Ready keeps measured/predicted writing and reading, the tool's harness check and one dim slower-than-modeled line.
- NO_COLOR and TERM=dumb use ASCII identity and glyphs with static progress. Native-console ASCII detection remains. PTY checks confirmed inline operation, restored terminal modes, and absence of OSC 11 background queries and alternate-screen entry.

## Files and boundaries

- New `internal/core/models.go` owns discovery, deduplication, journal ordering and launcher figures. New `models_test.go` covers the cache layouts, broken links, partial shards, MLX, local planning, measured provenance and Ready precedence.
- New `internal/core/preferences.go` holds the existing preference-to-find orchestration, trial argv, child environment cleanup, cloud-key and claim predicates, readable calibration status and update notice. These stdlib operations moved out of UI to meet the replacement budget.
- `internal/core/recommend.go` adds `Row.LocalPath` with `json:"-"`. `internal/core/setup.go` threads an optional local path into `PlanOptions.LocalModel` and the existing planner input.
- `internal/ui/tui/{root,session,launcher,views,components,theme,setup,calibration,chat}.go` contain the replacement navigation, rendering and event handling. `root_test.go`, `setup_test.go`, `chat_test.go` and `calibration_status_test.go` cover navigation, reduced motion, error expansion, lazy authorization, persistence and event-loop key counts.
- The smallest change outside core/TUI ownership is `internal/setup/plan.go`. Its MLX branch now retains a supplied local directory, so a discovered MLX cache is used by the actual start argv. Existing callers with an empty local path keep their previous behaviour.
- `docs/P2UX_REPORT.md` and the TUI goldens complete the handoff. No CLI source, command golden, go.mod or go.sum changed. The exported downloader and Ready signatures are unchanged.

## Verification

All Go gates used cached Go 1.26.10 with GOTOOLCHAIN=local and scratch HOME, XDG, triaLM, module and build-cache directories.

| Check | Result |
|---|---|
| `gofmt -l .` | No paths |
| `go vet ./...` | Pass |
| `GOOS=windows go vet ./...` | Pass |
| `go test -race ./...` | Pass |
| Command plain/JSON characterization and legacy-find equivalence | Pass with byte-identical `cmd/trialm/testdata` |
| TUI goldens | 168 frames, 28 states × 60/80/120 columns × colour/NO_COLOR |
| First-visit cached model | 3 keys including `s`, 1 approval |
| Cached model after calibration was dismissed | 2 keys, 1 approval |
| Ready setup | 1 key |
| Real binary in Python PTY | Launcher → setup approval → Ready → Codex fixture, exit 0 |
| Bare binary, NO_COLOR and TERM=dumb in PTY | Launcher without key/needs questions, ASCII where requested, terminal restored |
| `tools/harness-e2e` | 12/14 pass. Both Codex paths are blocked by nested macOS Seatbelt |

The key-count test runs the real root event loop and real Detect/Models callbacks against a scratch LM Studio cache and a saved Ready journal. Apply is a fake single approval and the coding-tool subprocess is a test helper. The lazy-key test checks that authorization resumes the same selected model's trial.

Logs are `/tmp/p2ux-{vet,windows-vet,race,budgets}-final.log`, `/tmp/p2ux-gates-final.log`, `/tmp/p2ux-pty-final.log`, `/tmp/p2ux-native-pty.log` and `/tmp/claude-501/p0b/p2ux-harness/summary.json`.

## Budgets

The repository's token-line scanner and binary budget test measured the final source.

| Measurement | Final | Ceiling |
|---|---:|---:|
| Product Go lines | 28,495 | 28,500 |
| CLI + all UI packages | 5,500 | 5,500 |
| `internal/cli` | 2,297 | 2,500 |
| `internal/core` | 1,349 | 2,500 |
| `internal/ui/tui` | 2,150 | 2,500 |
| `internal/setup` | 2,278 | 2,500 |
| `internal/ui` | 731 | 2,500 |
| `internal/ui/window` | 322 | 2,500 |
| `cmd/trialm` physical lines | 13 | 30 |
| Stripped macOS arm64 binary bytes | 11,659,218 | 14,000,000 |

## Real-binary PTY frames

These are captures from an 80-column Python PTY, with two columns of card margin. Colour escape sequences were interpreted by a terminal emulator before copying the frames. Raw output is `/tmp/claude-501/p2ux-pty-final/transcript.bin`.

The launcher/setup/Ready sequence used `trialm home --device 'Apple M5' --ram 24 --backend cpu`, a scratch triaLM home, a tiny cache placeholder, a fake llama-server HTTP implementation and a Codex shim. The fixture journal marks the engine installation complete. It prevents a release/model download while exercising the production planner, executor, streaming Ready checks, bridge and TTY handoff. Speeds in these captures measure the fake HTTP stream timing and carry no model-performance claim. The `what-if` header labels the hardware override. Separate PTY runs exercised bare `trialm` with no arguments. Native hardware probing in this sandbox cannot read RAM, so that header honestly shows `RAM unknown`.

### Launcher

```text
╭────────────────────────────────────────────────────────────────────────────╮
│ ▲ triaLM    what-if Apple M5 10c · 24.0 GB · Codex ✓                       │
╰────────────────────────────────────────────────────────────────────────────╯

  Your models
  ❯ ◐ Qwen3.5 9B                ~8.4 tok/s   on disk · not set up
    ○ Qwen3.6 35B-A3B            ~21 tok/s   13.2 GB · not on disk
    ○ Qwen3-Coder 30B-A3B        ~24 tok/s   12.9 GB · not on disk

    ✦ Find something faster or smarter

enter run in Codex  t cloud trial  / search  ? help
```

### Setup approval

```text
╭────────────────────────────────────────────────────────────────────────────╮
│ Set up Qwen3.5 9B for Codex                                                │
│ already on disk · 16.7 MB download · 1 approval · no password              │
│ Not touched · drivers and OS startup services, existing model caches,      │
│ harness defaults                                                           │
│                                                                            │
│ 1 of 5 done                                                                │
╰────────────────────────────────────────────────────────────────────────────╯

enter go  esc back  d details
```

### Ready

```text
╭────────────────────────────────────────────────────────────────────────────╮
│ ✦ Ready · Qwen3.5 9B                                                       │
│                                                                            │
│                          measured       predicted                          │
│ Writing at start            26 tok/s      ~8.4 tok/s                       │
│ Writing at 64K              25 tok/s      ~6.0 tok/s                       │
│ Reading                   2600 tok/s      ~900 tok/s                       │
│ ✓ Codex · harness                                                          │
╰────────────────────────────────────────────────────────────────────────────╯

enter open Codex now  d details  q later
```

## Limits and owner checks

- The default harness runner passed Claude Code, OpenCode, Aider, Qwen Code, Cline and Continue through trial and bridge. Both Codex cases report `sandbox-exec: sandbox_apply: Operation not permitted`. Rerun `go run ./tools/harness-e2e -only codex -output /tmp/p2ux-codex-owner` outside the outer sandbox to close that gate.
- Real OpenRouter authorization/billing, real model quality and performance, Metal calibration, Windows console rendering and native Ollama/LM Studio/MLX setup were not exercised here. Cache/plan behaviour has local fixture coverage. Repeat the launcher path in a normal Mac terminal with the owner's existing 9B cache, then check Tab with each installed coding tool.
- Check Enter on calibration and Ctrl-C during a real run. Check setup progress on an authorized download, `d` for details, the short Ready celebration and immediate tool opening. No model weights were downloaded and no administrative step ran here.
- Earlier PTY fixture cleanup signals were denied by the outer sandbox. Its own 15-minute idle worker subsequently removed the server PID record and stopped the fixture. The final fixture exposed a test-only HTTP shutdown endpoint, and connection refusal confirmed shutdown. Both fixture server PID records were gone after their idle cleanup. Owner harness configuration and real model caches were untouched.

## Golden manifest

The following lists every changed golden and its state-specific reason. ANSI padding in table/card fixtures is intentional, so patches use `--whitespace=nowarn`. The other three existing frames remained byte-identical. Each state retains all three widths and both colour modes.

### calibrating

Removed the standalone progress screen. Calibration runs inside the launcher.

- `internal/ui/tui/testdata/calibrating-120-colorfalse.golden` — removed.
- `internal/ui/tui/testdata/calibrating-120-colortrue.golden` — removed.
- `internal/ui/tui/testdata/calibrating-60-colorfalse.golden` — removed.
- `internal/ui/tui/testdata/calibrating-60-colortrue.golden` — removed.
- `internal/ui/tui/testdata/calibrating-80-colorfalse.golden` — removed.
- `internal/ui/tui/testdata/calibrating-80-colortrue.golden` — removed.

### calibration

Removed the standalone offer. The launcher contains the remembered recommendation card.

- `internal/ui/tui/testdata/calibration-120-colorfalse.golden` — removed.
- `internal/ui/tui/testdata/calibration-120-colortrue.golden` — removed.
- `internal/ui/tui/testdata/calibration-60-colorfalse.golden` — removed.
- `internal/ui/tui/testdata/calibration-60-colortrue.golden` — removed.
- `internal/ui/tui/testdata/calibration-80-colorfalse.golden` — removed.
- `internal/ui/tui/testdata/calibration-80-colortrue.golden` — removed.

### chat

Kept chat with fixed semantic footer styling and ASCII fallback.

- `internal/ui/tui/testdata/chat-120-colorfalse.golden` — updated.
- `internal/ui/tui/testdata/chat-120-colortrue.golden` — updated.
- `internal/ui/tui/testdata/chat-60-colorfalse.golden` — updated.
- `internal/ui/tui/testdata/chat-60-colortrue.golden` — updated.
- `internal/ui/tui/testdata/chat-80-colorfalse.golden` — updated.
- `internal/ui/tui/testdata/chat-80-colortrue.golden` — updated.

### claim

Kept sourced claim cards with fixed footer styling and ASCII fallback.

- `internal/ui/tui/testdata/claim-120-colorfalse.golden` — updated.
- `internal/ui/tui/testdata/claim-120-colortrue.golden` — updated.
- `internal/ui/tui/testdata/claim-60-colorfalse.golden` — updated.
- `internal/ui/tui/testdata/claim-60-colortrue.golden` — updated.
- `internal/ui/tui/testdata/claim-80-colorfalse.golden` — updated.
- `internal/ui/tui/testdata/claim-80-colortrue.golden` — updated.

### details

Kept recipe detail controls with fixed footer styling and ASCII fallback.

- `internal/ui/tui/testdata/details-120-colorfalse.golden` — updated.
- `internal/ui/tui/testdata/details-120-colortrue.golden` — updated.
- `internal/ui/tui/testdata/details-60-colorfalse.golden` — updated.
- `internal/ui/tui/testdata/details-60-colortrue.golden` — updated.
- `internal/ui/tui/testdata/details-80-colorfalse.golden` — updated.
- `internal/ui/tui/testdata/details-80-colortrue.golden` — updated.

### detecting

Added per-line detection status while models load.

- `internal/ui/tui/testdata/detecting-120-colorfalse.golden` — added.
- `internal/ui/tui/testdata/detecting-120-colortrue.golden` — added.
- `internal/ui/tui/testdata/detecting-60-colorfalse.golden` — added.
- `internal/ui/tui/testdata/detecting-60-colortrue.golden` — added.
- `internal/ui/tui/testdata/detecting-80-colorfalse.golden` — added.
- `internal/ui/tui/testdata/detecting-80-colortrue.golden` — added.

### error

Added a friendly failure sentence with the raw failure hidden.

- `internal/ui/tui/testdata/error-120-colorfalse.golden` — added.
- `internal/ui/tui/testdata/error-120-colortrue.golden` — added.
- `internal/ui/tui/testdata/error-60-colorfalse.golden` — added.
- `internal/ui/tui/testdata/error-60-colortrue.golden` — added.
- `internal/ui/tui/testdata/error-80-colorfalse.golden` — added.
- `internal/ui/tui/testdata/error-80-colortrue.golden` — added.

### error-details

Added the explicitly expanded raw failure.

- `internal/ui/tui/testdata/error-details-120-colorfalse.golden` — added.
- `internal/ui/tui/testdata/error-details-120-colortrue.golden` — added.
- `internal/ui/tui/testdata/error-details-60-colorfalse.golden` — added.
- `internal/ui/tui/testdata/error-details-60-colortrue.golden` — added.
- `internal/ui/tui/testdata/error-details-80-colorfalse.golden` — added.
- `internal/ui/tui/testdata/error-details-80-colortrue.golden` — added.

### first

Removed the default OpenRouter gate. Cloud authentication now uses the lazy key card.

- `internal/ui/tui/testdata/first-120-colorfalse.golden` — removed.
- `internal/ui/tui/testdata/first-120-colortrue.golden` — removed.
- `internal/ui/tui/testdata/first-60-colorfalse.golden` — removed.
- `internal/ui/tui/testdata/first-60-colortrue.golden` — removed.
- `internal/ui/tui/testdata/first-80-colorfalse.golden` — removed.
- `internal/ui/tui/testdata/first-80-colortrue.golden` — removed.

### gallery

Kept the curated claims lane with fixed footer styling and ASCII fallback.

- `internal/ui/tui/testdata/gallery-120-colorfalse.golden` — updated.
- `internal/ui/tui/testdata/gallery-120-colortrue.golden` — updated.
- `internal/ui/tui/testdata/gallery-60-colorfalse.golden` — updated.
- `internal/ui/tui/testdata/gallery-60-colortrue.golden` — updated.
- `internal/ui/tui/testdata/gallery-80-colorfalse.golden` — updated.
- `internal/ui/tui/testdata/gallery-80-colortrue.golden` — updated.

### help

Added the complete key reference, including aliases, needs, claims and error details.

- `internal/ui/tui/testdata/help-120-colorfalse.golden` — added.
- `internal/ui/tui/testdata/help-120-colortrue.golden` — added.
- `internal/ui/tui/testdata/help-60-colorfalse.golden` — added.
- `internal/ui/tui/testdata/help-60-colortrue.golden` — added.
- `internal/ui/tui/testdata/help-80-colorfalse.golden` — added.
- `internal/ui/tui/testdata/help-80-colortrue.golden` — added.

### home

Removed the old history-first home in favour of the model launcher.

- `internal/ui/tui/testdata/home-120-colorfalse.golden` — removed.
- `internal/ui/tui/testdata/home-120-colortrue.golden` — removed.
- `internal/ui/tui/testdata/home-60-colorfalse.golden` — removed.
- `internal/ui/tui/testdata/home-60-colortrue.golden` — removed.
- `internal/ui/tui/testdata/home-80-colorfalse.golden` — removed.
- `internal/ui/tui/testdata/home-80-colortrue.golden` — removed.

### home-running

Removed the separate running-home variant. Ready models share the launcher list.

- `internal/ui/tui/testdata/home-running-120-colorfalse.golden` — removed.
- `internal/ui/tui/testdata/home-running-120-colortrue.golden` — removed.
- `internal/ui/tui/testdata/home-running-60-colorfalse.golden` — removed.
- `internal/ui/tui/testdata/home-running-60-colortrue.golden` — removed.
- `internal/ui/tui/testdata/home-running-80-colorfalse.golden` — removed.
- `internal/ui/tui/testdata/home-running-80-colortrue.golden` — removed.

### key

Added the OpenRouter card reached only when a cloud trial needs a key.

- `internal/ui/tui/testdata/key-120-colorfalse.golden` — added.
- `internal/ui/tui/testdata/key-120-colortrue.golden` — added.
- `internal/ui/tui/testdata/key-60-colorfalse.golden` — added.
- `internal/ui/tui/testdata/key-60-colortrue.golden` — added.
- `internal/ui/tui/testdata/key-80-colorfalse.golden` — added.
- `internal/ui/tui/testdata/key-80-colortrue.golden` — added.

### launch

Updated the semantic footer and ASCII fallback during the TTY handoff.

- `internal/ui/tui/testdata/launch-120-colorfalse.golden` — updated.
- `internal/ui/tui/testdata/launch-120-colortrue.golden` — updated.
- `internal/ui/tui/testdata/launch-60-colorfalse.golden` — updated.
- `internal/ui/tui/testdata/launch-60-colortrue.golden` — updated.
- `internal/ui/tui/testdata/launch-80-colorfalse.golden` — updated.
- `internal/ui/tui/testdata/launch-80-colortrue.golden` — updated.

### launcher

Added Ready, cached and recommended model rows, the detected-tool footer and the Find action.

- `internal/ui/tui/testdata/launcher-120-colorfalse.golden` — added.
- `internal/ui/tui/testdata/launcher-120-colortrue.golden` — added.
- `internal/ui/tui/testdata/launcher-60-colorfalse.golden` — added.
- `internal/ui/tui/testdata/launcher-60-colortrue.golden` — added.
- `internal/ui/tui/testdata/launcher-80-colorfalse.golden` — added.
- `internal/ui/tui/testdata/launcher-80-colortrue.golden` — added.

### launcher-calibrating

Added inline calibration with a settled gradient progress bar and ETA.

- `internal/ui/tui/testdata/launcher-calibrating-120-colorfalse.golden` — added.
- `internal/ui/tui/testdata/launcher-calibrating-120-colortrue.golden` — added.
- `internal/ui/tui/testdata/launcher-calibrating-60-colorfalse.golden` — added.
- `internal/ui/tui/testdata/launcher-calibrating-60-colortrue.golden` — added.
- `internal/ui/tui/testdata/launcher-calibrating-80-colorfalse.golden` — added.
- `internal/ui/tui/testdata/launcher-calibrating-80-colortrue.golden` — added.

### launcher-offer

Added first-visit calibration focus above the model list.

- `internal/ui/tui/testdata/launcher-offer-120-colorfalse.golden` — added.
- `internal/ui/tui/testdata/launcher-offer-120-colortrue.golden` — added.
- `internal/ui/tui/testdata/launcher-offer-60-colorfalse.golden` — added.
- `internal/ui/tui/testdata/launcher-offer-60-colortrue.golden` — added.
- `internal/ui/tui/testdata/launcher-offer-80-colorfalse.golden` — added.
- `internal/ui/tui/testdata/launcher-offer-80-colortrue.golden` — added.

### needs

Kept needs behind n and updated the fixed footer and ASCII fallback.

- `internal/ui/tui/testdata/needs-120-colorfalse.golden` — updated.
- `internal/ui/tui/testdata/needs-120-colortrue.golden` — updated.
- `internal/ui/tui/testdata/needs-60-colorfalse.golden` — updated.
- `internal/ui/tui/testdata/needs-60-colortrue.golden` — updated.
- `internal/ui/tui/testdata/needs-80-colorfalse.golden` — updated.
- `internal/ui/tui/testdata/needs-80-colortrue.golden` — updated.

### paste

Kept masked paste and headless code entry, with the gradient wordmark and fixed footer.

- `internal/ui/tui/testdata/paste-120-colorfalse.golden` — updated.
- `internal/ui/tui/testdata/paste-120-colortrue.golden` — updated.
- `internal/ui/tui/testdata/paste-60-colorfalse.golden` — updated.
- `internal/ui/tui/testdata/paste-60-colortrue.golden` — updated.
- `internal/ui/tui/testdata/paste-80-colorfalse.golden` — updated.
- `internal/ui/tui/testdata/paste-80-colortrue.golden` — updated.

### ready

Replaced the long report with measured/predicted rows, the harness check and one dim warning.

- `internal/ui/tui/testdata/ready-120-colorfalse.golden` — updated.
- `internal/ui/tui/testdata/ready-120-colortrue.golden` — updated.
- `internal/ui/tui/testdata/ready-60-colorfalse.golden` — updated.
- `internal/ui/tui/testdata/ready-60-colortrue.golden` — updated.
- `internal/ui/tui/testdata/ready-80-colorfalse.golden` — updated.
- `internal/ui/tui/testdata/ready-80-colortrue.golden` — updated.

### ready-details

Added the expanded Ready log tail.

- `internal/ui/tui/testdata/ready-details-120-colorfalse.golden` — added.
- `internal/ui/tui/testdata/ready-details-120-colortrue.golden` — added.
- `internal/ui/tui/testdata/ready-details-60-colorfalse.golden` — added.
- `internal/ui/tui/testdata/ready-details-60-colortrue.golden` — added.
- `internal/ui/tui/testdata/ready-details-80-colorfalse.golden` — added.
- `internal/ui/tui/testdata/ready-details-80-colortrue.golden` — added.

### ready-sparkle

Added the celebration frame. NO_COLOR keeps the static Ready identity.

- `internal/ui/tui/testdata/ready-sparkle-120-colorfalse.golden` — added.
- `internal/ui/tui/testdata/ready-sparkle-120-colortrue.golden` — added.
- `internal/ui/tui/testdata/ready-sparkle-60-colorfalse.golden` — added.
- `internal/ui/tui/testdata/ready-sparkle-60-colortrue.golden` — added.
- `internal/ui/tui/testdata/ready-sparkle-80-colorfalse.golden` — added.
- `internal/ui/tui/testdata/ready-sparkle-80-colortrue.golden` — added.

### recipes

Kept the recipe table behind Find and added n for needs, with fixed semantic footer styling.

- `internal/ui/tui/testdata/recipes-120-colorfalse.golden` — updated.
- `internal/ui/tui/testdata/recipes-120-colortrue.golden` — updated.
- `internal/ui/tui/testdata/recipes-60-colorfalse.golden` — updated.
- `internal/ui/tui/testdata/recipes-60-colortrue.golden` — updated.
- `internal/ui/tui/testdata/recipes-80-colorfalse.golden` — updated.
- `internal/ui/tui/testdata/recipes-80-colortrue.golden` — updated.

### search

Kept search with fixed semantic footer styling.

- `internal/ui/tui/testdata/search-120-colortrue.golden` — updated.
- `internal/ui/tui/testdata/search-60-colortrue.golden` — updated.
- `internal/ui/tui/testdata/search-80-colortrue.golden` — updated.

### setup

Replaced the full plan dump with the single-approval setup card.

- `internal/ui/tui/testdata/setup-120-colorfalse.golden` — updated.
- `internal/ui/tui/testdata/setup-120-colortrue.golden` — updated.
- `internal/ui/tui/testdata/setup-60-colorfalse.golden` — updated.
- `internal/ui/tui/testdata/setup-60-colortrue.golden` — updated.
- `internal/ui/tui/testdata/setup-80-colorfalse.golden` — updated.
- `internal/ui/tui/testdata/setup-80-colortrue.golden` — updated.

### setup-details

Added the expanded typed steps, argv and log tail.

- `internal/ui/tui/testdata/setup-details-120-colorfalse.golden` — added.
- `internal/ui/tui/testdata/setup-details-120-colortrue.golden` — added.
- `internal/ui/tui/testdata/setup-details-60-colorfalse.golden` — added.
- `internal/ui/tui/testdata/setup-details-60-colortrue.golden` — added.
- `internal/ui/tui/testdata/setup-details-80-colorfalse.golden` — added.
- `internal/ui/tui/testdata/setup-details-80-colortrue.golden` — added.

### setup-disk

Added the already-on-disk approval wording.

- `internal/ui/tui/testdata/setup-disk-120-colorfalse.golden` — added.
- `internal/ui/tui/testdata/setup-disk-120-colortrue.golden` — added.
- `internal/ui/tui/testdata/setup-disk-60-colorfalse.golden` — added.
- `internal/ui/tui/testdata/setup-disk-60-colortrue.golden` — added.
- `internal/ui/tui/testdata/setup-disk-80-colorfalse.golden` — added.
- `internal/ui/tui/testdata/setup-disk-80-colortrue.golden` — added.

### setup-paused

Updated the resumable compact card and retained journal summary.

- `internal/ui/tui/testdata/setup-paused-120-colorfalse.golden` — updated.
- `internal/ui/tui/testdata/setup-paused-120-colortrue.golden` — updated.
- `internal/ui/tui/testdata/setup-paused-60-colorfalse.golden` — updated.
- `internal/ui/tui/testdata/setup-paused-60-colortrue.golden` — updated.
- `internal/ui/tui/testdata/setup-paused-80-colorfalse.golden` — updated.
- `internal/ui/tui/testdata/setup-paused-80-colortrue.golden` — updated.

### setup-progress

Updated the animated step and download card, with a settled gradient at 61 percent.

- `internal/ui/tui/testdata/setup-progress-120-colorfalse.golden` — updated.
- `internal/ui/tui/testdata/setup-progress-120-colortrue.golden` — updated.
- `internal/ui/tui/testdata/setup-progress-60-colorfalse.golden` — updated.
- `internal/ui/tui/testdata/setup-progress-60-colortrue.golden` — updated.
- `internal/ui/tui/testdata/setup-progress-80-colorfalse.golden` — updated.
- `internal/ui/tui/testdata/setup-progress-80-colortrue.golden` — updated.

### summary

Kept trial ratings and setup actions with fixed footer styling and ASCII fallback.

- `internal/ui/tui/testdata/summary-120-colorfalse.golden` — updated.
- `internal/ui/tui/testdata/summary-120-colortrue.golden` — updated.
- `internal/ui/tui/testdata/summary-60-colorfalse.golden` — updated.
- `internal/ui/tui/testdata/summary-60-colortrue.golden` — updated.
- `internal/ui/tui/testdata/summary-80-colorfalse.golden` — updated.
- `internal/ui/tui/testdata/summary-80-colortrue.golden` — updated.

### trial

Kept the cloud meter with fixed semantic footer styling and ASCII fallback.

- `internal/ui/tui/testdata/trial-120-colorfalse.golden` — updated.
- `internal/ui/tui/testdata/trial-120-colortrue.golden` — updated.
- `internal/ui/tui/testdata/trial-60-colorfalse.golden` — updated.
- `internal/ui/tui/testdata/trial-60-colortrue.golden` — updated.
- `internal/ui/tui/testdata/trial-80-colorfalse.golden` — updated.
- `internal/ui/tui/testdata/trial-80-colortrue.golden` — updated.

### why

Kept provenance and uncertainty with fixed footer styling and ASCII fallback.

- `internal/ui/tui/testdata/why-120-colorfalse.golden` — updated.
- `internal/ui/tui/testdata/why-120-colortrue.golden` — updated.
- `internal/ui/tui/testdata/why-60-colorfalse.golden` — updated.
- `internal/ui/tui/testdata/why-60-colortrue.golden` — updated.
- `internal/ui/tui/testdata/why-80-colorfalse.golden` — updated.
- `internal/ui/tui/testdata/why-80-colortrue.golden` — updated.

