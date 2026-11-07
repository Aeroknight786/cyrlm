# triaLM refactor status

*Updated 2026-11-07 (P1a, P1b and P2 merged on `trialm-refactor`; P2 is the last phase in this run). The plan is [PLAN.md](PLAN.md); this file says where each phase stands and what only the owner can check.*

## Where things are

| | Branch / tag | State |
|---|---|---|
| **P0a** rename + safety net | `main` = tag **v0.2.0** (released) | Done. Release assets and both installers are on `releases/latest`; installs checked on clean macOS and Linux runners; the old `ccr-db94bc6e-e47992` installers forward to triaLM. |
| **P0b** harness fixes + M5 calibration | `trialm-refactor` (pushed, not released) | Done except the owner checks below. Not tagged v0.2.1 and not merged to `main` until those pass. |
| **P1a** core + agent CLI + private trial | `trialm-refactor` (pushed, not released) | Done against local fakes; the live OpenRouter checks below are yours. Details: [P1A_REPORT.md](P1A_REPORT.md). |
| **P1b** new Charm CLI | `trialm-refactor` (pushed, not released) | Done except owner checks 9–11. Details: [P1B_REPORT.md](P1B_REPORT.md). All 7 CLIs pass both harness paths (the agent's sandbox blocked Codex; rerun outside it: 14/14). |
| **P2** one-approval setup + launch | `trialm-refactor` (pushed, not released) | Done except owner checks 12–16. Reports: [setup](P2SETUP_REPORT.md), [downloader](P2DL_REPORT.md), [verify](P2VERIFY_REPORT.md), [launch](P2LAUNCH_REPORT.md). |

## P0b: what is done

- **All 7 coding CLIs pass end to end** (greeting, tool call, follow-up) through both `trialm try` and `trialm bridge` against the scripted fake upstream: on the owner's M5 and on Linux CI (`Harness e2e` workflow, every push). Wiring per PLAN §7: Claude Code output cap and 12k prompt, OpenCode via `OPENCODE_CONFIG_CONTENT`, Aider follows the runtime's protocol, Qwen Code/Cline/Continue through triaLM-owned config files, `--harness` optional.
- **Protocol fixes:** ToolSearch `tool_reference`, deferred tools, reasoning forwarded upstream, bridge `max_tokens` clamp, warnings on the try path, Ollama bridged through native `/api/chat`, streamed tool-call verify checks.
- **Bug fixes:** one figure per metric with band-limited precision, deduped notes, rollback limits, fit fallback, FileTag, truncation (6,876 → 16 tokens fixed), the 8 dead OpenRouter ids (live stand-ins, labelled), `--background` banner.
- **One-time calibration** (`trialm calibrate`, PLAN §8): offered on the first interactive run (Enter runs, `s` skips, remembered). Synthetic models with real shapes and llama-bench; measured on the M5: full run **192 s**, `--quick` **55 s**, about 6 GB of temporary disk deleted as it goes. Default picks after calibration: Qwen3.6 35B-A3B IQ3_XXS ~29 tok/s **±27%** (was ±60%), gpt-oss 20B MXFP4 26 tok/s **±24%** (was ±45%). `trialm calibrate --check <file.gguf>` compares with a real model already on disk.

## P0b: known gaps (not done)

- **±25% on the M5 is not established.** The one real check (Qwen3.5 9B Q4_K_M, already in the HF cache) measured 14.4 tok/s, then 22 tok/s an hour later on the same file: this Mac varies about 1.5x between runs (heat, background apps). The uncalibrated prediction (22.3) matched the second run. Cross-quant transfer within the hybrid family is weak (that model gets a ±126% band). The quiet-machine real-model bench decides it.
- MLA (DeepSeek/GLM) is not calibrated; three fits were rejected by their held-out check (dense IQ3_XXS decode, Gemma decode, dense MXFP4 prefill).
- MTP layer count: not fixed (the cause was not established). FileTag: unsloth's metadata-only first shard is not handled. The simulator reports, but does not slow down, a client `num_ctx` that no longer fits memory.

## P1a: what is done

- `cmd/trialm` is a 13-line entry; `internal/core` (Session: Detect, Find, Try), `internal/figure` (every number carries value, range, unit, kind and source), `internal/cli`, `internal/ui`.
- Five verbs: `find`, `try`, `setup` (`--dry-run` prints a `plan_id`; `--yes=<plan_id>` refuses a changed plan; execution arrives in v0.5), `launch` (v0.5 stub), `key`. Old verbs are hidden aliases; `trialm help advanced` lists them. `trialm --json` returns state plus `next`.
- `--json` is NDJSON with Figures and `next` on every result; plain output has no escape bytes when piped; exit codes 0/1/2/3/4/130.
- `trialm key`: env key, browser sign-in (PKCE), headless code, masked paste, `--remove`; stored 0600 in `~/.trialm/credentials`.
- Private trials: only providers that do not store data (`--zdr` stricter), a $2 cap, `--pace p50|p90`, fidelity labels, cloud-bound moments, and a summary card. `trialm update` with a sha256 check.
- Gates: `find --json` equals the old `recommend --json` on all six fixtures; source 22.9k of 26k lines; binary 9.4 of 14 MB. All 7 CLIs still pass the e2e runner.

## P1b: what is done

- Bare `trialm` on a terminal opens one inline Charm app (bubbletea v2.1.1, lipgloss v2.0.6, bubbles v2.2.1, huh v2.0.3; Go 1.26.10): detection → OpenRouter key (Enter = browser, `p` paste, `s` skip) → **the calibration offer as its own screen** (Enter runs it with a progress bar and ETA, `s` skips, remembered) → needs form (Enter takes every default) → recipe table → details (`d`) → trial (hands the terminal to the coding tool, or `w` opens a window) → summary card with a 1/2/3 rating ("1 too slow" re-ranks toward speed) → returning-user home with trial history.
- Window mode, a chat screen for `use=chat`, and a claims gallery of only the three sourced claims PLAN cites. Plain and `--json` output are unchanged off a TTY.
- Gates: 96 screen goldens (16 screens × 60/80/120 columns × colour/NO_COLOR); Journey A in 4 keys + 1 browser click; binary 11.7 of 14 MB; govulncheck clean.
- Review fixes after driving the real binary on the M5 (calibration ran from the offer screen in ~3.5 min): the needs form defaults to the installed tool; Ctrl-C during calibration now also stops llama-bench and deletes the temporary file (it left a 4 GB file and an orphaned process before); a stop prints `Stopped.`; status lines are words, not llama-bench flags.

## P2: what is done

- **`trialm setup`** turns a recipe into a typed plan (closed step set, argv templates, no shell strings) with a `plan_id`, counted prompts (1 approval; +1 OS password per admin row; "1 more after each restart" for the Mac wired limit) and a byte total that subtracts files already on disk. One Y/n, then it installs, fetches, starts, and runs Ready. Ctrl-C pauses; running it again resumes. `--undo` reverses everything (models and engine only with `--delete-models` / `--remove-engine`) and understands cyrlm setups.
- **Engines:** llama.cpp **b12182** from pinned ggml-org assets with GitHub's sha256 (macOS arm64, Linux CPU/Vulkan/CUDA, Windows CPU/CUDA), so no Homebrew is needed; Ollama, LM Studio and mlx-lm adapters as typed steps. `internal/runtime` became `internal/engine`; the old shell-script builders are gone.
- **Downloader:** Range resume, sha256, redirect handling that never forwards the HF token, and reuse of HF-cache / LM Studio / Ollama files by hard link.
- **Ready:** warm-up, writing at short/8K/target depth, reading, cached follow-up, the speculative-verify sweep (exact 1–4 token counts via llama-server's `/completion`), the harness probe, the low-bit tool-call test and the spill check; signed "we were N% optimistic" lines. Real measurements go into `calib.json` under the machine fingerprint.
- **`trialm launch [tool]`** starts the engine (and the bridge in-process when the tool needs it), hands the terminal to the tool, keeps the engine warm 15 min; `--print`, `--serve cline|continue|zed` (with tested snippets), `--stop`, `--check`. TUI: Setup, progress, Ready and the returning-user home with ready setups first.
- **Verified here:** race tests on all three OSes; the new **engine-e2e** CI job runs real setup → Ready → undo with the pinned release on Ubuntu, macOS and Windows; `harness-e2e -local` passes **all 7 CLIs** through `trialm launch` on a real CPU llama-server (on this Mac, Codex included); trial/bridge e2e still 14/14.
- **Real-local on the M5** (scratch triaLM home, your HF cache): `trialm setup qwen3.5:9b --runtime llamacpp --quant Q4_K_M --harness codex` reused the 5.7 GB file (0 bytes downloaded, same inode), installed b12182 (16.7 MB), moved off port 8080 (your `chat.py` holds it), and reached Ready in about 4 min. Measured: writing 21 tok/s at short context (predicted 22), 9 tok/s at 64K (we were ~80% optimistic there), reading 420 tok/s. A second run measured 14 / 9.1 / 280: this Mac still varies about 1.5x between runs, and both runs shared the GPU and memory with your own 9B `llama-server` on port 8081 (and `chat.py` on 8080), so treat them as busy-machine numbers. Full undo left your HF file untouched.
- **Bugs this run found and fixed:** Ready overflowed the context at the target depth (prompt sizing); reading was measured after the 64K request and included llama-server's prompt-cache save (160 vs 420 tok/s); the spec sweep could never succeed through the chat template; the Windows setup lock blocked its own journal rename; `stop` did not wait for exit (Windows kept the DLLs locked).

## After P2: the Mac experience (2026-11-07, afternoon)

Driven by the owner using the app on their M5:
- **Launcher.** Bare `trialm` opens to the models on this Mac (HF cache, Ollama, LM Studio, old cyrlm launchers) and the best picks, each with one figure: the time per reply in the selected tool. Enter sets up (one approval) and opens the tool; `f` opens the recipe table (`FIRST REPLY · LATER REPLIES · WRITING`, "for Claude Code on this Mac"); the setup card is four plain lines, details behind `d`.
- **One route for every agent.** Every tool reaches a local engine through triaLM's bridge, which reshapes requests so strict chat templates accept them (Claude Code 2.1.204 failed on every request before: "System message must be at the beginning"). `trialm launch --serve` gives any custom agent one address that speaks OpenAI Chat/Responses, Anthropic Messages and Ollama.
- **Measured opening prompts.** The bridge records each tool's real first-request size; predictions use it (Claude Code: 12K assumed before; 20K measured headless, 41–47K in the owner's interactive sessions).
- **Low Power Mode** is detected on macOS: the launcher warns, calibration refuses, and throttled Ready numbers are never saved (it cut the 9B from 18–21 to 6–8 tok/s).
- **`trialm launch --stop`** stops any setup's engine, Ready or not.
- **Speed model** reviewed by two other models (docs/MODEL_REVIEW.md): the KV cache at depth is read at a fitted rate on Macs (`tools/fit-attn`), real measurements apply at their own depth, first replies include the whole answer.
- **Real run:** Claude Code fixed a bug in a scratch project on the local Qwen3.5 9B in 3.7 min; 90 s of that was a cache miss caused by the owner's MCP server connecting after the first request (Claude Code's behaviour; not changed).
- **Known gap being fixed next:** calibration and setups used different llama.cpp builds, so real measurements were rejected ("fingerprint changed").

