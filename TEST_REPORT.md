# EVAChat Flow — Test Report

## Passed

### Go unit tests

```text
go test ./internal/bot ./internal/model ./internal/uistore

ok   chitchatbot/internal/bot
?    chitchatbot/internal/model [no test files]
ok   chitchatbot/internal/uistore
```

The bot test package includes new checks for:

- starter TXT pack bootstrap and validation
- stage transition: opener → age/gender → country → middle chat
- slang-normalized matching
- no reuse of a spent reply pool
- age gate / end-state behavior
- per-account `EVAChatSessions` state persistence through the runtime adapter
- legacy stored `keyword` setting migration to `eva_chat_flow`

### Windows compile validation

```text
CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build ./...
```

Completed successfully. This verifies the changed Go source compiles for the project's Windows target.

### Frontend checks

```text
node --check cmd/chitchatui/frontend/dist/app.js
```

Completed successfully. Additional structural checks verified the EVAChat mode radio button, EVAChat panel, data-folder action, and absence of old keyword-panel DOM references.

## Not run in this Linux workspace

A native Windows Wails desktop bundle cannot be produced here. Run the documented Windows command before release:

```powershell
wails build -clean -webview2 error
# or
.\scripts\bundle-windows.ps1 -Clean
```

The unmodified repository-wide Linux browser test also requires a runnable Chromium desktop environment, and the root Windows-license package uses the Windows-only WMI dependency. Those environment limitations are unrelated to the EVAChat changes.

## Sensitive-data verification

The delivered source tree has no `data/` runtime directory, profile sessions, login JSON, Badger database, cookies, or account-specific logs. `.gitignore` excludes `data/` to prevent these from being committed in a future release.
