# Harness end-to-end checks

From the repository root, run `go run ./tools/harness-e2e`. Use `-only claude-code` (or another harness ID) to select one CLI, `-latest` to use current releases, `-keep` to retain scratch homes and installations, and `-output DIR` to select the summary and log directory. The default is `harness-e2e-logs`. The runner needs Go, Node/npm, Python 3, and uv with Python 3.12 available. It builds the checked-out trialm and does not download model weights.

The pinned versions come from `research/refactor-2026-11/harness-results.json`; that file has no Codex entry, so Codex is pinned separately at 0.167.0. The runner uses fresh isolated homes, config directories, work directories, and uv tool/Python installations. It saves each installed CLI's `--help` output (`exec-help.log` as well for Codex). It parses the connection instructions printed by each command, then adds headless flags. Missing runnable CLI instructions are failures, including cases where only extension instructions are printed. triaLM currently offers no machine-readable connect output for these commands.

The fake upstream responds to a greeting, asks the CLI to create `tool.txt`, and answers after the tool result. Aider uses an applied edit and a deliberately failing first test to elicit its follow-up. `summary.json` records each harness and path; detailed logs include CLI output, connection instructions, server output, and fake upstream requests. The CLI may make other network requests; this is not a network sandbox.

Codex uses `exec --sandbox workspace-write` with approval policy `never`. Its macOS Seatbelt sandbox cannot run inside another Seatbelt sandbox. A tool result containing `sandbox-exec: sandbox_apply: Operation not permitted` is reported as a failure that needs a rerun outside the outer sandbox; the runner does not disable Codex's sandbox.

Cline's greeting has multiple words because its CLI treats a single word as a command name. Continue runs with `--allow Write`, and the fake calls `Write` with `filepath` and `content`, matching cn's advertised schema. Missing tools and required arguments fail at the fake upstream rather than falling back to an unrelated tool.

Run the focused checks with `go test ./tools/harness-e2e` and `python3 -m unittest discover -s tools/harness-e2e -p 'test_*.py'`.
