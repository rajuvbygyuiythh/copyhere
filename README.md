# ChitChat Bot — EVAChat Flow source

Clean source-only project for the Windows Wails desktop app and CLI worker.

## Included

- Go module source (`app.go`, `main.go`, `cmd/`, `internal/`)
- Wails dashboard source/assets
- EVAChat Flow engine and embedded editable starter TXT pack
- Build script, test report, database/editing guide, and security `.gitignore`

## Not included

No runtime account data is included: no `data/` folder, cookies, browser profiles,
login JSON, session storage, tokens, Badger DB, logs, cache, binaries, Chromium,
or WebView2 runtime.

## Build on Windows

```powershell
wails build -clean -webview2 error
# or build the complete bundle
.\scripts\bundle-windows.ps1 -Clean
```

See [`EVACHAT_FLOW_GUIDE.md`](EVACHAT_FLOW_GUIDE.md) and
[`TEST_REPORT.md`](TEST_REPORT.md) before building or publishing a release.
