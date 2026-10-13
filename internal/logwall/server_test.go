package logwall

import (
	"bufio"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"chitchatbot/internal/model"
)

func newTestServer(t *testing.T) *Server {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.png"), []byte("png"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	s := New(Options{
		Port:          0, // default, falls through free ports
		BackgroundDir: dir,
		Stats: func() (model.HomeStats, error) {
			return model.HomeStats{BotAccounts: 3, MessagesSent: 42}, nil
		},
		History: func(limit int) ([]model.LogEntry, error) {
			return []model.LogEntry{{Time: "2026-01-01T00:00:00Z", Level: "activity", Message: "SEND | bot a -> user b | message: hi"}}, nil
		},
		StatsInterval: 50 * time.Millisecond,
	})
	if _, err := s.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func TestSnapshotAndBackgrounds(t *testing.T) {
	s := newTestServer(t)
	resp, err := http.Get(s.URL() + "api/snapshot")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var snap snapshot
	if err := json.NewDecoder(resp.Body).Decode(&snap); err != nil {
		t.Fatal(err)
	}
	if snap.Stats.MessagesSent != 42 || len(snap.Logs) != 1 {
		t.Fatalf("unexpected snapshot: %+v", snap)
	}
	if len(snap.Backgrounds) != 1 || snap.Backgrounds[0] != "a.png" {
		t.Fatalf("backgrounds: %v (txt must be ignored)", snap.Backgrounds)
	}

	bg, err := http.Get(s.URL() + "bg/a.png")
	if err != nil {
		t.Fatal(err)
	}
	bg.Body.Close()
	if bg.StatusCode != 200 || bg.Header.Get("Content-Type") != "image/png" {
		t.Fatalf("bg status=%d ctype=%s", bg.StatusCode, bg.Header.Get("Content-Type"))
	}
	// path traversal / non-image must 404
	for _, p := range []string{"bg/notes.txt", "bg/../server.go", "bg/"} {
		r, err := http.Get(s.URL() + p)
		if err != nil {
			t.Fatal(err)
		}
		r.Body.Close()
		if r.StatusCode != 404 {
			t.Fatalf("%s: expected 404 got %d", p, r.StatusCode)
		}
	}
}

func TestIndexServed(t *testing.T) {
	s := newTestServer(t)
	resp, err := http.Get(s.URL())
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 || !strings.Contains(string(body), "LIVE WALL") {
		t.Fatalf("index not served: %d", resp.StatusCode)
	}
}

func TestHostGuard(t *testing.T) {
	s := newTestServer(t)
	req, _ := http.NewRequest("GET", s.URL()+"api/snapshot", nil)
	req.Host = "evil.example.com"
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403 for foreign host, got %d", resp.StatusCode)
	}
}

func TestEventsStream(t *testing.T) {
	s := newTestServer(t)
	resp, err := http.Get(s.URL() + "events")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if ct := resp.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("content-type %q", ct)
	}
	reader := bufio.NewReader(resp.Body)
	readEvent := func() event {
		t.Helper()
		deadline := time.Now().Add(3 * time.Second)
		for time.Now().Before(deadline) {
			line, err := reader.ReadString('\n')
			if err != nil {
				t.Fatal(err)
			}
			if strings.HasPrefix(line, "data: ") {
				var ev event
				if err := json.Unmarshal([]byte(strings.TrimPrefix(strings.TrimSpace(line), "data: ")), &ev); err != nil {
					t.Fatal(err)
				}
				return ev
			}
		}
		t.Fatal("no event")
		return event{}
	}

	if ev := readEvent(); ev.Type != "running" {
		t.Fatalf("first event should be running, got %s", ev.Type)
	}
	// wait until the client is registered, then publish
	for i := 0; i < 50 && s.ClientCount() == 0; i++ {
		time.Sleep(10 * time.Millisecond)
	}
	s.Publish(model.LogEntry{Time: "t", Level: "activity", Message: "RECEIVE | user b -> bot a | message: yo"})
	s.SetRunning(true)
	s.LogsCleared()

	seen := map[string]bool{}
	for len(seen) < 4 {
		ev := readEvent()
		seen[ev.Type] = true
	}
	for _, want := range []string{"log", "running", "logs_cleared", "stats"} {
		if !seen[want] {
			t.Fatalf("missing event %s (seen %v)", want, seen)
		}
	}
}

func TestStartIdempotentAndClose(t *testing.T) {
	s := newTestServer(t)
	u1, _ := s.Start()
	u2, _ := s.Start()
	if u1 != u2 || !s.Started() {
		t.Fatalf("start not idempotent: %s %s", u1, u2)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Start(); err == nil {
		t.Fatal("start after close should fail")
	}
}
