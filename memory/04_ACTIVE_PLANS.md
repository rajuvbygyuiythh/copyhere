# 04 — ACTIVE PLANS (status: DONE / APPROVED / PROPOSED / LATER)

## P1 — EVAChat Flow port + AI compose layer — DONE
`3b42d6c`, `c424251`. Spec `docs/CHAT_ENGINE_MAPPING.md`.

## P2 — apitest lab (chat-improvement steps 1–3) — DONE
Director, persona, scenarios, compare, debug passes (`da13e90` … `ceb3638`).
Step 4 (main-bot integration, typing delays, silent-user follow-up) → P6.

## P3 — Human pacing phase 1 — DONE
`2869017`, `01601e8`. Docs `docs/HUMAN_PACING.md`. Phase 2 (registry
contention at 700 accounts) → LATER.

## P4 — apitest anti-spam share design — DONE (2026-10-14)
`be22ddc` snap_share deck, `598f740` share mix (no-share 30%, hint 60/40,
username deck). Awaiting the owner's real-API run for a debug round.

## P5-R — API Chat mode in the MAIN bot — SHIPPED steps 1–5 + 7 (2026-10-14)

Owner approved ("humm perfect") and asked for the API option INSIDE the
Message page as a 4th chat mode. Shipped (commit after `1ff392b`):
- `internal/bot/apichat/` — `brain.go` = verbatim port of apitest director /
  persona / decks / roll / sanitise / polish (CLI parts dropped), `engine.go`
  = bot-facing `Engine` (`New`, `Start`, `Reply`, `Test`, `Forget`,
  `EnsureData`), embedded seeds in `assets/`, tests = apitest tests +
  `engine_test.go` (mock HTTP server: direct / hint→direct / no-share close /
  no-username close / API error fallback→close / restart turn numbering).
- `internal/bot/api_chat.go` — bridge: `apiChatStart/Reply` behind
  `evaChatStart/Reply` when `ChatMode == api_chat`; username deck per account
  (`APIUsernameOrder/Cursor` in `AccountState`), counters `APIShareDirect/
  Hint/HintBite/NoShareClose/UserAsked/Errors`, `data/api_chat/errors.log`.
- `internal/model/ui.go` — `ChatModeAPIChat = "api_chat"`, settings
  `api_settings_saved`, `api_share_hot/warm/cold`, `api_no_share_percent`,
  `api_hint_percent`, `api_no_share_min/max_replies`, `api_no_share_strict`
  (base URL / key / model reuse `ai_base_url/ai_api_key/ai_model`).
- `app.go` — `SaveMessageSettings` accepts `api_chat`; worker config loads
  `apichat.New(data/api_chat, opts)` (start fails with a clear message when
  URL/key/model empty); `TestAPIChat(settings)`, `OpenAPIChatDataFolder()`.
- Frontend — 4th radio "API Chat" + `section-apichat-mode` (URL/model/key,
  Test API, Open folder, HOT/WARM/COLD, no-share %, hint %, min/max, strict).
- Data folder `data\api_chat\{persona.txt,snap_share.txt,snap_hint.txt}`
  seeded on first start / folder open; owner's edits win.
REMAINING: step 6 caps (per-handle 30/day, per-account 12/h — still
unconfirmed, OFF), counters on Home tiles + Live Wall (stored in
bot_state already, not displayed yet), AI lead-in share lines are already in
(from apitest). Owner must build on Windows (`install.bat`) and run a real
chat; expect a debug round from pasted logs.

### Original P5-R plan text (for reference)

Owner decision 2026-10-14: in the main bot he will chat ONLY through the API.
So the apitest brain becomes the main bot's engine when `ai_enabled` is on:
1. New package `internal/bot/apichat/` = port of `apitest/main.go` logic:
   director (tiers, moods, roll no-share/hint/direct, share timing, memory of
   stranger facts, retry/sanitise/polish), persona session (f 20–25, lore),
   decks (`snap_share.txt`, `snap_hint.txt`, usernames). Pure logic + one
   OpenAI-compatible client (reuse `eva_ai.go` http/sanitiser pieces).
2. Wiring in `internal/bot/logic.go` at the 5 EVA call sites
   (`evaChatStart` ×3, `evaChatReply` ×2): if API chat is on → apichat engine
   decides flow AND text; TXT engine untouched and still used when API is off.
   Per-user state inside `EVAChatSession` (new `API` sub-struct: history,
   director fields, snapUser, shareType, bailAt, hinted…) → `bot_state.json`.
