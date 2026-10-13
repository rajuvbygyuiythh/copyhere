package browser

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"chitchatbot/internal/model"
	"chitchatbot/internal/runtimeassets"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/cdp"
	"github.com/go-rod/rod/lib/launcher"
	"github.com/go-rod/rod/lib/proto"
)

type RodBackend struct {
	browser             *rod.Browser
	page                *rod.Page
	startURL            string
	disablePageRecovery bool
	stopProxyAuth       func()
	options             Options
	// proc/exitCh own the Chrome process this backend launched (launchRodBrowser
	// starts it manually, so rod's CDP Browser.close alone can leave the whole
	// Chrome process tree orphaned when Stop fails or the connection died).
	proc      *exec.Cmd
	exitCh    chan error
	closed    atomic.Bool
	closeOnce sync.Once
}

func (b *RodBackend) Name() string {
	return "rod"
}

func (b *RodBackend) Start(ctx context.Context, options Options) error {
	b.closed.Store(false)
	controlURL, proc, exitCh, err := launchRodBrowser(ctx, options)
	if err != nil {
		return err
	}
	// Own the launched process from this moment on: any later failure must go
	// through b.Close(), which kills the process tree if CDP close fails.
	b.proc = proc
	b.exitCh = exitCh

	browser := rod.New().
		ControlURL(controlURL).
		NoDefaultDevice()
	b.browser = browser

	if err := browser.Connect(); err != nil {
		_ = b.Close()
		return err
	}

	if options.Proxy != nil && options.Proxy.HasAuth() {
		b.stopProxyAuth = startRodProxyAuth(ctx, browser, options.Proxy.Username, options.Proxy.Password)
	}

	b.options = options

	// Open the page blank first so timezone/locale/stealth are in place
	// before the app loads, then navigate. All start-up page work runs on
	// the caller's context: cancelling it (dashboard Stop) aborts a slow
	// proxied page load immediately instead of waiting for it to finish.
	page, err := browser.Page(proto.TargetCreateTarget{URL: "about:blank"})
	if err != nil {
		_ = b.Close()
		return err
	}
	startPage := page.Context(ctx)
	if err := b.preparePage(startPage); err != nil {
		fmt.Printf("rod: page emulation setup failed: %v\n", err)
	}
	if err := startPage.Navigate(options.StartURL); err != nil {
		_ = b.Close()
		return err
	}

	if err := startPage.WaitLoad(); err != nil && !isRecoverablePageError(err) {
		_ = b.Close()
		return err
	}

	b.page = page
	b.startURL = options.StartURL
	b.disablePageRecovery = options.DisablePageRecovery
	return nil
}

// preparePage installs the timezone/locale emulation and the stealth init
// script on a page BEFORE it loads the app. Called for the start page and
// for every page recoverPage adopts.
func (b *RodBackend) preparePage(page *rod.Page) error {
	var firstErr error
	if b.options.Timezone != "" {
		if err := (proto.EmulationSetTimezoneOverride{TimezoneID: b.options.Timezone}).Call(page); err != nil && firstErr == nil {
			firstErr = fmt.Errorf("timezone %q: %w", b.options.Timezone, err)
		}
	}
	if b.options.Timezone != "" || b.options.Stealth {
		if err := (proto.EmulationSetLocaleOverride{Locale: localeOrDefault(b.options.Locale)}).Call(page); err != nil && firstErr == nil {
			firstErr = fmt.Errorf("locale: %w", err)
		}
	}
	if b.options.Stealth {
		if _, err := page.EvalOnNewDocument(stealthScript(b.options)); err != nil && firstErr == nil {
			firstErr = fmt.Errorf("stealth script: %w", err)
		}
	}
	if b.options.SocketTap {
		if _, err := page.EvalOnNewDocument(socketTapScript); err != nil && firstErr == nil {
			firstErr = fmt.Errorf("socket tap script: %w", err)
		}
	}
	return firstErr
}

