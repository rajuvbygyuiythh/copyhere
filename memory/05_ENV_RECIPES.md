# 05 — ENVIRONMENT RECIPES & DEAD ENDS

## Sandbox (Arena) facts
- Outbound internet only: github.com, api.github.com, codeload.github.com,
  registry.npmjs.org, pypi.org, files.pythonhosted.org. Real LLM APIs are
  unreachable → test apitest with a local mock only.
- Sandbox resets wipe `/tmp`, `/home/user/.local`, every `dist/` dir,
  background processes, and roll the local git HEAD back to `c2c30d5` while
  keeping working files. Repair:
  ```
  cd /home/user/copyhere && git fetch -q origin arena/30e64ce8-copyhere \
    && git reset -q origin/arena/30e64ce8-copyhere \
    && git checkout -- cmd/chitchatui/frontend/dist && git status --short
  ```
- Keep tools under `/home/user/gotool` (survives; `.local` does not).

## Go toolchain (only source that works offline: PyPI `go-bin`)
```
mkdir -p /home/user/gotool && cd /tmp && pip download go-bin==1.27.2 --no-deps -d /tmp/gobin -q
cd /tmp/gobin && unzip -q -o go_bin-*.whl -d x && mv x/go /home/user/gotool/go && chmod +x /home/user/gotool/go/bin/*
export GOROOT=/home/user/gotool/go PATH=/home/user/gotool/go/bin:$PATH GOPROXY=off GOTOOLCHAIN=local GOSUMDB=off
```

## apitest verify (no deps, builds anywhere)
```
cd apitest && gofmt -l . ; go vet . && go test . && CGO_ENABLED=0 GOOS=windows go build -o /tmp/apitest.exe .
```
Mock API for end-to-end runs: a tiny Python HTTP server on 127.0.0.1:8091
answering `/v1/chat/completions` with `{"choices":[{"message":{"content":...}}],"usage":{...}}`
(script was `/tmp/mock.py`, rewrite if missing); run dir `/tmp/run` with
`apitest.config.txt` (`BASE_URL=http://127.0.0.1:8091/v1`), persona/scenarios/
snap_*.txt copied in; `./apitest scenario <name>`.

## Root package (main bot) verify — ONE COMMAND (2026-10-14)
`bash memory/scripts/offline_typecheck.sh` — copies the repo to `/tmp/_gotest`,
drops rod/chromedp browser files, stubs gorilla/websocket, utls, x/net/proxy,
wails, badger, wmi with tiny packages, then `go vet` internal pkgs,
`GOOS=windows go build .` and runs the dep-free tests. If a new external
symbol is used, add it to the stub in the script. `internal/uistore` tests
fail under the badger stub — expected.

## (older notes) Root package verify — out-of-repo build dir
Root module deps are unreachable offline (GOPROXY=off, no module cache after
reset). Recipe used before: `/home/user/_gotest` = copy of the repo with
`go.mod` replace directives + a compile stub for `go-toast` in
`/home/user/_stubs/go-toast`; sync with
```
tar -cf - --exclude=.git --exclude='*.zip' --exclude=go.mod --exclude=go.sum . | (cd /home/user/_gotest && tar -xf -)
```
then in `_gotest`: `GOOS=windows go vet ./...`,
`go test ./internal/bot ./internal/logwall ./internal/uistore ./internal/workerplan`,
`GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -tags desktop,production -ldflags "-w -s -H windowsgui" -o /tmp/chitchatui.exe .`
If the module cache is gone this may be impossible in the sandbox; then rely
on package-level tests for dep-free packages (`internal/bot` needs deps →
state it honestly and let the owner build on Windows with `install.bat`).
`internal/license` (windows-only) and `internal/browser` (rod, no Chromium)
always fail in the sandbox — ignore. Frontend check: `node --check
cmd/chitchatui/frontend/dist/app.js` (+ jsdom smoke for wall.html).

## Live Wall preview
Server binds 127.0.0.1 only → use the demo's public proxy:
```
go build -o /home/user/gotool/livewalldemo ./cmd/livewalldemo
/home/user/gotool/livewalldemo -port 47401 -bg /home/user/gotool/wallbg -listen 0.0.0.0:47400
```
(start with start_process; internal and public ports must differ; generate a
placeholder bg jpg into `/home/user/gotool/wallbg/` — never the owner's face).

## Zips (after every code push)
```
(apitest-only zip REMOVED 2026-10-14 — one zip only)
git archive --format=zip --prefix=chitchat-bot/ -o downloads/ChitChatBot-full-source.zip HEAD -- . ':!downloads'
git add downloads && git commit -m "downloads: rebuild zips" && git push origin arena/30e64ce8-copyhere
```
"Ready" zip (with the owner's config) is built only on request and never
contains a key the agent stores.

## Windows (owner's PC)
- `install.bat` installs Go/Wails/WebView2 and builds; `run-dashboard.bat`
  runs `build\bin\chitchatui.exe`; data in `data\` next to it.
- `apitest\apitest.bat [chat|ping|scenario <name|all>|compare <scenario>]`.
- Featherless works with `deepseek-ai/DeepSeek-V3-0324`; compare list
  `mistralai/Mistral-Nemo-Instruct-2407`, `NousResearch/Hermes-3-Llama-3.1-70B`;
  avoid `meta-llama/*`, `google/gemma-*`, `*-R1-Distill-*`, `*Thinking*`, `gpt-oss-*`.

## Dead ends (do not retry)
- GitHub release asset upload/download from the sandbox → blocked.
- Drive `go_erp.zip` (1.29 GB) → cannot download.
- Go module proxies / golang.org → unreachable.
- `rsync` not installed (use tar pipe / cp). `sed` with `/` in replacement →
  use python. gofmt realigns `var (...)` blocks → anchor regexes carefully.
- Long mock runs + git + zip in one bash call can exceed the timeout → split.
- jsdom: KeyboardEvent on document does not reach window listeners (not a bug).
- Headless browser not available; visual checks = jsdom smoke only.
