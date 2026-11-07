# P2 launch handoff

Implemented on the current branch without commits. Apply `/tmp/claude-501/p0b/p2launch-1.patch` and `p2launch-2.patch` in order with `git apply --whitespace=nowarn`. Their `.txt` files describe the milestones. The series was replayed on a fresh `git archive HEAD` export and its complete file manifest matched the final tree.

## Implemented

- `trialm launch [tool]` selects the newest Ready journal. `--setup <slug>` selects a particular journal. A missing Ready setup exits 3 with `needs=setup` and `trialm setup`. Launch restarts the saved engine through `setup/proc`, waits for health, preserves effective port substitutions and reuses a healthy process.
- The wired-limit path reads the current value before re-applying the approved reset row. Its plan counts one OS password prompt after a restart. A non-TTY OS password prompt returns a typed needs-user error and exit 3. TUI administrative rows use Bubble Tea's process hand-off to release and restore the terminal. Tests inspect that message and argv template without executing an administrative command.
- Bridge command handling moved into `internal/server`. Launch creates its required bridge in-process and closes it after the tool exits. Setup's existing typed bridge row also runs in-process during verification. Bounded bridge shutdown closes an unfinished editor connection after its grace period.
- CLI hand-off reuses the existing environment, argument and owned-file wiring for every harness. Both Claude parent markers are removed. Launch-generated harness files are namespaced by setup under the triaLM harness directory. `--print` prepares any triaLM-owned config required by the printed environment and command, and starts no process. `--serve cline|continue|zed` prints and writes the IDE snippet and serves a foreground bridge with a four-line meter. `--stop` handles detached processes and native stop inverses. `--check` repeats `verify.Ready` and prints `Report.Plain()`.
- A detached worker starts a 15-minute idle timer after hand-off or serving ends. Journal-locked leases and generation markers protect a later or concurrent launch. PID-bearing leases allow a later timer to discard a crashed caller's lease. Explicit stop cancels the recorded idle worker. Native-adapter inverses also run at idle expiry.
- IDE snippets have tests for parseability, base URL, model and dummy key. Continue's `.yaml` example uses the JSON subset of YAML 1.2 and was loaded by the real `cn` CLI. Zed uses the provider ID `trialm` and `TRIALM_API_KEY=trialm`, printed alongside its settings JSON. Zed documents environment/keychain configuration for compatible providers. [Zed provider documentation](https://zed.dev/docs/ai/use-api-access#openai-compatible-endpoints)
- Setup, download progress, pause/resume, Ready, launch hand-off and returning-user home render in the existing root TUI. The setup view shows sizes, untouched files, counted prompts, approval, step states, gradient progress, transfer rate, ETA and a five-line log viewport. Ready compares predictions with measurements and signed misses, lists checks and offers launch and undo. Home lists Ready setups before finding another model. The calibration offer displays current defaults, and `s` accepts them while skipping calibration. Recipes offers `r` to edit needs.
- Core setup events drive the screens. Successful setup records real calibration when fingerprinting succeeds, emits the calibrated event and re-ranks. A blocked or failed fingerprint leaves a concrete log message. Ready displays the calibration label only after the save succeeds.
- Successful setup ends with a `result` event containing top-level `ready=true` and a launch command in `next`. The existing `data.ready` field remains available. JSON home lists compact Ready summaries. Non-TTY launch follows the existing window machinery and reports the started engine plus hand-off command. A blocked opener returns the terminal fallback with exit 3.
- `tools/harness-e2e -local` generates a two-layer Q4_K_M GGUF, registers its reviewed local recipe/journal and saved listing, runs the setup resume command, stops the engine and launches the real CLI. A fixture bridge supplies scripted language and tool calls. Every scripted POST also requires a healthy engine and a real CPU completion with timings. The fixture route advertises a 64K harness context while those proof completions use a small prompt on a 2K physical engine. Its scripted speed values establish protocol behavior. The direct CPU test measures the physical engine separately.
- The local runner checks approval counts, restart, health, real inference, greeting, tool edit and follow-up. Continue additionally checks `--print`, `--check`, all three foreground snippets and loading the Continue IDE example. Timeout cleanup uses a fresh context. The workflow has a second Ubuntu matrix job for all seven local CLI paths.

## Files and integration boundaries