// DrainSocketEvents returns the realtime frames the in-page tap collected.
func (b *RodBackend) DrainSocketEvents(ctx context.Context) ([]string, error) {
	var frames []string
	err := b.withPageRetry(ctx, func(page *rod.Page) error {
		result, err := page.Eval("(" + drainSocketTapFunction + ")()")
		if err != nil {
			return err
		}
		return result.Value.Unmarshal(&frames)
	})
	if err != nil {
		return nil, err
	}
	return frames, nil
}

// Inspect returns what the page's JavaScript sees.
func (b *RodBackend) Inspect(ctx context.Context) (Inspection, error) {
	var result Inspection
	err := b.withPageRetry(ctx, func(page *rod.Page) error {
		obj, err := page.Eval("(" + inspectFunction + ")()")
		if err != nil {
			return err
		}
		return obj.Value.Unmarshal(&result)
	})
	return result, err
}

func launchRodBrowser(ctx context.Context, options Options) (string, *exec.Cmd, chan error, error) {
	debugPort, err := reserveDebugPort()
	if err != nil {
		return "", nil, nil, err
	}

	l := launcher.New().
		Context(ctx).
		UserDataDir(options.UserDataDir).
		Headless(options.Headless).
		RemoteDebuggingPort(debugPort)

	if runtime.GOOS == "windows" {
		// Windows Defender commonly flags Rod's leakless helper. Disable it and
		// let the app manage browser shutdown directly.
		l.Leakless(false)
	}

	if options.Proxy != nil {
		l.Proxy(options.Proxy.BrowserURL())
		l.Set("proxy-bypass-list", "<-loopback>")
	}
	applyRodLaunchOptions(l, options)

	bin, err := resolveRodBrowserBinary(options.UserDataDir)
	if err != nil {
		return "", nil, nil, err
	}
	l.Bin(bin)

	cmd := exec.Command(bin, l.FormatArgs()...)

	if err := cmd.Start(); err != nil {
		return "", nil, nil, err
	}

	exitCh := make(chan error, 1)
	go func() {
		exitCh <- cmd.Wait()
	}()

	controlURL, err := waitForRodDebugURL(ctx, exitCh, debugPort)
	if err != nil {
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		return "", nil, nil, err
	}

	return controlURL, cmd, exitCh, nil
}

// applyRodLaunchOptions adds the stealth / low-memory flags. Rod's defaults
// include --enable-automation (navigator.webdriver = true + infobar) and
// --no-startup-window; both are removed in stealth mode.
func applyRodLaunchOptions(l *launcher.Launcher, options Options) {
	if options.Stealth {
		l.Delete("enable-automation")
		l.Set("disable-blink-features", "AutomationControlled")
		l.Set("lang", localeOrDefault(options.Locale))
		// no local/VPN IP leak via WebRTC when a proxy is in use
		l.Set("webrtc-ip-handling-policy", "disable_non_proxied_udp")
		l.Set("force-webrtc-ip-handling-policy")
		size := options.WindowSize
		if size == "" {
			size = PickWindowSize(options.UserDataDir)
		}
		l.Set("window-size", size)
		l.Set("no-default-browser-check")
		l.Set("password-store", "basic")
	}
	// Background mode OFF unconditionally: with it on, closing the window
	// leaves chrome.exe alive in the tray and a later page recovery can pop
	// a fresh window open — the "browser reopened by itself" effect. The
	// bot owns this profile, so the process must die with its window.
	l.Set("disable-background-mode")
	if options.LowMemory {
		l.Set("renderer-process-limit", "2")
		l.Set("disable-extensions")
		l.Set("disable-component-update")
		l.Set("disable-domain-reliability")
		l.Set("disable-speech-api")
		l.Set("no-pings")
		l.Set("js-flags", "--max-old-space-size=256")
	}
}

func localeOrDefault(locale string) string {
	if strings.TrimSpace(locale) == "" {
		return "en-US"
	}
	return locale
}

