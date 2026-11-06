# Runtime profiles

Runtime behaviour lives in [`internal/runtime/profiles.json`](../internal/runtime/profiles.json), not in code, because Ollama, LM Studio and llama.cpp change defaults every few releases.

## Schema (v1)

| Field | Meaning |
|---|---|
| `id`, `name`, `versions` | profile identity and the runtime versions it describes |
| `engine` | `llamacpp` or `mlx` — selects the efficiency table |
| `formats` | model formats it loads (`gguf`, `mlx`) |
| `platforms` | `darwin/arm64`, `darwin/amd64`, `linux`, `windows` → supported backends |
| `backend_preference` | order used to pick a backend the hardware supports |
| `apis` | protocols the **real** runtime exposes (cyrlm itself serves all) |
| `defaults.context` | how the default context is chosen: `vram_tiers` + `otherwise_tokens`, `fixed_tokens`, `fit_to_memory`, or `native` |
| `defaults.honors_request_num_ctx` | per-request `options.num_ctx` changes the context (and reloads the model) |
| `defaults.keep_alive_s` | idle seconds before unloading (`-1` = never) |
| `defaults.slots`, `slot_similarity` | prompt-cache slots and the similarity below which the least-recently-used slot is taken |
| `defaults.kv_cache_type`, `flash_attention` | memory and attention settings |
| `defaults.overflow` | prompt larger than the context: `error`, `truncate_oldest`, `truncate_middle` |
| `defaults.on_context_full` | generation fills the context: `stop` or `shift` |
| `defaults.swa_rollback`, `checkpoints` | sliding-window/hybrid models: `free`, `checkpoints` (only to saved points), `none` |
| `defaults.ram_cache_gib` | host-RAM prompt cache that restores evicted prompts |
| `confidence` | per-setting `high`/`medium`/`low`; low ones are printed as assumptions |
| `tuned` | recommended agentic settings and human-readable steps (`{{ctx}}` is substituted) |
| `perf` | runtime multipliers over the engine model, and load overhead |
| `notes` | shown to users |

## Updating a profile

1. Change the setting, bump `versions`, and set `confidence` honestly (`high` only when documented and checked).
2. Add the source to the PR description (release notes, docs page, or a reproducible measurement).
3. `go test ./internal/runtime/ ./internal/sim/` — tests encode the behaviours the simulator relies on.

## Validation plan

A nightly job should replay recorded harness sessions (request bodies only) against the real runtimes on a small device lab and compare per-request TTFT, cache reuse and errors with the simulator. Profiles with diverging behaviour get their confidence lowered until fixed.
