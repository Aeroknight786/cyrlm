# P1b handoff — triaLM v0.4.0 CLI

Implemented on this branch. No commits, tags or pushes were made: `.git` is read-only. The ordered, incremental patches are `/tmp/claude-501/p0b/p1b-1.patch` through `p1b-6.patch`, each with a one-line `.txt`. The series is replayed against a fresh archive of HEAD and compared with the final files. Apply with `git apply --whitespace=nowarn` in numerical order; terminal-frame goldens intentionally contain padding and ANSI bytes.

## What changed

- `go.mod`, `go.sum`: Go **1.26.10**; exact pins for bubbletea **v2.1.1**, lipgloss **v2.0.6**, bubbles **v2.2.1**, huh **v2.0.3**. The skeleton's Go 1.26.0 passed the initial checks, but its standard library had reachable vulnerability advisories. Go 1.26.10 clears the final scan. No Charm import escapes `internal/ui` and its subpackages; core and lower packages remain stdlib.
- `internal/ui/tui/{root,session,components,views,theme,auth,calibration,chat,claims}.go`, `claims.json`: one root model and screen stack, inline rendering, fixed themes without a background query, semantic palette and wordmark, native-console ASCII detection, explicit NO_COLOR precedence, huh select/input submodels, bubbles table/help/key/spinner/progress/textinput/viewport. Figures format the numbers. Narrow tables collapse columns; smaller terminals use cards.
- First run shows detection, browser/paste/skip key entry, then a separate calibration offer. Browser PKCE and masked headless code/paste flows keep secrets out of views and transcripts. Credits refresh asynchronously and are cached. Calibration runs the existing command as a plain child, reads its progress/ETA events, logs output, and preserves the existing `calibration_offer` preference.
- Needs cover tool, agent/chat, model family, priority, native context, quality floor, engines, boosts, privacy, budget and ZDR. Enter accepts defaults. The default quality floor follows GPU memory. Details use the existing memory/settings/scenario evaluation, show flash/KV/native-context verdicts, and say exactly `not simulated yet (v0.6)` for spec. There is no n-gram line. Space changes boosts and re-ranks.
- Enter uses `tea.ExecProcess` to hand the terminal to a plain triaLM child and the existing harness launcher, then resumes the card. Chat keeps the same root and starts only a plain server child; closing its private stdin pipe shuts it down and saves the summary on every platform. The chat implementation is **213 lines**.
- `internal/ui/window/{window,meter,process_unix,process_windows}.go`: private generated scripts, all four opener families, shell/PowerShell quoting, inherited agent-marker cleanup, launch acknowledgement, process liveness and precise terminal fallback. The TUI displays four live meter lines. Plain/JSON callers get human meter output without cursor-control bytes. Window trials checkpoint summaries once a second; an abruptly closed window returns a labelled partial card. A checkpoint cannot replace a final card or erase a rating.
- `internal/store/{history,store}.go`: bounded 100-entry history with ratings and partial-card state, shared numbered-recipe wire type, ZDR preference. Rating 1 advances long → quality → balanced → speed and immediately re-ranks. `trialm --json` now includes trial cards, including after a window trial.
- Returning home puts the ready section first, then finding models and recent trials, with an asynchronous update notice. P1a has no setup executor or verified ready setups: the ready section truthfully says none yet. Setup and daily launch retain their v0.5 boundary.
- The claims file contains only three citations already present in PLAN/REFACTOR_RESEARCH: Strata's RTX 5070 Q2_0 4K table (V), Avalon's RX 7900 XT short-prompt table (S here because the cited row omits RAM capacity), and Splash's matched-file M5 Pro comparison (S because the cited conditions disagree/incompletely identify the hardware). Source names, URLs, hardware, recipe, figures, date and date basis are explicit. These records never enter ranking. The lane matches GPU/OS; the gallery browses the curated citations. Pasted commands show `claim parsing arrives in v0.6` and are never executed.

The small changes outside UI are `internal/cli/{entry,main,tui,window,key,requirements,bridge,agentflow}.go`, `internal/store`, and the two-line 8-bit quality-floor case in `internal/core/recommend.go`. Key and prompt rendering moved into `internal/ui` to keep CLI below its package limit. `bridge.go` gains the private parent-pipe lifetime signal used by chat. Existing non-TTY/background harness paths remain intact. `.github/workflows/ci.yml` adds module verification and a Linux vulnerability scan pinned to govulncheck v1.9.0; setup-go already follows go.mod.

## Verification

All final Go checks use Go 1.26.10 with scratch GOPATH, GOMODCACHE, GOCACHE and TRIALM_HOME.

| Check | Result |
|---|---|
| `gofmt -l .` | Empty |
| `go vet ./...` | Pass |
| `go test -race ./...` | Pass, including existing characterization goldens |
| `GOOS=windows GOARCH=amd64 go vet ./...` | Pass |
| Windows amd64 and Linux amd64 cross-builds | Pass |
| `go mod tidy`, `go mod verify` | Pass |
| `go run golang.org/x/vuln/cmd/govulncheck@v1.9.0 ./...` | No vulnerabilities found |
| TUI frames | 96 goldens: 16 screens × 60/80/120 columns × colour/NO_COLOR |
| Journey A | 4 UI keys + 1 browser click to the first fake trial request; 5 keys including install paste |
| Real macOS PTY smoke | Recipes → details → back → quit; Ctrl-C exits 130; terminal modes restored |
| Terminal protocol smoke | No OSC 11 background query, no alternate screen; NO_COLOR=yes beats FORCE_COLOR=1 |