func resolveRodBrowserBinary(userDataDir string) (string, error) {
	if bin, reason := runtimeassets.BundledChromiumPathForProfile(userDataDir); bin != "" {
		fmt.Printf("rod: using bundled browser: %s\n", bin)
		return bin, nil
	} else if reason != "" {
		fmt.Printf("rod: skipping bundled browser: %s\n", reason)
	}

	if bin, ok := launcher.LookPath(); ok {
		fmt.Printf("rod: using installed browser: %s\n", bin)
		return bin, nil
	}

	fmt.Println("rod: no installed browser found, falling back to managed Chromium download")
	return launcher.NewBrowser().Get()
}

func reserveDebugPort() (int, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer listener.Close()

	addr, ok := listener.Addr().(*net.TCPAddr)
	if !ok {
		return 0, fmt.Errorf("unexpected listener address type %T", listener.Addr())
	}

	return addr.Port, nil
}

func waitForRodDebugURL(ctx context.Context, exitCh <-chan error, port int) (string, error) {
	endpoint := fmt.Sprintf("127.0.0.1:%d", port)
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()

	for {
		u, err := launcher.ResolveURL(endpoint)
		if err == nil {
			return u, nil
		}

		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case waitErr := <-exitCh:
			if waitErr == nil {
				return "", fmt.Errorf("rod browser exited before the debugging endpoint became ready")
			}
			return "", fmt.Errorf("rod browser exited before the debugging endpoint became ready: %w", waitErr)
		case <-ticker.C:
		}
	}
}

// Close ALWAYS leaves the Chrome process this backend launched fully dead:
// graceful CDP Browser.close first, then a forced process-tree kill if the
// process is still alive (CDP close alone silently no-ops when the connection
// already broke — the old "Stop doesn't totally close the browser" bug).
// Idempotent: the fresh-window path and its worker both close the backend.
func (b *RodBackend) Close() error {
	b.closed.Store(true)
	var closeErr error
	b.closeOnce.Do(func() {
		if b.stopProxyAuth != nil {
			b.stopProxyAuth()
			b.stopProxyAuth = nil
		}
		if b.browser != nil {
			// rod's Browser.Close has no timeout; never let a hung CDP
			// connection block Stop forever.
			done := make(chan error, 1)
			browser := b.browser
			go func() { done <- browser.Close() }()
			select {
			case closeErr = <-done:
			case <-time.After(4 * time.Second):
			}
			b.browser = nil
			b.page = nil
		}
		if err := b.ensureProcessExited(); closeErr == nil {
			closeErr = err
		}
	})
	return closeErr
}

// ensureProcessExited waits briefly for Chrome to exit, then force-kills the
// whole process tree (Windows spawns renderer/GPU children that survive a
// plain Process.Kill of the parent).
func (b *RodBackend) ensureProcessExited() error {
	if b.proc == nil || b.proc.Process == nil {
		return nil
	}
	if b.exitCh != nil {
		select {
		case <-b.exitCh:
			return nil
		case <-time.After(2 * time.Second):
		}
	}
	killBrowserProcessTree(b.proc.Process)
	if b.exitCh != nil {
		select {
		case <-b.exitCh:
			return nil
		case <-time.After(3 * time.Second):
		}
	}
	return fmt.Errorf("rod: browser process %d did not exit after kill", b.proc.Process.Pid)
}

func killBrowserProcessTree(proc *os.Process) {
	if proc == nil {
		return
	}
	if runtime.GOOS == "windows" {
		// /T kills the entire child tree (renderers, GPU, network service).
		_ = exec.Command("taskkill", "/F", "/T", "/PID", strconv.Itoa(proc.Pid)).Run()
		return
	}
	_ = proc.Kill()
}

