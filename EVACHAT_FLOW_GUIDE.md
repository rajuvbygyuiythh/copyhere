# EVAChat Flow — Build and Data Guide

## What changed

The Message dashboard still has three modes:

1. **Single Sequence** — unchanged
2. **Folder Sequences** — unchanged
3. **EVAChat Flow** — replaces the old Keyword Based mode

EVAChat Flow is a stateful, TXT-driven reply engine. It is implemented in Go and has no Python runtime dependency.

## Start and use

1. Build and start the app.
2. Open **Message**.
3. Select **EVAChat Flow**.
4. Click **Save Messages**.
5. Click **Open EVAChat Data Folder** to create and open the editable dataset. The same dataset is also created automatically when EVAChat Flow starts.
6. Stop and start the bot after editing the TXT files so worker configurations reload them.

## Editable data layout

The app creates the following runtime folder next to its normal `data` directory:

```text
data/eva_chat/
├── input/       # one trigger phrase per line
├── output/      # one reply per line
├── countries.txt
└── README.txt
```

Snap / Telegram username share (same mechanism as the other two modes):

```text
input/snap_request.txt   -> user phrases such as "whats your snap", "insta?", "tg"
output/snap_share.txt    -> e.g. "sure, add me on snap, its %username%"
```

`%username%` is filled from **Message → Usernames** (one snap/tg handle per
line), exactly like Single/Folder sequences. The line is sent **once per user**
when the user asks for it, after 3 middle-chat replies, or when the user says
they are leaving. The dashboard `snap/tg/user` counter and flagged-user
tracking increase in the same way. If the Usernames list is empty the share
step is skipped and the chat continues normally.

A category is connected by file name:

```text
input/how_are_you.txt   -> phrases such as "how are you"
output/how_are_you.txt  -> replies chosen by the engine
```

The starter flow uses neutral categories including greeting, age/gender, country, small talk, warm replies, busy/later, follow-up, and closing.

## Engine behavior

- Stages: `NEW → WAIT_AGE_GENDER → WAIT_COUNTRY → MIDDLE → (snap share) → END`
- Match order: exact, normalized punctuation, whole phrase, slang-normalized, then token subset.
- A reply pool is selected randomly without repeating a line until the pool is exhausted.
- Conversation state is stored per user in the existing bot-state JSON, so it continues through later polling cycles.
- Under-18 age signals end the EVAChat Flow safely.
- The reply cap uses a neutral closing pool; the snap/tg line is sent before the cap closes the chat.
- After the snap/tg line, the next user message receives a closer and the flow ends.

## Windows build

Use the source root (`go_erp`) in PowerShell:

```powershell
# Required once: Go 1.25+ and Wails v2 CLI
wails doctor
.\scripts\bundle-windows.ps1 -Clean
```

The build script creates the Windows bundle under:

```text
dist\windows-x64\
```

For a development build:

```powershell
wails build -clean -webview2 error
```

## Security requirement before publishing

Do **not** add runtime files to Git or a public ZIP. This source package intentionally excludes `data/` and has a `.gitignore` rule for it.

Never publish:

```text
data/profiles/**
data/ui/badger/**
login_info_*.json
bot_state.json
browser cookies, tokens, session storage, logs, or account-specific JSON
```

The source contains only embedded starter templates under `internal/bot/eva_assets/`; these contain no accounts, cookies, sessions, or user data.
