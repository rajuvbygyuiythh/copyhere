# Browser mode (Browser page → "Run mode" cards)

The Browser page has two separate run-mode cards; clicking one saves it at
once and the Start log prints `Browser page run mode: 1. CLASSIC ...` or
`... 2. BROWSER MODE ...`. Stored as `browser_mode` (bool) in the settings.

**1. Classic (default):** nothing changes. The browser opens only when a login is
missing/expired, `login_info.json` is saved, the window closes, and the bot
chats with Go http + Go websocket as before.

**2. Browser mode:** one real browser window per account stays open from Start until Stop
and every request goes through that page.

| Step | What happens | Log prefix |
|------|--------------|-----------|
| Start | `profiles/<account>/browser-data/<backend>` opened **visible** (Headless ignored). Proxy picked once per window, fixed. | `[browser] Browser mode on: opening visible ... window` |
| Login | Profile already logged in → continue. Otherwise saved cookies from `login_info.json` are injected and the page reloaded; if still logged out the bot waits for you to log in in the window. `login_info.json` is (re)written when authenticated. | `[browser] Injecting the saved session`, `Waiting for the site to be logged in`, `Logged in as <id>` |
| Chat | Same loop, same modes (EVAChat Flow / API Chat / ...). `api.Client` executes every request with the page's own `fetch()` (`credentials: "include"`): real cookies, TLS, UA, proxy. Browser-managed headers (Cookie/User-Agent/Origin/Referer/Sec-*) are stripped before the call. Websocket listener is off; a **message poller** (`browser_events.go`) feeds the same `matchUpdate` / `chatMessage` / closed events the socket would, from three sources: (1) the SPA's own socket frames captured in-page by the WebSocket tap, (2) `GET /match/active` + message reads, (3) the DM conversation list — see "Browser match detection" below. Outgoing messages are `POST …/conversations/{id}/messages` through the page once a source supplied the id. | normal logs + `[browser] Match detected via …` |
| You click around in the window | If an in-page request fails because the page left the app, the executor navigates back to the app once and retries. Do not close the **tab** (chromedp cannot recover it; rod reopens one). | — |
| 401 | Not fatal: window navigated back to the app, bot waits for a new login, then resumes with the fresh session. | `[browser] Session expired. Log in again inside the browser window` |
| Window closed by you | Detected by the error text (websocket/EOF/…) or, for any other error, after 3 consecutive failures by a 5 s probe of the window. That worker stops with one line; other accounts keep running. | `[browser] browser window closed or unreachable ... Stopping this worker` |
| Stop | Session captured once more (fresh cookies/storage) → `login_info.json` saved → window closed. Next Start logs in automatically. | `[browser] Session saved to ...`, `Browser window closed.` |

Robustness: in Browser mode a failed cycle (any non-fatal error) is logged as
`[browser] Cycle failed (...) Window stays open; retrying` and retried after
the normal poll wait; only 10 failures in a row stop the worker. Classic mode
keeps its old behaviour (stop on error).

Safety: a quick Stop → Start on the same account waits for the old window to
finish closing (`Previous window of this profile is still closing; waiting`),
so two Chromes never share one profile folder.

Limits (phase 1): one Chrome per account (~300–500 MB RAM) → meant for a few
accounts, the dashboard warns above 10. Replies arrive with the poll latency
(≤ 5 s) instead of instantly. Messages are sent through the site's API from
inside the page, not by typing into the DOM (DOM typing = phase 3 in
`memory/04_ACTIVE_PLANS.md`). Concurrency: all in-page fetches of one window
are serialised (chat loop + poller share one CDP session).

Verification available offline: `bash memory/scripts/offline_typecheck.sh`
(bot/api tests incl. executor + poller), `bash memory/scripts/browser_pkg_check.sh`
(real rod/chromedp compile + browser tests), `node memory/scripts/ui_smoke.js`
(Browser form round-trip in jsdom).

Code: `internal/bot/browser_mode.go` (window lifecycle, executor),
`internal/bot/browser_events.go` (poll → events relay),
`internal/api/client.go` (`Executor`, `NewVia`), `internal/browser/*`
(`Navigate`, `SetCookies`), `internal/bot/run.go` (branches on
`Config.BrowserMode`), `app.go` (setting → config), Browser page in
`cmd/chitchatui/frontend/dist/index.html` + `app.js`.

## Live Browser v2 (2026-10-14)

**Run Fresh Browsers follows the run mode.** Classic: fresh window → login →
`login_info_<id>.json` saved → window closes (as before). Browser mode: the
same window **stays open** and a live worker starts chatting in it at once
(`[acct] Live window worker started`); Stop saves the session and closes it.
A later Start skips accounts that are already chatting in a live window.

**Every close is explained**: `[browser] Closing window — reason: Stop pressed
| window closed or unreachable | account banned | worker error: …`.

**Stealth (all windows, all modes, always on)**
- launch flags: `--enable-automation` removed (no `navigator.webdriver`, no
  "controlled by automated software" bar), `--disable-blink-features=AutomationControlled`,
  `--lang=en-US`, realistic window size per profile, WebRTC
  `disable_non_proxied_udp` (no local/VPN IP leak behind a proxy);
