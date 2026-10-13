# Human pacing — opener policy, shifts, limit cooldown

Why: ChitChat throttles an account after roughly 40–60 minutes of non-stop
chatting, and a bot that opens every match within a second is the loudest bot
signal there is. Phase 1 of the "strong setup" plan adds, in a single new file
plus small hooks, three behaviours:

| Behaviour | Default | Where |
|---|---|---|
| **User speaks first** in most matched chats; the bot only replies. Silent user → skipped (next match). | bot opens 30 % / user 70 %, wait 45–120 s | `executeMatchEVAChat` (`internal/bot/logic.go`) |
| **Bot opener delay** when the bot does open | 8–25 s | same |
| **Shifts**: active window → rest window, randomised per account; rest pauses NEW matches only, DM replies continue | active 25–40 min, rest 10–25 min | `pacer.gate()` in `internal/bot/run.go` |
| **Hourly chat budget** per account | 30 matched chats / rolling hour | `pacer` |
| **Platform limit (HTTP 429)** → cooldown instead of counting towards "dead" | 20 min + up to 50 % jitter | `match()`, EVA sends, `registry.go` |
| **Human reply delay** when the dashboard reply delay is 0 | 1.5 s + 55 ms/char (cap 9 s) + jitter | `humanReplyDelay` |

Everything lives in `internal/bot/pacing.go`; `Pacing` is copied from
`GeneralSettings` in `app.go` (`pacingFromSettings`). Turning the Setting-tab
switch off restores the exact previous behaviour (bot opens instantly, no shifts).

## Counters

* Home / Live Wall: **User first**, **Bot first**, **Resting** (resting or
  limited workers). Counters are persisted per account in `bot_state.json`
  (`user_first_count`, `bot_first_count`, `user_first_skipped`).
* Worker phases are exported through `bot.SetWorkerPhase` /
  `bot.RestingWorkerCount()` and cleared on Stop.

## Settings (Setting tab → "Human pacing")

`pacing_disabled`, `bot_first_percent`, `opener_wait_min/max_seconds`,
`bot_open_delay_min/max_seconds`, `active_min/max_minutes`,
`rest_min/max_minutes`, `chats_per_hour_max`, `limit_cooldown_minutes`.
Zero / missing values fall back to `model.DefaultPacing()`.

## Not changed on purpose

* Friend-DM first messages (`processDMAutoResponses`) — the user initiated the
  friend request, so the bot greeting there is natural.
* Legacy Single/Folder sequence and keyword match modes keep the old opener.
* Anti-detect transport (utls fingerprint, transport-encoding, nonce, typing
  indicator) untouched.
* CLI worker (`cmd/chitchatbot`) uses `DefaultConfig` → pacing disabled there.

## Log lines to look for

```
Human pacing on: bot opens 30% of matches, shift 25m0s-40m0s active / 10m0s-25m0s rest, max 30 chats/hour
[pacing] Letting <user> speak first (up to 87 seconds, silent = skip)...
[pacing] <user> opened the chat. Replying.
[pacing] <user> stayed silent for 87 seconds. Skipping to the next match.
[pacing] Bot opens this match: waiting 14s before the opener.
[pacing] shift finished: pausing new matches until 13:42:10 (DM replies continue).
[pacing] Platform limit detected. Cooling down until 14:05:33.
```

Tests: `go test ./internal/bot -run 'Pacer|Opener|WorkerPhase'`.
