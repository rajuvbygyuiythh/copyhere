package bot

// Browser mode — the real browser window stays open for the whole run and
// every request the bot makes (match, conversations, typing, send) is
// executed INSIDE that page (Backend.Fetch = page fetch with the real
// cookies, TLS, user agent and proxy). No Go http client, no Go websocket.
//
// Lifecycle per account (worker):
//  1. open the browser with the account's persistent profile folder
//     (visible unless Headless), inject the saved cookies if the folder is
//     new, wait until the site is authenticated (owner logs in if needed),
//     save login_info.json;
//  2. chat loop as usual, but client = browser-executed fetch and no Go
//     realtime listener (the loop polls);
//  3. session expired (401) → tell the owner to log in again in the window
//     and wait, instead of stopping;
//  4. Stop (or any exit) → capture the session once more, save
//     login_info.json, close the browser.
// Everything here is only used when Config.BrowserMode is true.

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"chitchatbot/internal/api"
	"chitchatbot/internal/browser"
	"chitchatbot/internal/model"
	"chitchatbot/internal/proxy"
	"chitchatbot/internal/store"
)

// errBrowserGone is returned when the browser window/page is no longer
// usable (owner closed it, crash). The worker stops with one log line.
var errBrowserGone = errors.New("browser window closed or unreachable")

// browserHeadersDropped are set by the browser itself; passing them to
// fetch() is ignored or rejected, so the executor removes them.
var browserHeadersDropped = map[string]bool{
	"cookie": true, "origin": true, "referer": true, "user-agent": true,
	"sec-fetch-dest": true, "sec-fetch-mode": true, "sec-fetch-site": true,
	"sec-ch-ua": true, "sec-ch-ua-mobile": true, "sec-ch-ua-platform": true,
	"accept-encoding": true, "connection": true, "content-length": true, "host": true,
}

// browserExecutor adapts browser.Backend to api.Executor. mu (optional)
// serialises in-page fetches of one window: the chat loop and the message
// poller share the page and CDP session. appURL (optional) lets the executor
// bring the page back to the app when an in-page fetch fails (owner clicked
// away to another site → cross-origin fetch has no cookies). failures
// (optional) counts consecutive errors; after a few the window is probed and
// reported as gone if it does not answer, whatever the error text was.
type browserExecutor struct {
	backend  browser.Backend
	mu       *sync.Mutex
	appURL   string
	failures *atomic.Int32
}

// browserProbeAfterFailures = consecutive errors before a liveness probe.
const browserProbeAfterFailures = 3

func (e browserExecutor) Fetch(ctx context.Context, req api.FetchRequest) (api.FetchResponse, error) {
	if e.mu != nil {
		e.mu.Lock()
		defer e.mu.Unlock()
	}
	headers := make(map[string]string, len(req.Headers))
	for k, v := range req.Headers {
		if browserHeadersDropped[strings.ToLower(strings.TrimSpace(k))] {
			continue
		}
		headers[k] = v
	}
	breq := browser.FetchRequest{Method: req.Method, URL: req.URL, Headers: headers, Body: req.Body}

	resp, err := e.backend.Fetch(ctx, breq)
	if err == nil && resp.Status == 0 && resp.StatusText == "FETCH_ERROR" && e.appURL != "" && ctx.Err() == nil {
		// The page may have left the app (manual click). Go back once and retry.
		if navErr := e.backend.Navigate(ctx, e.appURL); navErr == nil {
			resp, err = e.backend.Fetch(ctx, breq)
		}
	}
	if err != nil {
		if ctx.Err() != nil {
			return api.FetchResponse{}, ctx.Err()
		}
		if isBrowserGoneError(err) || e.windowGone(ctx) {
			return api.FetchResponse{}, fmt.Errorf("%w: %v", errBrowserGone, err)
		}
		return api.FetchResponse{}, err
	}
	if resp.Status == 0 && resp.StatusText == "FETCH_ERROR" {
		// network error inside the page (offline, proxy down, page navigated
		// away mid-request): report it as a normal request failure.
		if e.windowGone(ctx) {
			return api.FetchResponse{}, fmt.Errorf("%w: %s", errBrowserGone, strings.TrimSpace(resp.Text))
		}
		return api.FetchResponse{}, fmt.Errorf("browser fetch failed: %s", strings.TrimSpace(resp.Text))
	}
	if e.failures != nil {
		e.failures.Store(0)
	}
	return api.FetchResponse{OK: resp.OK, Status: resp.Status, StatusText: resp.StatusText, Text: resp.Text}, nil
}

