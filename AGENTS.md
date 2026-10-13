# AGENTS.md — project memory for any agent working on this repo

Read this first. It is the single source of truth for the goal, the locked
scope and the hard rules. Do not re-interpret the goal.

## 1. What this project is

ChitChat Bot — a Windows desktop app (Go + Wails) plus a CLI worker that
automates chats on chitchat.gg. The dashboard **Message** page has three
message modes:

| # | Mode | Status |
|---|------|--------|
| 1 | Single Sequence | original, untouched |
| 2 | Folder Sequences | original, untouched |
| 3 | **EVAChat Flow** | replaced the old *Keyword Based* mode |

**The only product change in this repository is mode 3.** Everything else
(browser, API, proxy, realtime, registry, sequences, worker plan, license,
CLI) is the original project and must stay byte-for-byte as it was unless a
change is strictly required to wire mode 3.

## 2. What EVAChat Flow must do

EVAChat Flow is the EVA bot's TXT-driven chat engine ported to Go. The
authoritative description of the engine is `docs/CHAT_ENGINE_MAPPING.md`.
Short version:

* `data/eva_chat/input/<category>.txt`  = what the USER says (triggers)
* `data/eva_chat/output/<category>.txt` = what the BOT replies (reply pool)
* same file name = same category. User SMS → matched against input files →
  category detected → stage/logic rules → reply from the same-named output
  file.
* Stages: `NEW → WAIT_AGE_GENDER → WAIT_COUNTRY → MIDDLE → END`.
* MIDDLE is "detect next user msg": snap request / yes-after-ask / horny /
  flirty / how are you / your age / english only / greeting again / busy /
  warm reply / fallback flirty question.
* Horny or flirty reply → the bot's **next** message is always `ask_snap.txt`
  → positive → `share_snap.txt` (with `%username%`) → END. Refusal → one
  `retry_nudge.txt` → second refusal → `closer.txt` → END.
* Direct snap request from the user ("ur sc?", "whats ur snap") → share
  immediately at any stage.
* Age < 18 revealed → never share, polite end.
* Every output pool fires once per user except the repeatable pools
  (`flirty_questions`, `horny`, `middle_chat/flirty_reply`, `ask_snap`,
  `horny_answer`, `closer`). Never the same line twice until the pool is
  exhausted.
* Reply cap (8) → `closer.txt` → END.
* `%username%` / `{snap}` come from the dashboard **Message → Usernames**
  list (same mechanism as the other two modes; increments the
  `snap/tg/user` counter). `{country}` = the user's detected country.
* Optional **AI compose layer** (`internal/bot/eva_ai.go`): an
  OpenAI-compatible API rewrites the reply text (context-aware, random
  persona f 20–25 / Europe from `persona.txt` + `persona_countries.txt`).
  The TXT engine still decides the flow; share lines are never rewritten;
  any failure falls back to the TXT line. Settings live in
  `MessageSettings.AI*` (local data/ only — never commit a key).

## 3. Where the code lives (EVA-specific files only)

| File | Role |
|------|------|
| `internal/bot/eva_chat.go` | the engine (loader, matcher, stage machine, pick) |
| `internal/bot/eva_ai.go` + `eva_ai_test.go` | optional AI compose layer (OpenAI-compatible client, persona, output guard) |
| `internal/bot/eva_assets.go` + `eva_assets/**` | embedded starter TXT pack, seeded to `data/eva_chat/` on first use |
| `internal/bot/eva_chat_test.go` | engine tests (mirror of the reference `test_flow.py`) |
| `internal/bot/logic.go` | 3 call sites (DM first msg, DM reply, realtime) + `executeMatchEVAChat` |
| `internal/model/model.go` | `EVAChatSession` persisted per user in the bot state JSON |
| `internal/model/ui.go` | `ChatModeEVAChat = "eva_chat_flow"`, legacy `keyword` migration |
| `app.go` | `SaveMessageSettings`, `OpenEVAChatDataFolder`, worker config for EVA mode |
| `cmd/chitchatui/frontend/dist/{index.html,app.js}` | dashboard radio + EVA panel |
| `internal/bot/pacing.go` | human pacing: opener policy (user speaks first), shifts, hourly budget, 429 cooldown (see `docs/HUMAN_PACING.md`) |
| `internal/logwall/` + `app_livewall.go` | Live Wall: Home counters + activity log mirrored to a local-only browser page (see `docs/LIVE_WALL.md`) |

