package apichat

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func mockAPI(t *testing.T, reply func(n int) string, status int) (*httptest.Server, *int32) {
	t.Helper()
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := int(atomic.AddInt32(&calls, 1))
		if !strings.HasSuffix(r.URL.Path, "/chat/completions") {
			http.NotFound(w, r)
			return
		}
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		if _, ok := body["messages"]; !ok {
			t.Errorf("request without messages")
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{"message": map[string]any{"content": reply(n)}, "finish_reason": "stop"}},
			"usage":   map[string]int{"prompt_tokens": 100, "completion_tokens": 5},
		})
	}))
	t.Cleanup(srv.Close)
	return srv, &calls
}

func newTestEngine(t *testing.T, baseURL string) *Engine {
	t.Helper()
	dir := t.TempDir()
	if err := EnsureData(dir); err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{"persona.txt", "snap_share.txt", "snap_hint.txt"} {
		if _, err := os.Stat(filepath.Join(dir, n)); err != nil {
			t.Fatalf("EnsureData did not seed %s", n)
		}
	}
	opt := Defaults()
	opt.BaseURL, opt.APIKey, opt.Model, opt.Timeout = baseURL+"/v1", "k", "x", 5*time.Second
	e, err := New(dir, opt)
	if err != nil {
		t.Fatal(err)
	}
	if len(e.Warns) != 0 {
		t.Fatalf("unexpected warns: %v", e.Warns)
	}
	return e
}

func forceType(e *Engine, key string, noShare, hint bool) {
	e.mu.Lock()
	c := e.convos[key]
	c.Director.noShare, c.Director.hintMode = noShare, hint
	c.Director.tier, c.Director.score = tierHot, 6
	e.mu.Unlock()
}

func TestNewRequiresKey(t *testing.T) {
	if _, err := New(t.TempDir(), Options{}); err == nil {
		t.Fatal("expected error without API key")
	}
}

func TestDirectShareFlow(t *testing.T) {
	srv, calls := mockAPI(t, func(int) string { return "haha ok, u?" }, 200)
	e := newTestEngine(t, srv.URL)
	rng := newRng()
	key := "acc|u1"
	e.get(key, rng, 0)
	forceType(e, key, false, false)

	var shared Result
	for i, msg := range []string{"hey", "m 22", "ur cute", "lol"} {
		r := e.Reply(key, msg, "queen_a", rng, 0)
		if r.Turn != i+1 || len(r.Lines) == 0 || r.Fallback {
			t.Fatalf("turn %d: %+v", i+1, r)
		}
		if r.ShareNow {
			shared = r
		}
	}
	if !shared.ShareNow || shared.Turn != 4 {
		t.Fatalf("direct HOT chat should share on reply 4: %+v", shared)
	}
	if !strings.Contains(strings.Join(shared.Lines, " "), "queen_a") {
		t.Fatalf("share lines must carry the handle: %v", shared.Lines)
	}
	if atomic.LoadInt32(calls) == 0 {
		t.Fatal("API never called")
	}
	if !strings.Contains(shared.Status, "snap SHARED") {
		t.Fatalf("status: %s", shared.Status)
	}
}

func TestHintThenDirect(t *testing.T) {
	srv, _ := mockAPI(t, func(int) string { return "haha ok" }, 200)
	e := newTestEngine(t, srv.URL)
	rng := newRng()
	key := "acc|u2"
	e.get(key, rng, 0)
	forceType(e, key, false, true)
	var hint, share Result
	for _, msg := range []string{"hey", "m 22", "ur cute", "im from texas lol"} {
		r := e.Reply(key, msg, "queen_b", rng, 0)
		if r.HintNow {
			hint = r
		}
		if r.ShareNow {
			share = r
		}
	}
	if !hint.HintNow || hint.Turn != 3 || strings.Contains(strings.Join(hint.Lines, " "), "queen_b") {
		t.Fatalf("hint on reply 3 without handle: %+v", hint)
	}
	if !share.ShareNow || share.Turn != 4 || share.Why != "hint missed -> direct share" {
		t.Fatalf("direct share on reply 4: %+v", share)
	}
}

