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
| Chat | Same loop, same modes (EVAChat Flow / API Chat / ...). `api.Client` executes every request with the page's own `fetch()` (`credentials: "include"`): real cookies, TLS, UA, proxy. Browser-managed headers (Cookie/User-Agent/Origin/Referer/Sec-*) are stripped before the call. Websocket listener is off; a **message poller** (`browser_events.go`) reads the conversation list through the window every 3.5–5 s and the messages of changed conversations only, and feeds the same `matchUpdate` / `chatMessage` / closed events the socket would — so match chats, "user speaks first" and reply waits work unchanged. | normal logs + `[browser] Realtime websocket is off ... polls` |
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
