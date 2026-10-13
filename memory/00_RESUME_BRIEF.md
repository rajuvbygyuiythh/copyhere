# 00 — RESUME BRIEF (always current; last update: 2026-10-14 (P5-R shipped), Asia/Dhaka)

## Where we are (one paragraph)

Repo `rajuvbygyuiythh/copyhere`, work branch **`arena/30e64ce8-copyhere`**
(session is pinned to this branch; never switch). PR #1 open from it. Latest
commits: `598f740` apitest share-mix, `bfdb573` downloads zips. The main bot
(ChitChat Bot, Go + Wails, Windows) has: EVAChat Flow TXT engine (1:1 port),
optional AI compose layer, human pacing (user-speaks-first 70/30, shifts,
hourly budget), Live Wall browser page. The separate **`apitest/`** lab
(Featherless / OpenAI-compatible) now has the full anti-spam share design:
`snap_share.txt` deck, 30% no-share chats, hint-first 60/40, one username per
chat from `snap_usernames.txt`. Owner tests apitest on his PC against the real
API and pastes output; agent fixes bugs from that output.

## Last thing done (most recent turn first)

0000. Owner: "one physical file, fix" → bot now reads
      **`cmd\apitest\config_rules.json` directly** (`apichat.FindProjectRules()`
      walks up ≤6 levels from cwd and exe dir; `Options.RulesPath`;
      `EnsureData(dir, skip...)` does not seed a second copy into
      `data\api_chat\` when the project file exists). Fallback to
      `data\api_chat\config_rules.json` only if the project tree is absent.

000. Owner: dashboard must stay clean → funnel knobs (share HOT/WARM/COLD,
     no-snap %, hint %, min/max, strict) REMOVED from the API Chat panel and
     from `MessageSettings`; they live in **`config_rules.json`**
     (`internal/bot/apichat/rules.go`, embedded default in `assets/`, seeded
     to `data\api_chat\config_rules.json`; lab reads
     `cmd\apitest\config_rules.json`, overriding apitest.config.txt keys).
     Dashboard API Chat panel = URL / model / key / Test API / Open folder
     only. Test API + bot start log print the loaded rules.

00. **Owner angry about two things → both fixed:** (a) the old hybrid "AI
    reply text" block inside EVAChat Flow is REMOVED from the UI (API
    settings exist only in the API Chat panel; EVAChat = pure TXT; `eva_ai.go`
    dormant, `ai_enabled` always false). (b) **ONE root project**: `apitest/`
    (own module) is gone; its CLI is `internal/bot/apichat/lab.go` +
    `cmd/apitest/` (thin main + data files + bat). Only ONE zip now
    (`downloads/ChitChatBot-full-source.zip`); `apitest-only.zip` deleted.
    Do NOT recreate a separate apitest module or zip.

0. Owner said the API mode is not visible → it IS in the pushed UI; he was
   on an old exe. Added "UI build 2026-10-14 · 4 chat modes" tag on the
   Message header. If he still reports 3 modes: he must re-download the zip
   and rebuild (UI is go:embed-ed into the exe).
0b. **P5-R SHIPPED — "API Chat" = 4th chat mode on the Message page.**
   New `internal/bot/apichat/` (apitest brain ported 1:1 + `Engine`),
   `internal/bot/api_chat.go` bridge, model/settings/app.go/frontend wiring,
   data folder `data\api_chat\`. All vet/tests green via the offline stub
   build (`memory/scripts/offline_typecheck.sh`). Owner has NOT yet built it
   on Windows → next message is probably a build/run log to debug.
1. **Memory folder created** (`memory/`, this file) + `AGENTS.md` §0 resume
   protocol. Owner asked for real-time memory so no re-explaining is needed.
2. **Plan delivered (not coded)**: port the share-mix to the MAIN bot
   (Phase A–D, see `04_ACTIVE_PLANS.md` → "P5"). Found that the main bot's
   bundled `eva_assets/output/share_snap.txt` contains filter-evasion style
   lines (`a.dd`, `ad,d`, `@ %username%`, `lick me`) and that the main bot
   redirects in ~100% of chats — the real risk is there, not in apitest.
3. `598f740` apitest share mix (no-share 30% / hint 60% / direct 40%,
   username deck, scenarios `hint_bites`, `hint_ignores`, `noshare`, 9 tests).
4. `be22ddc` apitest `snap_share.txt` deck (26 styles, `a | b` two-message
   styles, `/share` preview).

## Open decisions waiting on the owner (ask these first)

- **Caps** (per-handle 30/day, per-account 12 shares/hour) still
  unconfirmed → not implemented (OFF). Counters are stored but not shown on
  Home/Live Wall yet.
- (history) **P5 REVISED (2026-10-14)**: owner said *"main bot-e ami just API diye chat
  korbo — only API chat logic/rules, nothing else"*. So P5 is no longer
  "port share-mix into the TXT engine"; it is **"make the apitest brain
  (director + persona + decks + share mix) the main bot's chat engine when
  API is on; TXT engine only as fallback"**. Plan P5-R written in
  `04_ACTIVE_PLANS.md`; waiting for the owner's "go" (+ whether to keep the
  per-handle/per-account caps, proposed 30/day and 12/hour).
- Older, still unanswered: "chat list ato long → short" — (a) compact console
  status line in apitest, or (b) share snap earlier (HOT 4/WARM 6/COLD 8)?
- Owner has NOT yet pasted a real-API run of the new share-mix build
  (`scenario hint_bites` / `noshare`); expect a debug round when he does.

## Exact next steps (in order)

1. Wait for the owner's Windows build/run of API Chat mode; fix whatever the
   pasted log shows (`[api]` lines in the dashboard log, `data\api_chat\errors.log`).
2. Then P5-R leftovers: caps (if confirmed), API counters on Home + Live Wall.
3. Later (Step 4 leftovers): silent-user follow-up, AI lead-in on the share
   turn in the main bot (as apitest does), reply typing delays review.
4. Update the Drive handoff docs when a milestone lands (ids in
   `01_PROJECT_MAP.md`).

## Sandbox state (may be gone after a reset — see `05_ENV_RECIPES.md`)

- Go toolchain `/home/user/gotool/go` (1.27.2 via PyPI `go-bin`); if missing
  reinstall (recipe in 05). Whole-project offline type-check/build:
  `bash memory/scripts/offline_typecheck.sh` (stubs all external deps in
  `/tmp/_gotest`; proves compile, not a release binary).
- Live Wall preview process may be dead; recipe in 05 (`livewalldemo -port
  47401 -listen 0.0.0.0:47400`).
- After a reset: `git fetch origin && git reset origin/arena/30e64ce8-copyhere
  && git checkout -- cmd/chitchatui/frontend/dist` (dist is dropped by
  snapshots; local HEAD rolls back to `c2c30d5`).

## Download links the owner uses

- full source: `https://github.com/rajuvbygyuiythh/copyhere/raw/arena/30e64ce8-copyhere/downloads/ChitChatBot-full-source.zip`
- branch archive (always latest): `https://github.com/rajuvbygyuiythh/copyhere/archive/refs/heads/arena/30e64ce8-copyhere.zip`
- Rebuild zips after every code push (commands in 05). `downloads/` is
  `export-ignore` so the full zip never nests itself.
