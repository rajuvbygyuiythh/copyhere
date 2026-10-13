# Live Wall — Home screen mirrored to a PC browser

The Live Wall is a local-only web page (`http://127.0.0.1:47321/`) that shows
exactly what the dashboard **Home** tab shows — the 8 counters and the live
`SEND | … / RECEIVE | … | message:` chat log — but full-screen, in a dark
sci-fi / hacking "live wallpaper" style with auto-rotating themes and layouts.

## How it works

```
bot worker ──Activityf──▶ app.go a.log()
                               ├─▶ Badger (uiStore.AppendLog)        (unchanged)
                               ├─▶ Wails event "ui:log" → dashboard  (unchanged)
                               └─▶ logwall.Server.Publish(entry)     (new)
                                       │  SSE  /events   (log, stats every 3s, running, logs_cleared)
                                       │  JSON /api/snapshot (8 counters + last 300 logs + background list)
                                       └─ GET  /            single-file page internal/logwall/assets/wall.html
```

* `internal/logwall/server.go` — HTTP + Server-Sent-Events hub, **binds to
  127.0.0.1 only** and rejects requests whose `Host` is not loopback.
  Counters come from the same `homeStats()` the dashboard uses; logs are the
  same `model.LogEntry` values. No sessions, cookies, tokens or profile data
  are ever read or served.
* `app_livewall.go` — glue: `ensureLiveWall()`, `autoOpenLiveWall()` (runs on
  every **Start Bot**), bindings `OpenLiveWall()` and `OpenLiveWallFolder()`.
* `app.go` — 4 small hooks: `log()` → `Publish`, `emitStatus()` → `SetRunning`,
  `ClearLogs()` → `LogsCleared`, `shutdown()` → `Close`, `StartBot()` → `go autoOpenLiveWall()`.
* Settings (`GeneralSettings`): `live_wall_disabled` (default off = enabled),
  `live_wall_port` (default 47321, next 20 ports tried if busy).

## Using it

1. Start the bot → the default browser opens the wall automatically
   (skipped if a wall page is already connected). Home tab also has a
   **Live Wall** button; Setting tab has the on/off switch, port and a
   **Background photos folder** button.
2. Drop `png/jpg/jpeg/webp/gif` files into `data\logwall\` — they are shown as
   a faint, shadowed background (grayscale, ~35% brightness, slow Ken Burns zoom,
   crossfade slideshow every 45 s when there are several).
3. Keys inside the page: `T` next theme, `L` next layout, `P` pause auto-rotate,
   `B` next background, `C` clear screen, `F` / double-click fullscreen.
   URL params: `?theme=matrix&layout=terminal&rotate=0`.

Themes: cyan, matrix, crimson, amber, violet, ice (auto every 60 s, with a
glitch transition; layout switches every second rotation).
Layouts: `hud` (stat tiles left + chat bubbles), `terminal` (stat strip +
console lines), `focus` (big chat + slim stats).

Page features: matrix rain canvas, grid + scanline + CRT sweep overlays,
count-up numbers with sparklines, radar widget with running-worker count,
typewriter effect for new messages, gold highlight for snap/telegram shares,
system console strip for info/error lines, SSE auto-reconnect.

## Preview without the bot

```
go run ./cmd/livewalldemo -bg data\logwall -open
```

streams fake traffic to the wall so themes/backgrounds can be tuned.

## Tests

`go test ./internal/logwall` — snapshot, background serving (non-images and
path traversal → 404), host guard (403 for foreign Host), SSE event flow,
idempotent start / close.
