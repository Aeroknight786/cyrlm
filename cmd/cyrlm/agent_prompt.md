Help me find, try and set up the best local LLM for coding on this machine, using the `cyrlm` command-line tool. You are running in <HARNESS> (use that as `--harness`: claude-code, codex, opencode, aider, cline, continue, zed or qwen-code).

Rules — follow them strictly:
- Before every download, install, config change or service restart, show me the exact command, what it changes and the download size, and wait for my "yes".
- Never install or update GPU drivers. Never use sudo/admin without asking first.
- Prefer runtimes that are already installed. Back up any config file before editing it.
- Never ask me to paste API keys into this chat. If a key is needed, ask me to run `cyrlm login` (or set the environment variable) myself in my own terminal.
- Keep your answers short: tables over paragraphs.

Steps:
1. Run `cyrlm version`. If it's missing, install it (macOS/Linux: `curl -fsSL https://raw.githubusercontent.com/aeroknight786/cyrlm/main/install.sh | sh`; Windows: the .zip from https://github.com/aeroknight786/cyrlm/releases; or `go install github.com/aeroknight786/cyrlm/cmd/cyrlm@latest`).
2. Run `cyrlm detect --json`. Summarise my machine and installed runtimes in two lines.
3. Run `cyrlm recommend --harness <HARNESS> --json`. Show the top 3 options as a table: model, runtime, download size, first reply, follow-up turn, task time, feel. Mention the expected error and any warnings from the JSON. Ask which one I want (or whether to see `cyrlm predict <model>` details).
4. Offer a live trial before anything is downloaded: it uses the real model in the cloud, paced to my machine's speed, inside my own coding tool. It needs my cyrlm account key (`cyrlm login`) or an `OPENROUTER_API_KEY`. If I agree, tell me to run this in a separate terminal (it keeps running): `cyrlm try <model> --runtime <runtime> --tuned --harness <HARNESS>`. It prints how to open a second session of my coding tool against it. Do not run the trial inside this session. Wait until I come back and tell you how it felt.
5. When I say go, run `cyrlm plan <model> --runtime <runtime> --tuned --harness <HARNESS> --json`. Show me the steps (with total download size) as a numbered list. Then execute them in order, asking before each one. If a step fails, diagnose and fix it, but never work around the rules above.
6. Run the plan's final `cyrlm verify …` step and show me measured vs predicted. If the prompt cache was not reused, help me fix the runtime setting.
7. Ask if I want to share the anonymous result (timings and hardware class only) to improve predictions for everyone. If yes, run `cyrlm share --last --dry-run`, show me the payload, and after my "yes" run `cyrlm share --last --yes`. If I'm not logged in, tell me to run `cyrlm login` myself.
8. Finish with: how to start my coding tool with the new local model, the exact settings that were changed, and how to undo them.
