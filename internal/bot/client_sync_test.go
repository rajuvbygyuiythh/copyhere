package bot

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"chitchatbot/internal/api"
	"chitchatbot/internal/model"
)

const testTransportKey = "33FsVurAchunk-84DSi8nlzE"

func TestXorEncodeRoundTrip(t *testing.T) {
	payload := []byte(`{"userAgent":"Mozilla/5.0","language":"en-US"}`)
	encoded := xorEncode(testTransportKey, payload)
	if len(encoded) != 8+len(payload)*2 {
		t.Fatalf("encoded length %d, want %d", len(encoded), 8+len(payload)*2)
	}
	for _, c := range encoded {
		if !strings.ContainsRune("0123456789abcdefghijklmnopqrstuvwxyz", c) {
			t.Fatalf("unexpected char %q in %s", c, encoded)
		}
	}
	decoded, err := xorDecode(testTransportKey, encoded)
	if err != nil {
		t.Fatal(err)
	}
	if string(decoded) != string(payload) {
		t.Fatalf("round trip mismatch: %s", decoded)
	}
}

// The real client's header, captured from DevTools, decodes to the user id.
func TestResponseTimeIDMatchesRealClientFormat(t *testing.T) {
	captured := "k5lsceh76e67493807267944386b2f7f692b65354a07342a3c392345"
	decoded, err := xorDecode(testTransportKey, captured)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(decoded); got != "6ac826c2066bac56ea1a1017" {
		t.Fatalf("captured header decoded to %q", got)
	}

	ours := responseTimeID(testTransportKey, "6ac826c2066bac56ea1a1017")
	if len(ours) != len(captured) {
		t.Fatalf("our header length %d, real %d", len(ours), len(captured))
	}
	back, _ := xorDecode(testTransportKey, ours)
	if string(back) != "6ac826c2066bac56ea1a1017" {
		t.Fatalf("our header decoded to %q", back)
	}
}

func TestMatchHeaders(t *testing.T) {
	cfg := Config{TransportKey: testTransportKey}
	h := matchHeaders(cfg, "6a9257898ac97f8d3c432262")
	if h["accept"] != "application/json" || h["content-type"] != "application/json" {
		t.Fatalf("headers: %v", h)
	}
	if h["response-time-id"] == "" {
		t.Fatal("response-time-id missing")
	}
	if _, ok := matchHeaders(cfg, "")["response-time-id"]; ok {
		t.Fatal("response-time-id must be omitted without a user id")
	}
}

func TestActiveMatchTracking(t *testing.T) {
	noteActiveMatch("u1", "c1")
	if got := takeActiveMatch("u1"); got != "c1" {
		t.Fatalf("take = %q", got)
	}
	if got := takeActiveMatch("u1"); got != "" {
		t.Fatalf("second take = %q, want empty", got)
	}
	noteActiveMatch("u1", "c2")
	noteActiveMatch("u1", "")
	if got := takeActiveMatch("u1"); got != "" {
		t.Fatalf("cleared take = %q", got)
	}
}

type recordingExecutor struct {
	mu    sync.Mutex
	calls []api.FetchRequest
	reply func(req api.FetchRequest) api.FetchResponse
}

func (r *recordingExecutor) Fetch(_ context.Context, req api.FetchRequest) (api.FetchResponse, error) {
	r.mu.Lock()
	r.calls = append(r.calls, req)
	r.mu.Unlock()
	if r.reply != nil {
		return r.reply(req), nil
	}
	return api.FetchResponse{OK: true, Status: 200, Text: "{}"}, nil
}

func (r *recordingExecutor) snapshot() []api.FetchRequest {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]api.FetchRequest, len(r.calls))
	copy(out, r.calls)
	return out
}

func TestMatchDisconnectsPreviousMatchFirst(t *testing.T) {
	exec := &recordingExecutor{reply: func(req api.FetchRequest) api.FetchResponse {
		return api.FetchResponse{OK: true, Status: 201, Text: `{"ok":true}`}
	}}
	client := api.NewVia(exec, model.LoginInfo{}, "https://app.chitchat.gg/")
	cfg := Config{APIBaseURL: "https://api.chitchat.gg", TransportKey: testTransportKey, Profile: "t-disc"}

	noteActiveMatch("user-a", "conv-1")
	if err := match(context.Background(), client, cfg, "user-a"); err != nil {
		t.Fatal(err)
	}
	calls := exec.snapshot()
	if len(calls) != 2 {
		t.Fatalf("expected disconnect + match, got %d calls", len(calls))
	}
	if calls[0].Method != "PATCH" || !strings.HasSuffix(calls[0].URL, "/match/disconnect") {
		t.Fatalf("first call = %s %s", calls[0].Method, calls[0].URL)
	}
	if calls[0].Headers["response-time-id"] == "" || calls[1].Headers["response-time-id"] == "" {
		t.Fatal("response-time-id missing on disconnect/match")
	}
	if calls[1].Method != "POST" || !strings.HasSuffix(calls[1].URL, "/match") {
		t.Fatalf("second call = %s %s", calls[1].Method, calls[1].URL)
	}
	if calls[1].Headers["accept"] != "application/json" {
		t.Fatalf("match accept header = %q", calls[1].Headers["accept"])
	}

	// No active match → straight to POST /match.
	exec2 := &recordingExecutor{reply: exec.reply}
	client2 := api.NewVia(exec2, model.LoginInfo{}, "https://app.chitchat.gg/")
	if err := match(context.Background(), client2, cfg, "user-b"); err != nil {
		t.Fatal(err)
	}
	if calls := exec2.snapshot(); len(calls) != 1 || calls[0].Method != "POST" {
		t.Fatalf("expected a single POST /match, got %+v", calls)
	}
}

