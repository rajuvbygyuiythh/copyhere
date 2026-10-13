package bot

import (
	"context"
	"fmt"
	"os"
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

func TestAPITracerQuotaAndLog(t *testing.T) {
	dir := t.TempDir()
	var mu sync.Mutex
	var lines []string
	cfg := Config{DataDir: dir, Profile: "acct one", DebugAPI: true, Logf: func(format string, args ...any) {
		mu.Lock()
		lines = append(lines, fmt.Sprintf(format, args...))
		mu.Unlock()
	}}
	tr := newAPITracer(cfg)
	registerTracer(cfg.Profile, tr)
	defer registerTracer(cfg.Profile, nil)
	if tr.path == "" || !strings.HasSuffix(tr.path, "acct_one_api.log") {
		t.Fatalf("trace path = %q", tr.path)
	}

	exec := &recordingExecutor{reply: func(req api.FetchRequest) api.FetchResponse {
		if strings.HasSuffix(req.URL, "/messages") {
			return api.FetchResponse{OK: true, Status: 201, Text: "{}", Headers: map[string]string{
				"x-ratelimit-remaining": "0", "x-ratelimit-limit": "10", "x-ratelimit-reset": "1"}}
		}
		return api.FetchResponse{OK: false, Status: 400, Text: `{"message":"bad"}`}
	}}
	client := tr.Attach(api.NewVia(exec, model.LoginInfo{}, "https://app.chitchat.gg/"))

	if _, err := client.Fetch(context.Background(), api.FetchRequest{Method: "POST", URL: "https://api.chitchat.gg/users/me/conversations/6ac8272e3dfe02a07b8eee37/messages"}); err != nil {
		t.Fatal(err)
	}
	if _, err := client.Fetch(context.Background(), api.FetchRequest{Method: "GET", URL: "https://api.chitchat.gg/users/me/limits"}); err != nil {
		t.Fatal(err)
	}

	wait := tr.SendQuotaWait()
	if wait <= 0 || wait > 2*time.Second {
		t.Fatalf("SendQuotaWait = %s, want ~1.5s", wait)
	}
	start := time.Now()
	if err := waitForSendQuota(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	if time.Since(start) < time.Second {
		t.Fatalf("waitForSendQuota returned too early (%s)", time.Since(start))
	}
	if tr.SendQuotaWait() != 0 {
		t.Fatal("quota should be free after the reset window")
	}

	tr.Close()
	data, err := os.ReadFile(tr.path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	if !strings.Contains(text, "POST /users/me/conversations/6ac8…e37/messages 201") || !strings.Contains(text, "rl=0/10 reset=1s") {
		t.Fatalf("trace file missing send line: %s", text)
	}
	if !strings.Contains(text, "GET /users/me/limits 400") || !strings.Contains(text, `body={"message":"bad"}`) {
		t.Fatalf("trace file missing error line: %s", text)
	}
	if !strings.Contains(text, "SUMMARY calls=2") {
		t.Fatalf("trace file missing summary: %s", text)
	}
	mu.Lock()
	joined := strings.Join(lines, "")
	mu.Unlock()
	if !strings.Contains(joined, "[ratelimit] message quota used up") {
		t.Fatalf("missing ratelimit warning: %s", joined)
	}
	if !strings.Contains(joined, "[api] POST /users/me/conversations/6ac8…e37/messages 201") {
		t.Fatalf("missing [api] live line: %s", joined)
	}
	if !strings.Contains(joined, "[api] final summary: 2 calls (201×1 400×1)") {
		t.Fatalf("missing final summary: %s", joined)
	}
}

func TestAPIPathMasksIDs(t *testing.T) {
	got := apiPath("https://api.chitchat.gg/users/me/conversations/6ac8272e3dfe02a07b8eee37/read?x=1")
	if got != "/users/me/conversations/6ac8…e37/read" {
		t.Fatalf("apiPath = %q", got)
	}
}

func TestParseMessageEditedAndInfoEvents(t *testing.T) {
	packet := `42["messageEdited",{"message":{"id":"m1","conversationId":"c1","author":{"id":"bot-id","username":"us"},"content":"Horny?","type":"TEXT","flags":1}}]`
	event, ok, err := parseRealtimeEvent(packet, "bot-id")
	if err != nil || !ok {
		t.Fatalf("parse: ok=%v err=%v", ok, err)
	}
	if event.Type != "messageEdited" || event.Flags != 1 || event.UserID != "bot-id" || event.Content != "Horny?" {
		t.Fatalf("event = %+v", event)
	}
	if event.forwardable() {
		t.Fatal("messageEdited must not be forwarded to chat loops")
	}
	var lines []string
	cfg := Config{Logf: func(format string, args ...any) { lines = append(lines, fmt.Sprintf(format, args...)) }}
	logRealtimeEvent(cfg, "bot-id", event)
	joined := strings.Join(lines, "")
	if !strings.Contains(joined, "[moderation] ChitChat redacted OUR message") || !strings.Contains(joined, "WARN our text") {
		t.Fatalf("moderation log missing: %s", joined)
	}

	info, ok, err := parseRealtimeEvent(`42["updateRelationship",{"relationship":{"id":"r1","status":3}}]`, "bot-id")
	if err != nil || !ok || info.Type != "updateRelationship" || info.forwardable() {
		t.Fatalf("updateRelationship: %+v ok=%v err=%v", info, ok, err)
	}
	if ev, _, _ := parseRealtimeEvent(`42["chatMessage",{"message":{"id":"m","conversationId":"c","author":{"id":"p"}}}]`, "bot-id"); !ev.forwardable() {
		t.Fatal("chatMessage must stay forwardable")
	}
}
