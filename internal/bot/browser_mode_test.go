package bot

import (
	"context"
	"errors"
	"testing"
	"time"

	"chitchatbot/internal/api"
	"chitchatbot/internal/browser"
	"chitchatbot/internal/model"
)

type fakeBackend struct {
	last browser.FetchRequest
	resp browser.FetchResponse
	err  error
}

func (f *fakeBackend) Name() string                                 { return "fake" }
func (f *fakeBackend) Start(context.Context, browser.Options) error { return nil }
func (f *fakeBackend) CaptureSession(context.Context) (model.LoginInfo, error) {
	return model.LoginInfo{}, nil
}
func (f *fakeBackend) Navigate(context.Context, string) error           { return nil }
func (f *fakeBackend) SetCookies(context.Context, []model.Cookie) error { return nil }
func (f *fakeBackend) Close() error                                     { return nil }
func (f *fakeBackend) Fetch(_ context.Context, req browser.FetchRequest) (browser.FetchResponse, error) {
	f.last = req
	return f.resp, f.err
}

func TestBrowserExecutorStripsBrowserManagedHeaders(t *testing.T) {
	fb := &fakeBackend{resp: browser.FetchResponse{OK: true, Status: 200, Text: "ok"}}
	exec := browserExecutor{backend: fb}
	body := "x"
	resp, err := exec.Fetch(context.Background(), api.FetchRequest{
		Method: "POST", URL: "https://api.chitchat.gg/match", Body: &body,
		Headers: map[string]string{
			"Cookie": "token=secret", "User-Agent": "UA", "Origin": "o", "Referer": "r",
			"Sec-Fetch-Mode": "cors", "sec-ch-ua": "x",
			"Content-Type": "application/json", "transport-encoding": "abc", "accept": "*/*",
		},
	})
	if err != nil || resp.Status != 200 || resp.Text != "ok" {
		t.Fatalf("unexpected: %v %+v", err, resp)
	}
	for _, dropped := range []string{"Cookie", "User-Agent", "Origin", "Referer", "Sec-Fetch-Mode", "sec-ch-ua"} {
		if _, ok := fb.last.Headers[dropped]; ok {
			t.Fatalf("header %s must not be passed to the page fetch", dropped)
		}
	}
	for _, kept := range []string{"Content-Type", "transport-encoding", "accept"} {
		if _, ok := fb.last.Headers[kept]; !ok {
			t.Fatalf("header %s must be kept", kept)
		}
	}
	if fb.last.Body == nil || *fb.last.Body != "x" || fb.last.Method != "POST" {
		t.Fatalf("body/method not forwarded: %+v", fb.last)
	}
}

func TestBrowserExecutorErrors(t *testing.T) {
	gone := browserExecutor{backend: &fakeBackend{err: browser.ErrPageUnavailable}}
	if _, err := gone.Fetch(context.Background(), api.FetchRequest{Method: "GET", URL: "u"}); !errors.Is(err, errBrowserGone) {
		t.Fatalf("page unavailable must map to errBrowserGone, got %v", err)
	}
	ws := browserExecutor{backend: &fakeBackend{err: errors.New("websocket: close 1006 (abnormal closure)")}}
	if _, err := ws.Fetch(context.Background(), api.FetchRequest{Method: "GET", URL: "u"}); !errors.Is(err, errBrowserGone) {
		t.Fatalf("websocket drop must map to errBrowserGone, got %v", err)
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	stop := browserExecutor{backend: &fakeBackend{err: context.Canceled}}
	if _, err := stop.Fetch(cancelled, api.FetchRequest{Method: "GET", URL: "u"}); errors.Is(err, errBrowserGone) {
		t.Fatalf("Stop (worker ctx cancelled) must not be reported as a closed window, got %v", err)
	}
	netfail := browserExecutor{backend: &fakeBackend{resp: browser.FetchResponse{Status: 0, StatusText: "FETCH_ERROR", Text: "TypeError: Failed to fetch"}}}
	if _, err := netfail.Fetch(context.Background(), api.FetchRequest{Method: "GET", URL: "u"}); err == nil || errors.Is(err, errBrowserGone) {
		t.Fatalf("in-page network error must be a plain error, got %v", err)
	}
	// a 401 is a normal response (the loop turns it into a re-login), not an error
	unauth := browserExecutor{backend: &fakeBackend{resp: browser.FetchResponse{Status: 401, StatusText: "Unauthorized"}}}
	if resp, err := unauth.Fetch(context.Background(), api.FetchRequest{Method: "GET", URL: "u"}); err != nil || resp.Status != 401 {
		t.Fatalf("401 must pass through: %v %+v", err, resp)
	}
}

func TestBrowserModeClientUsesWindow(t *testing.T) {
	fb := &fakeBackend{resp: browser.FetchResponse{OK: true, Status: 200, Text: "{}"}}
	s := &browserSession{backend: fb, cfg: Config{AppURL: "https://app.chitchat.gg/"}}
	c := s.client(model.LoginInfo{})
	if !c.Via() {
		t.Fatal("browser-mode client must delegate to the window")
	}
	if _, err := c.Fetch(context.Background(), api.FetchRequest{Method: "GET", URL: "https://api.chitchat.gg/users/me"}); err != nil {
		t.Fatal(err)
	}
	if fb.last.URL != "https://api.chitchat.gg/users/me" {
		t.Fatalf("request did not reach the window: %+v", fb.last)
	}
}

func TestWindowSlotSerialisesSameProfile(t *testing.T) {
	cfg := Config{}
	release, err := acquireWindowSlot(context.Background(), cfg, "slot-test")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, err := acquireWindowSlot(ctx, cfg, "slot-test"); err == nil {
		t.Fatal("second window on the same profile must wait until the first is released")
	}
	release()
	release2, err := acquireWindowSlot(context.Background(), cfg, "slot-test")
	if err != nil {
		t.Fatalf("slot must be free after release: %v", err)
	}
	release2()
	// a different profile is never blocked
	release3, err := acquireWindowSlot(ctx, cfg, "slot-other")
	if err != nil {
		t.Fatal(err)
	}
	release3()
}
