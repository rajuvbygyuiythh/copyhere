# Browser mode (Browser page → "Browser mode" checkbox)

**Off (default):** nothing changes. The browser opens only when a login is
missing/expired, `login_info.json` is saved, the window closes, and the bot
chats with Go http + Go websocket as before.

**On:** one real browser window per account stays open from Start until Stop
and every request goes through that page.

| Step | What happens | Log prefix |
|------|--------------|-----------|
| Start | `profiles/<account>/browser-data/<backend>` opened **visible** (Headless ignored). Proxy picked once per window, fixed. | `[browser] Browser mode on: opening visible ... window` |
| Login | Profile already logged in → continue. Otherwise saved cookies from `login_info.json` are injected and the page reloaded; if still logged out the bot waits for you to log in in the window. `login_info.json` is (re)written when authenticated. | `[browser] Injecting the saved session`, `Waiting for the site to be logged in`, `Logged in as <id>` |
| Chat | Same loop, same modes (EVAChat Flow / API Chat / ...). `api.Client` executes every request with the page's own `fetch()` (`credentials: "include"`): real cookies, TLS, UA, proxy. Browser-managed headers (Cookie/User-Agent/Origin/Referer/Sec-*) are stripped before the call. Websocket listener is off; new messages come from the normal poll cycle. | normal logs + `[browser] Realtime websocket is off` |
| 401 | Not fatal: window navigated back to the app, bot waits for a new login, then resumes with the fresh session. | `[browser] Session expired. Log in again inside the browser window` |
| Window closed by you | That worker stops with one line; other accounts keep running. | `[browser] browser window closed or unreachable ... Stopping this worker` |
| Stop | Session captured once more (fresh cookies/storage) → `login_info.json` saved → window closed. Next Start logs in automatically. | `[browser] Session saved to ...`, `Browser window closed.` |

Safety: a quick Stop → Start on the same account waits for the old window to
finish closing (`Previous window of this profile is still closing; waiting`),
so two Chromes never share one profile folder.

Limits (phase 1): one Chrome per account (~300–500 MB RAM) → meant for a few
accounts, the dashboard warns above 10. Replies are picked up per cycle (no
realtime push). Messages are sent through the site's API from inside the page,
not by typing into the DOM (DOM typing = phase 3 in `memory/04_ACTIVE_PLANS.md`).

Code: `internal/bot/browser_mode.go` (window lifecycle, executor),
`internal/api/client.go` (`Executor`, `NewVia`), `internal/browser/*`
(`Navigate`, `SetCookies`), `internal/bot/run.go` (branches on
`Config.BrowserMode`), `app.go` (setting → config), Browser page in
`cmd/chitchatui/frontend/dist/index.html` + `app.js`.
