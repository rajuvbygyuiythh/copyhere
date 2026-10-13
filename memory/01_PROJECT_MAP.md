# 01 — PROJECT MAP

Repo root = `/home/user/copyhere` in the sandbox (owner's PC: any folder,
built with `install.bat`). Go module root = main bot; (since 2026-10-14 `apitest/` is NOT a separate module any more — see `cmd/apitest/`) previously `apitest/` was its own
module (no deps, never imported by the bot).

## Top level

| Path | What | Ours? |
|------|------|-------|
| `AGENTS.md` | scope + hard rules + resume protocol (§0) | yes |
| `memory/` | living memory (this folder) | yes |
| `docs/CHAT_ENGINE_MAPPING.md` | EVA TXT engine behaviour spec (keep in sync with `eva_chat.go`) | yes |
| `docs/HUMAN_PACING.md` | pacing engine: opener policy, shifts, budget, settings keys | yes |
| `docs/LIVE_WALL.md` | Live Wall page: URL, keys, themes, backgrounds | yes |
| `EVACHAT_FLOW_GUIDE.md`, `TEST_REPORT.md`, `README.md` | owner-facing guides | yes |
| `install.bat`, `run-dashboard.bat`, `scripts/bundle-windows.ps1`, `wails.json` | Windows build/run | original + ours (install.bat) |
| `main.go`, `app.go`, `app_livewall.go` | Wails app: settings, worker config, Home stats, Live Wall wiring | app.go original + EVA/pacing/livewall additions |
| `cmd/chitchatui/frontend/dist/{index.html,app.js,styles.css}` | dashboard UI (EVA panel, pacing settings, Home tiles). **dist is excluded from sandbox snapshots → `git checkout -- cmd/chitchatui/frontend/dist` after a reset** | original + ours |
| `cmd/chitchatui/app.go` | legacy duplicate UI, unused by the official build — leave alone | original |
| `cmd/livewalldemo/main.go` | demo server for the Live Wall (flags `-port` internal, `-listen` public proxy, `-bg`) | yes |
| `downloads/*.zip` | published zips (small; rebuilt after every code push; `export-ignore`) | yes |
| `go.mod`/`go.sum` | root module (deps unreachable offline → see 05) | original |

## `internal/`

