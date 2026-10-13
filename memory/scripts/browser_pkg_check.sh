#!/usr/bin/env bash
# Real compile + vet of internal/browser (rod.go / chromedp.go) with the TRUE
# dependency sources fetched from codeload.github.com (the only reachable host).
# offline_typecheck.sh deletes rod.go/chromedp.go; this script covers them.
set -euo pipefail
REPO=${REPO:-/home/user/copyhere}
DEPS=/tmp/deps
export PATH=/home/user/gotool/go/bin:$PATH GOROOT=/home/user/gotool/go GOPROXY=off GOTOOLCHAIN=local GOSUMDB=off GOFLAGS=-mod=mod
mkdir -p "$DEPS"; cd "$DEPS"
fetch() { # owner/repo ref dir
  local repo=$1 ref=$2 dir=$3 url
  [ -d "$dir" ] && return 0
  if [[ $ref == v* ]]; then url="https://codeload.github.com/$repo/tar.gz/refs/tags/$ref"; else url="https://codeload.github.com/$repo/tar.gz/$ref"; fi
  curl -sL "$url" -o "$dir.tgz"; mkdir -p "$dir"; tar -xzf "$dir.tgz" -C "$dir" --strip-components=1
  [ -f "$dir/go.mod" ] || echo "module github.com/$repo" > "$dir/go.mod"
}
fetch go-rod/rod v0.116.2 rod-0.116.2
fetch chromedp/chromedp v0.14.2 chromedp-0.14.2
fetch chromedp/cdproto 08a3db8b4327 cdproto-08a3db8b4327
fetch ysmood/fetchup v0.2.3 fetchup; fetch ysmood/goob v0.4.0 goob; fetch ysmood/got v0.40.0 got
fetch ysmood/gotrace v0.6.0 gotrace; fetch ysmood/gson v0.7.3 gson; fetch ysmood/leakless v0.9.0 leakless
fetch ysmood/gop v0.2.0 gop; fetch gobwas/ws v1.4.0 ws; fetch gobwas/httphead v0.1.0 httphead
fetch gobwas/pool v0.2.1 pool; fetch ledongthuc/pdf 0c2507a12d80 pdf; fetch orisano/pixelmatch fb0b55479cde pixelmatch
fetch chromedp/sysutil v1.1.0 sysutil; fetch go-json-experiment/json e39067aee2d2 json; fetch golang/sys v0.34.0 sys

W=/tmp/_browsercheck; rm -rf "$W"; mkdir -p "$W/internal"; cd "$W"
cp -r "$REPO/internal/browser" "$REPO/internal/model" "$REPO/internal/runtimeassets" "$REPO/internal/proxy" internal/
rm -f internal/browser/*_test.go
cat > go.mod <<'MOD'
module chitchatbot
go 1.24
require (
	github.com/chromedp/cdproto v0.0.0-20250724212937-08a3db8b4327
	github.com/chromedp/chromedp v0.14.2
	github.com/go-rod/rod v0.116.2
)
replace github.com/chromedp/cdproto => /tmp/deps/cdproto-08a3db8b4327
replace github.com/chromedp/chromedp => /tmp/deps/chromedp-0.14.2
replace github.com/go-rod/rod => /tmp/deps/rod-0.116.2
replace github.com/ysmood/fetchup => /tmp/deps/fetchup
replace github.com/ysmood/goob => /tmp/deps/goob
replace github.com/ysmood/got => /tmp/deps/got
replace github.com/ysmood/gotrace => /tmp/deps/gotrace
replace github.com/ysmood/gson => /tmp/deps/gson
replace github.com/ysmood/leakless => /tmp/deps/leakless
replace github.com/ysmood/gop => /tmp/deps/gop
replace github.com/gobwas/ws => /tmp/deps/ws
replace github.com/gobwas/httphead => /tmp/deps/httphead
replace github.com/gobwas/pool => /tmp/deps/pool
replace github.com/ledongthuc/pdf => /tmp/deps/pdf
replace github.com/orisano/pixelmatch => /tmp/deps/pixelmatch
replace github.com/chromedp/sysutil => /tmp/deps/sysutil
replace github.com/go-json-experiment/json => /tmp/deps/json
replace golang.org/x/sys => /tmp/deps/sys
MOD
go vet ./internal/browser/ && GOOS=windows go build ./internal/browser/ && echo "internal/browser (rod.go + chromedp.go) REAL BUILD OK"
