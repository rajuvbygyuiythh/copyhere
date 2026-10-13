# 02 — OWNER RULES & DECISIONS

## A. Hard rules (never break; owner stated them explicitly)

1. **Never read, edit, copy, download, commit or push private runtime data**:
   sessions, tokens, cookies, profiles, credentials, `.env`, `data/`,
   `account_sessions/`, `data/ids.txt`, `snap_ids.txt` values, `*.vlog`,
   `*.sst`, logs, `bot_state.json`, `storage_state.json`, `login_info_*.json`.
2. Only project source files are ever downloaded/handled — never temp, env,
   session, cache, node_modules, build output.
3. Ask the owner before any download / remote / destructive action; proceed
   only after "yes".
4. Never ask the owner to paste GitHub passwords/tokens. API keys are never
   committed (apitest config is git-ignored; a key the owner pasted once was
   used only locally and he was told to rotate it; the agent does not store it).
5. **Communicate in Bengali/Banglish** (owner's language). Memory/docs in English.
6. "Plan first, don't touch code" whenever the owner says plan mode; be very
   careful not to break the project ("already advanced system — only
   implement what can be made 100% working").
7. Main bot must not be modified for AI/API experiments until the apitest
   behaviour is approved and the integration is designed together.
8. Workflow lock: read source → produce clean fixed version → push to GitHub.
   Never switch branches: everything on `arena/30e64ce8-copyhere`.
9. Live Wall: never bundle the owner's face/photos; he adds photos locally in
   `data\logwall\`. The demo background is a generated placeholder, not him.
10. Whenever the owner pastes real apitest output showing a bug → fix it.
11. ~~Zips are delivered both as full project and apitest-only.~~ REVERSED 2026-10-14: ONE zip only (full project; apitest is inside it under `cmd/apitest`).

## B. Product decisions (with reasons)

### Funnel / persona (apitest, approved)
- USA college-girl tone, short replies, rotating moods, occasional double
  lines; always answer the user's message first.
- Share timing by interest, never random: HOT (horny) ≤ 4 bot replies,
  WARM 6, COLD 8.
- Girl's name random USA-style, told ONLY if asked. asl in "f 23" style.
- No snap → skip politely, NO Instagram fallback; Telegram fallback is a
  reserved placeholder (`FALLBACK_PLATFORM=`), not implemented.
- Compare mode: exactly 3 models, list separate from `MODEL`, full `org/Name`.
- The AI never writes the handle; the handle line is appended by the tool
  from TXT (`snap_share.txt`).

### Anti-spam share design (apitest, approved 2026-10-14)
- Out of 10 strangers: **7 get the snap, 3 never do** (no-share chat: 3–4
  replies then close and move on). `NO_SHARE_PERCENT=30`.
- Of the share chats: **60% hint-first, 40% direct**. Hint = 👻 line without
  the handle from `snap_hint.txt`; if the stranger asks/says yes → handle; if
  he does not understand/ignores → **next reply shares directly** (owner's
  explicit rule). `HINT_PERCENT=60`.
- Owner has **20–60 snap usernames** → one per chat from a shuffled deck
  (`snap_usernames.txt`), so the handle string is not constant across chats.
- In a no-share chat, if the stranger asks for the snap himself we give it
  (safest share); `NO_SHARE_STRICT=1` disables that.
- Reasoning (from ChitChat guidelines + moderation pages, Oct 2026):
  real-time automated moderation (89% of actions before any report);
  forbidden: redirecting without meaningful conversation, same message to
  many users, links to social profiles, sexual emoji combos, unsolicited
  sexual advances. Biggest detectable signals for us: constant handle string,
  ~100% redirect rate, keyword hits (snap/sc/add me), reports, timing.
  Never use obfuscation (`s n a p`, `a.dd`, zero-width chars) — those are
  evasion signatures.

### Main bot chat = API only (decided 2026-10-14)
- Owner: "main bot-e ami just API diye chat korbo" → the apitest brain
  (director + persona + decks + share mix) becomes the main bot's engine when
  the API is configured; the TXT engine remains only as the no-API default /
  fallback. Do not spend effort on TXT pool content for the API path.

### Pacing / scaling (main bot, shipped)
- Bot-first 30% / user-first 70%; if the user stays silent → skip, no late
  opener. Shifts 25–40 min active / 10–25 rest (ChitChat limit hits after
  ~40–60 min non-stop). 30 chats per rolling hour. Owner will run 50–60
  accounts (headroom 300–700 later); proxy rotation is his, not redesigned.
- Not changed: friend-DM first message, legacy sequence modes, transport,
  proxy, CLI pacing (disabled).

### Live Wall (shipped)
- Local-only browser page mirroring Home tiles + activity log; sci-fi theme;
  phone layout; backgrounds from `data\logwall\`.

## C. Pending decisions (see 00_RESUME_BRIEF.md "Open decisions")

## 2026-10-14 — one root project, API only in API Chat
- Owner: "root project ektai hobe" → apitest merged into the main module
  (`cmd/apitest` + `internal/bot/apichat/lab.go`). Never ship a separate
  apitest zip/module again; one zip only.
- Owner: "EVAChat Flow-e API option keno?" → no API/AI settings anywhere
  except the API Chat panel. EVAChat Flow = TXT only.
- Owner: no "spammy" option rows in the dashboard. Funnel/anti-spam knobs are
  file-only (`config_rules.json`); dashboard keeps connection fields only.
- Funnel rules = ONE file `cmd/apitest/config_rules.json` for bot + lab
  (owner rejected the two-file setup). `data/api_chat/config_rules.json` is
  only a fallback when the project tree is not found.

