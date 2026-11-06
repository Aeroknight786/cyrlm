You are helping me find, try and set up the best local AI model for coding on this computer with the `cyrlm` CLI. Do the work yourself, step by step. You are running in <HARNESS> — use that as `--harness` (claude-code, codex, opencode, aider, cline, continue, zed or qwen-code).

Rules — follow them strictly:
- Before every download, install, config change or service restart, show me the exact command, what it changes and the download size, and wait for my "yes". Read-only commands (detect, recommend, plan) need no approval.
- Never install or update GPU drivers. Never use sudo/admin without asking first. Prefer runtimes that are already installed. Back up any config file before editing it.
- Never change the settings of the coding tool session you are running in right now: anything that would point this session at the local model happens only in a new session I start myself.
- Never ask me to paste an API key into this chat and never print one. When a key is needed, ask me to run `cyrlm login` myself in my own terminal; it stores my OpenRouter key on this machine only.
- Be brief: tables over paragraphs, a few lines between steps.

1. Machine — run `cyrlm detect --json` and summarise my machine and installed runtimes in two lines.

2. Options — run `cyrlm recommend --harness <HARNESS> --json`. Show the top 3 as a table: model, runtime, download size, first reply, follow-up turn, task time, feel. Add one line on the expected error and any warnings (for example a "trial stand-in" or "none will feel usable"). Ask which one I want.

3. Trial — offer to let me feel the model before anything is downloaded: the real model runs in the cloud, but answers arrive at my machine's predicted speed, in a second session of my own coding tool.
   - If `cyrlm config` shows "openrouter (none)", ask me to run `cyrlm login` in my own terminal (key from https://openrouter.ai/keys) and tell you when done.
   - Start the trial server in the background so it keeps running after your command returns, with output and errors in one log:
     macOS/Linux: `nohup cyrlm try <model> --runtime <runtime> --harness <HARNESS> > "$HOME/.cyrlm-trial.log" 2>&1 &`
     Windows (PowerShell): `Start-Process cyrlm -ArgumentList 'try','<model>','--runtime','<runtime>','--harness','<HARNESS>' -RedirectStandardOutput "$HOME\.cyrlm-trial.log" -RedirectStandardError "$HOME\.cyrlm-trial.err" -WindowStyle Hidden`
   - Wait a few seconds and read the log (and the .err file on Windows). If it shows an error, fix it with me. Otherwise find the "Connect" section:
     - If it says "Add to <file>", back that file up, show me the snippet, and apply it after my "yes".
     - Then give me the single command to run in a NEW terminal window to start my coding tool against the trial. Do not start that session yourself.
   - While I try it, you can read the log to explain what happened (first-reply times, cache misses, truncation). When I come back, stop the trial with the command printed in the log's "Running (pid …)" line, show me the session summary at the end of the log, and ask how it felt.

4. Plan — when I say go, run `cyrlm plan <model> --runtime <runtime> --harness <HARNESS> --json`. Show the steps as a numbered list with the total download size; mark optional ones. Skip optional steps unless I ask for them — and never apply a step that would make the local model the default for the coding tool you are running in. Execute the other steps in order, asking before each one. If a step fails, diagnose and fix it within the rules above.

5. Verify — run the plan's final `cyrlm verify …` step and show measured vs predicted. If the prompt cache was not reused, help me fix that setting.

6. Finish — tell me how to start my coding tool with the new local model, which settings were changed (and where the backups are), and how to undo each change.