Owned implementation files include `internal/cli/{launch,bridge,entry,main}.go`, `internal/core/launch.go`, `internal/server/{bridge,local,meter}.go`, `internal/harness/{harness,wiring,snippet}.go`, `internal/ui/{json.go,tui/{root,session,setup,views}.go,window/window.go}`, `tools/harness-e2e/{main,local}.go`, `tools/harness-e2e/fake_upstream.py` and `.github/workflows/harness-e2e.yml`. Focused tests accompany launch, bridge shutdown, snippets, TUI events, window generation and the real CPU smoke.

The following small changes cross the ownership boundary.

- `internal/cli/harness_test.go` calls the moved bridge entry point and owns its test-only endpoint regex.
- `internal/cli/plan.go` emits the completed setup contract, accepts bare `--yes` when resuming and opens the direct interactive setup screen.
- `internal/core/setup.go` closes the successful setup contract, records real measurements and adapts a ranked recipe into a plan. `internal/core/session.go` carries the administrative callback so the TUI can own its TTY hand-off. This keeps recipe planning and execution in the shared application API within the UI budget.
- `internal/setup/executor.go` runs the existing bridge step in-process. New `internal/setup/{launch,idle}.go` reuse the executor's journal lock, path expansion, effective ports, native inverses and validation for engine launch and retention.
- `internal/setup/proc/{idle,detach_unix,detach_windows}.go` detach the idle worker and provide process-liveness checks.

The exported downloader and `verify.Ready` contracts retain their signatures. Dependencies and `go.mod` are unchanged. Files under `cmd/trialm/testdata/` are unchanged.

## Verified

All final Go checks used Go 1.26.10 with `GOTOOLCHAIN=local`, scratch HOME, XDG directories, TRIALM_HOME and Go caches.

| Check | Result |
|---|---|
| `gofmt -l .` | Empty |
| `git diff --check` | Pass |
| `go vet ./...` | Pass |
| `GOOS=windows go vet ./...` | Pass |
| `go test -race ./...` | Pass |
| Python harness fixture tests | Six pass |
| TUI frames | 126 frames at 60/80/120 columns with colour and NO_COLOR |
| Journey A model-loop test | Seven keys including installation, one browser click, one approval and zero copy-paste, from fresh state using displayed defaults and skipping calibration |
| Idle retention | Short-timer tests pass for expiry, supersession, concurrent leases and native stop inverses |
| Administrative path | Counted argv and TTY hand-off inspected, zero administrative executions |
| Direct CPU launch smoke | Pass with a generated synthetic GGUF and Homebrew llama-server build 11960, CPU offload disabled |
| Real-local seven-CLI command | Six pass, Codex reaches the route and hits nested Seatbelt when executing its shell tool |
| Current-tree Continue rerun | Pass, including final setup/home agent contracts and IDE snippet loading |
| Patch replay | Ordered series matches a fresh HEAD archive plus the patches |

The direct CPU smoke exercises `--print`, stop, restart via `--check`, `--serve continue`, warm retention and final stop. The synthetic model's check exits 4 with the printed tool-call failure after real speed and cache probes. Random weights produce gibberish. A server-side generation cap bounds these compatibility replies. This verifies truthful failure reporting alongside the separate scripted harness protocol checks.

The full local command passed Claude Code, OpenCode, Aider, Qwen Code, Cline and Continue. Codex returned `sandbox-exec: sandbox_apply: Operation not permitted`. Its setup, greeting and launched route succeeded before the shell tool was blocked. The command's overall exit is 1 because it retains that failure. The first exploratory run exposed fixture configuration/path errors, corrected before the six-pass run. A final Continue-only rerun also validates the final agent result and compact Ready-home contracts.

Evidence lives in `/tmp/p2launch-patch2-gates.log`, `/tmp/p2launch-journey-fresh.log`, `/tmp/p2launch-replay-race.log`, `/tmp/p2launch-race-final.log`, `/tmp/p2launch-vet-final.log`, `/tmp/p2launch-winvet-final.log`, `/tmp/p2launch-budget-final.log`, `/tmp/p2launch-package-lines.log`, `/tmp/p2launch-real-capped.log`, `/tmp/p2launch-idle-last.log`, `/tmp/p2launch-python.log`, `/tmp/claude-501/p0b/p2launch-harness-final/` and `/tmp/claude-501/p0b/p2launch-contract-final/`.

## Budgets

Counts use the committed token-containing source-line scanner.

