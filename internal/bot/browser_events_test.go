package bot

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"chitchatbot/internal/api"
	"chitchatbot/internal/browser"
	"chitchatbot/internal/model"
)

// scriptedBackend answers conversation/message GETs from an in-memory state.
type scriptedBackend struct {
	fakeBackend
	mu       sync.Mutex
	convs    []model.DMConversation
	messages map[string][]model.Message
	calls    map[string]int
}

func (s *scriptedBackend) Fetch(_ context.Context, req browser.FetchRequest) (browser.FetchResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.calls == nil {
		s.calls = map[string]int{}
	}
	var payload any
	switch {
	case strings.Contains(req.URL, "/conversations/dm"):
		s.calls["dm"]++
		payload = s.convs
	case strings.Contains(req.URL, "/messages"):
		s.calls["messages"]++
		id := strings.TrimSuffix(strings.TrimPrefix(req.URL[strings.Index(req.URL, "/conversations/")+len("/conversations/"):], ""), "")
		id = id[:strings.Index(id, "/")]
		payload = s.messages[id]
	default:
		return browser.FetchResponse{Status: 404, StatusText: "Not Found"}, nil
	}
	raw, _ := json.Marshal(payload)
	return browser.FetchResponse{OK: true, Status: 200, Text: string(raw)}, nil
}

func conv(id, updated string, unread int, me, partner string) model.DMConversation {
	var c model.DMConversation
	c.ID, c.UpdatedAt, c.UnreadMessageCount = id, updated, unread
	c.Participants = make([]struct {
		Profile struct {
			ID       string `json:"id"`
			Username string `json:"username"`
		} `json:"profile"`
	}, 2)
	c.Participants[0].Profile.ID, c.Participants[0].Profile.Username = me, "bot"
	c.Participants[1].Profile.ID, c.Participants[1].Profile.Username = partner, "guy"
	return c
}

func msg(id, author, content, created string) model.Message {
	var m model.Message
	m.ID, m.Content, m.CreatedAt = id, content, created
	m.Author.ID = author
	return m
}

func TestBrowserEventPollerEmitsPartnerMessagesAndClose(t *testing.T) {
	const me, partner = "me", "u1"
	sb := &scriptedBackend{
		convs:    []model.DMConversation{conv("old", "2026-10-14T10:00:00.000Z", 0, me, "u0")},
		messages: map[string][]model.Message{"old": {msg("m0", "u0", "earlier", "2026-10-14T09:59:00.000Z")}},
	}
	cfg := Config{APIBaseURL: "https://api.chitchat.gg", DMLimit: 100, MessageFetchLimit: 100}
	client := api.NewVia(browserExecutor{backend: sb}, model.LoginInfo{}, "https://app.chitchat.gg/")
	p := newBrowserEventPoller(cfg, client, me, nil)

	// round 1: initial snapshot → no events, no message fetch for old conversations
	events, ok := p.poll(context.Background())
	if !ok || len(events) != 0 {
		t.Fatalf("initial round must be silent: ok=%v events=%+v", ok, events)
	}
	if sb.calls["messages"] != 0 {
		t.Fatalf("initial round must not read messages of existing conversations")
	}

	// round 2: a new match conversation appears with the partner's first message + the bot's own
	sb.mu.Lock()
	sb.convs = append(sb.convs, conv("new", "2026-10-14T10:05:00.000Z", 1, me, partner))
	sb.messages["new"] = []model.Message{
		msg("m2", me, "hey from bot", "2026-10-14T10:05:02.000Z"),
		msg("m1", partner, "hi there", "2026-10-14T10:05:01.000Z"),
	}
	sb.mu.Unlock()
	events, ok = p.poll(context.Background())
	if !ok || len(events) != 2 {
		t.Fatalf("expected match event + one partner message event, got %+v", events)
	}
	if e := events[0]; e.Type != "matchUpdate" || e.Closed || e.Target.UserID != partner || e.Target.ConversationID != "new" || e.Target.Username != "guy" {
		t.Fatalf("bad match event: %+v", e)
	}
	if e := events[1]; e.Type != "chatMessage" || e.ConversationID != "new" || e.UserID != partner || e.Content != "hi there" || e.Username != "guy" {
		t.Fatalf("bad event: %+v", e)
	}

	// round 3: nothing changed → nothing emitted, no extra message read
	before := sb.calls["messages"]
	events, _ = p.poll(context.Background())
	if len(events) != 0 || sb.calls["messages"] != before {
		t.Fatalf("unchanged round must be silent: %+v", events)
	}

	// round 4: partner replies again (updatedAt changes) → one new event, old ones not repeated
	sb.mu.Lock()
	sb.convs[1].UpdatedAt = "2026-10-14T10:06:00.000Z"
	sb.messages["new"] = append(sb.messages["new"], msg("m3", partner, "u there?", "2026-10-14T10:06:00.000Z"))
	sb.mu.Unlock()
	events, _ = p.poll(context.Background())
	if len(events) != 1 || events[0].MessageID != "m3" {
		t.Fatalf("expected only m3, got %+v", events)
	}

	// round 5: old conversation changes → only the message newer than the start snapshot
	sb.mu.Lock()
	sb.convs[0].UpdatedAt = "2026-10-14T10:07:00.000Z"
	sb.messages["old"] = append(sb.messages["old"], msg("m4", "u0", "new dm", "2026-10-14T10:07:00.000Z"))
	sb.mu.Unlock()
	events, _ = p.poll(context.Background())
	if len(events) != 1 || events[0].MessageID != "m4" || events[0].ConversationID != "old" {
		t.Fatalf("old conversation: expected only m4, got %+v", events)
	}

	// round 6: the match conversation vanishes → matchUpdate closed for it
	sb.mu.Lock()
	sb.convs = sb.convs[:1]
	sb.mu.Unlock()
	events, _ = p.poll(context.Background())
	if len(events) != 1 || events[0].Type != "matchUpdate" || !events[0].Closed || events[0].ConversationID != "new" {
		t.Fatalf("expected closed event for new, got %+v", events)
	}
}

func TestBrowserEventPollerBackoffOnErrors(t *testing.T) {
	cfg := Config{APIBaseURL: "https://api.chitchat.gg", DMLimit: 100, MessageFetchLimit: 100}
	unauth := api.NewVia(browserExecutor{backend: &fakeBackend{resp: browser.FetchResponse{Status: 401, StatusText: "Unauthorized"}}}, model.LoginInfo{}, "")
	if _, ok := newBrowserEventPoller(cfg, unauth, "me", nil).poll(context.Background()); ok {
		t.Fatal("401 must ask for a backoff")
	}
	gone := api.NewVia(browserExecutor{backend: &fakeBackend{err: browser.ErrPageUnavailable}}, model.LoginInfo{}, "")
	if _, ok := newBrowserEventPoller(cfg, gone, "me", nil).poll(context.Background()); ok {
		t.Fatal("browser gone must ask for a backoff")
	}
}

func TestBrowserEventPollerRunStopsWithContext(t *testing.T) {
	cfg := Config{APIBaseURL: "https://api.chitchat.gg", DMLimit: 100, MessageFetchLimit: 100}
	sb := &scriptedBackend{messages: map[string][]model.Message{}}
	client := api.NewVia(browserExecutor{backend: sb}, model.LoginInfo{}, "")
	ctx, cancel := context.WithCancel(context.Background())
	events := startBrowserEventPoller(ctx, cfg, client, "me", nil)
	time.Sleep(30 * time.Millisecond)
	cancel()
	select {
	case _, open := <-events:
		if open {
			t.Fatal("no events expected")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("poller did not stop")
	}
}
