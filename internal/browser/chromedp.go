package browser

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"chitchatbot/internal/model"
	"chitchatbot/internal/runtimeassets"
	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/cdproto/emulation"
	cdpfetch "github.com/chromedp/cdproto/fetch"
	"github.com/chromedp/cdproto/page"

	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/cdproto/runtime"
	"github.com/chromedp/chromedp"
)

type ChromeDPBackend struct {
	ctx         context.Context
	allocCancel context.CancelFunc
	cancel      context.CancelFunc
}

func (b *ChromeDPBackend) Name() string {
	return "chromedp"
}

func (b *ChromeDPBackend) Start(parent context.Context, options Options) error {
	execOpts := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.UserDataDir(options.UserDataDir),
		chromedp.Flag("headless", options.Headless),
		chromedp.Flag("no-first-run", true),
		chromedp.Flag("disable-popup-blocking", false),
		chromedp.Flag("disable-extensions", true),
	)
	if options.Stealth {
		size := options.WindowSize
		if size == "" {
			size = PickWindowSize(options.UserDataDir)
		}
		execOpts = append(execOpts,
			chromedp.Flag("enable-automation", false),
			chromedp.Flag("disable-blink-features", "AutomationControlled"),
			chromedp.Flag("lang", localeOrDefault(options.Locale)),
			chromedp.Flag("webrtc-ip-handling-policy", "disable_non_proxied_udp"),
			chromedp.Flag("force-webrtc-ip-handling-policy", true),
			chromedp.WindowSize(windowSizeParts(size)),
			chromedp.Flag("password-store", "basic"),
		)
	}
	if options.LowMemory {
		execOpts = append(execOpts,
			chromedp.Flag("renderer-process-limit", "2"),
			chromedp.Flag("disable-component-update", true),
			chromedp.Flag("disable-background-mode", true),
			chromedp.Flag("disable-domain-reliability", true),
			chromedp.Flag("disable-speech-api", true),
			chromedp.Flag("no-pings", true),
			chromedp.Flag("js-flags", "--max-old-space-size=256"),
		)
	}
	if options.Proxy != nil {
		execOpts = append(execOpts, chromedp.ProxyServer(options.Proxy.BrowserURL()))
	}
	if bin, reason := runtimeassets.BundledChromiumPathForProfile(options.UserDataDir); bin != "" {
		fmt.Printf("chromedp: using bundled browser: %s\n", bin)
		execOpts = append(execOpts, chromedp.ExecPath(bin))
	} else if reason != "" {
		fmt.Printf("chromedp: skipping bundled browser: %s\n", reason)
	}

	allocCtx, allocCancel := chromedp.NewExecAllocator(parent, execOpts...)
	ctx, cancel := chromedp.NewContext(allocCtx)

	if options.Proxy != nil && options.Proxy.HasAuth() {
		chromedp.ListenTarget(ctx, func(ev any) {
			authEvent, ok := ev.(*cdpfetch.EventAuthRequired)
			if !ok || authEvent.AuthChallenge == nil {
				return
			}
			if authEvent.AuthChallenge.Source != cdpfetch.AuthChallengeSourceProxy {
				return
			}

			go func() {
				_ = cdpfetch.ContinueWithAuth(authEvent.RequestID, &cdpfetch.AuthChallengeResponse{
					Response: cdpfetch.AuthChallengeResponseResponseProvideCredentials,
					Username: options.Proxy.Username,
					Password: options.Proxy.Password,
				}).Do(ctx)
			}()
		})
	}

	actions := []chromedp.Action{
		network.Enable(),
	}
	if options.Proxy != nil && options.Proxy.HasAuth() {
		actions = append(actions, cdpfetch.Enable().WithHandleAuthRequests(true))
	}
	if options.Timezone != "" {
		actions = append(actions, emulation.SetTimezoneOverride(options.Timezone))
	}
	if options.Timezone != "" || options.Stealth {
		actions = append(actions, emulation.SetLocaleOverride().WithLocale(localeOrDefault(options.Locale)))
	}
	if options.Stealth {
		actions = append(actions, chromedp.ActionFunc(func(execCtx context.Context) error {
			_, err := page.AddScriptToEvaluateOnNewDocument(stealthScript(options)).Do(execCtx)
			return err
		}))
	}
	actions = append(actions,
		chromedp.Navigate(options.StartURL),
	)

	if err := chromedp.Run(ctx, actions...); err != nil {
		cancel()
		allocCancel()
		return err
	}

	b.ctx = ctx
	b.cancel = cancel
	b.allocCancel = allocCancel
	return nil
}

func (b *ChromeDPBackend) Close() error {
	if b.cancel != nil {
		b.cancel()
	}
	if b.allocCancel != nil {
		b.allocCancel()
	}
	return nil
}