| Measurement | Final | Limit |
|---|---:|---:|
| Product Go source | 28,011 | 28,500 |
| Increase from measured base 26,909 | 1,102 | 1,591 remaining global lines |
| CLI plus all UI | 5,499 | 5,500 |
| `internal/cli` | 2,310 | 2,500 |
| `internal/ui` | 726 | 2,500 |
| `internal/ui/tui` | 2,141 | 2,500 |
| `internal/ui/window` | 322 | 2,500 |
| `internal/core` | 958 | 2,500 |
| `internal/server` | 902 | 2,500 |
| `internal/harness` | 550 | 2,500 |
| `internal/setup` | 2,243 | 2,500 |
| `internal/setup/proc` | 331 | 2,500 |
| `cmd/trialm` physical lines | 13 | 30 |
| Stripped macOS arm64 binary | 11,608,930 bytes | 14,000,000 bytes |

The same-toolchain baseline binary measured 11,508,818 bytes. The increase is 100,112 bytes.

## Owner checks and remaining limits

1. One superseded timeout fixture remains healthy on port 65444. A cross-tool-call stop returned `operation not permitted` in this sandbox. Its generated model path was deleted, but the process still holds its mapping. Run the scratch-isolated cleanup below from an owner terminal. The corrected runner uses a fresh cleanup context, and the final successful local paths completed their stop steps.
2. Run `go run ./tools/harness-e2e -local -only codex` outside nested Seatbelt. Six of seven local CLI paths are established here.
3. Run the Ubuntu local matrix job and exercise Windows launch/idle behavior on Windows. Windows cross-vet passed. Terminal/window opening and actual IDE extension or Zed UI import remain owner checks.
4. Walk the first-run TUI in a real terminal. The seven-key route uses `s` on the calibration offer to accept its displayed needs defaults and skip calibration. Choosing calibration and then the needs form adds a key. Recipes offers `r` to edit the needs. The model-loop test uses fake setup and harness callbacks. The local runner independently establishes the journal's one-approval, zero-password CPU path.
5. Check a real wired-limit recipe's OS password experience, reboot reset and native adapters on their target systems. All local execution here used CPU llama.cpp. Password prompts were rendered and counted during tests.
6. Hardware fingerprinting is denied by this sandbox's RAM detection. Successful real-model calibration persistence and re-ranking need an ordinary host check. The screen fixtures cover the successful label and measured/predicted presentation.
7. The 15-minute duration is tested through short timers. A wall-clock 15-minute terminal session remains an owner check.

```sh
fixture=/private/var/folders/6j/kxb0cfjn2t9fd0c0msp9mf740000gn/T/trialm-harness-e2e-3843266340
env HOME="$fixture/opencode/home" \
  XDG_CONFIG_HOME="$fixture/opencode/config" \
  XDG_CACHE_HOME="$fixture/opencode/cache" \
  XDG_DATA_HOME="$fixture/opencode/data" \
  TRIALM_HOME="$fixture/opencode/trialm" \
  "$fixture/trialm-bin" launch --stop --plain
```

## Golden changes

Twenty-four existing frames changed and thirty frames were added. Every listed path is relative to `internal/ui/tui/testdata/`. The other TUI frames are unchanged.

