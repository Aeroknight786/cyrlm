# Research data and scripts

All scripts are Python standard library only.

## accuracy/

- `bench_data.csv` — 565 llama.cpp/MLX measurements transcribed from public sources (URL per row): llama.cpp discussions #4167 (Apple), #15013 (CUDA), #10879 (Vulkan), #15021 (ROCm), #23313 (SYCL), #15396 (gpt-oss long prompts), #16578 and lhl/strix-halo-testing (depth sweeps), issue #13565, XiongjieDai/GPU-Benchmarks-on-LLM-Inference, ik_llama.cpp #164, mlx-lm #1847, one r/LocalLLaMA report.
- `build_dataset.py` — rebuilds the CSV from the transcribed values.
- `specs.py` — device specs and model geometry used for fitting.
- `predict.py` — roofline predictor, llmfit reimplementation, held-out evaluation (`python3 -I predict.py bench_data.csv`, output in `out_primary.txt`).
- `summary.py` — per-segment error table.
- `refit_prefill.py` — prefill efficiency refit with/without 2024-era builds (`python3 -I refit_prefill.py .`).
- `memfit.py` — memory-fit checks.

Limitations: ~55% of rows are Llama-2-7B scoreboard runs; builds span 2023–2026; few CPU-only, MoE, Windows or thermal data points. A crowd dataset (localmaxxing, bundled with llmfit) was used for an independent check during research but is not redistributed here because its terms are unclear.

## hybrid/

- `prefill_offload.py` — cloud prefill + KV transfer vs local prefill (break-even link speeds).
- `handoff.py` — cloud-first reply with mid-response hand-off to local decode.