- CDP emulation per page: timezone **auto-set from the window's proxy exit
  country** before launch (backend geolocates the exit IP through the proxy
  itself — `internal/proxy.ResolveTimezone`; there is no dashboard timezone
  setting any more. Proxy line suffix `|tz=America/Chicago` remains as a
  manual override; no proxy / lookup failure → the PC's own timezone) +
  locale `en-US`. Watch the `[timezone] …` log line per window;
- init script before any page script: webdriver=false, chrome.runtime stub,
  languages/plugins non-empty, notifications permission consistent. No
  canvas/WebGL spoofing on purpose (inconsistent spoofs are a stronger signal).
- after login: `[browser] check: webdriver=false timezone=… languages=…
  platform=… screen=… -> OK|WARN …` — what the site's JavaScript sees.

**Low memory (always on)**: `renderer-process-limit=2`, no extensions /
component updates / background mode / speech API / pings, JS heap cap 256 MB
→ roughly 150–250 MB per window instead of 300–500. A shared-Chrome
per-account-context backend (~70–120 MB/account) is the next phase.

## Real-client parity (2026-10-14, from the owner's DevTools captures)

Captured from the real ChitChat web app (Chrome, 2026-10-09) and now mirrored
by the bot in **both** Classic and Browser mode (`internal/bot/client_sync.go`):

| Real client does | Bot now does |
|---|---|
| `POST /match` with `accept: application/json` + header `response-time-id` | same; header = current user id XOR-encoded with the transport key + fresh 8-char salt (same scheme as `transport-encoding`, verified by decoding 25 captured values) |
| `PATCH /match/disconnect` (also with `response-time-id`) before the next `POST /match` | `match()` sends the disconnect first whenever the account was inside a match (`noteActiveMatch` / `takeActiveMatch`); a closed-match `matchUpdate` clears the note |
| `PATCH /users/me/conversations/{id}/read` for every partner message shown | `withReadReceipts` relay around the event channel: 0.3–1.2 s after a partner `chatMessage`, debounced 1.5 s per conversation, best effort |
| socket: `40{"release":…}` then `42["presenceSync"]` immediately and every 30 s | `realtime.go` emits `presenceSync` on connect + 30 s ticker (write mutex shared with pongs) |
| app load: `GET /match/active`, `GET /moderation/standing` | `logAccountStanding` at worker start → `[standing] GOOD — no warnings, no limits.` or `[standing] WARN standing=… pendingWarnings=… activeViolations=… limitedFeatures=…` |
| fingerprint `hardwareConcurrency` = the PC's real core count (28 on the owner's PC, identical for every account) | stealth init script pins `navigator.hardwareConcurrency` to a stable per-profile value (4/6/8/12/16, `browser.PickHardwareConcurrency`) |

Other capture facts worth remembering: message send rate limit is 10 per 3 s
(`x-ratelimit-limit: 10`, reset 3), `GET …/messages` 500 per minute; the server
sends `messageEdited` with `flags=1` when it redacts a flagged text (e.g.
"Horny?"); the `transport-encoding` fingerprint schema in the bot matches the
real one field for field (`userAgent … hardwareConcurrency`), and the fingerprint
carries the browser timezone — so the timezone emulation shipped in Live Browser
v2 also fixes the value the site receives on every message.

All of this is best effort: a failing parity call is logged once per account
(`[sync] …`) and never stops the worker. Request bodies of `/read` and
`/match/disconnect` were not in the capture; the bot sends `{}` (same as the
working `/typing` call). If the live log shows `[sync] … -> 400 …`, send that
line and the body will be adjusted.

## Debug layer (2026-10-14e)

Settings → General → **Debug → API trace** (`general.debug_api`, default off).

Always on (no toggle needed):
- `x-ratelimit-*` headers are read on every response (Go client and in-page
  fetch). After a send that answers `x-ratelimit-remaining: 0`, the next send
  waits for the reset window (`[ratelimit] message quota used up (0/10); …`).
- Errors (status ≥ 400 or transport error) and slow calls (> 3 s) appear as
  `[api] POST /match 403 412ms body={"message":…}` (ids masked `6ac8…e37`).
- `[api] final summary: N calls (201×12 204×30 …), E transport errors, avg …`
  when a worker stops; a periodic summary every 10 min when something failed.
- `GET /moderation/standing` re-checked every 30 min → `[standing] CHANGED: …`.
- Socket `messageEdited` with `flags=1` → `[moderation] ChitChat redacted OUR
  message / partner X: "text"`; our own redaction is also an activity line so
  the owner can soften the template. `newNotification` → `[realtime] notification: …`.
- Startup also reads `/users/me/notifications` (`[sync] N unread …`).

With the toggle on:
- `data\debug\<account>_api.log` (rotated at 5 MB → `.1`): one line per
  request `15:04:05.123 POST /users/me/conversations/6ac8…e37/messages 201 388ms rl=9/10 reset=3s via=browser`.
  Never cookies, tokens or request bodies; error responses keep 160 chars.
