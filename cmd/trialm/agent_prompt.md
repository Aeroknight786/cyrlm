You are helping me find, try and set up the best local AI model for coding on this computer with the `trialm` CLI. Do the work yourself, step by step. You are running in <HARNESS> — use that as `--harness` (claude-code, codex, opencode, aider, cline, continue, zed or qwen-code).

Rules — follow them strictly:
- Before every download, install, config change or service start, show me the exact command, what it changes and the download size, and wait for my "yes". Read-only commands (detect, recommend, plan, verify) need no approval. To save round trips, show the whole plan once with every approval it will need, then ask step by step.
- Never install or update GPU drivers. Never use sudo/admin without asking first. Prefer runtimes that are already installed. Back up any config file before editing it, and keep settings I or my tools added.
- Never change the settings of the coding tool session you are running in right now: anything that would point this session at the local model happens only in a new session I start myself.
- Never ask me to paste an API key into this chat and never print one. When a key is needed, ask me to run `trialm login` myself in my own terminal; it stores my OpenRouter key on this machine only.
- Use triaLM's commands as printed. Do not write your own proxies, bridges or config formats: if my coding tool cannot talk to the runtime, `trialm bridge` is the supported fix. If a trialm command is wrong for my versions, tell me what failed instead of improvising.
- Long-running servers: always start them with triaLM's `--background` or `trialm bg` commands (they detach properly and return only once the server answers). Do not use `nohup … &` or `Start-Process` yourself: your command runner may kill those when the command returns.
- My terminal may not have the same PATH as yours: when giving me a command to run, use the exact text trialm printed (it uses full paths where needed).
- Be brief: tables over paragraphs, a few lines between steps. Quote the error ranges and warnings trialm prints for this machine and model; never substitute general accuracy figures.

1. Machine — run `trialm detect --json` and summarise my machine and installed runtimes in two lines.

2. Options — run `trialm recommend --harness <HARNESS> --json`. Show the top 3 as a table: model, file (quant) and download size, runtime, first reply, follow-up turn, task time, feel. Add one line with this prediction's error range and any warnings (for example a 3-bit "quality caveat", a "trial stand-in" or "none will feel usable"). Say that the ranking is by speed and model size, not a measured coding-quality score. Ask which one I want. Use each option's `quant` and `runtime` (or its `try_command`) in every later command.

3. Trial — offer to let me feel the model before anything is downloaded: the real model runs in the cloud, but answers arrive at my machine's predicted speed, in a second session of my own coding tool.
   - If `trialm config` shows "openrouter (none)", ask me to run `trialm login` in my own terminal (key from https://openrouter.ai/keys) and tell you when done.
   - Start it: `trialm try <model> --runtime <runtime> --quant <quant> --harness <HARNESS> --background`. It returns once the trial server answers and prints the "Connect" section and the log path. If it reports an error, fix it with me.
   - If the Connect section says "Add to <file>", back that file up, show me the snippet, and apply it after my "yes". Then give me the single command to run in a NEW terminal window. Do not start that session yourself.
   - While I try it, you can read the log to explain what happened (first-reply times, cache misses, truncation). When I come back, stop the trial with the "Stop:" command it printed, show me the session summary at the end of the log, and ask how it felt.

4. Plan — when I say go, run `trialm plan <model> --runtime <runtime> --quant <quant> --harness <HARNESS> --json`. Show the steps as a numbered list with the total download size and every warning; mark optional ones. Skip optional steps unless I ask for them — and never apply a step that would make the local model the default for the coding tool you are running in. Execute the other steps in order, asking before each one. Downloads can be silent for minutes: tell me, and check progress by the growing file size rather than assuming a hang. If a step fails, diagnose and fix it within the rules above.

5. Verify — run the plan's `trialm verify … --harness <HARNESS>` step. Show measured vs predicted speed, then the compatibility line. The setup is done only when it says "Ready" (greeting, tool call and follow-up all ✓). If it says NOT READY, show me the error; do not tell me the setup is finished, and do not hand-patch around it.

6. Finish — give me the exact command trialm printed to start my coding tool with the local model (for tools that need it, after the bridge from the plan is running), which settings were changed (and where the backups are), how to stop the servers it started, and how to undo each change.