func startRodProxyAuth(parent context.Context, browser *rod.Browser, username string, password string) func() {
	restoreFetch := browser.EnableDomain("", &proto.FetchEnable{
		HandleAuthRequests: true,
	})

	authCtx, cancel := context.WithCancel(parent)
	go browser.Context(authCtx).EachEvent(
		func(e *proto.FetchRequestPaused) {
			if err := (proto.FetchContinueRequest{RequestID: e.RequestID}).Call(browser); err != nil && authCtx.Err() == nil {
				fmt.Printf("rod: failed to continue paused proxy request: %v\n", err)
			}
		},
		func(e *proto.FetchAuthRequired) {
			response := &proto.FetchAuthChallengeResponse{
				Response: proto.FetchAuthChallengeResponseResponseDefault,
			}

			if e.AuthChallenge != nil && e.AuthChallenge.Source == proto.FetchAuthChallengeSourceProxy {
				response = &proto.FetchAuthChallengeResponse{
					Response: proto.FetchAuthChallengeResponseResponseProvideCredentials,
					Username: username,
					Password: password,
				}
			}

			if err := (proto.FetchContinueWithAuth{
				RequestID:             e.RequestID,
				AuthChallengeResponse: response,
			}).Call(browser); err != nil && authCtx.Err() == nil {
				fmt.Printf("rod: failed to continue proxy auth challenge: %v\n", err)
			}
		},
	)()

	return func() {
		cancel()
		restoreFetch()
	}
}

func (b *RodBackend) CaptureSession(ctx context.Context) (model.LoginInfo, error) {
	var login model.LoginInfo

	err := b.withPageRetry(ctx, func(page *rod.Page) error {
		cookies, err := page.Cookies([]string{
			"https://app.chitchat.gg/",
			"https://api.chitchat.gg/",
		})
		if err != nil {
			return err
		}

		storageResult, err := page.Eval(storageFunction)
		if err != nil {
			return err
		}

		fingerprintResult, err := page.Eval(fingerprintFunction)
		if err != nil {
			return err
		}

		var storage struct {
			LocalStorage   map[string]string `json:"local_storage"`
			SessionStorage map[string]string `json:"session_storage"`
		}
		if err := storageResult.Value.Unmarshal(&storage); err != nil {
			return err
		}

		var fingerprint model.TransportFingerprint
		if err := fingerprintResult.Value.Unmarshal(&fingerprint); err != nil {
			return err
		}

		login = model.LoginInfo{
			Backend:              b.Name(),
			Cookies:              make([]model.Cookie, 0, len(cookies)),
			UserAgent:            fingerprint.UserAgent,
			LocalStorage:         storage.LocalStorage,
			SessionStorage:       storage.SessionStorage,
			TransportFingerprint: fingerprint,
		}

		for _, cookie := range cookies {
			login.Cookies = append(login.Cookies, model.Cookie{
				Name:     cookie.Name,
				Value:    cookie.Value,
				Domain:   cookie.Domain,
				Path:     cookie.Path,
				Expires:  float64(cookie.Expires),
				Secure:   cookie.Secure,
				HTTPOnly: cookie.HTTPOnly,
			})
		}

		return nil
	})

	return login, err
}

// Navigate loads url in the current page and waits for the load event.
func (b *RodBackend) Navigate(ctx context.Context, url string) error {
	return b.withPageRetry(ctx, func(page *rod.Page) error {
		if err := page.Navigate(url); err != nil {
			return err
		}
		if err := page.WaitLoad(); err != nil && !isRecoverablePageError(err) {
			return err
		}
		return nil
	})
}