func (b *ChromeDPBackend) CaptureSession(ctx context.Context) (model.LoginInfo, error) {
	if b.ctx == nil {
		return model.LoginInfo{}, ErrPageUnavailable
	}

	var cookies []*network.Cookie
	if err := chromedp.Run(b.ctx, chromedp.ActionFunc(func(execCtx context.Context) error {
		var err error
		cookies, err = network.GetCookies().
			WithURLs([]string{
				"https://app.chitchat.gg/",
				"https://api.chitchat.gg/",
			}).
			Do(execCtx)
		return err
	})); err != nil {
		return model.LoginInfo{}, err
	}

	var storage struct {
		LocalStorage   map[string]string `json:"local_storage"`
		SessionStorage map[string]string `json:"session_storage"`
	}
	if err := b.eval(ctx, "("+storageFunction+")()", &storage); err != nil {
		return model.LoginInfo{}, err
	}

	var fingerprint model.TransportFingerprint
	if err := b.eval(ctx, "("+fingerprintFunction+")()", &fingerprint); err != nil {
		return model.LoginInfo{}, err
	}

	login := model.LoginInfo{
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
			Expires:  cookie.Expires,
			Secure:   cookie.Secure,
			HTTPOnly: cookie.HTTPOnly,
		})
	}

	return login, nil
}

// Navigate loads url in the controlled tab.
func (b *ChromeDPBackend) Navigate(ctx context.Context, url string) error {
	if b.ctx == nil {
		return ErrPageUnavailable
	}
	return chromedp.Run(b.ctx, chromedp.Navigate(url))
}

// SetCookies injects cookies into the browser.
func (b *ChromeDPBackend) SetCookies(ctx context.Context, cookies []model.Cookie) error {
	if b.ctx == nil {
		return ErrPageUnavailable
	}
	return chromedp.Run(b.ctx, chromedp.ActionFunc(func(execCtx context.Context) error {
		for _, c := range cookies {
			if strings.TrimSpace(c.Name) == "" {
				continue
			}
			params := network.SetCookie(c.Name, c.Value).
				WithSecure(c.Secure).
				WithHTTPOnly(c.HTTPOnly)
			if c.Domain != "" {
				params = params.WithDomain(c.Domain)
			} else {
				params = params.WithURL("https://app.chitchat.gg/")
			}
			if c.Path != "" {
				params = params.WithPath(c.Path)
			} else {
				params = params.WithPath("/")
			}
			if c.Expires > 0 {
				expires := cdp.TimeSinceEpoch(time.Unix(int64(c.Expires), 0))
				params = params.WithExpires(&expires)
			}
			if err := params.Do(execCtx); err != nil {
				return err
			}
		}
		return nil
	}))
}

func (b *ChromeDPBackend) Fetch(ctx context.Context, req FetchRequest) (FetchResponse, error) {
	if b.ctx == nil {
		return FetchResponse{}, ErrPageUnavailable
	}

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

	script := fmt.Sprintf("(%s)(%s)", fetchFunction, string(payloadJSON))

	var raw []byte
	if err := chromedp.Run(b.ctx, chromedp.ActionFunc(func(execCtx context.Context) error {
		value, exception, err := runtime.Evaluate(script).
			WithAwaitPromise(true).
			WithReturnByValue(true).
			Do(execCtx)
		if err != nil {
			return err
		}
		if exception != nil {
			return exception
		}
		raw = value.Value
		return nil
	})); err != nil {
		return FetchResponse{}, err
	}

	var response FetchResponse
	if err := json.Unmarshal(raw, &response); err != nil {
		return FetchResponse{}, err
	}

	return response, nil
}

// Inspect returns what the page's JavaScript sees.
func (b *ChromeDPBackend) Inspect(ctx context.Context) (Inspection, error) {
	var result Inspection
	if b.ctx == nil {
		return result, ErrPageUnavailable
	}
	err := b.eval(ctx, "("+inspectFunction+")()", &result)
	return result, err
}

func windowSizeParts(size string) (int, int) {
	parts := strings.Split(size, ",")
	if len(parts) != 2 {
		return 1366, 768
	}
	var w, h int
	if _, err := fmt.Sscanf(strings.TrimSpace(parts[0]), "%d", &w); err != nil || w <= 0 {
		return 1366, 768
	}
	if _, err := fmt.Sscanf(strings.TrimSpace(parts[1]), "%d", &h); err != nil || h <= 0 {
		return 1366, 768
	}
	return w, h
}

func (b *ChromeDPBackend) eval(ctx context.Context, expression string, dest any) error {
	var raw []byte
	if err := chromedp.Run(b.ctx, chromedp.Evaluate(expression, &raw)); err != nil {
		return err
	}
	return json.Unmarshal(raw, dest)
}