// windowGone counts a failure and, after browserProbeAfterFailures in a row,
// asks the window for its cookies; no answer = the window is gone.
func (e browserExecutor) windowGone(ctx context.Context) bool {
	if e.failures == nil {
		return false
	}
	if e.failures.Add(1) < browserProbeAfterFailures {
		return false
	}
	probeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if _, err := e.backend.CaptureSession(probeCtx); err != nil && ctx.Err() == nil {
		return true
	}
	e.failures.Store(0)
	return false
}

// isBrowserGoneError recognises "the browser itself is gone" failures from
// both backends: our own sentinel, the CDP websocket dropping (rod) and the
// browser context dying (chromedp) while the worker context is still alive.
func isBrowserGoneError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, browser.ErrPageUnavailable) || errors.Is(err, context.Canceled) {
		return true
	}
	msg := strings.ToLower(err.Error())
	for _, needle := range []string{
		"websocket", "use of closed network connection", "connection reset", "broken pipe",
		"eof", "target closed", "browser has disconnected", "browser is closed", "no such session",
		"chrome has closed", "process already finished", "connection refused",
	} {
		if strings.Contains(msg, needle) {
			return true
		}
	}
	return false
}

// browserSession is the open window of one worker.
type browserSession struct {
	backend  browser.Backend
	cfg      Config
	p        paths
	proxy    *proxy.Entry
	release  func()
	fetchMu  sync.Mutex
	relogin  atomic.Bool  // true while waiting for a new login in the window
	failures atomic.Int32 // consecutive in-page fetch failures
}

// paused reports whether the window is busy with a re-login.
func (s *browserSession) paused() bool { return s.relogin.Load() }

// windowSlots serialises windows per profile folder: Stop followed by a
// quick Start must not launch a second Chrome on the same user-data-dir
// while the previous window is still saving its session.
var windowSlots sync.Map // profile dir -> chan struct{} (capacity 1)

