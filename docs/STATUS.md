# triaLM refactor status

*Updated 2026-11-07. The plan is [PLAN.md](PLAN.md); this file says where each phase stands and what only the owner can check.*

## Where things are

| | Branch / tag | State |
|---|---|---|
| **P0a** rename + safety net | `main` = tag **v0.2.0** (released) | Done. Release assets and both installers are on `releases/latest`; installs checked on clean macOS and Linux runners; the old `ccr-db94bc6e-e47992` installers forward to triaLM. |
| **P0b** harness fixes + M5 calibration | `trialm-refactor` (pushed, not released) | Done except the owner checks below. Not tagged v0.2.1 and not merged to `main` until those pass. |
| **P1a** core + agent CLI + private trial | `p1a` (uncommitted patches) | Implemented and tested against local fakes; owner live checks pending. See [P1A_REPORT.md](P1A_REPORT.md). |
| P1b, P2 | — | Not started. |

## P0b: what is done

- **All 7 coding CLIs pass end to end** (greeting, tool call, follow-up) through both `trialm try` and `trialm bridge` against the scripted fake upstream: on the owner's M5 and on Linux CI (`Harness e2e` workflow, every push). Wiring per PLAN §7: Claude Code output cap and 12k prompt, OpenCode via `OPENCODE_CONFIG_CONTENT`, Aider follows the runtime's protocol, Qwen Code/Cline/Continue through triaLM-owned config files, `--harness` optional.
- **Protocol fixes:** ToolSearch `tool_reference`, deferred tools, reasoning forwarded upstream, bridge `max_tokens` clamp, warnings on the try path, Ollama bridged through native `/api/chat`, streamed tool-call verify checks.
- **Bug fixes:** one figure per metric with band-limited precision, deduped notes, rollback limits, fit fallback, FileTag, truncation (6,876 → 16 tokens fixed), the 8 dead OpenRouter ids (live stand-ins, labelled), `--background` banner.
- **One-time calibration** (`trialm calibrate`, PLAN §8): offered on the first interactive run (Enter runs, `s` skips, remembered). Synthetic models with real shapes and llama-bench; measured on the M5: full run **192 s**, `--quick` **55 s**, about 6 GB of temporary disk deleted as it goes. Default picks after calibration: Qwen3.6 35B-A3B IQ3_XXS ~29 tok/s **±27%** (was ±60%), gpt-oss 20B MXFP4 26 tok/s **±24%** (was ±45%). `trialm calibrate --check <file.gguf>` compares with a real model already on disk.

## P0b: known gaps (not done)

- **±25% on the M5 is not established.** The one real check (Qwen3.5 9B Q4_K_M, already in the HF cache) measured 14.4 tok/s, then 22 tok/s an hour later on the same file: this Mac varies about 1.5x between runs (heat, background apps). The uncalibrated prediction (22.3) matched the second run. Cross-quant transfer within the hybrid family is weak (that model gets a ±126% band). The quiet-machine real-model bench decides it.
- MLA (DeepSeek/GLM) is not calibrated; three fits were rejected by their held-out check (dense IQ3_XXS decode, Gemma decode, dense MXFP4 prefill).
- MTP layer count: not fixed (the cause was not established). FileTag: unsloth's metadata-only first shard is not handled. The simulator reports, but does not slow down, a client `num_ctx` that no longer fits memory.

## Owner checks (each needs you)

1. **P0a on the M5:** `curl -fsSL https://github.com/Aeroknight786/trialm/releases/latest/download/install.sh | sh`. The first `trialm` run moves your real `~/.cyrlm` into `~/.trialm` (a symlink stays at `~/.cyrlm`). Check that the old key and setup still launch.
2. **P0b build:** in `~/code/trialm` on `trialm-refactor`: `go build -o ~/.local/bin/trialm ./cmd/trialm`, then `trialm` (the calibration offer appears; press Enter).
3. **Calibration against a real file you already have (no download, ~2 min):**
   `trialm calibrate --check ~/.cache/huggingface/hub/models--unsloth--Qwen3.5-9B-GGUF/snapshots/3885219b6810b007914f3a7950a8d1b469d598a5/Qwen3.5-9B-Q4_K_M.gguf`
   Close other apps first; run it twice to see the run-to-run spread.
4. **Codex + bridge on a real llama-server (P0b acceptance), no download with the 9B:**
   `llama-server -m <the path above> -c 65536 -fa on --jinja -ngl 99 --port 8080 --alias qwen3.5:9b`
   then in another terminal `trialm bridge --endpoint http://127.0.0.1:8080 --harness codex --background`, then run the Codex command it prints: greeting, a tool call, a follow-up. The plan's acceptance names Qwen3.6 35B-A3B, which needs its 13 GB download.
5. **Claude Code at 40k no longer compacts on turn 1:** needs your OpenRouter key (`trialm login` in your own terminal), then `trialm try qwen3.6:35b-a3b --harness claude-code --launch`.
6. **The real-model M5 bench:** `tools/m5-bench.sh` (asks before its ~25 GB download, ~15 min). Then compare with `trialm predict qwen3.6:35b-a3b --runtime llamacpp --quant IQ3_XXS` and `trialm predict gpt-oss:20b --runtime llamacpp`.
7. **Strata on Metal:** run Strata's installer on the M5 to settle whether it works there.

## How P0b was built

Five Codex agents (gpt-6.1-sol and gpt-6.1-terra, high effort) worked in separate clones, each with an owned-files brief; a coordinator reviewed, committed and merged every branch, regenerated the goldens once, and ran the GPU work outside the agents' sandbox (it blocks Metal). Commit messages name the agent that wrote each change.