The PTY mode comparison ignores macOS's transient PENDIN bookkeeping bit; canonical input, echo and all other saved flags match. No model weights or synthetic calibration files were generated during verification.

New tests are in `internal/ui/tui/{root,chat}_test.go`, `internal/ui/window/{window,meter}_test.go`, `internal/ui/architecture_test.go`, `internal/store/history_test.go`, `internal/cli/tui_test.go`, `cmd/trialm/p1b_test.go`, and the focused additions to `internal/core/session_test.go`. The UI float-format guard and source budget now traverse UI subpackages. Tests cover explicit model/context/budget preservation, bracketed pastes into masked input and chat, empty-state and busy footers, the real root tea message loop, fake PKCE/credits and trial servers, a child process handoff, masked/headless key paths, calibration choices/events, preferences, ratings, source grades and isolation of unparsed pastes from curated sources, console glyphs, footer bindings, shell injection/quoting, script permissions, opener failures, closed windows, checkpoints and chat lifetime shutdown.

**No file under `cmd/trialm/testdata/` changed.** Only the new TUI goldens were generated, using `go test ./internal/ui/tui -run TestViewGoldens -update`.

## Budgets

| Metric | Measured | Limit |
|---|---:|---:|
| Product Go source lines | 25,232 | 26,000 |
| CLI + all UI subpackages | 5,221 | 5,500 |
| cmd/trialm physical lines | 13 | 30 |
| Chat implementation physical lines | 213 | 250 |
| Stripped macOS arm64 binary | 11,357,058 bytes | 14,000,000 |
| Stripped Windows amd64 binary | 12,401,152 bytes | 14,000,000 |
| Stripped Linux amd64 binary | 12,001,442 bytes | 14,000,000 |

The native baseline built from HEAD with the same Go 1.26.10 compiler is 8,861,026 bytes: an increase of 2,496,032 bytes. Builds use `-trimpath -ldflags='-s -w'`; the CI binary budget also sets CGO_ENABLED=0.

The binary increase over the same-compiler HEAD baseline is justified by the four required Charm modules and the terminal/form/renderer functionality. No glamour, alternate-screen dashboard or catalog expansion was added. Cached 120-column recipe `View()` measured **0.258 ms/op** on this M5 (100 iterations); this is a render benchmark, not a claim about network or first-token latency.

## Harness runner and limits

*Coordinator rerun outside the agent sandbox (2026-11-07): all 14 paths pass, Codex included. The 12/14 below is the agent's own run.*

`GOTOOLCHAIN=go1.26.10 go run ./tools/harness-e2e -output /tmp/claude-501/p0b/p1b-harness-final` completed with **12/14 paths passing**: Claude Code, OpenCode, Aider, Qwen Code, Cline and Continue pass trial and bridge. Both Codex paths reach the fake model but the shell tool is blocked by the outer macOS sandbox: `sandbox-exec: sandbox_apply: Operation not permitted`. The unchanged runner identifies this as nested Seatbelt and asks for a rerun outside the outer sandbox. Therefore a fully green seven-CLI run cannot be claimed here. Logs and `summary.json` are in that output directory. No permission or sandbox bypass was attempted.

Real OpenRouter authorization/credit/billing and real-cloud coding sessions remain owner checks from P1a. Real GPU calibration was not run. Actual Windows Terminal/conhost/VS Code rendering, Linux GUI/tmux opening, and macOS LaunchServices/TCC were not exercised; opener argv, script contents, fallback and Windows compilation were tested. A forced window close can lose up to the checkpoint interval; its card is marked partial. Setup execution, local ready/launch, YaRN, speculative simulation and claim parsing remain in their planned later releases.

## Owner checks

1. **Journey D prompt counts:** from real Claude Code and Codex, retain the permission mode you normally use and run `trialm --json`, follow the key step, run `trialm find --json`, choose a recipe, then `trialm try 1 --window`. After quitting the tool, run `trialm --json` and inspect `data.trials`. Record agent version, permission mode, tool prompts, user replies, browser clicks, approvals and terminal trips separately. Repeat with automatic window selection by omitting `--window`. If the window is blocked, confirm exit 3 has `needs: terminal` and the exact `run` command. These counts are **not measured** by this agent.
2. **macOS TCC:** build `go build -o /tmp/trialm-p1b ./cmd/trialm`, then in your terminal set `TRIALM_HOME` to a new scratch directory. Run `/tmp/trialm-p1b find --for codex` and `/tmp/trialm-p1b try 1 --window` after configuring a key there. Count every macOS prompt. Repeat with Claude Code and with `TRIALM_AGENT=1` and no explicit `--window`. Confirm the original four-line meter, final rating card and `trialm --json` history; also close a trial window early and check the partial card. The implementation uses `open <id>.command`, with no AppleScript. **Zero TCC prompts is not claimed without this check.**
3. **Codex harness acceptance:** outside this outer sandbox, run `go run ./tools/harness-e2e -only codex -output /tmp/p1b-codex-owner`. Its HOME, XDG and harness directories are isolated by the runner. The two paths must pass before recording the full harness gate as green.
4. Check window opening and ASCII rendering in Windows Terminal, conhost and VS Code, and tmux/terminal opening on Linux. The automatic window markers are CLAUDECODE, CODEX_SANDBOX and TRIALM_AGENT; explicit `--here`/`--serve` preserve in-place/server flows. SSH and VS Code prefer running in place.
