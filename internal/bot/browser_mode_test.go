package bot

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"chitchatbot/internal/api"
	"chitchatbot/internal/browser"
	"chitchatbot/internal/model"
	"chitchatbot/internal/proxy"
	"chitchatbot/internal/store"
)

type fakeBackend struct {
	last       browser.FetchRequest
	resp       browser.FetchResponse
	err        error
	captureErr error
	navigated  []string
	fetches    int
	afterNav   *browser.FetchResponse // response once Navigate was called
}

func (f *fakeBackend) Name() string                                 { return "fake" }
func (f *fakeBackend) Start(context.Context, browser.Options) error { return nil }
func (f *fakeBackend) CaptureSession(context.Context) (model.LoginInfo, error) {
	return model.LoginInfo{}, f.captureErr
}
func (f *fakeBackend) Navigate(_ context.Context, url string) error {
	f.navigated = append(f.navigated, url)
	return nil
}
func (f *fakeBackend) SetCookies(context.Context, []model.Cookie) error { return nil }
func (f *fakeBackend) Inspect(context.Context) (browser.Inspection, error) {
	return browser.Inspection{Timezone: "America/New_York", Languages: []string{"en-US"}}, nil
}
func (f *fakeBackend) DrainSocketEvents(context.Context) ([]string, error) { return nil, nil }
func (f *fakeBackend) Close() error                                        { return nil }
func (f *fakeBackend) Fetch(_ context.Context, req browser.FetchRequest) (browser.FetchResponse, error) {
	f.last = req
	f.fetches++
	if f.afterNav != nil && len(f.navigated) > 0 {
		return *f.afterNav, nil
	}
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

func TestBrowserExecutorReturnsToAppAfterFetchError(t *testing.T) {
	fb := &fakeBackend{
		resp:     browser.FetchResponse{Status: 0, StatusText: "FETCH_ERROR", Text: "TypeError: Failed to fetch"},
		afterNav: &browser.FetchResponse{OK: true, Status: 200, Text: "back"},
	}
	exec := browserExecutor{backend: fb, appURL: "https://app.chitchat.gg/"}
	resp, err := exec.Fetch(context.Background(), api.FetchRequest{Method: "GET", URL: "u"})
	if err != nil || resp.Text != "back" {
		t.Fatalf("expected retry after navigating back: %v %+v", err, resp)
	}
	if len(fb.navigated) != 1 || fb.navigated[0] != "https://app.chitchat.gg/" || fb.fetches != 2 {
		t.Fatalf("expected one navigate + two fetches, got nav=%v fetches=%d", fb.navigated, fb.fetches)
	}
}

func TestBrowserExecutorProbesWindowAfterRepeatedFailures(t *testing.T) {
	var failures atomic.Int32
	// window answers the probe → plain errors keep flowing, counter resets
	alive := browserExecutor{backend: &fakeBackend{err: errors.New("some odd error")}, failures: &failures}
	for i := 0; i < browserProbeAfterFailures+1; i++ {
		if _, err := alive.Fetch(context.Background(), api.FetchRequest{Method: "GET", URL: "u"}); errors.Is(err, errBrowserGone) {
			t.Fatalf("window alive: must not be reported gone (iteration %d)", i)
		}
	}
	// window does not answer the probe → reported gone after N failures
	failures.Store(0)
	dead := browserExecutor{backend: &fakeBackend{err: errors.New("some odd error"), captureErr: errors.New("no window")}, failures: &failures}
	var last error
	for i := 0; i < browserProbeAfterFailures; i++ {
		_, last = dead.Fetch(context.Background(), api.FetchRequest{Method: "GET", URL: "u"})
	}
	if !errors.Is(last, errBrowserGone) {
		t.Fatalf("window dead: expected errBrowserGone after %d failures, got %v", browserProbeAfterFailures, last)
	}
}

func TestBrowserWindowOptionsStealthAndTimezone(t *testing.T) {
	cfg := Config{AppURL: "https://app.chitchat.gg/", Headless: false}
	opts := browserWindowOptions(cfg, "/tmp/x", nil)
	if !opts.Stealth || !opts.LowMemory || opts.Locale != "en-US" || opts.StartURL != cfg.AppURL {
		t.Fatalf("unexpected options: %+v", opts)
	}
	// No proxy and no resolved zone: empty timezone = the PC's own zone.
	if opts.Timezone != "" {
		t.Fatalf("no proxy must leave the timezone empty (PC zone), got %q", opts.Timezone)
	}
	if opts.SocketTap {
		t.Fatal("socket tap must be off outside Browser mode")
	}
	// A proxy whose exit-country timezone was resolved beforehand is used.
	withTZ := &proxy.Entry{Scheme: "http", Host: "1.2.3.4", Port: "8080", Timezone: "America/Chicago"}
	if got := browserWindowOptions(cfg, "/tmp/x", withTZ).Timezone; got != "America/Chicago" {
		t.Fatalf("resolved proxy timezone must be used, got %s", got)
	}
	noTZ := &proxy.Entry{Scheme: "http", Host: "1.2.3.4", Port: "8080"}
	if got := browserWindowOptions(cfg, "/tmp/x", noTZ).Timezone; got != "" {
		t.Fatalf("unresolved proxy timezone must stay empty (PC zone), got %s", got)
	}
	// Browser mode turns the socket tap on (match detection through the page).
	live := cfg
	live.BrowserMode = true
	if got := browserWindowOptions(live, "/tmp/x", nil); !got.SocketTap {
		t.Fatal("Browser mode must enable the socket tap")
	}
}

func TestCloseReason(t *testing.T) {
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if r := closeReason(cancelled, errors.New("x")); r != "Stop pressed" {
		t.Fatal(r)
	}
	if r := closeReason(context.Background(), nil); r != "worker finished" {
		t.Fatal(r)
	}
	if r := closeReason(context.Background(), errBrowserGone); r != "window closed or unreachable" {
		t.Fatal(r)
	}
	if r := closeReason(context.Background(), errors.New("boom")); r != "worker error: boom" {
		t.Fatal(r)
	}
}

func TestAdoptBrowserSessionUsesAttachedWindow(t *testing.T) {
	dir := t.TempDir()
	// backend that is "logged in": cookies with a token and an authenticated API answer
	fb := &loggedInBackend{}
	cfg := Config{
		DataDir: dir, Profile: "acct", Backend: "rod", AppURL: "https://app.chitchat.gg/",
		APIBaseURL: "https://api.chitchat.gg", BrowserMode: true, AttachedBackend: fb,
		LoginInfoPath: filepath.Join(dir, "login_info_acct.json"),
	}
	p := profilePaths(cfg)
	if err := store.EnsureDir(p.ProfileDir); err != nil {
		t.Fatal(err)
	}
	s, login, err := openBrowserSession(context.Background(), cfg, p, model.LoginInfo{})
	if err != nil {
		t.Fatalf("adopt failed: %v", err)
	}
	defer s.release()
	if s.backend != fb || login.Token() != "tok" || fb.started {
		t.Fatalf("attached window must be adopted, not launched: started=%v login=%+v", fb.started, login)
	}
	if _, err := os.Stat(p.LoginInfo); err != nil {
		t.Fatalf("login file must be written: %v", err)
	}
}

type loggedInBackend struct {
	fakeBackend
	started bool
}

func (l *loggedInBackend) Start(context.Context, browser.Options) error { l.started = true; return nil }
func (l *loggedInBackend) CaptureSession(context.Context) (model.LoginInfo, error) {
	return model.LoginInfo{Cookies: []model.Cookie{{Name: "token", Value: "tok"}}, LocalStorage: map[string]string{"cachedUserId": "u-1"}}, nil
}
func (l *loggedInBackend) Fetch(context.Context, browser.FetchRequest) (browser.FetchResponse, error) {
	return browser.FetchResponse{OK: true, Status: 200, Text: "[]"}, nil
}
