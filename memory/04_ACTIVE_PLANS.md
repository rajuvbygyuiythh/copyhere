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

## P5 — Port the share-mix to the MAIN bot — PROPOSED (plan delivered, awaiting 4 answers)

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