3. Data files in `data\eva_chat\` (seeded from embedded assets, owner's copies
   win): `persona.txt` (apitest version), `snap_share.txt`, `snap_hint.txt`.
   Usernames = dashboard Usernames list → per-account shuffled deck.
4. Settings (dashboard, same pattern as pacing): existing `ai_*` + `share_hot/
   warm/cold` (4/6/8), `no_share_percent` 30, `hint_percent` 60,
   `no_share_min/max_replies` 3/4, `no_share_strict` 0.
5. Failure policy: API error/timeout → one retry → tiny built-in fallback
   lines (never silent) → if two failures in a chat, polite close. Log `[api]`.
   Pacing's humanReplyDelay applies; two-message share styles go as two sends.
6. Caps (if owner keeps them): per-handle daily cap 30 (shared registry),
   per-account 12 shares/hour → extra chats become no-share.
7. Counters → Home + Live Wall: share_direct, share_hint, hint_bite,
   no_share_closed, user_asked_first, api_errors.
Tests: port apitest tests + mock-server test for the client. Not touched:
TXT engine behaviour, legacy modes, pacing, proxy, transport.
Dropped from old P5: Phase B pool cleanup (TXT pools irrelevant when API on;
only fallback lines matter).

## (old) P5 — Port the share-mix into the TXT engine — SUPERSEDED by P5-R

Why: live accounts run the main bot, which redirects in ~100% of chats and
ships risky share lines. apitest is only the lab.

**Phase A — share types in `internal/bot/eva_chat.go`**
1. `EVAChatSession` new fields: `ShareType` (none/hint/direct), `BailAt`
   (3–4), `Hinted bool`; rolled once per chat (same logic as apitest
   `director.roll`), persisted in `bot_state.json`.
2. NO-SHARE: never arm `AskNext`, never fire `ask_snap`; horny/pic pool picks
   that contain snap/👻 keywords are re-picked; `BotReplyCount >= BailAt` →
   `closer.txt` → END → next match. Stranger asks himself → share unless
   `no_share_strict`.
3. HINT: new pool `output/snap_hint.txt` replaces the `ask_snap.txt` pick at
   the ask step; next user message: asked/yes → `share_snap`; negative →
   existing `retry_nudge`; anything else → share directly. Missing pool →
   behaves like direct (never breaks).
4. DIRECT: today's flow unchanged.
5. Username deck: replace random `chooseUsername` with a per-account shuffled
   deck (cursor in `AccountState`), source = dashboard Usernames list.
6. Settings keys (same pattern as pacing, always serialised):
   `no_share_percent=30`, `hint_percent=60`, `no_share_min_replies=3`,
   `no_share_max_replies=4`, `no_share_strict=0`.

**Phase B — clean the bundled pools (`internal/bot/eva_assets/output/`)**
7. `share_snap.txt` → the 26 clean apitest styles (drop `a.dd`, `ad,d`,
   `@ %username%`, `lick`); `ask_snap.txt` → mixed wording (less `snp`);
   add `snap_hint.txt`. Owner's local `data\eva_chat\output\` overrides the
   bundle → ship the files in the zip + install note; merge his own lines.

**Phase C — caps**
8. Per-handle daily cap (shared registry JSON across accounts; proposed 30
   shares/handle/day): capped handle leaves the deck; all capped → chat
   becomes NO-SHARE.
9. Per-account share cap per hour (proposed 12): reached → NO-SHARE for the
   rest of the hour (next to the 30 chats/hour budget).

**Phase D — visibility**
10. Counters `share_direct`, `share_hint`, `hint_bite`, `no_share_closed`,
    `user_asked_first` → Home tiles + Live Wall tiles + `[share]` log prefix.

Files: `internal/bot/eva_chat.go`, `internal/model/model.go`,
`internal/bot/logic.go`, `internal/bot/message_sequence.go`, `app.go`,
`internal/bot/eva_assets/output/*`, `cmd/chitchatui/frontend/dist/{app.js,index.html}`,
`internal/logwall/*`, tests, `docs/CHAT_ENGINE_MAPPING.md`.
Not touched: transport, proxy, realtime WS, legacy sequence modes,
friend-DM opener, pacing engine.

Open questions → see `00_RESUME_BRIEF.md`. Suggested order: A+B first PR,
then C, then D.

## P6 — Step 4 leftovers — LATER
Silent-user follow-up; AI lead-in on the share turn in the main bot (apitest
style, TXT handle appended); typing-delay review; registry contention
(Phase 2 scaling).

## P7 — small open item
"chat list ato long → short": (a) compact apitest console status
(`COMPACT=1`), or (b) earlier share thresholds. Owner has not answered.