## Owner checks (each needs you)

1. **Already happened (my mistake):** at 07:21 on 2026-11-07 the coordinator ran `trialm help --all` once without a scratch home, so the migration ran on your real machine: `~/.cyrlm` is now a symlink to `~/.trialm`, and your key, the 9B launcher and the results file moved intact (checked; your 9B server on 8081 kept running). What's left of check 1 is the install itself and launching the old setup.
   **P0a on the M5:** `curl -fsSL https://github.com/Aeroknight786/trialm/releases/latest/download/install.sh | sh`. The first `trialm` run moves your real `~/.cyrlm` into `~/.trialm` (a symlink stays at `~/.cyrlm`). Check that the old key and setup still launch.
2. **P0b build:** in `~/code/trialm` on `trialm-refactor`: `go build -o ~/.local/bin/trialm ./cmd/trialm`, then `trialm` (the calibration offer appears; press Enter).
3. **Calibration against a real file you already have (no download, ~2 min):**
   `trialm calibrate --check ~/.cache/huggingface/hub/models--unsloth--Qwen3.5-9B-GGUF/snapshots/3885219b6810b007914f3a7950a8d1b469d598a5/Qwen3.5-9B-Q4_K_M.gguf`
   Close other apps first; run it twice to see the run-to-run spread.