func acquireWindowSlot(ctx context.Context, cfg Config, key string) (func(), error) {
	v, _ := windowSlots.LoadOrStore(key, make(chan struct{}, 1))
	slot := v.(chan struct{})
	release := func() { <-slot }
	select {
	case slot <- struct{}{}:
		return release, nil
	default:
	}
	logln(cfg, "[browser] Previous window of this profile is still closing; waiting for it...")
	select {
	case slot <- struct{}{}:
		return release, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// openBrowserSession starts the visible browser for this worker and returns
// once the site is authenticated. The browser is started with a detached
// context on purpose: Stop cancels the worker context, and we still need the
// window alive for a moment to save the session before closing it.
func openBrowserSession(ctx context.Context, cfg Config, p paths, saved model.LoginInfo) (*browserSession, model.LoginInfo, error) {
	backend, err := browser.New(cfg.Backend)
	if err != nil {
		return nil, model.LoginInfo{}, err
	}
	userDataDir := filepath.Join(p.ProfileDir, "browser-data", cfg.Backend)
	if err := store.EnsureDir(userDataDir); err != nil {
		return nil, model.LoginInfo{}, err
	}
	release, err := acquireWindowSlot(ctx, cfg, userDataDir)
	if err != nil {
		return nil, model.LoginInfo{}, err
	}
	fail := func(err error) (*browserSession, model.LoginInfo, error) {
		release()
		return nil, model.LoginInfo{}, err
	}

	var selectedProxy *proxy.Entry
	if cfg.ProxyRotator != nil {
		if nextProxy, ok := cfg.ProxyRotator.Next(); ok {
			selectedProxy = &nextProxy
			logf(cfg, "[browser] Window proxy (fixed for this window): %s\n", nextProxy.Label())
		}
	}

	mode := "visible"
	if cfg.Headless {
		mode = "headless"
	}
	logf(cfg, "[browser] Browser mode on: opening %s %s window for %q (profile data: %s). It stays open until you press Stop.\n", mode, cfg.Backend, cfg.Profile, userDataDir)

	if err := backend.Start(context.Background(), browser.Options{
		StartURL:    cfg.AppURL,
		UserDataDir: userDataDir,
		Headless:    cfg.Headless,
		Proxy:       selectedProxy,
	}); err != nil {
		return fail(fmt.Errorf("browser mode: could not start the browser: %w", err))
	}
	session := &browserSession{backend: backend, cfg: cfg, p: p, proxy: selectedProxy, release: release}

	// Already logged in inside the browser profile? Then nothing to inject.
	authenticated := false
	if current, err := backend.CaptureSession(ctx); err == nil && current.Token() != "" {
		if ok, authErr := browser.SessionAuthenticated(ctx, backend, cfg.APIBaseURL); authErr == nil && ok {
			authenticated = true
		}
	}
	if !authenticated && saved.Token() != "" && len(saved.Cookies) > 0 {
		logf(cfg, "[browser] Injecting the saved session (%d cookies) into the window.\n", len(saved.Cookies))
		if err := backend.SetCookies(ctx, saved.Cookies); err != nil {
			logf(cfg, "[browser] Cookie injection failed (%v). Please log in inside the window.\n", err)
		} else if err := backend.Navigate(ctx, cfg.AppURL); err != nil {
			logf(cfg, "[browser] Reload after cookie injection failed: %v\n", err)
		}
	}
	if !authenticated {
		logln(cfg, "[browser] Waiting for the site to be logged in inside the browser window...")
	}

	login, err := waitForLogin(ctx, backend, cfg, p)
	if err != nil {
		_ = backend.Close()
		return fail(err)
	}
	logf(cfg, "[browser] Logged in as %s. Chatting through the browser window from now on.\n", login.UserID)
	return session, login, nil
}

// client returns the api client that runs inside the window.
func (s *browserSession) client(login model.LoginInfo) *api.Client {
	return api.NewVia(browserExecutor{backend: s.backend, mu: &s.fetchMu, appURL: s.cfg.AppURL, failures: &s.failures}, login, s.cfg.AppURL)
}

// relogin is used when the API answers 401 in browser mode: bring the
// window back to the app and wait for the owner (or the cookies) to log in.
func (s *browserSession) reloginInWindow(ctx context.Context) (model.LoginInfo, error) {
	s.relogin.Store(true)
	defer s.relogin.Store(false)
	s.fetchMu.Lock()
	defer s.fetchMu.Unlock()
	logln(s.cfg, "[browser] Session expired. Log in again inside the browser window; the bot resumes automatically.")
	if err := s.backend.Navigate(ctx, s.cfg.AppURL); err != nil {
		if ctx.Err() != nil {
			return model.LoginInfo{}, ctx.Err()
		}
		if isBrowserGoneError(err) {
			return model.LoginInfo{}, fmt.Errorf("%w: %v", errBrowserGone, err)
		}
		logf(s.cfg, "[browser] Could not reload the app page: %v\n", err)
	}
	login, err := waitForLogin(ctx, s.backend, s.cfg, s.p)
	if err != nil {
		return model.LoginInfo{}, err
	}
	logf(s.cfg, "[browser] Logged in again as %s. Resuming.\n", login.UserID)
	return login, nil
}

// saveAndClose captures the live session one last time (fresh cookies /
// storage), writes login_info.json and closes the window. It uses its own
// short timeout because the worker context is usually already cancelled.
func (s *browserSession) saveAndClose(previous model.LoginInfo) {
	if s.release != nil {
		defer s.release()
	}
	s.fetchMu.Lock()
	defer s.fetchMu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	login, err := s.backend.CaptureSession(ctx)
	switch {
	case err != nil:
		logf(s.cfg, "[browser] Could not read the session before closing (%v). Keeping the previously saved login.\n", err)
	case login.Token() == "":
		logln(s.cfg, "[browser] Window is logged out; keeping the previously saved login file.")
	default:
		login.UserID = login.CurrentUserID()
		if login.UserID == "" {
			login.UserID = previous.UserID
		}
		login.Profile = s.cfg.Profile
		login.Backend = s.cfg.Backend
		login.SavedAt = time.Now().UTC()
		if err := store.SaveJSON(s.p.LoginInfo, login); err != nil {
			logf(s.cfg, "[browser] Could not save the session: %v\n", err)
		} else {
			logf(s.cfg, "[browser] Session saved to %s for the next start.\n", s.p.LoginInfo)
		}
	}
	if err := s.backend.Close(); err != nil {
		logf(s.cfg, "[browser] Close: %v\n", err)
	} else {
		logln(s.cfg, "[browser] Browser window closed.")
	}
}