func TestNoShareCloses(t *testing.T) {
	srv, _ := mockAPI(t, func(int) string { return "lol ok" }, 200)
	e := newTestEngine(t, srv.URL)
	rng := newRng()
	key := "acc|u3"
	e.get(key, rng, 0)
	forceType(e, key, true, false)
	e.mu.Lock()
	e.convos[key].Director.bailTurn = 3
	e.mu.Unlock()
	var last Result
	for _, msg := range []string{"hey", "m 22", "u horny?"} {
		last = e.Reply(key, msg, "queen_c", rng, 0)
		if last.ShareNow || last.HintNow {
			t.Fatalf("no-share chat shared: %+v", last)
		}
	}
	if !last.Ended || last.EndReason != "no-share chat closed" {
		t.Fatalf("expected close on reply 3: %+v", last)
	}
	after := e.Reply(key, "u there?", "queen_c", rng, 0)
	if !after.Ended || len(after.Lines) != 0 {
		t.Fatalf("ended chat must stay silent: %+v", after)
	}
}

func TestNoUsernameNeverSendsBrokenLine(t *testing.T) {
	srv, _ := mockAPI(t, func(int) string { return "ok" }, 200)
	e := newTestEngine(t, srv.URL)
	rng := newRng()
	key := "acc|u4"
	e.get(key, rng, 0)
	forceType(e, key, false, false)
	var r Result
	for _, msg := range []string{"hey", "m 22", "ur cute", "lol"} {
		r = e.Reply(key, msg, "", rng, 0)
	}
	if r.ShareNow || !r.Ended || strings.Contains(strings.Join(r.Lines, " "), usernamePlaceholder) {
		t.Fatalf("without a username the share turn must close: %+v", r)
	}
}

func TestAPIFailureFallbackThenClose(t *testing.T) {
	srv, _ := mockAPI(t, func(int) string { return "" }, 500)
	e := newTestEngine(t, srv.URL)
	rng := newRng()
	key := "acc|u5"
	r1 := e.Reply(key, "hey", "q", rng, 0)
	if !r1.Fallback || len(r1.Lines) != 1 || r1.Ended || r1.Err == nil {
		t.Fatalf("first failure -> fallback line: %+v", r1)
	}
	r2 := e.Reply(key, "u there", "q", rng, 0)
	if !r2.Ended || r2.EndReason != "api errors" {
		t.Fatalf("second failure -> polite close: %+v", r2)
	}
}

func TestStartOpenerAndRestartKeepsTurns(t *testing.T) {
	srv, _ := mockAPI(t, func(int) string { return "ok" }, 200)
	e := newTestEngine(t, srv.URL)
	rng := newRng()
	r := e.Start("acc|u6", rng)
	if r.Turn != 0 || len(r.Lines) != 1 || r.Lines[0] == "" {
		t.Fatalf("opener: %+v", r)
	}
	e.Forget("acc|u6")
	if e.Known("acc|u6") {
		t.Fatal("forget failed")
	}
	// bot restarted after 3 replies: numbering continues
	r2 := e.Reply("acc|u7", "hey", "q", rng, 3)
	if r2.Turn != 4 {
		t.Fatalf("turn after restart = %d, want 4", r2.Turn)
	}
}

func TestEngineTest(t *testing.T) {
	srv, _ := mockAPI(t, func(int) string { return "ya im here lol" }, 200)
	e := newTestEngine(t, srv.URL)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := e.Test(ctx)
	if err != nil || out == "" {
		t.Fatal(err, out)
	}
	if !strings.Contains(e.Describe(), "API Chat") {
		t.Fatal(e.Describe())
	}
}
