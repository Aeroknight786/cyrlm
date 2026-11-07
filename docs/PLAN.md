# triaLM: refactor plan

*Status: proposed 2026-11-06, awaiting the owner's go. Already decided by the owner: the name triaLM, a Charm-based CLI, the OpenRouter key asked early, OpenRouter as the model database, unhosted models later.*

**Base.** draft-user's surface and journeys, draft-arch's order and data registry, and draft-accuracy's evidence layer, with every judge and critique finding fixed. **[R]** = measured or from the 2026-11-06 research and harness tests; **~** = simulated with a band over 25%; **fit** = simulated with an efficiency fitted to the very data it is compared with (in-sample, so it validates nothing). Mocks show the v1.0 state and are illustrative unless marked [R]; §10 says which release lands each element. The M5 pick (about 40 tok/s, 11 s turn, 10 s first reply) carries today's band of **±60% writing / ±70% reading**, so M5 mocks show `~` until the P0b bench.

---

## 1. North star

The user is a developer who already works inside a coding agent (Codex, Claude Code, OpenCode, Aider, Qwen Code, Cline, Continue) and owns one capable machine: a 24 GB MacBook, a Windows or Linux RTX box, a 3090 with 128 GB of RAM, a Radeon or a Strix Halo. They have read Reddit posts claiming 100 tok/s and will not spend an evening and 90 GB of downloads learning the claim does not hold for them. The promise: **within a minute you use any open model inside your own coding tool at the speed your machine would give it, with the uncertainty shown and every number labelled claimed, simulated or measured; one approval installs and verifies the best setup for your needs; after that one keystroke launches it daily; and the whole thing is a CLI, with an agent optional.** The honest limit: a band is only as good as the device data. On the owner's M5 it is ±60/70% today, so the M5 is benchmarked first (P0b, target ≤ ±25% for the default picks).