4. **Codex + bridge on a real llama-server (P0b acceptance), no download with the 9B:**
   `llama-server -m <the path above> -c 65536 -fa on --jinja -ngl 99 --port 8080 --alias qwen3.5:9b`
   then in another terminal `trialm bridge --endpoint http://127.0.0.1:8080 --harness codex --background`, then run the Codex command it prints: greeting, a tool call, a follow-up. The plan's acceptance names Qwen3.6 35B-A3B, which needs its 13 GB download.
5. **Claude Code at 40k no longer compacts on turn 1:** needs your OpenRouter key (`trialm login` in your own terminal), then `trialm try qwen3.6:35b-a3b --harness claude-code --launch`.
6. **The real-model M5 bench:** `tools/m5-bench.sh` (asks before its ~25 GB download, ~15 min). Then compare with `trialm predict qwen3.6:35b-a3b --runtime llamacpp --quant IQ3_XXS` and `trialm predict gpt-oss:20b --runtime llamacpp`.
7. **P1a live OpenRouter checks:** `trialm key` (browser sign-in), then check the credit it shows against your OpenRouter dashboard; `trialm try` in Codex and in Claude Code; and a trial's spend on the summary card against the dashboard.
8. **Strata on Metal:** run Strata's installer on the M5 to settle whether it works there.
9. **The new app, first run (now the launcher):** `go build -o ~/.local/bin/trialm ./cmd/trialm && TRIALM_HOME=$(mktemp -d) trialm` in Terminal or iTerm. Walk it: browser sign-in, Enter on calibration, Enter on needs, `d` on a recipe, Enter to try. Ctrl-C at any point should restore the terminal.
10. **Journey D prompt counts from real Claude Code and Codex** (keep your normal permission mode): `trialm --json`, `trialm find --json`, `trialm try 1 --window`, then `trialm --json` and read `data.trials`. Count tool prompts, approvals and browser clicks.
11. **macOS window mode:** from Claude Code, `trialm try 1` (it picks window mode automatically) — count any macOS permission prompts; close the window early and check the card says partial. Also Windows Terminal/conhost and Linux tmux if you use them.