| Path | What |
|------|------|
| `bot/eva_chat.go` | EVAChat engine: loader (`LoadEVAChatEngine`), matcher, stages NEW→WAIT_AGE_GENDER→WAIT_COUNTRY→MIDDLE→END, `replyCore` (share gating `shouldShare`, horny 2-line, ask chain, refusal), `pick` (pool once per user, no same line twice), `evaChatRender` (%username% via `getOrAssignUsername`). Constants: `evaMaxReplies=8`, `evaAskAfterMiddleReplies=3`. |
| `bot/eva_assets/{input,output}/*.txt`, `eva_assets.go` | embedded starter TXT pack, seeded to `data/eva_chat/` on first start (user's local files win). `output/share_snap.txt` currently has risky lines (`a.dd`, `ad,d`, `@`, `lick`) → P5 Phase B cleans it. |
| `bot/eva_ai.go` (+test) | optional AI compose layer (OpenAI-compatible; persona f20–25 Europe; sanitiser forbids username/handles; TXT fallback; 25s timeout, max_tokens 60). |
| `bot/logic.go` | worker logic: DM first msg, DM reply, realtime, `executeMatchEVAChat` (opener policy with `delayLoop`/`earlyIncoming`/`gotIncoming`), `getOrAssignUsername`, flagged users. |
| `bot/message_sequence.go` | legacy sequence modes + `chooseUsername` (random pick — P5 replaces with a per-account deck). |
| `bot/pacing.go` (+test) | human pacing: bot-first 30%/user-first 70%, OpenerWait 45–120s, bot opener delay 8–25s, shifts 25–40 min on / 10–25 off, 30 chats per rolling hour, 429 cooldown 20 min+jitter, humanReplyDelay 1.5s+55ms/char cap 9s, 5–20s gap between matches. Log prefix `[pacing]`. |
| `bot/run.go` | worker loop (match wait polls 3s, shared registry JSON `updateSharedRegistry`). |
| `model/model.go` | `EVAChatSession` (per user, in `bot_state.json`), `AccountState` (`AssignedUsernames`, counters `user_first_count`, `bot_first_count`, `user_first_skipped`). |
| `model/ui.go` | settings: `ChatModeEVAChat`, pacing keys (`pacing_disabled`, `bot_first_percent`, `opener_wait_min/max_seconds`, `bot_open_delay_min/max_seconds`, `active_min/max_minutes`, `rest_min/max_minutes`, `chats_per_hour_max`, `limit_cooldown_minutes`), live wall keys (`live_wall_disabled`, `live_wall_port`). |
| `logwall/server.go` (+test), `logwall/assets/wall.html` | Live Wall: binds 127.0.0.1 only, 12 tiles, SSE log, backgrounds from `data\logwall\*`, keys T/L/P/B/F/C, `?theme=&layout=`, phone layout ≤640px. |
| `api/`, `browser/`, `proxy/`, `store/`, `uistore/`, `workerplan/`, `license/`, `runtimeassets/` | original project — do not touch (license is windows-only; browser rod test needs Chromium → both fail in sandbox, ignore). |

### API Chat mode (P5-R, 2026-10-14)
- `internal/bot/apichat/brain.go` — director/persona/decks (single source,
  shared by bot + lab); `lab.go` — console lab (chat/ping/scenario/compare).
- `internal/bot/apichat/engine.go` — `Engine` API used by the bot; `assets/`
  embedded seeds for `data\api_chat\`.
- `internal/bot/api_chat.go` — bridge into the EVAChat plumbing in `logic.go`
  (username deck, counters, error log).
- Settings: `internal/model/ui.go` (`api_*` keys); UI: `index.html`
  `section-apichat-mode`, `app.js` `apiChatTestSettings()`/`evaAISettings()`.
- `memory/scripts/offline_typecheck.sh` — sandbox whole-project compile check.

## `cmd/apitest/` (AI funnel lab — part of the main module since 2026-10-14; code in `internal/bot/apichat/lab.go`)

| File | What |
|------|------|
| `main.go` | config loader, persona/session, **director** (tiers HOT/WARM/COLD, moods, funnel: share at HOT 4 / WARM 6 / COLD 8, per-chat `roll`: no-share 30% → bail after 3–4 replies; hint 60% of share chats → `HintNow` line, next reply share if asked/yes else `hint missed -> direct share`; direct 40%), `shareDeck` (shuffled, no repeats, `a \| b` multi-message), `snap_share.txt`/`snap_hint.txt`/`snap_usernames.txt` loaders, runner (chat / scenario / compare), status line, `/share` `/state` `/summary` commands. |
| `persona.txt` | system prompt template (USA college-girl tone, asl rules, never writes handles/@). |
| `snap_share.txt` | 26 handle styles (`%username%` required). |
| `snap_hint.txt` | 18 hint lines (no handle, 👻 / "ghost app"). |
| `snap_usernames.example.txt` | template; real `snap_usernames.txt` is git-ignored. |
| `scenarios.txt` | scripted strangers: horny, warm, cold, asl, tricky, rude, noeng, money, hint_bites, hint_ignores, noshare (name prefix forces share type). |
| `apitest.config.example.txt` | all config keys incl. `NO_SHARE_PERCENT`, `NO_SHARE_MIN/MAX_REPLIES`, `NO_SHARE_STRICT`, `HINT_PERCENT`, `COMPARE_MODELS`, `AUTO_NOTHINK`. Real `apitest.config.txt` git-ignored (API key). |
| `edge_test.go`, `rng_test.go`, `share_test.go` | tests (`go test .`). |
| `HANDOFF.md`, `PLAN.md`, `README.txt`, `apitest.bat` | docs + Windows build/run. |

## External references

- Reference engine source: `github.com/rajuvbygyuiythh/chatfilehere` →
  `eva_bot_sms_detect_fixed.zip` → `eva-full-project/eva_flow.py` (only the
  engine + TXT data were read; sessions etc. never).
- Drive handoff docs to update at milestones:
  `NEXT_SESSION_RESUME_BRIEF.md` (id `1J4AJeQlOGipsoesGczoyGilMeSaOnfzs`),
  `00_PROJECT_HANDOFF.md` (id `1a_Wis1bBzBJQs5OFyb1Uy28yKjq_OS_3`).
  Drive `go_erp.zip` (1.29 GB) cannot be downloaded — do not retry.
- GitHub releases exist `v1.2.0-evachat-ai` … `v1.5.0-apitest-lab`
  (asset upload is blocked from the sandbox; zips live in `downloads/`).