func TestWithReadReceiptsMarksPartnerMessagesOnly(t *testing.T) {
	exec := &recordingExecutor{}
	client := api.NewVia(exec, model.LoginInfo{}, "https://app.chitchat.gg/")
	cfg := Config{APIBaseURL: "https://api.chitchat.gg", Profile: "t-read"}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	in := make(chan realtimeEvent, 8)
	out := withReadReceipts(ctx, client, cfg, "me", in)

	in <- realtimeEvent{Type: "chatMessage", ConversationID: "c1", UserID: "me", MessageID: "m0"}
	in <- realtimeEvent{Type: "chatMessage", ConversationID: "c1", UserID: "partner", MessageID: "m1"}
	in <- realtimeEvent{Type: "chatMessage", ConversationID: "c1", UserID: "partner", MessageID: "m2"} // debounced
	in <- realtimeEvent{Type: "typing", ConversationID: "c1", UserID: "partner"}

	for i := 0; i < 4; i++ {
		select {
		case ev := <-out:
			if ev.ConversationID != "c1" {
				t.Fatalf("relayed event %+v", ev)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("event %d not relayed", i)
		}
	}

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if len(exec.snapshot()) >= 1 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	time.Sleep(200 * time.Millisecond)
	calls := exec.snapshot()
	if len(calls) != 1 {
		t.Fatalf("expected exactly one mark-read, got %d: %+v", len(calls), calls)
	}
	if calls[0].Method != "PATCH" || !strings.HasSuffix(calls[0].URL, "/users/me/conversations/c1/read") {
		t.Fatalf("mark-read call = %s %s", calls[0].Method, calls[0].URL)
	}

	// Closed match clears the active-match note.
	noteActiveMatch("me", "c1")
	in <- realtimeEvent{Type: "matchUpdate", ConversationID: "c1", Closed: true}
	select {
	case <-out:
	case <-time.After(2 * time.Second):
		t.Fatal("close event not relayed")
	}
	if got := takeActiveMatch("me"); got != "" {
		t.Fatalf("active match after close = %q", got)
	}
}

func TestLogAccountStandingParsesWarnings(t *testing.T) {
	var lines []string
	var mu sync.Mutex
	cfg := Config{
		APIBaseURL: "https://api.chitchat.gg",
		Profile:    "t-standing",
		Logf: func(format string, args ...any) {
			mu.Lock()
			lines = append(lines, fmt.Sprintf(format, args...))
			mu.Unlock()
		},
	}
	exec := &recordingExecutor{reply: func(req api.FetchRequest) api.FetchResponse {
		switch {
		case strings.HasSuffix(req.URL, "/match/active"):
			return api.FetchResponse{OK: true, Status: 200, Text: `{"inQueue":true}`}
		case strings.HasSuffix(req.URL, "/moderation/standing"):
			return api.FetchResponse{OK: true, Status: 200, Text: `{"standing":"LIMITED","limitedFeatures":["match"],"pendingWarnings":[{"id":"w1"}],"activeViolations":[]}`}
		}
		return api.FetchResponse{OK: false, Status: 404}
	}}
	client := api.NewVia(exec, model.LoginInfo{}, "https://app.chitchat.gg/")
	logAccountStanding(context.Background(), client, cfg)

	mu.Lock()
	joined := strings.Join(lines, "\n")
	mu.Unlock()
	if !strings.Contains(joined, "still in the match queue") {
		t.Fatalf("missing in-queue note: %s", joined)
	}
	if !strings.Contains(joined, "[standing] WARN standing=LIMITED pendingWarnings=1 activeViolations=0 limitedFeatures=1") {
		t.Fatalf("missing standing warning: %s", joined)
	}
	if !strings.Contains(joined, `limitedFeatures: ["match"]`) {
		t.Fatalf("missing limited features detail: %s", joined)
	}
}