- Every non-GET request is mirrored to the live log as `[api] …`; GET polls
  stay in the file only. `updateRelationship` / `friendStatus` socket events
  are logged too.

## Browser match detection (fix, UI 2026-10-14f)

Owner report: in Browser mode the window showed "Match found!" and the
partner's "Hi", but the bot never replied — the log sat on "Match request
sent. Waiting for a new conversation...".

Root cause: matched (anonymous) chats are **not DM conversations**. The
site's own sidebar says "No conversations yet" while a match is live, so
the old poller — which only watched `GET /users/me/conversations/dm` —
could never see the match conversation, never learned its id, and never
read the partner's messages. Classic mode never had this problem because
the Go websocket delivers `matchUpdate` directly. (Waiting also took an
"initial" DM snapshot right after `POST /match`, which would have hidden
even a listed conversation.)

Fix — three event sources in `browser_events.go`, deduplicated by
conversation/message id, all feeding the unchanged match-chat flow:

1. **Socket tap (primary).** With `browser.Options.SocketTap` (Browser
   mode only), an init script installed before the app loads wraps
   `window.WebSocket` and copies the SPA's own socket.io frames
   (`42["matchUpdate",…]`, `42["chatMessage",…]`) into a page queue.
   The poller drains it (`Backend.DrainSocketEvents`, rod + chromedp)
   every 3.5–5 s and parses frames with the classic `parseRealtimeEvent`.
   Log: `[browser] Match detected via the page socket: …`.
2. **`GET /match/active` (fallback).** Polled every round (and directly by
   `waitForNewMatchConversation` every 3 s); a flexible parser
   (`parseActiveMatch`) extracts the conversation from the socket-like or
   looser response shapes. New active match → `matchUpdate`, then its
   messages are read via `GET …/conversations/{id}/messages`. Returning to
   the queue (`inQueue:true`) or a closed closure → closed event.
   Log: `[browser] Match detected via /match/active: …`.
3. **DM list poll (unchanged)** for friend/DM conversations; the active
   match conversation is exempt from its "vanished = closed" check.

Outgoing needs no new code once the id is known: `sendDMMessage` POSTs
`…/conversations/{id}/messages` (typing indicator first), executed inside
the page by `browserExecutor` in Browser mode — the same endpoint classic
mode uses. With Settings → Debug → API trace on, each send appears as
`[api] POST /users/me/conversations/…/messages 201 … via=browser`.

If the bot still sits on "Waiting for a new conversation…" with this
build, paste the log lines after "Match request sent" — whichever of the
two `[browser] Match detected …` lines is missing shows which source the
site is not feeding, and the `[api]` trace shows the `/match/active`
answer.

## Auto timezone from the proxy country (UI 2026-10-14f)

The Browser page timezone field is gone. Before any window launches
(browser-mode windows, classic login capture, fresh browsers), the bot
resolves the window's proxy: an explicit `|tz=` suffix wins; otherwise it
asks a geo service (ip-api → ipwho → ipapi) **through that proxy** for the
exit IP's timezone (country→primary-zone fallback; cached per proxy),
and launches the browser with that CDP timezone override. No proxy or a
failed lookup leaves the PC's own timezone. Every window logs one
`[timezone] Proxy … exit country XX → browser timezone set to … before
launch.` line, and `[browser] check:` after login shows the zone the
site actually sees (it also flows into the per-message fingerprint,
which carries the browser timezone).

## Stop fully closes the browser (fix, UI 2026-10-14g)

Owner report: after one browser run, dashboard Stop did not totally close
the browser, and the browser seemed to run/reopen by itself. Three causes,
all fixed:

1. **Rod closed over CDP only.** `launchRodBrowser` starts Chrome manually
   but the backend never kept the process; `Close()` relied on CDP
   `Browser.close` alone, so a dead/hung connection left the whole Chrome
   process tree orphaned on screen. The backend now owns the launched
   process: graceful CDP close (4 s cap), then a forced process-tree kill
   (`taskkill /F /T` on Windows) and a wait until the process really
   exited. `Close()` is idempotent — the worker and the fresh-window path
   both close the same backend safely.
2. **Closed browsers could resurrect a tab.** Page recovery recreated a
   page whenever the session was lost, with no notion of "stopped". Both
   backends now carry a closed flag: after Stop, page recovery and further
   page work return `ErrPageUnavailable` instead of recreating anything.
3. **Stop never reached fresh windows waiting for login.** A Run Fresh
   Browsers window only became stoppable after the live-worker handover,
   so Stop during the manual-login wait did nothing to it. Fresh windows
   now register their cancel with the app the moment they open, and
   `StopBot` cancels StartBot workers + live workers + fresh windows and
   then **waits** (up to ~12 s) until every window reports closed,
   logging `Stop complete: all browser windows are closed.` App shutdown
   gets the same guarantee. The pre-close session save is capped at 8 s
   so a hung read can never keep a window alive.

Note: the Live Wall auto-open (Settings → Live Wall) still opens this
PC's *default* browser at the wall page on Start — unrelated to the bot
browser windows, and disableable in Settings.
