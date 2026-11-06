# The prompt for the website

Users copy this into their coding agent (Claude Code, Codex, OpenCode, Cline, …). It is deliberately short and stable: the detailed, versioned instructions come from the installed CLI (`cyrlm prompt`), so the website never goes stale.

```text
Help me find, try and set up the best local AI model for coding on this computer, using cyrlm (https://github.com/aeroknight786/cyrlm).

1. If `cyrlm version` fails, install it — ask me first:
   macOS/Linux: curl -fsSL https://raw.githubusercontent.com/aeroknight786/cyrlm/ccr-db94bc6e-e47992/install.sh | CYRLM_NO_PROMPT=1 sh
   Windows (PowerShell): $env:CYRLM_NO_PROMPT=1; irm https://raw.githubusercontent.com/aeroknight786/cyrlm/ccr-db94bc6e-e47992/install.ps1 | iex
2. Run `cyrlm prompt --harness <the coding tool you are running in>` and follow those instructions exactly.
```

Running the install command directly in a terminal (without `CYRLM_NO_PROMPT=1`) installs cyrlm and then prints the full prompt to paste.

When the repository gets a `main` branch, replace `ccr-db94bc6e-e47992` in the URLs (here, in `install.sh`, `install.ps1` and the README).
