# P2 setup handoff

Built on this clone's current HEAD. Apply `/tmp/claude-501/p0b/p2setup-1.patch` and `p2setup-2.patch` in order with `git apply --whitespace=nowarn`. Each has a one-line `.txt`. No commits or pushes were made. The series was replayed on a fresh `git archive HEAD` export and its file manifest matched this tree.

## Implemented

- Replaced the shell-string `Command` model and the four runtime builders with the closed `Kind` set, structured argv, download requests, file writes, health targets and typed journal inverses. `engine-delegate` is reserved for P3. Administrative commands are rendered and counted. Tests leave the administrative runner unset.
- Moved `internal/runtime` into `internal/engine`. `engines.json` embeds the existing profiles, argv templates, the compatible installed llama.cpp build range, pinned release assets and installer source. Template substitutions preserve each supplied value as one literal argument.
- Pinned llama.cpp **b12182** using GitHub's per-asset SHA256 digests. Coverage includes macOS arm64, Linux x64 CPU/Vulkan/CUDA and Windows x64 CPU/CUDA. CUDA plans include the separately pinned CUDA runtime libraries in their download total and process environment. The asset list and packaging workflow supplied the archive roots. [Release metadata](https://api.github.com/repos/ggml-org/llama.cpp/releases/tags/b12182), [packaging workflow](https://github.com/ggml-org/llama.cpp/blob/b12182/.github/workflows/release.yml)
- Existing llama.cpp is reused only within the embedded build range. The pinned CPU template explicitly selects no device, disables operation/KV offload and disables fitting. All llama.cpp templates set one server slot, both KV cache types, Jinja, the requested context and the local alias.
- Ollama uses pinned **v0.40.15** user-space archives when installation is needed. Its server gets the recipe's flash-attention, KV cache, keep-alive and parallelism settings. An existing daemon can keep its port while the approved setup chooses a free port for its own process. Linux checks for `zstd` before fetching its archive. LM Studio has SHA256-pinned, fully displayed Unix and PowerShell installers with isolated HOME/profile directories and PATH modification disabled. mlx-lm uses the allowlisted uv package install with its tool/bin/cache directories under the setup's engine directory. [Ollama release metadata](https://api.github.com/repos/ollama/ollama/releases/tags/v0.40.15)
- The planner resolves HF files against an immutable revision, records SHA256 and byte counts, and produces a deterministic approval hash. Offline what-if plans display an estimate and a resolution requirement. Applying those unresolved plans is refused before installation. `setup #n` retains the existing saved-listing selection.
- `Executor` uses the committed `download.Client.Fetch` and `download.Candidates`, with injectable `Fetcher` and `ReadyFunc` seams. It retries transient transport failures, resumes partial files, and re-fetches corrupt completed model files. A download growing by more than 10% stops with a concrete replacement plan and a new hash. Its additional approval is recorded.
- Journals are private, atomic JSON files under `setups/<slug>/journal.json`. They retain each original inverse, transferred bytes, progress, config backup bytes/modes, effective ports, pending approvals and the Ready report. Kernel-held locks serialize apply/undo and release on process exit. SIGINT pauses the journal.
- Undo runs inverses in reverse order. `--delete-models` and `--remove-engine` opt into those removals. Existing harness files are restored byte-for-byte with their modes. Legacy `-cyrlm` names and `.bak-cyrlm` backups are recognized. Native LM Studio imports use an explicit `trialm/<slug>` namespace, and opt-in deletion checks the approved file digests and the LM Studio home pointer.
- `internal/setup/proc` owns detached execution, health waits, PID records, stop/status, output logs and bounded log tails. Child command output is emitted while commands run. Start steps return after health answers. Effective ports remain in `Journal.Addresses` while the approved plan stays intact.
- `core.Session` exposes `Plan`, `Apply`, `Status` and `SetupExecutor`. Status includes live process health. Setup events include plan, step, progress, log, Ready, paused and undone states. NDJSON setup events include `next`. Progress fields carry measured Figures and predictions retain their bands. The plain renderer prints steps and progress. Its approval reads one key, with Enter accepting and Ctrl-C cancelling. Direct setup reaches its plan approval without the separate calibration offer.
- The final check calls `verify.Ready` with the recipe's predictions, target depth, bits per weight, harness and route APIs. A failing Ready report is persisted and mapped to CLI exit 4 with its failing check.

## Files and integration boundaries

The implementation is in `internal/setup/{types,plan,resolve,executor,policy,fetch,content,ports,archive,native,undo,lock_*}.go`, `internal/setup/proc`, `internal/engine`, `internal/core/setup.go`, `internal/cli/plan.go` and the plain/JSON renderers. The obsolete setup builders, harness shell builder, detach files and runtime package were deleted. Setup tests now exercise the typed model instead of characterizing generated shell scripts.

The downloader package and `internal/verify/ready.go` are unchanged. Their committed implementations in this clone are still stubs. Production defaults already call those contracts. The CPU e2e supplies a checksum-checking engine-only fetch fake and a Ready fake that checks real health and a real synthetic chat completion. Set `TRIALM_E2E_REAL_DOWNLOAD=1` and `TRIALM_E2E_REAL_READY=1` to select the parallel agents' implementations after merge.

- Runtime import-path replacements, with the old local `runtime` alias retained, in `internal/calibrationcli/{calibrate,calibrate_check}.go`, `internal/cli/{agentflow,main,recommend}.go`, `internal/core/recommend.go`, `internal/predict/{predict,predict_test,calibration_test}.go`, `internal/server/{server_test,fidelity_test}.go`, `internal/sim/{device,pacer,sim_test}.go`, `internal/ui/plain.go` and `internal/verify/verify_test.go`.
- Background startup moved out of `internal/cli/bridge.go`. `update.go` and `entry.go` call the proc package. The setup help line now describes execution.
- `internal/ui/{plan,json}.go` render the new model and events. TUI files and TUI goldens are unchanged.
- `cmd/trialm/p1a_test.go` accepts the new setup event names in the existing NDJSON contract test.
- `.github/workflows/engine-e2e.yml` adds CPU setup, synthetic inference and undo on Ubuntu, macOS and Windows.

For the launch agent, the journal is the durable recipe and process record. Start inverses identify `run/<slug>-<step-id>.pid`. `proc.Record` includes argv, environment, health URL and log path. `Journal.Addresses` contains approved-plan port substitutions. `recipe.json` preserves symbolic paths. Launch behaviour, idle retention, IDE snippets and TUI screens stay with that agent.

## Verified locally

Final gates used **Go 1.26.10** from the cached toolchain, `GOTOOLCHAIN=local`, a scratch HOME, XDG config/cache/data directories and TRIALM_HOME. Earlier exploratory runs used the host Go 1.27.3 and inherited HOME while TRIALM_HOME was scratch. No owner harness configuration was intentionally written. The final pinned-toolchain runs supersede those exploratory results.

- `gofmt -l .` produced no paths.
- `git diff --check`, `go vet ./...`, `GOOS=windows go vet ./...` and `go test -race ./...` passed.
- The real pinned macOS arm64 release was checksum-checked, extracted and run on CPU with a generated two-layer Qwen3-shaped Q4_K_M GGUF. Setup reached the fake Ready gate after real health and chat inference, then full undo retained only its journal. The generated GGUF was deleted. The test ran with the race detector. No model weights were downloaded.
- A subprocess received **SIGINT at download 61%**, exited **130**, and a second process resumed the partial file to Ready with **one recorded approval**. This uses a contract-shaped fake downloader and the durable journal. The context-cancellation unit test also verifies config restoration, bytes, modes and cleanup.
- Tests cover all required llama.cpp asset targets, the four adapter plans and harness-owned files, version-range fallback, stable and changed hashes, writable-path restrictions, literal argv substitutions, safe JSON path expansion, port collisions, locks, corrupt model re-fetch, Ready failures, legacy backups/names, deletion opt-ins and the second approval for download growth.
- A built CLI dry-run returned a 64-character `plan_id`. The same reviewed dry-run with that hash exited 0. Changing context and applying the old hash exited **4** before execution. The saved listing test retains `#1` recipe behaviour.
- The Mac IQ4_XS recipe's plain header showed **1 approval**, **1 OS password prompt** and **1 more after each restart**. The Linux CPU recipe showed **1 approval** and **0 OS password prompts**. Reserve tests reject wired limits leaving less than 4 GiB on 24 GiB Macs or 8 GiB on 64 GiB Macs. No administrative command was executed during validation.

Relevant logs are `/tmp/p2-race-final.log`, `/tmp/p2-vet-final.log`, `/tmp/p2-windows-vet-final.log`, `/tmp/p2-engine-e2e-final.log`, `/tmp/p2-sigint.log`, `/tmp/p2-approval.log`, `/tmp/p2-mac-prompts.log`, `/tmp/p2-linux-prompts.log` and `/tmp/p2-budgets-final.log`.

## Budgets

The existing scanner counts non-test Go source lines containing tokens.

| Measurement | Final | Limit / comparison |
|---|---:|---:|
| Product source | 26,163 | 28,500 |
| Net product increase | 782 | allowance 1,500 (base 25,381) |
| internal/cli | 2,347 | 2,500 (153 lines spare) |
| internal/setup | 2,011 | 2,500 |
| internal/setup/proc | 280 | 2,500 |
| internal/engine | 417 | 2,500 |
| internal/core | 761 | 2,500 |
| cli + ui | 5,169 | 5,500 |
| cmd/trialm | 13 physical lines | 30 |
| Stripped binary | 11,458,610 bytes | 14,000,000 |
| Binary increase | 101,536 bytes | measured base 11,357,074 (below 300 KB) |

## Golden changes

Only these eight plan fixtures changed. Both files in each row reflect the typed argv schema, plan ID, prompt counts, complete known download total, owned setup paths and final Ready check. Target-speed predictions use the recipe context depth instead of the scenario mid-depth.

| Files | Fixture-specific reason |
|---|---|
| `3090-128/plan.json`, `3090-128/plan.txt` | Pinned Linux CUDA llama.cpp plus its pinned CUDA libraries replace source/install shell commands and generated launcher scripts. |
| `4090-64/plan.json`, `4090-64/plan.txt` | The same pinned CUDA install composition and typed server/bridge steps replace the old shell plan. |
| `5070-64/plan.json`, `5070-64/plan.txt` | Pinned user-space Ollama, the zstd preflight and explicit private server environment replace the system installer and service-edit scripts. |
| `7900xt-64/plan.json`, `7900xt-64/plan.txt` | The Ollama typed adapter replaces its old installer, pull and service shell plan. |

All paths above are under `cmd/trialm/testdata/golden`. The M5 and CPU plan error fixtures are unchanged, as are prediction, recommendation, detection and TUI fixtures.

## Remaining limitations and owner checks

The measured one-approval path is llama.cpp CPU on this Mac. The native-adapter acceptance checks below remain open.

1. Merge the downloader and Ready implementations and rerun the e2e with both real-contract switches. The full speed, follow-up, tool, harness and spill checks were not exercised by this clone's Ready stub.
2. Run the new CPU job on Ubuntu and Windows. Their archives and paths were inspected, and Windows vet passed. Execution on those hosts remains for CI. GPU CUDA/Vulkan execution and real Mac wired-limit changes remain owner checks.
3. Fresh Linux Ollama installation requires `zstd` on PATH. The plan's preflight stops before downloading when it is absent. No dependency installer is silently invoked.
4. LM Studio has a fresh Windows PowerShell installer plan, covered by source-digest and scope tests. Its bootstrap still needs a real Windows acceptance run.
5. The pinned LM Studio installer and uv can fetch additional packages. Their complete byte totals are unknown and are labelled in the plan. LM Studio's KV-cache/flash-attention settings still require its native load-settings UI when they differ from defaults. These paths do not establish the full one-approval prompt guarantee.
6. Native import deletion covers the reserved LM Studio namespace in its standard models directory, including a configured home pointer. Separately customized model-directory settings and engine-generated metadata need real acceptance checks. A changed native model makes undo stop for review. Real Ollama, LM Studio and mlx-lm execution and licence prompts need adapter acceptance runs.
7. Legacy undo restores recognized files and backups. Old cyrlm processes without PID records cannot be reliably identified by this implementation. Stopping those processes remains a manual step.
8. Check Enter/Y/n and Ctrl-C in a real terminal and the OS password experience on a wired-limit recipe. Tests render/count admin rows and never execute them. Check Windows terminal process-tree cancellation as well.
