# cyrlm — try a local coding model before you download it

Paste one message into your coding agent (Claude Code, Codex, OpenCode, …). It finds the best local model for **your** computer, lets you **try the real model at your computer's real speed** inside your own coding tool — before downloading anything — and, if you like it, sets it up for you and checks the result.

## Get started

Paste this into your coding agent:

```text
Help me find, try and set up the best local AI model for coding on this computer, using cyrlm (https://github.com/aeroknight786/cyrlm).

1. If `cyrlm version` fails, install it — ask me first:
   macOS/Linux: curl -fsSL https://raw.githubusercontent.com/aeroknight786/cyrlm/ccr-db94bc6e-e47992/install.sh | CYRLM_NO_PROMPT=1 sh
   Windows (PowerShell): $env:CYRLM_NO_PROMPT=1; irm https://raw.githubusercontent.com/aeroknight786/cyrlm/ccr-db94bc6e-e47992/install.ps1 | iex
2. Run `cyrlm prompt --harness <the coding tool you are running in>` and follow those instructions exactly.
```

You approve every download and change. The only things you do yourself:

1. Save your OpenRouter key when asked — run `cyrlm login` in your own terminal (get a key at https://openrouter.ai/keys). The trial uses the real model in the cloud; a 30–45 minute trial typically costs $0.20–$2.
2. Open one extra terminal for the trial session — the agent gives you the exact command.

Prefer a terminal? `curl -fsSL https://raw.githubusercontent.com/aeroknight786/cyrlm/ccr-db94bc6e-e47992/install.sh | sh` installs cyrlm and prints the same message to paste. On Windows: `irm https://raw.githubusercontent.com/aeroknight786/cyrlm/ccr-db94bc6e-e47992/install.ps1 | iex`.

## What you get

- **What runs well here**: your GPU, memory and installed runtimes are detected, and the best model + runtime options are ranked for your coding tool — with how long the first reply, each later turn and a whole task will take.
- **A real trial**: answers come from the actual model, released at the speed your machine would produce them, with the waits a local setup really has (loading, reading long agent prompts, cache reuse). If a default setting would break your tool — such as a context window too small for Claude Code — you'll see it break.
- **Exact setup**: install the runtime, download the exact file, apply tuned settings, connect your tool — each step shown and approved.
- **A check**: measured speed vs the prediction, on your machine.

## Good to know

- **Privacy**: during the trial, what your coding tool sends to the model (prompts, code it read, command output) goes to OpenRouter's model providers. After setup, everything runs on your machine.
- **Accuracy**: typical speed error is ±15% on Apple Silicon and ±20–35% on NVIDIA/AMD desktops; laptops, integrated GPUs and CPU-only machines are less certain. The trial's answers come from the cloud version of the model, usually a bit sharper than the compressed file you'd download (gpt-oss matches exactly). Models no provider hosts (fine-tunes, abliterated models) are tried with their base model.
- **Status**: v0.1 — first real-machine tests are under way. Detection is tested on Linux and in CI on macOS/Windows.

More: [how it works](docs/HOW_IT_WORKS.md) · [research](docs/RESEARCH.md) · [architecture](docs/ARCHITECTURE.md) · [runtime profiles](docs/RUNTIME_PROFILES.md)