`cmd/chitchatui/app.go` is a legacy duplicate UI that the official build
(`wails.json`, `scripts/bundle-windows.ps1`) does not use. Leave it alone.

## 4. Hard rules

1. **Never read, copy, commit or upload runtime/private data**: `data/`,
   `data/profiles/`, sessions, cookies, `login_info_*.json`,
   `storage_state.json`, tokens, Badger DB (`*.sst`, `*.vlog`), logs, cache,
   browser runtimes, `node_modules`, `vendor`, build output. `.gitignore`
   already blocks them; check `git status` before every commit.
2. Only change EVA-specific files (section 3). Do not refactor, rename or
   "clean up" original files.
3. Keep the age gate: a revealed age under 18 blocks any snap share.
4. Ask the user before any remote or destructive action (push, merge,
   delete, overwrite of Drive files).
5. Never ask for or paste GitHub tokens / passwords in chat.
6. Verify with real runs before claiming success:
   `go vet ./internal/bot ./internal/model ./internal/uistore`,
   `go test ./internal/bot ./internal/uistore`,
   `CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build ./...`,
   `node --check cmd/chitchatui/frontend/dist/app.js`.

## 5. Reference source of the chat engine

`github.com/rajuvbygyuiythh/chatfilehere` → `eva_bot_sms_detect_fixed.zip`
→ `eva-full-project/`. Only these parts matter and were analysed:

* `eva_flow.py` — the engine (EvaFlowBot). The Go port follows it 1:1.
* `data/input/**`, `data/output/**`, `data/countries.txt` — the TXT database
  (copied into `internal/bot/eva_assets/`).
* `data/output/info.txt` — the author's Bangla guide of the flow.
* `test_flow.py` — the behaviour spec (checks mirrored in Go tests).

Everything else in that zip (account_sessions, browser automation, PyQt GUI,
local_db pipe databases, rules.py 6-flow engine) is out of scope and must
not be touched or read.

## 6. Build / run on Windows

```bat
install.bat          :: installs Go 1.25+/Wails v2.12.0/WebView2 if missing, then builds + runs
run-dashboard.bat    :: starts build\bin\chitchatui.exe (data\ is created next to it)
```
Manual: `wails build -clean -webview2 error` or `.\scripts\bundle-windows.ps1 -Clean`.

Dashboard → Message → choose **EVAChat Flow** → fill **Usernames** (one snap
handle per line) → Save → Start. First start seeds `data/eva_chat/`; edit
those TXT files and restart the bot to reload.

## 7. `apitest/` — the AI funnel lab (separate module, NOT wired to the bot)

**If you lost context, read `apitest/HANDOFF.md` first (2 minutes), then
`apitest/PLAN.md`.** Everything about the AI chat design lives there.

* `apitest/` is its own Go module (`module apitest`, no deps). It never
  imports the bot and the bot never imports it. Build: `apitest.bat`.
* Purpose: design and test, against a real OpenAI-compatible API
  (Featherless by default), HOW the AI girl chats: tone, funnel to the snap
  share, moods, memory, anti-bot-tells. Decisions are recorded in
  `apitest/PLAN.md`; the running log of what is done / what is next is
  `apitest/HANDOFF.md`.
* Rule from the owner: **do not touch the main bot for anything AI-related
  until the apitest behaviour is approved; then design the integration
  together (step 4 in HANDOFF.md).**
* Secrets: `apitest/apitest.config.txt` (API key, snap username) and
  `apitest/apitest.log` are gitignored. Never commit them.
* Owner communicates in Bengali/Banglish; reply in Bengali.
