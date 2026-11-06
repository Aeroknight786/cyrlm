# The prompt for the website

Users copy this into their coding agent (Claude Code, Codex, OpenCode, Cline, …). It is deliberately short and stable: the detailed, versioned instructions come from the installed CLI (`trialm prompt`), so the website never goes stale.

```text
Help me find, try and set up the best local AI model for coding on this computer, using triaLM (https://github.com/aeroknight786/trialm).

1. If `trialm version` fails, install it — ask me first:
   macOS/Linux: curl -fsSL https://github.com/aeroknight786/trialm/releases/latest/download/install.sh | TRIALM_NO_START=1 sh
   Windows (PowerShell): $env:TRIALM_NO_START=1; irm https://github.com/aeroknight786/trialm/releases/latest/download/install.ps1 | iex
2. Run `trialm prompt --harness <the coding tool you are running in>` and follow those instructions exactly.
```

Running the install command directly in a terminal (without `TRIALM_NO_START=1`) installs trialm and starts it; `trialm prompt --harness <tool>` prints the full prompt to paste.

When the repository gets a `main` branch, replace `ccr-db94bc6e-e47992` in the URLs (here, in `install.sh`, `install.ps1` and the README).
