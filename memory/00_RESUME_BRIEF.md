# 00 — RESUME BRIEF (always current; last update: 2026-10-14, Asia/Dhaka)

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

- **P5 main-bot share-mix**: approve Phase A+B as first PR? per-handle daily
  cap (proposed 30) and per-account share cap/hour (proposed 12)? replace his
  local `data\eva_chat\output\share_snap.txt` with the clean 26-style list
  (merge his own lines if any)? use apitest `snap_hint.txt` verbatim as the
  main-bot hint pool?
- Older, still unanswered: "chat list ato long → short" — (a) compact console
  status line in apitest, or (b) share snap earlier (HOT 4/WARM 6/COLD 8)?
- Owner has NOT yet pasted a real-API run of the new share-mix build
  (`scenario hint_bites` / `noshare`); expect a debug round when he does.

## Exact next steps (in order)

1. Wait for the P5 answers → implement Phase A (`eva_chat.go` share types,
   `model.go` session fields, username deck in `message_sequence.go`,
   settings keys in `app.go` + frontend) + Phase B (clean pools, add
   `snap_hint.txt` asset) in one commit; tests + Windows build; push; rebuild
   `downloads/*.zip`; update this memory.
2. Then Phase C (per-handle daily cap in shared registry, per-account share
   cap/hour) and Phase D (counters → Home tiles + Live Wall + `[share]` log).
3. Later (Step 4 leftovers): silent-user follow-up, AI lead-in on the share
   turn in the main bot (as apitest does), reply typing delays review.
4. Update the Drive handoff docs when a milestone lands (ids in
   `01_PROJECT_MAP.md`).

## Sandbox state (may be gone after a reset — see `05_ENV_RECIPES.md`)

- Go toolchain `/home/user/gotool/go` (1.27.2 via PyPI `go-bin`); if missing
  reinstall (recipe in 05). `/home/user/_gotest` (root-pkg build dir) and
  go-toast stub are usually gone → recreate only when a root build is needed.
- Live Wall preview process may be dead; recipe in 05 (`livewalldemo -port
  47401 -listen 0.0.0.0:47400`).
- After a reset: `git fetch origin && git reset origin/arena/30e64ce8-copyhere
  && git checkout -- cmd/chitchatui/frontend/dist` (dist is dropped by
  snapshots; local HEAD rolls back to `c2c30d5`).

## Download links the owner uses

- apitest only: `https://github.com/rajuvbygyuiythh/copyhere/raw/arena/30e64ce8-copyhere/downloads/apitest-only.zip`
- full source: `https://github.com/rajuvbygyuiythh/copyhere/raw/arena/30e64ce8-copyhere/downloads/ChitChatBot-full-source.zip`
- branch archive (always latest): `https://github.com/rajuvbygyuiythh/copyhere/archive/refs/heads/arena/30e64ce8-copyhere.zip`
- Rebuild zips after every code push (commands in 05). `downloads/` is
  `export-ignore` so the full zip never nests itself.