"Delightful" means measurable targets: 4 keys and 1 browser click from install to the first trial token, 7 to coding on the local model; 1 approval per user-space setup with 0 copy-paste and 0 terminal trips, plus 1 OS password prompt per admin row, counted on the plan before the Y/n (the owner's first test needed about 10 approvals, 16 tool prompts and 3 terminal trips [R]); every screen drawn in under 150 ms from cache, because no screen waits on the network (catalog, endpoint, credit and update fetches are async); one figure per metric (today one setup shows 224 tok/s on one screen and 165 on another [R]); precision never above what we know; colour that always means something; one question on screen at most; finished output stays in scrollback. **Low-bloat** is enforced by CI budgets derived from the feature list (§9) and by cutting scope the journeys do not need (§11).

## 2. User journeys

A **key** is one keypress or paste; an **approval** is consent to change the machine; browser clicks, agent tool prompts and OS password prompts are counted separately. Downloads assume 50 MB/s (Mac) and 100 MB/s (desktops).

### A. Maya: MacBook M5 (10-core GPU, 24 GB, 16 GB GPU budget), Codex

| # | Maya does | She sees | Keys | Appr. |
|---|---|---|---|---|
| 1 | Pastes the install line | The installer runs `exec trialm </dev/tty` | 1 | 0 |
| 2 | — | Detection, about 1.5 s: `Apple M5 · 24 GB · 16 GB for models · Codex, Claude Code · no engine yet · 412 GB free` | 0 | 0 |
| 3 | Enter ("sign in with browser"), then clicks Authorize | OAuth PKCE runs. The catalog refresh and ranking happen while she is in the browser. `✓ $9.71 credit` | 1 + click | 0 |
| 4 | Enter to accept the prefilled needs form | Codex · balanced · 64k · 3-bit ok · your repo, no-logging providers | 1 | 0 |
| 5 | Enter on recipe #1 | A one-time line (trials send this repo only to providers that do not store it), then Codex opens **in this terminal**, in her directory | 1 | 0 |
| 6 | Uses Codex | First reply about 10 s (load plus Codex's prompt), then about 11 s per turn [R pick, ±60% today] | — | 0 |
| 7 | Quits Codex, presses `3`, then `s` | The summary card, then the plan: llama.cpp b11502 (pinned ggml-org release, sha256; no Homebrew needed) · 13.2 GB · launcher · llama-server + in-process bridge · check | 1 (+1 optional) | 0 |
| 8 | Enter (Y) | Progress, then `✓ Ready`, with measured and predicted side by side, and `Codex greeting ✓ tool ✓ follow-up ✓` | 1 | **1** |
| 9 | Enter ("open Codex now") | Codex on the local model | 1 | 0 |

- **First trial token:** about 60 s after the paste (25 s in the browser, then a 10 s simulated cold start she is meant to feel); 4 keys, 1 click.
- **Working local setup:** about 6 min after the trial (4.4 min download, 10 s engine, 90 s check); 7 keys (8 with the rating), 1 approval, 0 password prompts. Next day: `trialm`, Enter.
- **Details pane:** MTP "not worth it for this A3B MoE on Metal llama.cpp (≈0.93×, 1 published point; check measures it)"; YaRN "not needed: native 256K ≥ 64K"; a separate IQ4_XS 17.7 GB row (a quality tier up) needs the GPU limit raised to 20 GB, and the row says the cost: 1 admin password at setup and 1 after every restart.

### B. Dev: RTX 3090 24 GB + 128 GB DDR4, Linux, Claude Code, chasing a Reddit config

| # | Dev does | He sees | Keys | Appr. |
|---|---|---|---|---|
| 1 | `trialm` | Home. `OPENROUTER_API_KEY` is already in his env, so there is no key step. Detection also shows PCIe 4.0 x16, RAM channels and speed (or a 5 s bandwidth probe), and AVX-512 | 1 | 0 |
| 2 | Pastes `llama-server -m gpt-oss-120b-F16.gguf -ngl 999 --n-cpu-moe 26 -b 4096 -ub 4096 -fa on -c 65536 --jinja` and "1682 pp / 32 tg on 3090" | Home spots a command line and opens the claim card (§4): every flag kept, 0 dropped. Prefill 1,580 simulated vs 1,682 claimed (−6%), so **reproduced**. Each follow-up costs at least 1.5 s of PCIe copying. A better pick for Claude Code is listed below | 1 | 0 |
| 3 | Enter: try this config | Claude Code inline, with the full env wiring (§7) | 1 | 0 |
| 4 | Exits; on the card presses `t` (the better pick); exits | Two summary cards in trial history | 1 | 0 |
| 5 | `s` on the one he liked, then Enter | One plan: pinned CUDA llama.cpp release asset · 22 GB or 63 GB · start · wire · check | 2 | **1** |

- **First trial token:** about 30 s, 3 keys. **Working local setup:** about 6 min (22 GB) or 13 min (63 GB) including the check; 6 keys, 1 approval.
- **Tuning:** if the check misses by more than 20% on a hybrid recipe, Ready offers `t tune` (a 3 min llama-bench sweep of `-ub`/`--n-cpu-moe`); it touches only triaLM-owned files, so it needs no new approval.
- **No paste?** `trialm find --claims` lists the curated gallery for a 3090 with ≥ 64 GB RAM (§4).

### C. Ren: RX 7900 XT 20 GB + 64 GB DDR4-3200 + PCIe 3.0, Linux ROCm 7.2, OpenCode, wants Strata or Avalon

| # | Ren does | He sees | Keys | Appr. |
|---|---|---|---|---|
| 1 | `trialm`; in the form, Priority → `quality`; Enter | `gfx1100 · ROCm 7.2 · PCIe 3.0 x16 · DDR4 ~34 GB/s measured · pinned-RAM limit 61.5 GiB · NVMe · 400 GB free` | 3 | 0 |
| 2 | Reads the table | **#1 Avalon** Qwen3.8-Flash-Next IQ3_KS (94.85 GB, 33 shards) + 1.48 GB MTP head: `~95 fit` tok/s short, CLAIM `98.4 short / 81.1 at 250K` [R]; `new · 1 maintainer · AI-written kernels`; VRAM ≥ 19 GB ✓ pinned RAM ✓ disk ✓ gfx1100 ✓. **#2 Strata** IQ3_S HIP + MTP: `~65 fit`, CLAIM `68.4 short` (Strata v0.2.9, Avalon README) [R]. **#3** stock llama.cpp `--n-cpu-moe`, ~12 tok/s with the Gated DeltaNet penalty (§5), shown so the gap is visible. **#4** stable: Qwen3.6-35B-A3B fully on GPU | 0 | 0 |
| 3 | `d` (optional) | "Where decode time goes" bar (dense · cache hits · CPU misses · drafting); MTP **on**: `+40–50% fit`, CLAIM +49% short / +42% at 250K [R] | 1 | 0 |
| 4 | Enter: try | `qwen/qwen3.8-flash`, `≈ family: hosted sibling, full precision · your IQ3_KS ppl 1.443 vs 1.316 for UD-Q4_K_XL` [R]; MTP-burst pacing (α ≈ 0.6 prose, 0.95 edits) | 1 | 0 |
| 5 | `s`, then Enter | One plan: Avalon `install.sh` (`AMDGPU_TARGETS=gfx1100 SKIP_MODEL=1`, pinned commit + sha, 10–30 min build) in parallel with triaLM's downloader (shards, MTP head, ranking file) · start on a free port · OpenCode wired natively · 20 s warm-up · check incl. hit rate | 2 | **1** |

- **First trial token:** about 40 s. **Working local setup:** about 30–35 min, set by the 96 GB download and the parallel build; 6 keys (7 with `d`), 1 approval. If ROCm is missing, the plan stops *before* the Y/n with the one admin command; triaLM never installs drivers. Until the Avalon adapter lands (P5), step 5 shows manual steps.
- **`fit` is not validation:** Avalon's and Strata's efficiencies were fitted to these same author runs, so the band stays at the prior until verify data from ≥ 3 machines exists.
- **RTX 5070 12 GB + 64 GB DDR5 variant:** #1 is Strata Q2_0 + MTP, `~90 fit`, CLAIM `93.0 at 4K` (Strata DETAILS) [R]. From P3 the plan embeds `strata launch --plan --family qwen --model q2_0 --output json` as its own rows, runs `--yes --no-tui --port P`, and checks with `strata connect opencode --check --output json` plus `/metrics`.
- **Strix Halo 128 GB variant:** detection reads the UMA carve-out and GTT limit (the real GPU budget); Lemonade-managed llama.cpp backends rank; DeepSeek V4 Flash on ds4 (32 tok/s) or Lucebox (42 at 8K) sits in the claims lane; `t tune` is suggested because prefill varies 2–3× by backend [R].
- **Who tests this:** the owner's M5 runs neither Avalon (Linux ROCm/CUDA only) nor, probably, Strata (contradictory research; settled on the M5 in P0b), so Journey C is accepted on rented and volunteer hardware (decision 7).

### D. Sam pastes one line into Claude Code or Codex

> *Use triaLM to find, try and set up the best local coding model on this machine: run `curl -fsSL https://github.com/aeroknight786/trialm/releases/latest/download/install.sh | TRIALM_NO_START=1 sh && trialm --json` and follow its `next` field.*

| # | The agent runs | Sam does | Prompts (Claude Code) | Appr. |
|---|---|---|---|---|
| 1 | The install, then `trialm --json`. It gets state plus `next: [{cmd:"trialm key", why:"no OpenRouter key"}]` | Approves twice, the second time with "don't ask again for `trialm`" | 2 | 0 |
| 2 | `trialm key`. An NDJSON `needs_user` event fires and the browser opens on Sam's machine | 1 click. The key never enters the transcript | 0 | 0 |
| 3 | `trialm find --json`, then shows the top 3 | Replies "1" | 0 | 0 |
| 4 | `trialm try 1 --window`. Window mode is the default when `CLAUDECODE` or `CODEX_SANDBOX` is set | Uses his tool in the new window, then closes it. `trialm --json` returns the card | 0 | 0 |
| 5 | `trialm setup 1 --dry-run --json`, which prints the plan and a `plan_id` | Replies "yes" | 0 | **1** |
| 6 | `trialm setup 1 --yes=<plan_id> --json`, which streams events and ends `{"type":"result","ready":true,"next":[{"cmd":"trialm launch claude-code"}]}` | — | 0 | 0 |

- **Claude Code:** 2 tool prompts (0 in auto permission mode, ≈ v2.1.283+), 2 replies, 1 click, 1 approval, 0 terminal trips (today: about 10 + 16 + 3).
- **Codex is worse, and we say so:** its sandbox blocks network, localhost binds and new windows by default, with no equivalent prefix rule, so each escalation prompts (estimated 4–6), and a blocked window exits 3 with `{"needs":"terminal","run":"trialm try 1"}`: 1 terminal trip. P1b measures and publishes both counts.
- Some agents refuse `curl | sh`; the README gives a 3-step form (download, check sha256, run). **First trial token:** about 2 min. **Local setup:** the download plus about 2 min.

### E. Ana: Windows 11, RTX 4070 Ti 12 GB + 32 GB DDR5, Claude Code in Windows Terminal

- `irm https://github.com/aeroknight786/trialm/releases/latest/download/install.ps1 | iex`, then the same home, key and needs form (conhost gets ASCII glyphs).
- Recipes: Qwen3.6-35B-A3B Q4_K_M with experts of N layers in RAM. The fit gate uses real VRAM minus the reserve, never the driver's sysmem fallback (a silent spill to shared memory at RAM speed); a margin under 1 GB shows `spill risk`.
- Trial inline, or a new tab via `wt -w 0 nt -d`. Setup: pinned llama.cpp CUDA asset + cudart (sha256), `run.ps1`, 1 approval, no admin. Verify flags a likely spill (writing at depth < 50% of predicted) with the manual NVIDIA Control Panel fix ("Prefer No Sysmem Fallback" for llama-server.exe); a WDDM pinned-memory failure retries with `GGML_CUDA_NO_PINNED=1` inside the approved plan.
- Tested by CPU engine e2e on the Windows CI runner from P2 and one real Windows + NVIDIA run (rented or volunteer) before v1.0.

## 3. The CLI

**Design source.** Toad is Python + Textual 8.2.7, not Charm [R]. We reproduce its qualities in Go with Charm v2: inline output that keeps scrollback, partial redraws (bubbletea v2's synchronized-output renderer, mode 2026), a footer showing only the keys that work now, and minimal chrome. The trade-off against the tested 290-line stdlib raw-mode UI is decision 1 (§12).

### Command surface: bare `trialm` plus 5 verbs (today: 19)

| Command | Does | Main flags |
|---|---|---|
| `trialm` | Home. On first run: detect, key, needs, recipes. Later runs show ready setups first (Enter = launch). `trialm --json` returns state plus `next` | `--json --plain` |
| `trialm find [query \| cmdline \| url]` | Ranked recipes. A pasted command line or a claim opens the claim card; `--claims` lists the gallery for this hardware | `--for --want --ctx --yarn --max-gb --engines --boosts --claims --as "RTX 3090" --ram 64` |
| `trialm try [#n \| slug \| model \| cmdline]` | A trial. With no argument it runs #1 for the saved needs | `--window --here --serve --playground --zdr --budget 2 --pace p50\|p90` |
| `trialm setup <#n \| slug>` | Plan, one Y/n, execute, verify. Running it again resumes | `--dry-run --yes[=plan_id] --undo --check --tune` |
| `trialm launch [tool]` | Starts the engine (and the bridge if needed), then hands the terminal to the tool | `--print` (env + command for shell aliases), `--serve [cline\|continue\|zed]` (foreground server + a tested settings snippet), `--stop` |
| `trialm key` | OpenRouter sign-in | `--paste --headless --remove` |

`help advanced` lists `serve`, `bridge`, `inspect`, `predict`, `data` and `update` (replaces the binary from the latest release, sha256-checked; home shows one dim line when one exists, checked async at most daily). `status`, `models`, `runtimes`, `login`, `logout`, `config`, `share`, `prompt` and `bg` are folded or deleted (home = status, `/` = models, `setup --check` = verify, `--json` + `next` replaces `agent_prompt.md`); old verbs stay as hidden aliases for one release. Slugs look like `qwen3.6-35b-a3b/iq3_xxs@llamacpp`; `#1`–`#9` refer to the last listing (`~/.trialm/last.json`).

### Screens (inline)

**First run.**
```
 triaLM  feel a local model in your coding tool before you download it
 ✓ Apple M5 · 10-core GPU · 24 GB unified · 16 GB for models
 ✓ Coding tools   Codex · Claude Code
 · Engines        none yet (setup installs one)
 Connect OpenRouter: trials run the real model in the cloud, paced to this Mac
 ❯ enter sign in with browser    p paste a key    s skip (browse only)
```

**Needs form.** Enter accepts every default.
```
 What do you need?                              ↑↓ row  ←→ change  enter go
 ❯ Tool          ‹ Codex ›              found: Codex, Claude Code
   Model         ‹ any ›                any · a family · a specific model (/)
   Priority      ‹ balanced ›           speed · balanced · quality · long context
   Context       ‹ 64k native ›         64k native · 64k via YaRN ×2 · 512k via YaRN ×2 (Qwen3.5/3.6)
   Quality       ‹ 3-bit ok ›           3-bit ok · 4-bit+ · 8-bit
   Engines       ‹ stable + new ›       stable · stable + new · installed only
   Speed boosts  ‹ when they help ›     off · when they help · everything
   Trial         ‹ this repo · no-logging providers · $2 ›    playground · ZDR only · budget
```

**Recipes.** The selection is cyan, `3-bit` and `new` are yellow, `exact` is green, and cost is magenta. Row 1 is the [R] pick; the other rows are illustrative.
```
 Recipes for Codex on this Mac    balanced · 64k · simulated, ±60% writing / ±70% reading until checked
     MODEL             FILE · ENGINE                 WRITES   TURN   FIRST
 ❯ 1 Qwen3.6 35B-A3B   UD-IQ3_XXS 13.2 GB · llama.cpp  ~40 t/s  ~11 s  ~10 s  3-bit
   2 gpt-oss 20B       MXFP4 12.1 GB · llama.cpp       ~45 t/s  ~13 s  ~11 s  exact trial
   3 Qwen3.6 35B-A3B   IQ4_XS 17.7 GB · llama.cpp      ~30 t/s  ~13 s  ~11 s  password per restart
 Claims we can't simulate yet
   · Qwen3.6 35B-A3B   Splash 1.0 · DFlash2    claim 175 t/s on M5 Pro 20c 48 GB, UD-Q4_K_M (README)
 trial: real model on OpenRouter (no-logging), paced to this Mac · ≈ $0.10–0.40 per 30 min · cap $2
 enter try  s set up  d details  w window  r needs  / search  ? why  q quit
```

**Details** (`d`). Each technique carries a verdict and its change in task time.
```
 Qwen3.6 35B-A3B · UD-IQ3_XXS · llama.cpp            qwen3.6-35b-a3b/iq3_xxs@llamacpp
 Memory   ██████████████░░ 14.2 of 16 GB     Turn ~11 s: reading 57% · writing 30% · fixed 13%
 ✓ flash attention, 1 slot     on: keeps Codex's prompt cached
 · 8-bit KV                    not needed: 64k fits at 16-bit
 · YaRN                        not needed: native 256K ≥ 64k
 · MTP                         not worth it: A3B MoE on Metal ≈0.93× (1 point), task −0%
 ? n-gram drafting             measured after your trial (0 GB)          space toggles
```

**After the trial.**
```
 Trial over · Qwen3.6 35B-A3B IQ3_XXS · llama.cpp on this Mac · 23 min · $0.21 of $2
   38 requests · your Mac ~14 min · cloud 3 min 10 s · 2 moments were cloud-bound
   ██████████████░░░░░░░░  reading 44% · writing 35% · tools 21%
   first reply ~10 s · typical turn ~9.8 s · slowest 41 s (#17: cache miss after compaction)
   answers: deepinfra fp8 (declared), no-logging · vs your 3-bit file · the trial is an upper bound on quality
   n-gram drafting replayed on this transcript: 31% accepted → ~6% faster, not worth it
 How did it feel?  1 too slow  2 fine  3 great     s set up (13.2 GB)  t try another
```

**Setup.** One approval, cargo-style progress, then Ready.
```
 Set up Qwen3.6 35B-A3B for Codex                 ≈ 6 min · undo: trialm setup --undo
   install   llama.cpp b11502 (ggml-org release)  31 MB · sha256 pinned
   download  Qwen3.6-35B-A3B-UD-IQ3_XXS.gguf      13.2 GB · sha256 from Hugging Face
   write     ~/.trialm/setups/qwen3.6-35b-a3b…/run.sh
   start     llama-server :8080 · bridge in-process (Codex needs Responses)
   check     speed at 3 depths · Codex greeting / tool call / follow-up
   Not touched: Codex config, shell profile, other models. No admin rights.   Go? [Y/n]
 ✓ llama.cpp b11502                                             4 s
   Downloading ━━━━━━━━━━━━━━━━━━━━╸━━━━━━━━━  8.1/13.2 GB  47 MB/s  1:49   ctrl-c pauses
```
```
 ✓ Ready · Apple M5                  predicted   measured
   writing @ 2K                      ~44 t/s     39.8 t/s   −10%
   writing @ 40K                     ~36 t/s     31.2 t/s   −13%
   reading                           ~800 t/s    655 t/s    −18%
   Codex  greeting ✓  tool call ✓  follow-up ✓        calibrated this Mac · re-ranked
 enter open Codex now        later: trialm launch
```

**Returning user.**
```
 triaLM  Apple M5 · 24 GB · Codex, Claude Code · OpenRouter $9.50     v0.6.1 available · trialm update
 ● Qwen3.6 35B-A3B · llama.cpp · ready (stopped)
 ❯ Launch Codex with it       enter
   Find or try another        f
   Trials: Nov 6 Qwen3.6 ★★★ $0.21 · gpt-oss 20B ★★ $0.06
```

### Charm components

bubbletea v2.1.1, lipgloss v2.0.6, bubbles v2.2.1, huh v2.0.3 (`charm.land/*`).
- **One long-lived root `tea.Model`** with a screen stack, inline, no alt screen; chained huh/tea programs are banned (each re-queries the terminal and loses keystrokes [R]). **huh** fields as sub-models (`Select(...).Inline(true)`, `Confirm` for the one Y/n, `Input` + `EchoPassword` for a key).
- **bubbles:** `spinner`, `progress`, `table`, `textinput`, `viewport` (log tail, meter, chat), `help`/`key`. **lipgloss:** style tokens, `Join`, `lipgloss/table` for plain mode. **`tea.ExecProcess`** hands the TTY to the harness and resumes for the card; engine and server output goes to `~/.trialm/logs/` plus the viewport, never the TTY.
- **Not used:** glamour (+12.3 MB [R]), wish, fang, cobra; flags stay on stdlib `flag`.

### Keyboard map

| Where | Keys |
|---|---|
| Everywhere | `↑↓`/`jk` move · `←→`/`hl` change · `enter` primary · `esc` back · `q` quit · `?` why/derivation · `/` search all models · `ctrl-c` restores the terminal (during setup it pauses at the next step boundary and keeps partial downloads) |
| Recipe table | `enter` try · `s` set up · `d` details · `w` try in a new window · `r` needs · paste = claim card |
| Details | `space` toggles a technique (re-simulated live) · `enter` try · `s` set up |
| Claim card | `enter` try this config · `t` try the better pick · `s` set up · `?` sources and formulas |
| Trial card | `1 2 3` rate · `s` set up · `t` try another |
| Ready | `enter` launch · `t` tune (offered on a miss) · `u` undo |

The footer shows at most 6 keys, and only the ones that work on the current screen.

### Colour and typography rules

- **Six semantic roles,** never a background query (`HasDarkBackground` blocks stdin up to 2 s and loses keys [R]): cyan = interactive/selected, green = good/fits/measured ✓, yellow = caveat (3-bit, tight, `new`, `fit`), red = blocked, magenta = cloud and money, dim = secondary. On ANSI-16 the terminal theme supplies them; on 256-colour or truecolor profiles a tuned palette that reads on light and dark replaces them, plus two identity touches: the `triaLM` wordmark (`tria` in the accent, `LM` bold) and gradient fills on progress and time-split bars. Identity never carries meaning.
- **Provenance is never colour alone.** Simulated numbers are plain (`~` above a 25% band, `fit` when in-sample); claims carry the word `claim` and appear only in the claims lane, claim card, gallery and a ranked row's separate `CLAIM` column; measured numbers only under a `measured` header; JSON always carries the kind.
- **Precision follows the band:** ≤ 10% → 3 significant figures, ≤ 25% → 2, above → `~` and 2, with the range on `?`. Never `36.4 tok/s` on a ±60% band.
- Bold for titles only; numbers right-aligned with units; glyphs `✓ ✗ ● · ❯ ━` with ASCII fallbacks (`v x * - > =`) on `TERM=dumb` and conhost; below 90 columns FIRST then WRITES collapse, below 60 rows become stacked cards; nothing wraps mid-cell.
- triaLM applies `NO_COLOR` itself (colorprofile v0.4.3 ignores `NO_COLOR=yes` [R]); precedence `NO_COLOR` > `CLICOLOR_FORCE`/`FORCE_COLOR` > non-TTY.

### Plain, JSON and non-TTY modes

- **Mode selection:** the TUI runs only when stdin and stdout are TTYs and none of `--json`, `--plain`, `CI`, `TRIALM_NO_INPUT` is set. Plain mode has the same content, no prompts and 0 escape bytes (verified in the demo [R]); a command that needs input exits 3 and names the flag that would supply it.
- **`--json`:** NDJSON on stdout, human text on stderr: `{"v":1,"type":"detect|recipes|plan|step|progress|check|needs_user|result|error",...}`. Every number is a Figure, `{"v":40,"lo":16,"hi":64,"unit":"tok/s","kind":"simulated","src":"bands:apple-m5/llamacpp@2026-11-06"}`. Every `result` carries `next:[{cmd,why}]`, so agents need no prompt document.
- **`--yes=<plan_id>`** executes only the plan whose hash `--dry-run` printed and refuses a changed one; bare `--yes` is for scripts.
- **Exit codes:** 0 ok · 1 error · 2 usage · 3 needs the user (key, sudo, a choice, a terminal) · 4 not ready · 130 interrupted.

### First run: the OpenRouter key, asked early

Right after detection, because provider pinning and trial cost need `/endpoints`. In order: (1) `OPENROUTER_API_KEY` in env, 0 keys; (2) OAuth PKCE (S256) with a callback on a random localhost port (documented to work on any port), code exchanged at `POST /api/v1/auth/keys`: 1 key + 1 click; (3) headless (`SSH_CONNECTION` or no display): the copy-a-code mode into a masked input; (4) `p` for a masked paste. **Balance** comes from `GET /api/v1/credits` (total minus usage); `GET /api/v1/key` only has the key's own limit, null for an unlimited OAuth key. Both, and headless mode, are confirmed in P1a's first live call. The key lives in `~/.trialm/credentials` (0600), is never printed or put in a transcript, and the browser round trip overlaps the catalog refresh. Skipping is allowed: only `try` asks again. The budget is enforced client-side (plus a per-key limit if `auth/keys` accepts one). Attribution: the consent page says `localhost:PORT`, and API calls send `X-Title: triaLM` and `HTTP-Referer: https://github.com/aeroknight786/trialm`; no hosted callback before v1.

### Launch-into-trial behaviour

Enter on any recipe starts a trial with no further questions. The trial server runs in-process on the next free port from 11535 (clear of Ollama's 11434 and hipfire's 11435). The harness runs as a child on the same TTY in the user's directory, wired only by env vars, flags and **triaLM-owned files** under `~/.trialm/harness/` (§7); user tool configs are never edited, and `CLAUDECODE`/`CLAUDE_CODE_ENTRYPOINT` are always unset. IDE tools get `try --serve`: the server in the foreground with a meter, plus a settings snippet. **Window mode** (`w`, `--window`) is automatic when `CLAUDECODE`, `CODEX_SANDBOX` or `TRIALM_AGENT` is set or stdin is not a TTY. A generated `~/.trialm/run/<id>.sh|.ps1` (0700) opens with one of four openers: `open <id>.command` on macOS (LaunchServices, no AppleScript, so no Automation/TCC prompt is expected; P1b verifies and counts), `wt -w 0 nt -d` on Windows, tmux `new-window`, or `$TERMINAL`/`x-terminal-emulator`. Anything else, including a blocking sandbox, exits 3 with the command to run; over SSH or in VS Code it runs in place. The original terminal shows a 4-line meter (request, cache, wait, spend). `trialm try` with no argument skips home; `--as "RTX 4090"` feels hardware you do not own. With `use=chat`, the trial is a minimal inline chat screen (≤ 250 lines) instead of a harness.

## 4. Requirements → recipes

### Requirements model

The requirements live in `~/.trialm/prefs.json`, and every field is also a flag.

| Field | Default | What it changes |
|---|---|---|
| `tool` | Detected: parent env (`CLAUDECODE`, `CODEX_*`), then the most recently used installed binary | Scenario (measured base prompts, §7), protocol, minimum context, slot needs (title or commit side requests), wiring |
| `use` | `agent` (or `chat`) | `agent` hard-filters on tool calling: the endpoint's `supported_parameters` plus a tools-capable template. `chat` trials in the built-in chat screen and launches the engine's own web UI (llama-server `/`, LM Studio, Ollama) |
| `model` | `any` | A family (`qwen3.6`) or one model (`/`, `--model`) restricts the cross step |
| `priority` | `balanced` | Which point of the Pareto front is #1 (below) |
| `context` | max(tool minimum, base prompt + 32k), rounded up to 16k: Codex and OpenCode 64k (their current minimums); Claude Code, Qwen Code, Cline, Continue and Aider 48k | KV memory, decode at depth, and the depth the numbers are quoted at |
| `context_via` | `native`. The form toggle `via YaRN ×F` appears whenever a candidate model takes opt-in YaRN | Lets the owner deliberately try YaRN (§5), including on the default Qwen3.6 pick past 256K. Its cost and caveat are shown on the row |
| `quality_floor` | `3bit-ok` when the GPU budget is ≤ 24 GB, else `4bit+` | Quants enumerated. Below 3 bpw needs `boosts=everything` plus a tool-call smoke test |
| `max_download_gb` | min(free disk − 20 GB, 100 GB) | A filter. Download size also breaks ties |
| `engines` | `stable+new` | Candidate engines. `installed` prefers what is already there when it is within 5% of the best |
| `boosts` | `auto` | `off` = stock settings. `auto` = only techniques that change fit or save ≥ 5% of task time. `everything` = adds risky options (KV q4, static YaRN at ×4, 2-bit, Strata tail-skip) |
| `privacy` | `repo`, no-logging providers | Trials use only endpoints with OpenRouter `data_collection: "deny"`; `--zdr` restricts to zero-data-retention endpoints; `playground` (fixed task in `~/.trialm/playground`) or `predict-only` |
| `budget_usd` | 2 | Hard cap per trial |
| `hardware` | Detected | What-if via `--as`, `--ram`, `--vram` |
| Floors (flags only) | none | `--min-tps`, `--max-turn`, `--min-bits` |

A "1 too slow" rating moves `priority` one step toward speed and re-ranks immediately.

### Recipe model

Recipes are JSON, and they are the unit of setup, launch and claims.

```json
{ "id": "qwen3.8-flash-next/iq3_ks@avalon+mtp",
  "engine": {"id": "avalon", "parent": "llamacpp", "version": "2026.11.01", "commit": "…", "sha256": "…"},
  "model": {"hf": "Qwen/Qwen3.8-Flash-Next", "openrouter": ["qwen/qwen3.8-flash"],
            "files": [{"repo": "freriadoc/…-GGUF", "glob": "…IQ3KS-*-of-00033.gguf", "bytes": 94850000000, "role": "weights"},
                      {"path": "MTP/mtp-….gguf", "bytes": 1480000000, "role": "mtp"},
                      {"path": "moe-cache-ranking.bin", "role": "ranking"}]},
  "techniques": [{"id": "expert-cache", "params": {"reserve_mib": 400}},
                 {"id": "spec", "params": {"method": "mtp", "k": 2}},
                 {"id": "kv-quant", "params": {"k": "q8_0", "v": "q8_0"}},
                 {"id": "ubatch", "params": {"n": 9536}}],
  "settings": {"ctx": 262144, "slots": 1},
  "gates": {"os": ["linux"], "gpu_arch": ["gfx1100","gfx1101","gfx1102","gfx1200","gfx1201","sm_89"], "cpu": ["avx2"],
            "vram_gb_min": 19, "ram_gb_min": 32, "ram_rule": "rocm-pinned", "ram_gb_full": 64, "disk_gb": 98},
  "trial": {"openrouter": "qwen/qwen3.8-flash", "fidelity": "family"},
  "quality": [{"metric": "ppl-en", "this": 1.443, "ref": "UD-Q4_K_XL", "ref_value": 1.316, "src": "avalon README"}],
  "claims": ["avalon/7900xt/short@2026-11-01", "avalon/7900xt/250k@2026-11-01"],
  "preset": {"grade": "V", "sim": "fit", "measured_by": null},
  "risk": {"maturity": "experimental", "maintainers": 1, "ai_written_kernels": true},
  "provenance": "curated" }
```

`vram_gb_min: 19` is the research figure for 256K + MTP (16–18 GB cards get a 128K or no-MTP variant); `ram_rule: rocm-pinned` applies the §5 limit instead of the author's 16 GB. Harness wiring comes from `harnesses.json` at trial or launch time, so one recipe serves every tool. Recipes come from curated data, the enumerator, or a parsed command line, and all go through the same evaluator.

**Preset gate.** A curated recipe ships as a preset only when its source is V or C with full conditions, the simulator reproduces it on the claimant's hardware (≥ roughly), and anything below V has also been verified on matching hardware (owner, rented or volunteer). The snippet-graded (S) fast-config recipes stay in `claims.json` until measured.

### Enumeration and ranking

1. **Gate** on `use`, `model`, context (native, or YaRN when allowed), licence, download cap, hardware gates and platform hazards (§5).
2. **Cross** models × HF files (MTP, draft and mmproj sidecars attached) × engines whose `supports` matrix covers the arch, backend and format.
3. **Prune by dominance:** offload only when the model does not fit; spec only when a draft asset or MTP weights exist; KV quant only when it changes fit or context.
4. **Optimise continuous knobs** rather than enumerate them: the smallest `n_cpu_moe` that fits, `ub` ∈ {512…8192} for task time, spec depth k ∈ {1,2,3}. About 10k closed-form evaluations in under 100 ms.
5. **Predict** first reply, turn and 40-call task time for the harness scenario, each a Figure.
6. **Fit gate** with a reserve of max(400 MiB, 6% of VRAM clamped to 1–3 GiB); Strata fell from 102 to 13.7 tok/s at 0 MiB free [R].
7. **Pareto front** on task time ↓, quality tier ↑, risk ↓, with fit margin and download size as tie-breakers. `priority` picks #1: speed = minimum task time above the quality floor; balanced = best quality with turn ≤ the tool's usable threshold; quality = best quality with turn ≤ 2× that; long = maximum context, then time.
8. **Uncertainty-aware ties.** On one machine the errors are correlated (all candidates share the device efficiency), so A beats B on speed only when their task-time ratio differs by more than the **paired** band r_AB; otherwise the next axis decides. Priors: r = 10% for the same engine and backend, 25% across engine families, replaced by held-out paired residuals in `bands.json` once 5+ pairs exist. √(band_A² + band_B²) is used only across devices (what-if).
9. **Guards:** an experimental engine ranks #1 only with ≥ 25% better task time or a higher quality tier; ik_llama.cpp never ranks #1 (§5); at most 2 rows per model, at least 1 stable engine, and at least 1 `exact`-trial row when one exists.
10. **Claims never rank.** Ranking reads only simulated, calibrated or measured figures. A ranked row may show a matching claim in a separate `CLAIM` column, always labelled; a figure whose efficiency was fitted to that same claim is shown as `fit` and keeps the prior band. Engines we cannot simulate sit in the claims lane.

The **quality proxy** is a coarse tier labelled "proxy" and shown without decimals: the model tier (OpenRouter coding benchmarks or the Artificial Analysis index, else parameter count) minus a quant penalty from published ppl/KLD deltas in `quality.json` (for example IQ3_KS +9.7% ppl vs UD-Q4_K_XL [R]), minus a penalty for speed tricks with a known quality cost (Strata's STRATA_ROUTE_PRIOR scored 4.5 vs 5.6 of 10 agent tasks [R]; tail-skip; KV q4_0). A trick with no published cost carries a `quality unmeasured` badge instead of a number.

### Claims: "claimed vs simulated for your machine"

1. **Input:** a llama-server/llama-cli line, an ik_llama.cpp line (`-fmoe -rtr -ser -amb -ot`), an Ollama Modelfile, a `strata launch`, `vllm serve` or `mlx_lm.server` line, LM Studio `lms load` flags, a KTransformers command (conditions only), or a URL. URLs are fetched from the user's machine (raw GitHub; Reddit `.json` as best effort), falling back to "paste the post text", because Reddit was unreachable in research and its unauthenticated JSON is rate-limited [R].
2. **Parse** into a Recipe through the version-aware flag table (`--draft-max`→`--spec-draft-n-max`, `--no-mmap`→`-lm none`, bare `-fa`→`-fa on`).
3. **Flag audit, always shown:** each flag is kept, translated (old → new), not applicable here, or dropped (unknown or unsupported), and the card says "simulated and set up without: …". **Nothing pasted is executed:** setup fills the engine's embedded argv template only with typed values (numbers, enums, catalog file names) from kept flags. A user who wants a dropped flag adds it to the setup's `extra.args` themselves; triaLM never fills that file.
4. **Extract conditions** (hardware, quant, engine version, depth, output length, MTP, greedy or sampled, single-stream or aggregate). Each missing field becomes an assumption shown on the card ("RAM unknown → assumed DDR4-3200 2ch").
5. **Simulate on the claimant's hardware and workload;** r = sim/claimed − 1. |r| ≤ 15% → **reproduced**, ≤ 30% → **roughly**, otherwise **not reproduced**, naming the likely missing factor (depth 0, a peak, aggregate throughput, VRAM overcommit, an old build).
6. **Re-simulate on the user's machine** at their harness workload, with the delta each flag buys and the bottleneck.
7. **Grade and store:** V = engine docs with full conditions, C = community with full conditions, S = snippet or secondary blog (unverified). Only V/C claims with full conditions become fixtures.

**Where real claims come from.** Pastes, plus `claims.json`, curated monthly (about 1 h) with `tools/claims-harvest` run by the owner from an open network: Reddit's official API via a read-only app the owner registers (never scraping), engine READMEs and GitHub discussions. Candidates arrive as a PR with the parsed recipe, flag audit and reproduction grade; the owner assigns V/C/S. **Gallery:** `trialm find --claims` and a home row "Popular configs for your hardware (n)" list claims for the same GPU (or VRAM within 25% on the same backend), each with its simulated-for-you figure and grade. **Set up and measure** (`s`) works once an engine has an adapter (Strata, and Splash if the P2 spike passes, from P3; the claims-lane engines from P5); before that the card shows the engine's manual steps, displayed and never run. Once measured here, a recipe ranks normally.

```
 Claim  llama-server -m gpt-oss-120b-F16.gguf -ngl 999 --n-cpu-moe 26 -b 4096 -ub 4096 -fa on
        r/LocalLLaMA, May 2026 · RTX 3090 · RAM unknown → assumed DDR4-3200 2ch · source: snippet (S)
 flags  8 kept · 0 translated · 0 dropped
                      CLAIMED        SIMULATED (their 3090)    SIMULATED (yours, Claude Code)
 prefill (ub 4096)    1,682 t/s      1,580  −6%  reproduced    ~1,600
 writing              32 t/s         ~29   −9%  reproduced     ~30 (128 GB DDR4, 4 ch)
 follow-up turn       not stated     ≥ 1.5 s PCIe floor: 44 GB of experts cross per prompt ≥ 32 tokens
 why fast             experts in RAM → writing is RAM-bound · ub 512→4096: prefill 380 → 1,682, writing unchanged
 better for you       Qwen3.6 35B-A3B Q4_K_M fully on GPU · ~3 s/turn (blog claim 140 t/s, unverified)
 enter try this config   t try the better one   s set up   ? sources and formulas
```

## 5. Engines & techniques

### Engine tiers

| Tier | Engines | Why |
|---|---|---|
| **Core** (composed adapter: triaLM installs, launches, verifies, calibrates; CI e2e) | **llama.cpp** mainline (pinned ggml-org assets; Homebrew's only if already installed), with **buun** as a variant (`parent: llamacpp`) · **Ollama** · **LM Studio** · **mlx-lm** | llama.cpp has every hybrid knob plus `--moe-cache-mib`, MTP/DFlash/DSpark/EAGLE3 and Anthropic/Responses endpoints [R]; Ollama and LM Studio are the installed base (models recognised by sha); mlx-lm decodes about 8–15% faster on Macs [R] |
| **Opt-in variant** (via the llamacpp parent; contract tests only; never #1) | **ik_llama.cpp**, gated to MoE ≥ 200B on CUDA or CPU with ≥ 128 GB RAM | Contradictory hybrid-prefill data; no ROCm, Vulkan, Metal or `/v1/messages`. Promoted only after a controlled sweep (P5) |
| **Delegated** (the engine's own installer and launcher, pinned commit + sha; triaLM plans, runs, verifies, calibrates; contract tests on recorded output) | **Strata** (P3; a ~150-line plan-JSON plugin; CUDA and ROCm; macOS settled in P0b) · **Splash** (P3, if the P2 M5 spike shows ≥ 1.5× llama.cpp) · **Avalon** (P5; Linux ROCm/CUDA; installer, 33 shards, MTP head, ranking file; launches as llama-server, so launch/metrics/verify come from the parent) · **Lemonade** (P5; AMD/Strix Halo; can host Avalon) | Owner request #7. Strata and Avalon publish complete-condition numbers; Strata exposes `--plan/--yes/connect --check/--output json` and `/metrics`. Splash is the biggest niche win on the owner's Mac |
| **Claims lane** (recipe data for set-up-and-measure; ranked once measured here) | hipfire, Lucebox, ds4, KTransformers, ExLlamaV3/TabbyAPI, vLLM, SGLang, HyperQwen (Splash until P3) | Gains come from trained drafters, model-specific kernels or non-GGUF formats we cannot simulate yet; mostly self-reported peaks |
| **Later** | Lamina, NInfer, BeeLlama, strix-llama.cpp (via Lemonade), ROCmFPX, mistral.rs, exo, strata-mlx, FastFlowLM, OpenVINO | Days old, one hardware target, or niche |
| **No** | TensorRT-LLM, LMDeploy, MLC, PowerInfer, llamafile, IPEX-LLM (archived, security issues), ANE private-API engines, Sonar; Flash-MoE only as an SSD-tier fixture | Datacenter-first, stale or unsafe [R] |

Engine rules, stored as data: no Ollama for MoE offload (9.8 vs 22 tok/s [R]); vLLM, SGLang and ExLlamaV3 only with a full VRAM fit on NVIDIA or ROCm; detection probes `/v1/models` plus HEAD on `/v1/messages` and `/v1/responses`, because 8080 and 8000 are each shared by several engines [R]; the in-process bridge serves tools whose protocol the engine lacks (TabbyAPI, ik_llama.cpp, hipfire, mlx-lm, FastFlowLM, HyperQwen, Splash if needed).

**Platform hazards** (gates and warnings, stored as data):
- **Windows + NVIDIA:** the fit gate never relies on sysmem fallback, and verify flags a spill (Journey E); WDDM pinned-memory failures retry with `GGML_CUDA_NO_PINNED=1`.
- **ROCm pinned RAM:** CPU-resident pinned bytes ≤ RAM − RAM/64 − 1.5 GiB, the limit that crashed Avalon on 16–32 GB machines [R]. A gate.
- **Strix Halo:** GPU budget = UMA carve-out + GTT limit (`amdgpu.gttsize`/`ttm.pages_limit`), read at detection; raising them is an admin instruction that stops the plan.
- **mmap thrash:** CPU-resident weights above free RAM − 4 GB are blocked unless the engine has an SSD tier (then priced as SSD reads).
- **η_arch** (engine, arch, version range): Gated DeltaNet hybrids (Qwen3-Next, Coder-Next, Flash-Next) run far below their active size in llama.cpp (11.8 vs 63 tok/s, issue #17936 [R]); an upstream fix lifts it by version.

### Techniques: simulation, flags, exposure

D = always-read bytes per token, X = routed-expert bytes per token, KV(d) = KV bytes read at depth d, BW = bandwidth, η = η_engine,backend × η_arch (fitted efficiency), t0 = fixed time per token, L/E/a = layers, experts, active experts.

| Technique | Simulation model | Flags (llama.cpp first) | Exposure |
|---|---|---|---|
| **Partial offload** (dense) | `t = W_gpu/(η_g·BW_g) + W_cpu/(η_c·BW_ram) + t0`, serial; plus the PCIe prefill term below | `-ngl N` / `--fit` | Fallback only, with a "spills to RAM" badge |
| **MoE expert offload** | Decode: `t = (D + X·(L−N)/L + KV(d))/(η_g·BW_g) + X·N/L/(η_c·BW_ram) + t0` (sequential in llama.cpp). Prefill per ubatch: `W_cpu/(η_p·B_pcie) + ub·t_gpu` when ub ≥ 32 (`GGML_OP_OFFLOAD_MIN_BATCH`), so each follow-up has a floor of `W_cpu/B_pcie`. VRAM: `nonexpert + (L−N)·E_layer + KV(ctx) + buf(ub) + reserve`. It fits the gpt-oss-120b/3090 `-ub` sweep (380/1,113/1,682/2,091) within −16..+2% and decode within about 15% [R], **in-sample** (η_p and r0 came from that sweep); the P3 gate is a held-out sweep | `-ngl 999 --n-cpu-moe N` / `-ot exps=CPU`; ik `-fmoe`; `GGML_OP_OFFLOAD_MIN_BATCH` to keep small tool results on the CPU | Automatic when the model does not fit. Shown as "experts of N layers in RAM · ≥ 1.5 s per follow-up". N and ub are solved. Which end `--n-cpu-moe` counts layers from (an open question [R]) is probed for each version |
| **Expert cache** (Strata, Avalon, buun, `--moe-cache-mib`) | Slots `S = (VRAM − D_res − KV(ctx) − MTP − buf − reserve)/s_e`. `h(S)` is an **assumed** curve through the only 2 published points (0.50 static, 0.72 adaptive at 4,500 of 24,576 slots [R]). Experts touched per window `U(n) = E·(1 − (1 − a/E)^n)`; misses `m = (1−h)·X_win`; PCIe share `p = L_link/(L_link + k·R)` (Strata); CPU rate `R = min(η_c·BW_ram, cores·r_core(quant))` (Q2_0 with AVX-512 about 42 GB/s; i-quants about 5 GB/s per core [R]). `t_win = (D + KV(d))/(η_g·BW_g) + t_hit + t_miss − ω·min(t_hit, t_miss) + t_draft + t_fixed`, with `t_hit = h·X_win/(η_g·BW_g)` and `t_miss = max((1−p)·m/R, p·m/(η_p·L_link))`, plus an SSD tier when RAM is short; tok/s = τ/t_win. **ω per engine:** Strata ≈ 0, because its Table 5 is additive (14.6 GPU + 13.4 CPU + 2.0 drafting + 4.3 other = 34.3 ms for 3.23 tokens, about 94 tok/s [R]). Avalon and `--moe-cache-mib` each get a fitted ω (`fit`: 1–2 devices per engine). Sanity gate: flag any prediction above 50% of the bandwidth bound (Strata runs at 35–40% [R]) | Engine-native (Strata `--expert-cache auto`, `--vram-reserve-mib`; Avalon's ranking file and 400 MiB reserve); llama.cpp `--moe-cache-mib M` | A property of the recipe, shown in the "where time goes" bar. The hit rate is labelled `assumed (2 published points)` until check reads the real one. Context visibly steals cache: on 12 GB, 94.6 → 56.3 tok/s at 262K [R] |
| **KV quant** | KV × 8.5/16 (q8_0) or × 4.5/16 (q4_0), in memory and in KV(d) reads. Metal at depth: −13..−17% (a claim, applied as a wider band) | `-ctk/-ctv q8_0` (needs `-fa on`); Strata `--kv`; `OLLAMA_KV_CACHE_TYPE`; mlx `--kv-bits 8` (disables batching) | Only when it buys fit or context. q4_0 is `everything`-only and carries a quality penalty (§4) |
| **Flash attention** | Removes the O(ctx·ub) score buffer; better prefill at depth | `-fa on`; `OLLAMA_FLASH_ATTENTION=1` | Always on where supported. Not a choice |
| **ubatch** | MoE efficiency `r/(r + r0)` with `r = ub·a/E` rows per expert and r0 fitted per backend; compute buffer ∝ ub; amortises PCIe in hybrid mode | `-b U -ub U` (Avalon 9536) | Solved. Explained per flag: "ub 4096: prefill 380 → 1,682, writing unchanged" |
| **Prompt cache** (hybrid/SWA) | Reuse limited by SWA/recurrent rollback; checkpoints restore it | `--cache-ram M`, `-ctxcp N` sized from the layer layout and the harness's prefix churn | Set automatically in hybrid/SWA recipes |
| **Spec decoding** (MTP, draft model, n-gram, DFlash/EAGLE3) | `τ = (1 − α^(k+1))/(1 − α)`, `S = τ/(1 + k·o)`. `o` = draft bytes + draft KV + `verify(k+1) − verify(1)`, with `verify(n) = (D + s_e·L·U(n) + KV(d))/(η·BW) + n·2P/F_eff`; check measures it with `llama-bench -p 1,2,3,4,8 -n 0 -d <depth>`. Fixtures: o ≈ 0.3 for a dense 27B on a 3090 (1.67× at k=2); o ≈ 0.55 for an A3B MoE on CUDA (1.17×); **Metal is decided per model**: 0.93× for an A3B MoE (one data point), while a dense 27B goes 16 → 27 tok/s on an M5 Pro and 17 → 20 on an M3 Max (Splash README) [R]. Task saving = `f_decode·(1 − 1/S) − p·f_prefill`, with the MTP prefill penalty p as a version-gated prior of 10% (range 2–25%; the research is split) that check measures. α priors: code and tool JSON 0.85–0.9 (unmeasured at agent depth, so wide band), prose 0.75, draft model 0.6–0.75, offload-engine MTP 0.55–0.65 on prose and up to 0.95 on code [R]; check measures α per content type. **N-gram α is computed exactly** by replaying the user's trial tokens through `internal/tok`. Memory: MTP layer 0.3–1.5 GB, plus its KV and buffer. The pacer emits bursts of 1 + Geom(α, k) | `--spec-type draft-mtp --spec-draft-n-max 2` (build ≥ 2026-05-16 plus an MTP GGUF or sidecar), `draft-dflash -md {asset}`, `ngram-mod --spec-ngram-mod-n-match 24 --spec-ngram-mod-n-min 48 --spec-ngram-mod-n-max 64`; vLLM `--speculative-config`; Ollama `draft_num_predict`; Strata `--mtp on --drafting auto`; Avalon `-md MTP/… --spec-type draft-mtp`; mlx-lm draft model only (it strips `mtp.*`) | One "Speed boost" row (off · when it helps · everything). The verdict is given as task time, never as a tok/s headline |
| **YaRN** | `max_ctx = F·C0`. The flag itself is free; the cost is the extra context (KV memory, which steals cache slots, decode at depth, attention FLOPs). Quality: "small, unmeasured short-prompt loss", never a number | `--rope-scaling yarn --rope-scale F --yarn-orig-ctx C0 -c F·C0`; vLLM `--hf-overrides '{"rope_parameters":{…}}'`; SGLang `--json-model-override-args`; MLX `config.json` patch; Ollama unsupported | Automatic when needed, plus the explicit needs-form toggle |
| **Wired limit** (Mac) | A target transform: GPU budget = chosen MB, keeping ≥ 4 GB (24 GB Macs) or ≥ 8 GB (≥ 64 GB Macs) for macOS. Capacity only. The real default is read from llama.cpp's `recommendedMaxWorkingSetSize` log line at check time | `sudo sysctl iogpu.wired_limit_mb=N` (resets on reboot) | Shown only when it unlocks a recipe ≥ 15% better or a quality tier up. Its own plan row, through the OS password prompt; `launch` re-applies it after a reboot with 1 more prompt. The row states both costs |
| **Multi-GPU** | Layer split: summed VRAM minus overhead per GPU, `t = Σ B_i/(η·BW_i) + hops·t_hop`. Per-card bandwidth and topology come from `nvidia-smi` / `rocm-smi` / `topo -m` | `-sm layer -ts a,b` | Automatic, with a wider band. Tensor parallel (vLLM, exl3) is claims-lane only. Expert tiers (Lamina style) come later |

**Is MTP or speculative decoding necessary? No.** It is an optional boost the simulator decides per model, machine and harness, shown as a task-time change; `auto` turns it on when the task saves ≥ 5% and S ≥ 1.05.
- **Expert-cache engines:** on by default, +40–80% (Avalon 66.1 → 98.4 tok/s, claimed [R]).
- **Dense models fully on a discrete GPU:** usually on; about 20–35% of task time saved on NVIDIA, where decode is 55–84% of a Claude Code turn [R].
- **A3B MoEs fully on the GPU:** case by case; savings of about 4–11% [R] sit near the threshold, and the slot question below often tips them off.
- **Mac llama.cpp: per model.** The A3B example loses (0.93×, one point); a dense 27B gains (16 → 27 tok/s on an M5 Pro [R]); the P0b bench measures o on the M5.
- **Parallel slots:** when the harness sends side requests (Claude Code session titles, Aider commit messages), the simulator weighs MTP's `-np 1` limit (unverified on current builds; probed at setup) against a second slot.
- **N-gram drafting** costs 0 GB and is suggested only when the transcript replay shows ≥ 5% saved. MTP needs its weights to actually exist, not just a `num_nextn_predict_layers` field: MiniMax-M2, Qwen3-Coder-Next and Kimi K2 ship none [R].

**When is YaRN offered?**
- **Automatically**, only when the tool's context need exceeds the native window *and* the model takes YaRN as an opt-in: Qwen2.5-Coder, Qwen3 dense and the original Qwen3-30B-A3B (32K native), MiMo-7B, Ling-flash-2.0 [R]. Factor = need ÷ native rounded up to 2 or 4 (Qwen3: ×2 for 64K, ×4 for 128K).
- **Explicitly**, through the needs-form toggle ("I may want to try YaRN"): opt-in models may use YaRN even when a native-long alternative exists, including Qwen3.5/3.6 past 256K (validated to about 1M [R]) when memory allows, so the owner's default pick can try it. The row shows the cost (KV GB, writing at depth) and the caveat.
- **Under `priority=long`** for Flash-Next above 262K (×4, experimental in Strata). **Never** for baked-in models (gpt-oss, DeepSeek, Kimi, Devstral 2 / Small 2) [R].
- **In the trial**, the local limit is enforced (e.g. 40,960). Providers run native or dynamic YaRN, so the row says static-YaRN quality can only be felt after a local setup.

## 6. Catalog

- **Build time: OpenRouter is the spine, Hugging Face supplies the files.** A weekly CI job opens a PR with the diff, using an HF token with backoff (14 gated originals such as Llama and Gemma need it for `config.json`):
  1. `GET /api/v1/models` (no key), dropping `:free/:batch/:thinking`: 458 → 377 unique models, 155 with HF repos, plus 9 open-weight models from models.dev, plus `overrides.json` [R].
  2. GGUF discovery by HF lineage `base_model:quantized:<id>` **plus** an exact normalised-name match, an author allowlist (official, unsloth, bartowski, ggml-org, lmstudio-community, mradermacher) and finetune-marker rejection: 147/155 resolve, 89% strict auto-match. Top-by-downloads is wrong 30% of the time and is never used [R].
  3. Tree reads for sizes and sha256 (`lfs.oid`); `config.json` for E, a, shared experts, layer types, rope and nextn.
  4. GGUF header range reads (256 KiB sufficed for 156/158), opening every shard (`MergeSplits`). Each MoE quant gets its own read (sharing one header errs −22..+37%); dense quants may share one (within 1.3%) [R].
  5. Emit about 150 entries (about 0.5 MB, embedded gzipped), keyed by lowercase HF base repo with OpenRouter ids as aliases. Cost: about 6–8 GB and 15–25 min on 8 workers. **CI fails on any dead OpenRouter id**; 8 of today's 24 are dead [R]. A merged PR triggers a patch release.
- **Local-relevant scope today:** 44 tool-capable models ≤ 40B with GGUF (38 also MLX) plus 13 MoE models of 40–130B for offload [R]. MLX repos are recorded as `{Repo, Bits, Mode, Bytes}` (120/155 on mlx-community).
- **Runtime refresh:** OpenRouter models daily (about 80 KB gz, 24 h TTL; ETag support unknown); HF enrichment only for a model the user opens (1–2 header reads, cached at `~/.cache/trialm/hf/<repo>@<sha>.json`, stale only when the sha changes); `/endpoints` at trial start (4 KB, 1 h TTL; `:free`/`:batch` variants fetch their own). All async (§1).
- **Geometry with per-field provenance:** GGUF header > config.json > OpenRouter > curated. Known disagreements resolve by rule: 17 layer counts that include MTP layers, 30 KV-head and 27 head_dim mismatches from MLA, 11 context mismatches [R]. NextN blocks are excluded from per-token bytes unless MTP is on (which also fixes `summary.go` reporting 49 recurrent layers instead of 48).
- **Layout families, not per-arch plugins** (`catalog/arch`): about six data-driven layouts from config fields (dense GQA; SWA pattern; hybrid linear/recurrent from `layer_types`; MLA from `kv_lora_rank`; MoE experts; NextN/MTP) cover the ~23 hybrid/SWA and 29 MLA architectures, plus specials only for top-30 models they miss. DeepSeek-V4's compressed attention stays `geometry unverified`; anything unverified gets its band widened 10 points until check measures it.
- **File classes:** `FileTag` strips `-NNNNN-of-NNNNN` and folders (it returns "" for every split file today; 13/25 sampled repos have splits [R]). `mmproj-`, `mtp-`, `dflash-`, `eagle` and `imatrix` files are classified as **sidecars** first, never as quants.
- **Provider pinning** from `/endpoints`: (0) **data policy:** `data_collection: "deny"` by default, `zdr: true` with `--zdr`; if nothing qualifies, the trial asks once (playground, allow logging, or skip); (1) tools and `tool_choice` when the harness sends tools; (2) context ≥ the local context, but not YaRN-extended when the local setup is native (Qwen3-32B: the 40,960 endpoint); (3) the precision tier nearest the local quant (BF16/Q8 → bf16/fp16 then fp8; Q4–Q6 → fp8 then bf16; fp4/int4/unknown only as a last resort); (4) uptime, then price. Send `provider{only:[tag], data_collection:"deny", allow_fallbacks:false, require_parameters:true}`; whether `only` takes full endpoint tags is checked in P1a's first live call. **`exact` needs a matching pinned endpoint:** gpt-oss endpoints declare bf16, fp16, fp4, fp8, int4 and unknown, so `exact` means pinning fp4/mxfp4 [R]. Throughput and latency were null on all 851 endpoints without a key [R]; P1a re-checks with one, and even then they only predict cloud-bound moments, never pacing.
- **Trial cost** comes from the pinned endpoint, never the list price (spreads 1.88× median, 8.19× p90 [R]): `uncached×prompt + cached×cache_read + completion×completion (+ reasoning)`. Example: 2M mostly cached prompt tokens plus 60K output on qwen3.6-35b-a3b at deepinfra fp8 ≈ $0.26.
- **Staleness and id drift:** keyed by HF id, so an OpenRouter 404 re-resolves through it; `generated_at` is embedded and home warns after 30 days without an update; ids whose HF link may name sibling weights (`qwen/qwen3.8-flash` → Flash-Next) carry `fidelity: family`.
- **Unhosted models are deferred** (65% of popular local GGUF bases are not hosted [R]). v1: speed is always simulated exactly from the GGUF header (fine-tunes inherit base geometry); the trial uses the nearest hosted model by HF `base_model` lineage in the same family, architecture and size class, badged `stand-in · quality unknown`, else predict-only; the 4 HF ids that return 401 are cloud-only; "bring your own endpoint" comes later.

## 7. Try (trial)

- **Mechanics.** The in-process `server` serves OpenAI Chat, Responses, Anthropic and Ollama, backed by `sim.Device` paced by the recipe's `Timing` and by `upstream` pinned to one OpenRouter endpoint. The pacer already models load/reload, prefill at depth, cache slots with SWA/recurrent rollback limits, engine-exact truncation and queueing. New: spec bursts, the hybrid PCIe turn floor, a 20 s expert-cache warm-up, a cached-prefix TTFT per engine (Lamina 0.11–0.16 s vs llama.cpp 0.61–0.72 s [R]) and an `-np` queue. Inline, window or `--serve` (§3).
- **Honesty:** `--pace p90` lets the user feel the pessimistic edge of the band; moments where the cloud is slower than the simulated device are labelled `cloud-bound` and counted, so provider slowness is never blamed on the machine; the card's time-split bar (reading/writing/tools) shows a Mac user why MTP gains little.
- **Fidelity labels** on the pre-trial line and the card: `exact` (same weights, pinned declared precision); `≈ sharper` (cloud fp8/bf16 vs a local 3–4-bit file, with the published ppl/KLD delta when known); `≈ family` (a sibling, e.g. qwen3.8-flash for Flash-Next); `≈ base` (stand-in); `precision undisclosed` (241 of 851 endpoints [R]). Precision is declared by the provider and does not reliably track quality (TokenWatt: a bf16 endpoint scored below fp8 ones [R]), so labels say "declared". Files at ≤ 3 bits add "the trial is an upper bound on quality".
- **Budget:** $2 per trial, tracked from each response's `usage` at pinned prices, shown in magenta. At the cap the server returns an error the tool displays: `triaLM trial budget reached ($2.00): trialm try --budget 5 to continue`.
- **Summary card:** requests, local vs cloud time, the time split, first/typical/slowest turn (with cause), cache reuse, reloads, context peak and overflows, spend, fidelity and (from P3) the n-gram replay verdict. The 1/2/3 rating is stored in history and feeds `priority`. Trials run in the real repo, against no-logging providers, with a one-time notice; `p` switches to the playground.

### Per-harness wiring and test status

**What was tested.** On 2026-11-06 the six non-Codex CLIs ran through `try` and `bridge` against a **scripted fake upstream** on Linux [R]; none has yet met a real model, a real local llama-server or macOS, which the compat schedule below fixes before any "supported" label. Wiring uses env, flags and triaLM-owned files only.

| Tool (tested) | Protocol | Wiring for trial and `launch` | Result [R] | Folded-in fixes |
|---|---|---|---|---|
| **Codex** | Responses (bridge for local llama.cpp) | `-c model_providers.trialm.{base_url,wire_api="responses"}`, `model_context_window=N`, `stream_idle_timeout_ms=600000` (harness.go:118 path, no TOML editing) | The owner's real M5 run failed locally before the bridge existed; not retested since | **Real-local retest on the owner's M5 is P0b acceptance** |
| **Claude Code** 2.1.319 | Anthropic | `ANTHROPIC_BASE_URL`, `ANTHROPIC_AUTH_TOKEN`, `ANTHROPIC_API_KEY=""`, `ANTHROPIC_MODEL` + `ANTHROPIC_DEFAULT_{HAIKU,SONNET,OPUS}_MODEL`, `CLAUDE_CODE_MAX_CONTEXT_TOKENS=N`, **`CLAUDE_CODE_MAX_OUTPUT_TOKENS`** (8192 if N < 64k, else min(32000, N/4)), `CLAUDE_CODE_ATTRIBUTION_HEADER=0`, **`CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1`**; unset `CLAUDECODE`, `CLAUDE_CODE_ENTRYPOINT` | **pass** in trial and bridge, in `-p`, stream-json and the TUI | Claude Code reserves min(max_output, 20000) of the window and always sends max_tokens=32000. At 32,768 that leaves 12,768 effective, so it compacts on turn 1. At 16,384 it fails with "Prompt is too long" and sends nothing. **The fix is the output cap, not a 64k floor.** MinContext ≈ 45k by default, or ≈ 37k with the 8192 cap. Base prompt 33k → **~12k** (10.6k in `-p`, 12.0k interactive, 19.9k with `ENABLE_TOOL_SEARCH=false`). The attribution header is stable within a session but changes between sessions and subagents, so `=0` restores cross-session and subagent cache reuse. Handle `tool_reference` and `defer_loading` in the Anthropic decoder. Print request warnings on the try path too |
| **OpenCode** 1.19.5 | Chat | **`OPENCODE_CONFIG_CONTENT`** = inline provider `trialm` (trial) or `trialm-local` (setup), with `limit.context/output` and `agent.title.disable`; `OPENCODE_DISABLE_MODELS_FETCH=1`; headless `opencode run … </dev/null` | **pass** | Without `limit`, it sends max_tokens 32000 on every request. Disabling the title request removes a parallel request that queued 9.9 s and evicted the single slot. OpenCode mutates its prompt prefix between turns, which defeats llama.cpp prefix reuse; the sim models the lower reuse and hybrid/SWA recipes size `--cache-ram`/`-ctxcp` for it. The bridge must not say "simulated". The startup hang when models.dev is blocked is avoided by the fetch flag |
| **Aider** 0.86.2 (Python 3.12) | **Follows the runtime** | OpenAI route: `OPENAI_API_BASE`, `OPENAI_API_KEY=local`, `--model openai/<m>`, `--model-metadata-file <owned>`. Ollama route: `--model-settings-file <owned>` with `num_ctx` | **pass** (whole and diff formats, git auto-commits, both routes) | `main.go:514` must pass `Protocol` to `SetupFor`; today the trial always uses the Ollama route. Without pinned `num_ctx` every request reloads the model. The check is an *applied edit block*, because Aider sends no tool calls |
| **Qwen Code** 0.26.9 | Chat | `OPENAI_BASE_URL/KEY/MODEL`, `QWEN_CODE_SYSTEM_DEFAULTS_PATH=<owned>` (`contextWindowSize`, managed memory off, usage stats off), `QWEN_USAGE_STATISTICS_ENABLED=false`; headless: `--approval-mode auto-edit --allowed-tools run_shell_command` | **pass** | Without the window it assumes 1M and sends max_tokens 64000. Forward `reasoning_content` upstream (dropped today on both paths). The bridge clamps max_tokens to the context. Base prompt 25k → **~13k** (about 10k with read-only tools) |
| **Cline** 3.0.74 (CLI) | Chat (OpenAI-compatible) | `CLINE_PROVIDER_SETTINGS_PATH=<owned providers.json>` with `contextWindow` and the required `updatedAt`; `Binary: cline`; one-shot `cline "<task>"` | **pass** on both routes | It is a CLI, not IDE-only. Without `contextWindow` it sends 32768. `cline auth -p ollama -b` is rejected. Base prompt 12k → **~5.2k** |
| **Continue** cn 1.5.47 | Chat | `cn --config <owned yaml>` written from a fixed template (no YAML library): `name`, `version`, `schema: v1`, `defaultCompletionOptions: {contextLength, maxTokens}`; `-p` with `--allow Write --allow Edit` | **partial**: the printed snippet fails ("name: Required, version: Required") and passes once those are added | It is a CLI. Without `contextLength` it assumes 200K and dies on llama.cpp's context error instead of compacting. `Binary: cn` makes `--launch` work. Base prompt 8k → **~1.4k** (about 2k with write tools) |

Unchanged scenarios, not re-measured: Codex 9k, OpenCode 7k (consistent with the measured 2.1k system prompt plus 10 tool schemas), Aider 4k.

**IDE extensions and other tools are `manual`.** The Cline and Continue VS Code/JetBrains extensions, **Zed** (kept from `internal/harness`) and **generic** (any OpenAI, Anthropic or Ollama-compatible tool) keep their settings in configs triaLM never edits. `try --serve <tool>` and `launch --serve <tool>` run the server in the foreground with the meter and print a snippet with base URL, model id, key and context length (Cline's "OpenAI Compatible" fields, a Continue `config.yaml` block, a Zed `language_models.openai_compatible` block, or raw values), copied to the clipboard when possible. CI validates the Cline and Continue snippets by loading them into their CLI siblings (same schemas); the extensions and Zed are checked by hand on each minor release.

**Compat status.** Each CLI carries `compat: {status: fake-upstream|real-cloud|real-local, os, version, date}`, and the UI shows `limited testing` until real-cloud and real-local pass on macOS and Linux. Schedule: fake-upstream e2e on every PR and Codex real-local on the M5 (P0b); **real-cloud** (real tool calls, $0.10 cap per tool) for all 7 on Linux CI weekly and on release tags, and on macOS via an owner-run script (P1a, about $1); **real-local** for all 7 on the M5 and on Linux CI with Qwen3 1.7B Q4 on CPU, which checks wiring, not quality (P2). **Retest protocol** per tool: greeting · tool call (Aider: an applied edit) · follow-up after the tool result · a 20k-token prompt reused across turns · parallel side requests · a context overflow (does it compact or die?).

## 8. Setup & verify

- **Typed steps from a closed set:** `install` (an allowlisted package or a pinned release asset with sha256), `fetch` (an HF file with sha256), `run-installer` (downloaded, sha-pinned and shown before running), `write` (only under `~/.trialm`), `start`/`stop`, `engine-delegate` (e.g. Strata's plan), `admin` (sudo via the OS prompt) and `check`. Steps are argv templates from embedded data, never shell strings from data or pastes. The plan screen lists every change and what is *not* touched.
- **Engines install from pinned release assets** (ggml-org llama.cpp for macOS arm64, Linux CUDA/Vulkan/CPU and Windows CUDA, sha256 in `engines.json`), so a Mac without Homebrew needs no admin install; Homebrew's llama.cpp is used only if already installed and in range.
- **Consent: one approval per user-space plan; every other prompt is counted.** Read-only steps and triaLM-owned files never ask. The Y/n covers downloads, user-space installs, pinned installers and services. Each `admin` row costs 1 OS password prompt, a reboot-reset row (the wired limit) says "1 more after each restart", and the plan header totals both before the Y/n. Drivers, ROCm, CUDA and GTT changes are instructions that stop the plan. Agents bind the approval with `--yes=<plan_id>`.
- **"A little freer."** Inside an approved plan triaLM picks ports, retries, resumes, chooses mirrors, translates flags, re-fetches corrupt files, applies platform workarounds (`GGML_CUDA_NO_PINNED`) and falls back to the engine's downloader without asking. It asks again only on a material change (download +10%, a new install or admin step), and counts that approval too.
- **Executor and journal.** One runner per step kind; child output goes to a log with the last 5 lines in a viewport; start steps return only when the health URL answers. `~/.trialm/setups/<slug>/journal.json` records state, bytes, backups and each step's inverse. Ctrl-C pauses and `trialm setup` resumes ("2 of 5 done, download 61%"); `--undo` reverses the steps (model deletion and engine removal are checkboxes) and understands both cyrlm and trialm names.
- **Native downloader:** single stream with HTTP Range resume, sha256 against `lfs.oid`, rate and ETA; parallel ranges only if P2 measures under 70% of link speed on the M5. It follows Xet and legacy `cdn-lfs` redirects (the legacy host was blocked in the sandbox [R]; CI tests it from an open network), reuses HF-cache, LM Studio and Ollama blobs by sha (Ollama digest = HF sha256 is likely, unverified), and falls back to `llama-cli -hf`, `ollama pull hf.co/<repo>:<tag>` or `lms get`.
- **Engine-native hooks** declared in engine data. **Strata:** pinned to a version range; `launch --plan --family … --model … --output json` (rendered as triaLM plan rows), `--yes --no-tui --port P`, `launch --status/--stop`, `connect <tool> --check --output json`, `GET /metrics` (hit_rate, pcie_share, drafts_offered/accepted). The plan JSON is checked against recorded shapes per pinned version, falling back to displayed manual steps on a mismatch; a contract test probes for a licence prompt. **Avalon:** `install.sh` with `AMDGPU_TARGETS`, `MODEL_DIR`, `SKIP_MODEL=1`, `BIN_DIR`, then `avalon-server`, `-lv 4` logs, `/metrics`, `/slots`, `timings`. **Others:** Ollama `pull` + Modelfile, `lms get/load`, Lemonade `/v1/pull` and `/v1/system-info`.
- **Verify = speed + compatibility + quality smoke:** (1) warm up (about 20 s for an expert cache); (2) decode at depth about 0, 8K and the target, prompt reading at the recipe's ub, cached follow-up TTFT, the spec verify-cost sweep, acceptance per content type (prose, code, tool JSON at agent depth) and the MTP prefill penalty from logs; (3) the harness probe in the tool's wire format through the route `launch` will use; (4) under 3 bpw, 20 tool calls must parse (flash-moe's 2-bit build broke JSON [R]); (5) writing at depth below 50% of predicted is reported as a likely spill or mmap thrash. "Ready" only when all pass, else exit 4 naming the failing check.
- **Calibration** is keyed by a fingerprint (GPU, driver, RAM configuration, OS, engine version) that a change invalidates. It fits η_decode, η_prefill, B_pcie (a 2-point ub sweep on hybrid recipes), h, α and o, each shrunk toward its prior, into `~/.trialm/calibration/<fingerprint>.json`, then re-predicts and re-ranks every recipe on the machine as `calibrated`. The check screen shows misses honestly ("we were 34% optimistic on prompt reading"). **No post-calibration band is promised;** the shown band becomes the measured residual.
- **`setup --tune`**: a `llama-bench -o jsonl` sweep (`-ub`, `--n-cpu-moe`, `-fa`, `-ctk`, `-t`) that rewrites the launcher. Offered on Ready after a hybrid miss over 20%; never mandatory.
- **`trialm launch [tool]`** starts the engine detached if needed (the old `bg` logic) and waits for health, re-applies a reset wired limit (the counted password prompt), runs the bridge in-process when the API matrix needs it, and hands the TTY to the tool with its wiring. This replaces the hand-copied 300-character Codex command and the separate bridge process. The engine stays warm 15 min after exit; `--stop`, `--serve` (foreground, meter, IDE snippet) and `--print` (env + command for an alias).
- **One-time machine calibration at first run (owner decision 2026-11-07; built in P0b, Charm screen in P1b).** Right after detection, home offers it as the recommended step: `Calibrate this machine · ≈6 min · no model downloads · enter run · s skip`. Enter runs it; `s` skips, is remembered in `prefs.json`, and leaves one dim line on the recipe table (`calibrate for tighter numbers: trialm calibrate`). It writes synthetic GGUFs (random weights, the exact tensor shapes, vocab/output head and quant formats of the catalog's architecture families, two layer counts to separate fixed from per-layer cost), runs `llama-bench` on a separable grid (quant kernels on one family; dense, MoE, hybrid linear attention, MLA and sliding-window on one quant; depths 0/8k/32k; `-p 1,2,3,4 -n 0` for spec verify cost), and fits per-machine multipliers on η by family × quant × depth into `~/.trialm/calib.json`, keyed by the fingerprint. Every catalog model of a measured family is then predicted as `calibrated` on llama.cpp-kernel engines (llama.cpp, Ollama, LM Studio's llama.cpp engine), with the band taken from the fit residuals; MLX and what-if machines stay uncalibrated. `trialm calibrate [--quick] [--status] [--json]` reruns it; a new engine build or machine makes it stale and re-offers it. It needs a llama.cpp engine: an installed one is used, else (from P2) the pinned release asset is fetched as part of the same Enter. The real-model M5 bench (`tools/m5-bench.sh`) is the check that it works: P0b acceptance compares calibrated predictions with it. Expected effect on the M5, unverified: ±60/70% to roughly ±15–25%.
- **Synthetic-GGUF pre-download probe (P3, pulled forward from P5).** After the engine installs and before a 13–95 GB download, write a 2–4-layer GGUF with the target's real tensor shapes and quant types (random valid blocks, via the existing test writer), run llama-bench and extrapolate per layer. It measures dense and full-GPU speed on this exact machine, labelled "measured on a synthetic file"; it cannot measure expert-cache hit rates.

## 9. Architecture

### Target layout (module `github.com/aeroknight786/trialm`, go 1.26)

| Package | Responsibility | Key types |
|---|---|---|
| `cmd/trialm` | `os.Exit(cli.Main(ctx, os.Args))`, ≤ 30 lines | — |
| `internal/cli` | Verb table, flags → `Requirements`, mode selection, exit codes, agent contract | `Verb`, `Mode` |
| `internal/ui` | **The only package that imports Charm.** TUI, plain and JSON renderers over core events; theme and identity; NO_COLOR; glyph fallback; chat screen | `Renderer{Handle(core.Event)}`, `tui.Root` |
| `internal/core` | **The one API** | `Session` with `Detect`, `Find`, `Explain`, `Try`, `Plan`, `Apply`, `Launch`, `Status`; `Event`; `Sink func(Event)` |
| `internal/figure` | Provenance-carrying numbers and formatting (significant figures follow the band) | `Figure{V,Lo,Hi float64; Unit string; Kind; Src string; InSample bool}`, `Kind = Claimed\|Simulated\|Calibrated\|Measured` |
| `internal/registry` | Loads, validates and overlays data: embedded < `~/.trialm/data/` (user overrides); `TRIALM_OFFLINE=1` | `Registry` |
| `internal/hardware` (+ `hardware/probe`) | From `hw`, split in P3: per-OS probes (PCIe gen and width, RAM channels, ISA, NUMA, SSD, UMA/GTT, pinned-RAM limit, multi-GPU) in `probe`; `Target` (moved out of `predict`), fingerprint and what-if in `hardware` | `Machine`, `Target`, `Fingerprint` |
| `internal/catalog` (+ `catalog/arch`) | Model, Quant, Asset; Embedded, OpenRouter and HF sources (**absorbs `internal/hub`**, the HF lineage reader that stand-ins need); `FromGGUF` (one copy); stand-ins; layout families | `Source{List; Resolve}`, `Layout` |
| `internal/predict` (+ `predict/kernel`) | Pure math: memory plan, roofline, η_arch, scenario; technique kernels (offload, expertcache, spec, kv, yarn, ubatch, wired, multigpu) | `Timing`, `Kernel`, `MemPlan`, `Scenario` |
| `internal/engine` | Engine data with `parent` inheritance; version probe; flag translation; adapters (composed / delegated); command-line parser and flag audit | `Engine`, `Adapter`, `FlagAudit` |
| `internal/recipe` | Requirements, slugs, enumerate, rank (Pareto + paired ties), explain, claims, reproduction, preset gate | `Requirements`, `Recipe`, `Candidate`, `Claim` |
| `internal/calib` | Fixtures, held-out residuals → `bands.json`; per-machine fits | `Fixture`, `Band`, `MachineFit` |
| `internal/harness` | `harnesses.json`, detection, wiring (env, args, owned files), IDE snippets, inline or window launch | `Harness`, `Wiring{Env, Args, Files, Bridge, Snippet}` |
| `internal/trial` | Pinning (incl. data policy), budget, fidelity, summary, n-gram replay (wraps server + sim + upstream) | `Trial`, `Report` |
| `internal/openrouter` | PKCE, models, endpoints, credits, prices | `Client`, `Endpoint` |
| `internal/setup` | Plan, steps, executor, policy, journal, undo, downloader, proc (detach), self-update | `Plan`, `Step`, `Executor{Run(ctx, Step, Policy)}` |
| `internal/verify` | Measurements, harness probe (from `compat.go`), `/metrics` scrape → calib | `Report` |
| `internal/store` | `~/.trialm` paths, prefs, credentials, `last.json`, history, migration from `~/.cyrlm` | `Store` |
| Kept | `gguf`, `sim` (gains bursts, PCIe floor, `-np`), `server` (incl. bridge, budget hook), `api/*` (adds `tool_reference`), `llm`, `tok`, `upstream` (forwards reasoning) | — |

```go
type Timing interface {                                    // predict
    DecodeSec(depth int) float64; PrefillSec(from, to int) float64
    LoadSec(warm bool) float64;   TurnFloorSec(newTok int) float64; Burst() BurstModel }
type Kernel interface{ Memory(*MemPlan, Params); Wrap(Timing, Params) Timing }
type Adapter interface{ Plan(recipe.Recipe, hardware.Machine) (setup.Plan, error); Metrics(ctx context.Context, base string) (Metrics, error) }
```

**The one-number rule is enforced by type.** `ui` renders only `figure.Figure`, and a lint test fails if `ui` formats a raw float with a speed or time unit. Every figure for a recipe comes from one `Candidate` evaluation, cached by recipe id plus calibration version.

**Data files** are embedded JSON only (stdlib parses them; no YAML dependency). Hand-maintained: `engines.json`, `techniques.json`, `harnesses.json`, `devices.json` (about 120 GPUs plus PCIe), `hazards.json`, `recipes.json` (presets that passed the gate, §4), `claims.json` (everything else, graded), `quality.json`. Generated: `catalog.json.gz`, `efficiencies.json`, `bands.json`. Flag lookup is `engine.supports[tech] ?? technique.engines[engine|parent]`, so DFlash is one technique entry and Avalon is one engine entry plus its file set and installer step; a lint test checks references. **No remote data bundle before v1:** data ships in each release, data PRs trigger patch releases, and the update notice delivers them, so there is no signing key to manage (decision 5).

**What moves out of `cmd/`** (2,941 lines with 0% coverage [R]):

| From | To |
|---|---|
| `recommend.go` (`bestFileFor`, `chooseRuntime`) | `recipe` |
| `standin.go`, `modelFromGGUF` | `catalog` |
| `compat.go` | `verify` |
| `bridge.go` | `server` |
| `detach_*` and bg | `setup/proc` |
| `plan.go` | `setup` + `engine` |
| `card.go` and all printing | `ui` |
| `inspect.go` | advanced `cli` |
| `agentflow.go` + `agent_prompt.md` | `next` fields |

**Deleted:** `account` sharing, `share`, `login --account`, `config api-url` (both `doShare` branches are identical) and `CLOUD_API.md` (to "future"), with key storage moving to `store`; `internal/runtime` (→ engine data) and the `specs` tables (→ `devices.json`); about 950 lines of per-runtime setup builders (→ adapters + templates); the no-op `--openrouter` flag (`--tuned`/`--defaults` merge); the offload "no claim" `1/(1+6·frac)` branch; duplicate utf8, quoting and format helpers; the `ccr-db94bc6e-e47992` branch after one release as a stub.

**Dependencies and size.** Charm v2 needs **go 1.26** (bubbletea ≥ v2.0.10; `GOTOOLCHAIN=auto` fetched it cleanly) and adds about 38 modules, imported only by `internal/ui` [R]. No other third-party code; `govulncheck` and `go mod verify` in CI. Binary: 8.73 MB stripped today (12.34 unstripped) → about 10.98 MB with Charm → about 11.5 MB with the catalog [R].

**CI budgets, derived from the feature list.** The v1.0 estimate is ≈ 32k non-test lines (`ui`+`cli` 5k, `api` 4.5k, `hardware` 3.1k, `gguf` 2.3k, `setup` 2.2k, `predict` 2.2k, `catalog` 2k, `recipe` 1.8k, the rest 9k), after `cmd` shrinks to 30 lines and about 2.1k of `account`, `runtime`, `specs` and setup builders are deleted. Gates: ≤ 26k lines at v0.4, ≤ 31k at v0.6, ≤ 34k at v1.0 (20.3k today); `cli`+`ui` ≤ 5.5k; ≤ 2.5k per package, except `api/*` (wire types, 4.4k), `gguf` (2.3k, frozen) and `hw` (2.6k) until P3 splits it; stripped binary ≤ 14 MB (2.5 MB over the 11.5 MB estimate), and a PR adding over 300 KB must justify it.

**Test strategy.**
1. **Goldens first (P0a):** `--json` and plain output on 6 machine fixtures (M5 24 GB, 3090/128, 4090/64, 7900 XT/64, 5070 12 GB/64, CPU-only); from P1b, TUI `View()` frames at 60/80/120 columns with and without NO_COLOR.
2. **Calibration fixtures.** *Gates:* no regression on the 565-row leave-one-device-out bands; the physics gate (§5); from P3 a **held-out** ub sweep on a rented 4090 (±20%); the M5 bench split by model. *In-sample fits, published in a residual report, never gates:* the gpt-oss-120b/3090 ub sweep (−16..+2%), Strata RTX 5070 (Q2_0…IQ3_S, 1K–262K, Table 5, hit rates), Strata R9700 94.6, Avalon 7900 XT (66.1/98.4, 250K, 16/32/64 GB RAM), Avalon vs Strata on the 4090 (98.0/141.9 vs 101.5/141.4), Qwen3.6-27B MTP 1.67×, and the Splash llama.cpp MTP rows. Each becomes a gate once verify data from ≥ 3 machines exists.
3. **Properties:** more VRAM, bandwidth or RAM is never slower; longer context never fits better; S < 1.05 is never recommended; YaRN is never emitted for baked-in models; the reserve, pinned-RAM and mmap gates always hold; nothing dominated outranks what dominates it; no claim-kind figure reaches the ranker.
4. **Harness e2e** with real binaries (npm: Claude Code, OpenCode, Qwen Code, Cline, `cn`; pip: Aider on Python 3.12; Codex) on the fake upstream every PR, with the 6-check protocol; real-cloud and real-local on the §7 schedule.
5. **Engine e2e:** a llama.cpp CPU build with a 0.5B GGUF runs setup → launch → verify → undo on Linux, macOS and Windows runners; Strata and Avalon get contract tests on recorded `--plan --output json`, `/metrics` and logs per pinned version.
6. **Also:** a weekly llama.cpp `--help` drift test; PKCE against a fake server; fuzzing of the claim parser, flag translator and GGUF reader; a Windows Terminal, conhost and VS Code terminal checklist each minor release (the Charm demo was never run on a Windows console [R]).

## 10. Migration plan

Every phase ships a working tagged release; v1.0 follows P6. The order fixes friction and the owner's own machine first, on today's model set, while every move of business logic gets a behaviour-equivalence gate. **Size** is relative build effort (in agent-built working days, not a calendar): Claude builds each phase and hands the owner a release plus the "Owner tests" list, so elapsed time is set mostly by the owner's testing turnaround and by rented or volunteer hardware for P3 and P5 (decision 7).

| Phase | Deliverables | Acceptance | Size | Owner tests |
|---|---|---|---|---|
| **P0a Rename + safety net** (v0.2.0) | **Goldens** on 6 fixtures. **Name check first** (GitHub, Homebrew, npm, PyPI, crates.io); the binary stays `trialm` regardless. **Repo:** `aeroknight786/cyrlm` → `aeroknight786/trialm` (URLs redirect); a new default, protected `main` from `ccr-db94bc6e-e47992`, which stays one release with stub installers. **Code:** module path; `cmd/trialm`; OpenRouter `X-Title`/`HTTP-Referer` (`upstream.go:225-226`); `User-Agent` (`hub.go:51`, `account.go:313`); Codex `model_providers.trialm`; OpenCode provider ids; Continue `name`; launchd `com.trialm.*`; model suffix `-trialm` (both recognised, Ollama models not re-created); `.bak-cyrlm` restored by undo; auth token `trialm` (bridge accepts both). **Env and paths:** `TRIALM_*`, with `CYRLM_HOME/KEY/API_URL/UPSTREAM_API_KEY/DETACHED/INSTALL_DIR` as fallbacks for one release; `~/.cyrlm` → `~/.trialm` (atomic move, symlink back, owned scripts rewritten), `~/.cache/trialm`, `%LOCALAPPDATA%\trialm\bin`. **Release:** assets `trialm_<os>_<arch>`; `release.yml` titled "triaLM" and **uploading install.sh/install.ps1 as assets** for the `releases/latest` URL; installers `exec trialm </dev/tty` unless `TRIALM_NO_START=1`; the Go fallback becomes `GOTOOLCHAIN=auto go install …/cmd/trialm@latest` (Go ≥ 1.21 fetches 1.26 itself; older Go gets a clear message). **CI and docs:** `ci.yml` builds `./cmd/trialm`; README and `docs/*` reviewed by hand (75 tracked files mention cyrlm, some on purpose). A `cyrlm` shim execs `trialm`. Zed and generic stay as `manual` harnesses (§7) | `go test -race` green; goldens unchanged except listed intended diffs; upgrade test from a fixture `~/.cyrlm` keeps the key and setups; the README line installs from `releases/latest` on clean macOS and Linux; `git grep -i cyrlm` lists only intended mentions | 4 | Install on the M5: the old key and setup still launch |
| **P0b Harness fixes + the owner's M5** (v0.2.1) | **Harness fixes from §7** (wiring, base prompts, `Binary` for Cline and cn, Aider protocol, `tool_reference`, reasoning forwarding, max_tokens clamp, try-path warnings). **Bug fixes:** one figure per metric; deduped notes; `--harness` optional; `CLAUDECODE` unset; `hasRollbackLimits` = SWA \|\| recurrent; `MaxFitCtx==0` fallback; `FileTag` splits; MTP layer count; sim `truncate` over-cutting (6,876 → 16 tokens [R]); the 8 dead OpenRouter ids; `--background` prints only `Stop: kill N`. **CI harness e2e** for all 7 CLIs on the fake upstream. **One-time calibration** (§8): `trialm calibrate` on synthetic GGUFs + `llama-bench`, offered at first run (Enter runs, `s` skips), applied to every catalog model of a measured family. **M5 bench:** `tools/m5-bench.sh` runs llama-bench (`-p 512,4096 -n 128 -d 0,8192,32768`, plus `-p 1,2,3,4,8 -n 0` for spec verify cost) on Qwen3.6-35B-A3B UD-IQ3_XXS and gpt-oss-20B MXFP4 (about 15 min) into apple-m5 fixtures | 7 harness e2e green; **Codex + bridge on a real llama-server on the owner's M5 passes greeting, tool call and follow-up at 64k**; M5 band ≤ ±25% for the benched models (calibrated on synthetic files, checked against the real-model bench) | 8 | Claude Code at 40k no longer compacts on turn 1; run the M5 bench; run Strata's installer on the M5 to settle Strata-on-Metal |
| **P1a Core seam + agent CLI + private trial** (v0.3.0) | `core.Session` (Detect, Find, Try) extracted from `cmd`; `figure`; plain and JSON renderers with `next` (today's output becomes the plain renderer); `--yes=<plan_id>` plumbing; `key` (env, PKCE, headless, paste); **first live call** with the owner's key (`/credits` vs `/key`, `provider.only` tags, `data_collection`/`zdr`, authenticated `/endpoints` speeds); trial pinning with data policy, $2 cap, fidelity, `--pace`, cloud-bound labels, plain summary card; update notice and `update`; CI budgets | `find --json` equals the old `recommend --json` on the 6 fixtures; NDJSON schema test; 0 escape bytes when piped; `cmd/trialm` ≤ 30 lines; no logging provider is ever pinned by default (fixture test); real-cloud compat for all 7 CLIs on Linux (CI) and macOS (owner run) | 9 | `trialm try` in Codex and Claude Code via plain mode; credit shown matches the dashboard |
| **P1b The new CLI** (v0.4.0) | Go 1.26 + Charm root model; identity and palette; home; needs form; recipe table; window mode (the four openers); chat screen for `use=chat`; summary card with rating; claims lane and gallery from the graded `claims.json`. **Stubs until P3:** details show memory, turn split and flash/KV/YaRN verdicts (today's math), and the spec row reads "not simulated yet (v0.6)"; a pasted command line shows "claim parsing arrives in v0.6"; no n-gram line on the card | ≤ 5 keys + 1 click to the first trial token on macOS and Linux; binary ≤ 14 MB; TUI goldens; Journey D prompt counts measured in Claude Code and Codex and published; macOS window mode shows 0 TCC prompts, or the prompt is counted | 13 | Journey A up to the summary card, in Codex and Claude Code |
| **P2 One-approval setup + launch** (v0.5.0) | Planner, executor, policy (counted prompts), journal, resume, undo; downloader (Range, sha, Xet + LFS, reuse); composed adapters for llama.cpp (pinned release asset), Ollama, LM Studio and mlx-lm; verify incl. spec sweep and spill check; basic calibration (η decode and prefill) by fingerprint; `launch` with `--print/--serve/--stop` and IDE snippets; the conditional wired-limit row | Clean macOS (no Homebrew) and Linux VMs: 1 approval, 0 password prompts to Ready; Ctrl-C mid-download resumes with sha ✓; undo leaves an empty diff; `launch codex` with no copy-paste; **real-local for all 7 CLIs** on the M5 and Linux CI; IDE snippets load in the CLI siblings; Windows CPU engine e2e | 13 | The full journey A, then `trialm launch` the next day. **Splash spike:** install it on the M5 and measure 35B-A3B against calibrated llama.cpp; ≥ 1.5× moves its adapter into P3 |
| **P3 Evidence + technique simulator + Strata** (v0.6.0) | All §5 kernels, η_arch, platform hazards and the placement optimiser; `hw` split and new probes; `calib` and `bands.json`; paired ties; claim parser (all §4 formats), flag audit, claim card, gallery; `claims-harvest` and the first curated batch; the preset gate; `setup --tune`; synthetic-GGUF probe; **Strata delegated adapter** (and Splash's if the spike passed); n-gram replay | Property tests; no band regression; the held-out ub sweep within ±20%; an 8 GB 3070 Ti what-if gets a 35B-A3B hybrid recipe; the M5 MTP verdict uses measured o; **Strata plan → apply → `connect --check` on a rented 5070 or 4090**; residual report published with `fit` labels | 16 | `find --as "RTX 3090" --ram 64`; paste a Reddit line with an unknown flag and see it listed as dropped; the gallery on the M5 |
| **P4 Catalog at scale** (v0.7.0) | `catalog-gen` rewrite (OpenRouter → HF → GGUF/MLX) with HF token and backoff, weekly PR → patch release, dead-id gate, layout families, sidecars, runtime refresh and enrichment, per-endpoint precision, context, price and data policy | ≥ 140 models with geometry; 147/155 HF ids resolve; no `geometry unverified` in the top 30 tool-capable models that fit ≤ 128 GB (DeepSeek-V4 excepted and badged); cost estimates within ±30% on 3 real trials | 8 | `/glm` search; trial cost matches the OpenRouter dashboard |
| **P5 Avalon, Lemonade, ik, claims-lane setup** (v0.8.0) | Avalon and Lemonade adapters; buun; the ik_llama.cpp variant plus a controlled sweep vs mainline; set-up-and-measure for hipfire, Lucebox, ds4, ExLlamaV3, KTransformers and HyperQwen; port probing | **Avalon CUDA install → verify on a rented 4090**; Avalon ROCm on a volunteer 7900 XT, else `ROCm: unverified by triaLM` with contract tests; Lemonade contract tests (plus a volunteer Strix Halo if found) | 12 | Review the volunteer and rented-box reports; Journey C reproduced on whatever AMD box is available |
| **P6 Loop + trim** (v1.0) | Calibration of h, α, o and B_pcie from `/metrics`; ratings → prefs; remove the shim, symlink, old branch and verb aliases; docs generated from `bands.json` and `harnesses.json`; Windows pass (checklist plus one real Windows + NVIDIA run); revisit the data bundle | No `limited testing` badge left on the 7 CLIs; visible surface = bare + 5 verbs; ≤ 34k lines; binary ≤ 14 MB | 8 | A fresh install on a second machine using only the README line |

## 11. Risks & cuts

**Deliberately not built now:** a GUI, web or alt-screen dashboard; accounts, sharing and telemetry; a signed remote data bundle (revisited after v1); per-architecture plugins beyond the layout families; window openers beyond the four in §3 (no AppleScript or iTerm scripting); parallel downloads unless P2 measures a need; trials of unhosted fine-tunes beyond stand-ins, and bring-your-own endpoint; composed setup for vLLM, SGLang, ExLlamaV3 or KTransformers; Windows builds of forks; exo, RPC and tensor-parallel setups; our own quality evals; glamour, cobra, fang; YAML/TOML writers and any edit to user tool configs; automatic sudo or driver installs; launchd/systemd autostart; dynamic plugins; pacing from OpenRouter's speed fields.

| Risk | Mitigation |
|---|---|
| **Engine churn** (llama.cpp flags renamed; Avalon moved its RAM claims and headline (91 → 98) within about 2 weeks [R]; Strata releases every few days) | Version ranges and the flag-translation table; a weekly `--help` drift test; `LLAMA_ARG_*` env; recipes pinned to commit and sha; Strata's plan JSON checked against recorded shapes, with a manual-steps fallback; engine-data fixes ship as patch releases; claims pinned to engine version and date |
| **Self-reported claims** (mostly authors' peaks; Reddit read only through snippets) | Claims never rank; `fit` labels; the V/C/S grade and preset gate; the monthly API harvest; reproduction thresholds; the physics gate; the user's own check supersedes everything |
| **The simulator is confidently wrong** (sparse M5 data, a 2-point hit-rate curve, Strix Halo prefill varying 2–3×, arch slowdowns) | The M5 bench before the new CLI; bands from held-out residuals, widened 1.5× when n < 5; η_arch; assumed inputs named on `?`; paired ties; `--pace p90`; misses shown on the check screen |
| **The owner cannot run half the engines** (the M5 runs neither Avalon nor, probably, Strata) | Strata-on-Mac settled in P0b; Splash spiked in P2; $150 of rented 4090/5070 time; volunteers for the 7900 XT, Strix Halo and Windows; `unverified by triaLM` otherwise |
| **Ops burden for a solo owner** | Automated recurring jobs with known cost: weekly catalog PR, weekly real-cloud run (about $0.70) plus one per tag, weekly drift test; the monthly harvest (about 1 h); the Windows checklist only on minor releases; no signing key before v1 |
| **Charm / Go 1.26** (38 modules; colorprofile ignores `NO_COLOR=yes`; the background query loses keys; Windows consoles never run; older Go cannot build) | Charm confined to `ui`; exact pins; our own NO_COLOR; no background query; one root program; the plain renderer covers every flow; the stdlib UI as fallback; release binaries everywhere and `GOTOOLCHAIN=auto` for the Go fallback |
| **Agent sandboxes** (Codex blocks network, localhost and windows; some agents flag `curl \| sh`) | Exit 3 with a precise `needs` and command; the 3-step install; counts measured in P1b, not promised |
| **HF storage backends** (legacy `cdn-lfs` ranges were blocked here) | Tests against Xet and LFS from an open network; sha256 always; engine-downloader fallback |
| **OpenRouter id drift** (8 of 24 ids dead; some HF ids name sibling weights) | Keyed by HF id; the dead-id gate; runtime re-resolve; `fidelity: family` |
| **Safety of executing setup commands** | argv templates only from data embedded in the release, never from pastes, network text or model output; pastes supply typed values for known flags, and dropped flags are shown, never passed through. Closed step kinds; installers sha-checked and shown before running; writes only under `~/.trialm`; sudo only on a flagged, counted row; `--yes=<plan_id>`; the agent session is never modified; journal and undo |
| **Cost and privacy** (trials run on the real repo) | No-logging providers by default, ZDR one flag away; the $2 cap and live spend; a one-time notice; playground and predict-only modes |
| **Quality of low bits and speed tricks** | Below 3 bpw only under `everything`; tool-call smoke before Ready; "upper bound" trial labels; quality penalties or `quality unmeasured` badges |

## 12. Decisions needed from the owner

1. **UI stack — decided by the owner: Charm** ("use charm cli design"). Charm v2 on Go 1.26, confined to `internal/ui`: about 38 modules and +2.25 MB; Windows consoles get a manual check each minor release. The tested 290-line stdlib raw-mode UI stays as the documented fallback.
2. **Trial defaults.** *Recommended:* inline (window mode automatic under agents), $2 cap, your real repo sent only to no-logging providers after a one-time notice (`p` playground, `--zdr` stricter), p50 pacing with p90 one key away.
3. **What one approval covers.** *Recommended:* downloads, user-space and pinned engine installs (ggml-org assets, not Homebrew), services and files under `~/.trialm`, with self-correction inside the plan and a new approval only on a material change. Each sudo row is a separate, counted OS prompt (the wired limit also after each restart); drivers are never installed; user tool configs are never edited (`--print` aliases, `--serve` snippets).
4. **New and experimental engines.** *Recommended:* shown by default with `new`, `1 maintainer` and `fit` labels; #1 only with ≥ 25% better task time or a higher quality tier; ik_llama.cpp never #1 until its sweep; unsimulated engines stay in the claims lane until measured here.
5. **Remote data bundle.** *Recommended: not before v1.0.* Data ships in releases, data PRs trigger patch releases, and the notice plus `trialm update` deliver them; revisit a signed bundle only if that proves too slow.
6. **Rename mechanics.** *Recommended:* check the name first, rename the repo now (GitHub redirects) and make a new `main` default; "triaLM" in prose, `trialm` everywhere else; one `~/.trialm`, migrated from `~/.cyrlm`; a `cyrlm` shim and the old branch with a stub installer for one release; ship as v0.2.0.
7. **Hardware you do not own.** *Recommended:* $150 of rented RTX 4090/5070 time (P3 held-out sweep, Strata and Avalon-CUDA acceptance, P5 ik sweep), plus volunteers recruited by P3 for a 7900 XT, a Strix Halo and a Windows + NVIDIA box; without one, the engine ships `unverified by triaLM`.
8. **Where Reddit claims come from.** *Recommended:* a read-only Reddit API app you register, a monthly harvest (about 1 h) from your network, and you grading each candidate V/C/S in its PR. Only gated recipes become presets.
