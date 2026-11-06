# cyrlm account server API (v1)

The CLI works without an account: everything except the hosted trial and result sharing runs locally. This document is the contract the account server (website + API) must implement. The CLI side is built (`internal/account`, `cyrlm login/share/config`, `--upstream cyrlm`).

Base URL: configured by `CYRLM_API_URL` or `cyrlm config set api-url <url>` (`account.DefaultAPIURL` once the service exists).

## Authentication

- Users sign in on the website and copy a **key** from their account page.
- They run `cyrlm login` **themselves** (the agent prompt tells coding agents never to handle keys). The key is stored in `<user config dir>/cyrlm/config.json` with mode 0600; `CYRLM_KEY` overrides it.
- Every request: `Authorization: Bearer <key>`, `User-Agent: cyrlm`.
- Keys should be revocable and scoped (trial inference, measurement upload).

## Endpoints

### `GET /v1/me`
Validates the key. `200 {"account": "dev@example.com", "plan": "free", "credits_usd": 5.0}`; `401` for a bad key.

### `POST /v1/measurements`
Uploads one verification report (opt-in only: the CLI shows the full payload and asks unless the user chose `share always` or passed `--yes` after reviewing it). Body = `account.Report`:

```json
{
  "schema": 1, "id": "9c3081beda7ffb0a", "cyrlm_version": "0.2.0", "created_at": "2026-11-06T09:53:54Z",
  "machine": {"os": "darwin", "arch": "arm64", "cpu": "Apple M4 Pro", "cpu_cores": 14, "ram_gb": 48,
              "gpu": "Apple M4 Pro", "gpu_cores": 20, "unified_memory": true,
              "device_spec": "Apple M4 Pro 20c", "segment": "apple_pro"},
  "runtime": "ollama", "runtime_version": "0.12.6",
  "model": "qwen3.6-35b-a3b", "quant": "Q4_K_M",
  "settings": {"context_tokens": 90112, "kv_cache_type": "f16", "flash_attention": true, "backend": "metal"},
  "prompt_tokens": 7980, "follow_up_new_tokens": 1010, "output_tokens": 256,
  "load_s": {"measured": 6.1, "predicted": 6.4, "error_pct": 4.9},
  "prompt_processing_s": {"measured": 11.8, "predicted": 12.9, "error_pct": 9.3},
  "decode_tps": {"measured": 47.2, "predicted": 51.0, "error_pct": 8.1},
  "follow_up_ttft_s": {"measured": 2.1, "predicted": 1.9, "error_pct": -9.5},
  "cache_reused": true, "cache_check": "reused"
}
```
Never contains prompt text, file paths, hostnames, usernames or IP-derived data (the server should not store client IPs with reports). Response `200 {"id": "m_…"}`. The server should de-duplicate by `id`.

These reports are the dataset that recalibrates the predictor (per-backend efficiencies, runtime multipliers, segment accuracy bands) and validates runtime profiles.

### `POST /v1/chat/completions` (hosted trial)
OpenAI Chat Completions, streaming, used by `cyrlm try/serve --upstream cyrlm` so users don't need their own OpenRouter key. The server forwards to a provider pinned per model (precision where possible).

Because the user is not paying per token directly, **the server must enforce the trial's economics itself** — a local pacer is trivially bypassed for free fast inference:
- meter tokens against `credits_usd`;
- cap output speed per account to the fastest consumer device profile (~300 tok/s) and limit concurrent requests;
- per-account daily token limits; reject non-streaming bulk use.

### `GET /v1/models` (optional)
Models available for hosted trials, with the provider precision (`{"id": "qwen/qwen3.6-35b-a3b", "precision": "fp8"}`).

## Open decisions

- Hosting stack and auth provider for the website.
- Free trial credit amount and abuse limits.
- Retention and public release of the aggregated dataset.