12. **The full Journey A on your own home (P2):** `trialm` → pick a recipe → `s` set up → Enter. Watch the counted prompts on the plan, the download bar and the Ready card; then `trialm launch codex` the next day. This downloads the model you pick (13 GB for Qwen3.6 35B-A3B); the cached 9B costs nothing: `trialm setup qwen3.5:9b --runtime llamacpp --quant Q4_K_M --harness codex`.
13. **Codex + bridge on a real llama-server through `trialm launch codex`** (the P0b acceptance, now one command): greeting, a tool call, a follow-up at 64K.
14. **A Mac wired-limit recipe:** one OS password prompt, and the "1 more after each restart" line after a reboot.
15. **Clean-machine acceptance:** a macOS without Homebrew and a fresh Linux VM: 1 approval, 0 password prompts to Ready. CI covers this on GitHub runners; a real clean machine is still yours.
16. **The 15-minute warm engine** after quitting the tool, and the IDE snippets in the real Cline, Continue and Zed.

## How P0b was built

Five Codex agents (gpt-6.1-sol and gpt-6.1-terra, high effort) worked in separate clones, each with an owned-files brief; a coordinator reviewed, committed and merged every branch, regenerated the goldens once, and ran the GPU work outside the agents' sandbox (it blocks Metal). Commit messages name the agent that wrote each change.
