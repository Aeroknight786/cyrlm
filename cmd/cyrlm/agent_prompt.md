You are helping me find, try and set up the best local AI model for coding on this computer with the `cyrlm` CLI. Do the work yourself, step by step. You are running in <HARNESS> — use that as `--harness` (claude-code, codex, opencode, aider, cline, continue, zed or qwen-code).

Rules — follow them strictly:
- Before every download, install, config change or service restart, show me the exact command, what it changes and the download size, and wait for my "yes". Read-only commands (detect, recommend, predict, plan) need no approval.
- Never install or update GPU drivers. Never use sudo/admin without asking first. Prefer runtimes that are already installed. Back up any config file before editing it.
- Never ask me to paste an API key into this chat and never print one. When a key is needed, tell me to run `cyrlm login --openrouter` myself in a separate terminal (it stores my OpenRouter key on this machine only).
- Be brief: tables over paragraphs, no more than a few lines between steps.

1. Machine — run `cyrlm detect --json` and summarise my machine and installed runtimes in two lines.

2. Options — run `cyrlm recommend --harness <HARNESS> --json`. Show the top 3 as a table: model, runtime, download size, first reply, follow-up turn, task time, feel. Add one line on the expected error and any warnings (e.g. a "trial stand-in"). Ask which one I want.

3. Trial — offer to let me feel the model before anything is downloaded: the real model runs in the cloud, but answers arrive at my machine's predicted speed, in a second session of my own coding tool.
   - Check `cyrlm config`: if "openrouter" shows "(none)" and I'm not logged in, ask me to run `cyrlm login --openrouter` in my own terminal and tell you when done.
   - Start the trial server yourself in the background, logging to a file, e.g. `cyrlm try <model> --runtime <runtime> --tuned --harness <HARNESS> > "$HOME/.cyrlm-trial.log" 2>&1 &` (PowerShell: `Start-Process cyrlm -ArgumentList 'try','<model>','--runtime','<runtime>','--tuned','--harness','<HARNESS>' -RedirectStandardOutput "$HOME\.cyrlm-trial.log" -NoNewWindow`).
   - Read the log's "Connect" section and give me the single command to start my coding tool against the trial in a NEW terminal window. Do not start that session yourself.
   - While I try it, you can read the log to explain what happened (first-token times, cache misses, truncation). When I come back, stop the trial server and ask how it felt.

4. Plan — when I say go, run `cyrlm plan <model> --runtime <runtime> --tuned --harness <HARNESS> --json`. Show the steps as a numbered list with the total download size. Execute them in order, asking before each one. If a step fails, diagnose and fix it within the rules above.

5. Verify — run the plan's final `cyrlm verify …` step and show measured vs predicted. If the prompt cache was not reused, help me fix that setting.

6. Share — ask if I want to share the anonymous result (timings and hardware class only) to improve predictions for everyone. If yes, run `cyrlm share --last --dry-run`, show me the payload, and after my "yes" run `cyrlm share --last --yes` (skip if it says I'm not logged in).

7. Finish — tell me how to start my coding tool with the new local model, which settings were changed, and how to undo them.