// SetCookies injects cookies into the browser (all pages share the jar).
func (b *RodBackend) SetCookies(ctx context.Context, cookies []model.Cookie) error {
	if b.browser == nil {
		return ErrPageUnavailable
	}
	params := make([]*proto.NetworkCookieParam, 0, len(cookies))
	for _, c := range cookies {
		if strings.TrimSpace(c.Name) == "" {
			continue
		}
		param := &proto.NetworkCookieParam{
			Name:     c.Name,
			Value:    c.Value,
			Domain:   c.Domain,
			Path:     c.Path,
			Secure:   c.Secure,
			HTTPOnly: c.HTTPOnly,
		}
		if param.Domain == "" {
			param.URL = "https://app.chitchat.gg/"
		}
		if param.Path == "" {
			param.Path = "/"
		}
		if c.Expires > 0 {
			param.Expires = proto.TimeSinceEpoch(c.Expires)
		}
		params = append(params, param)
	}
	if len(params) == 0 {
		return nil
	}
	return b.browser.Context(ctx).SetCookies(params)
}

func (b *RodBackend) Fetch(ctx context.Context, req FetchRequest) (FetchResponse, error) {
	payload := map[string]any{
		"url":     req.URL,
		"method":  req.Method,
		"headers": req.Headers,
	}
	if req.Body != nil {
		payload["body"] = *req.Body
	}

	payloadJSON, err := json.Marshal(payload)
	if err != nil {
		return FetchResponse{}, err
	}

	var response FetchResponse
	err = b.withPageRetry(ctx, func(page *rod.Page) error {
		result, err := page.Eval(fetchFunction, json.RawMessage(payloadJSON))
		if err != nil {
			return err
		}
		return result.Value.Unmarshal(&response)
	})
	if err != nil {
		return FetchResponse{}, err
	}

	return response, nil
}

func (b *RodBackend) withPageRetry(ctx context.Context, run func(page *rod.Page) error) error {
	if b.closed.Load() {
		// Stopped/closed backends never recover a page: recovery used to
		// recreate a tab that looked like the browser "auto reopened".
		return ErrPageUnavailable
	}
	page, err := b.currentPage(ctx)
	if err != nil {
		return err
	}

	err = run(page)
	if !isRecoverablePageError(err) {
		return err
	}
	if b.disablePageRecovery {
		return ErrPageUnavailable
	}

	fmt.Println("rod: page session was lost, recovering active page")

	page, recoverErr := b.recoverPage(ctx)
	if recoverErr != nil {
		return recoverErr
	}

	return run(page)
}

func (b *RodBackend) currentPage(ctx context.Context) (*rod.Page, error) {
	if b.page == nil {
		return nil, ErrPageUnavailable
	}
	return b.page.Context(ctx), nil
}

func (b *RodBackend) recoverPage(ctx context.Context) (*rod.Page, error) {
	if b.closed.Load() || b.browser == nil {
		return nil, ErrPageUnavailable
	}

	pages, err := b.browser.Pages()
	if err != nil {
		return nil, err
	}

	for _, candidate := range pages {
		info, err := candidate.Info()
		if err != nil {
			if isRecoverablePageError(err) {
				continue
			}
			continue
		}

		if strings.HasPrefix(info.URL, b.startURL) || strings.HasPrefix(info.URL, "https://app.chitchat.gg/") {
			_ = b.preparePage(candidate) // timezone/locale apply immediately; init script for next loads
			b.page = candidate
			return candidate.Context(ctx), nil
		}
	}

	page, err := b.browser.Page(proto.TargetCreateTarget{URL: "about:blank"})
	if err != nil {
		return nil, err
	}
	if err := b.preparePage(page); err != nil {
		fmt.Printf("rod: page emulation setup failed: %v\n", err)
	}
	if err := page.Navigate(b.startURL); err != nil {
		return nil, err
	}

	if err := page.WaitLoad(); err != nil && !isRecoverablePageError(err) {
		return nil, err
	}

	b.page = page
	return page.Context(ctx), nil
}

func isRecoverablePageError(err error) bool {
	return errors.Is(err, cdp.ErrSessionNotFound) ||
		errors.Is(err, cdp.ErrNotAttachedToActivePage) ||
		errors.Is(err, cdp.ErrCtxNotFound) ||
		errors.Is(err, cdp.ErrCtxDestroyed)
}