- `calibration-120-colorfalse.golden` — Shows displayed defaults and the fast skip-and-accept action for the seven-key path.
- `calibration-120-colortrue.golden` — Shows displayed defaults and the fast skip-and-accept action for the seven-key path.
- `calibration-60-colorfalse.golden` — Shows displayed defaults and the fast skip-and-accept action for the seven-key path.
- `calibration-60-colortrue.golden` — Shows displayed defaults and the fast skip-and-accept action for the seven-key path.
- `calibration-80-colorfalse.golden` — Shows displayed defaults and the fast skip-and-accept action for the seven-key path.
- `calibration-80-colortrue.golden` — Shows displayed defaults and the fast skip-and-accept action for the seven-key path.
- `first-120-colorfalse.golden` — Replaces the setup-release placeholder with the installed-engine path.
- `first-120-colortrue.golden` — Replaces the setup-release placeholder with the installed-engine path.
- `first-60-colorfalse.golden` — Replaces the setup-release placeholder with the installed-engine path.
- `first-60-colortrue.golden` — Replaces the setup-release placeholder with the installed-engine path.
- `first-80-colorfalse.golden` — Replaces the setup-release placeholder with the installed-engine path.
- `first-80-colortrue.golden` — Replaces the setup-release placeholder with the installed-engine path.
- `home-120-colorfalse.golden` — Shows the stopped Ready setup before finding another model.
- `home-120-colortrue.golden` — Shows the stopped Ready setup before finding another model.
- `home-60-colorfalse.golden` — Shows the stopped Ready setup before finding another model.
- `home-60-colortrue.golden` — Shows the stopped Ready setup before finding another model.
- `home-80-colorfalse.golden` — Shows the stopped Ready setup before finding another model.
- `home-80-colortrue.golden` — Shows the stopped Ready setup before finding another model.
- `home-running-120-colorfalse.golden` — Adds the returning-user running Ready setup frame.
- `home-running-120-colortrue.golden` — Adds the returning-user running Ready setup frame.
- `home-running-60-colorfalse.golden` — Adds the returning-user running Ready setup frame.
- `home-running-60-colortrue.golden` — Adds the returning-user running Ready setup frame.
- `home-running-80-colorfalse.golden` — Adds the returning-user running Ready setup frame.
- `home-running-80-colortrue.golden` — Adds the returning-user running Ready setup frame.
- `launch-120-colorfalse.golden` — Adds the local-tool TTY hand-off frame.
- `launch-120-colortrue.golden` — Adds the local-tool TTY hand-off frame.
- `launch-60-colorfalse.golden` — Adds the local-tool TTY hand-off frame.
- `launch-60-colortrue.golden` — Adds the local-tool TTY hand-off frame.
- `launch-80-colorfalse.golden` — Adds the local-tool TTY hand-off frame.
- `launch-80-colortrue.golden` — Adds the local-tool TTY hand-off frame.
- `ready-120-colorfalse.golden` — Adds measured/predicted rows, signed misses, checks and launch/undo actions.
- `ready-120-colortrue.golden` — Adds measured/predicted rows, signed misses, checks and launch/undo actions.
- `ready-60-colorfalse.golden` — Adds measured/predicted rows, signed misses, checks and launch/undo actions.
- `ready-60-colortrue.golden` — Adds measured/predicted rows, signed misses, checks and launch/undo actions.
- `ready-80-colorfalse.golden` — Adds measured/predicted rows, signed misses, checks and launch/undo actions.
- `ready-80-colortrue.golden` — Adds measured/predicted rows, signed misses, checks and launch/undo actions.
- `setup-120-colorfalse.golden` — Replaces the placeholder with plan rows, sizes, untouched files and counted approval.
- `setup-120-colortrue.golden` — Replaces the placeholder with plan rows, sizes, untouched files and counted approval.
- `setup-60-colorfalse.golden` — Replaces the placeholder with plan rows, sizes, untouched files and counted approval.
- `setup-60-colortrue.golden` — Replaces the placeholder with plan rows, sizes, untouched files and counted approval.
- `setup-80-colorfalse.golden` — Replaces the placeholder with plan rows, sizes, untouched files and counted approval.
- `setup-80-colortrue.golden` — Replaces the placeholder with plan rows, sizes, untouched files and counted approval.
- `setup-paused-120-colorfalse.golden` — Adds the two-of-five, 61-percent resume frame.
- `setup-paused-120-colortrue.golden` — Adds the two-of-five, 61-percent resume frame.
- `setup-paused-60-colorfalse.golden` — Adds the two-of-five, 61-percent resume frame.
- `setup-paused-60-colortrue.golden` — Adds the two-of-five, 61-percent resume frame.
- `setup-paused-80-colorfalse.golden` — Adds the two-of-five, 61-percent resume frame.
- `setup-paused-80-colortrue.golden` — Adds the two-of-five, 61-percent resume frame.
- `setup-progress-120-colorfalse.golden` — Adds step progress, gradient transfer bar, rate, ETA and five log lines.
- `setup-progress-120-colortrue.golden` — Adds step progress, gradient transfer bar, rate, ETA and five log lines.
- `setup-progress-60-colorfalse.golden` — Adds step progress, gradient transfer bar, rate, ETA and five log lines.
- `setup-progress-60-colortrue.golden` — Adds step progress, gradient transfer bar, rate, ETA and five log lines.
- `setup-progress-80-colorfalse.golden` — Adds step progress, gradient transfer bar, rate, ETA and five log lines.
- `setup-progress-80-colortrue.golden` — Adds step progress, gradient transfer bar, rate, ETA and five log lines.

No model weights were downloaded. No administrative command ran. Owner harness configurations were untouched.
