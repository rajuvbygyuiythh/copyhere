package api

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"chitchatbot/internal/model"
	"chitchatbot/internal/proxy"
)

type FetchRequest struct {
	Method  string
	URL     string
	Headers map[string]string
	Body    *string
}

type FetchResponse struct {
	OK         bool   `json:"ok"`
	Status     int    `json:"status"`
	StatusText string `json:"statusText"`
	Text       string `json:"text"`
	// Headers holds a small allow-listed set of response headers (lowercase
	// names): rate-limit counters, retry-after, content-type, cf-ray.
	Headers map[string]string `json:"headers,omitempty"`
}

// TracedHeaders are the only response headers kept on FetchResponse.
var TracedHeaders = []string{
	"x-ratelimit-limit", "x-ratelimit-remaining", "x-ratelimit-reset",
	"retry-after", "content-type", "cf-ray", "x-envoy-upstream-service-time",
}

// FilterHeaders keeps only TracedHeaders (case-insensitive) from raw.
func FilterHeaders(raw map[string]string) map[string]string {
	if len(raw) == 0 {
		return nil
	}
	out := map[string]string{}
	for k, v := range raw {
		lk := strings.ToLower(strings.TrimSpace(k))
		for _, want := range TracedHeaders {
			if lk == want {
				out[lk] = strings.TrimSpace(v)
			}
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// RateLimit returns (remaining, limit, resetSeconds); -1 when the header is
// absent. ChitChat sends 10/3s for message sends and 500/60s for reads.
func (r FetchResponse) RateLimit() (remaining, limit, reset int) {
	get := func(name string) int {
		v, ok := r.Headers[name]
		if !ok {
			return -1
		}
		n, err := strconv.Atoi(strings.TrimSpace(v))
		if err != nil {
			return -1
		}
		return n
	}
	return get("x-ratelimit-remaining"), get("x-ratelimit-limit"), get("x-ratelimit-reset")
}

// TraceEvent describes one finished request (never carries cookies, tokens
// or request bodies).
type TraceEvent struct {
	Method   string
	URL      string
	Status   int
	Duration time.Duration
	Err      error
	Via      bool // executed inside the browser page
	Response FetchResponse
}

// TraceFunc receives every finished request of a Client.
type TraceFunc func(TraceEvent)

type Client struct {
	httpClient *http.Client
	login      model.LoginInfo
	appURL     string
	// via, when set, executes every request instead of httpClient (Browser
	// mode: the request runs inside the real browser page).
	via   Executor
	trace TraceFunc
}

// SetTrace installs a hook that sees every finished request (debug log,
// rate-limit tracking). Returns the client for chaining.
func (c *Client) SetTrace(fn TraceFunc) *Client {
	if c != nil {
		c.trace = fn
	}
	return c
}

func (c *Client) emitTrace(start time.Time, req FetchRequest, resp FetchResponse, err error) {
	if c == nil || c.trace == nil {
		return
	}
	c.trace(TraceEvent{
		Method:   req.Method,
		URL:      req.URL,
		Status:   resp.Status,
		Duration: time.Since(start),
		Err:      err,
		Via:      c.via != nil,
		Response: resp,
	})
}

// Executor runs one request somewhere else than the Go http client.
type Executor interface {
	Fetch(ctx context.Context, req FetchRequest) (FetchResponse, error)
}

// NewVia returns a client whose requests are executed by exec (Browser
// mode). login is kept for the token check and for ViaExecutor() callers.
func NewVia(exec Executor, login model.LoginInfo, appURL string) *Client {
	return &Client{login: login, appURL: appURL, via: exec}
}

// Via reports whether the client delegates to an Executor.
func (c *Client) Via() bool { return c != nil && c.via != nil }

func New(login model.LoginInfo, appURL string, proxyRotator *proxy.Rotator) *Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.DisableKeepAlives = true
	if proxyRotator != nil && proxyRotator.Len() > 0 {
		transport.Proxy = func(*http.Request) (*url.URL, error) {
			entry, ok := proxyRotator.Next()
			if !ok {
				return nil, nil
			}
			return entry.TransportURL(), nil
		}
	}

	return &Client{
		httpClient: &http.Client{
			Timeout:   30 * time.Second,
			Transport: transport,
		},
		login:  login,
		appURL: appURL,
	}
}

func (c *Client) Fetch(ctx context.Context, req FetchRequest) (FetchResponse, error) {
	start := time.Now()
	resp, err := c.fetch(ctx, req)
	c.emitTrace(start, req, resp, err)
	return resp, err
}

func (c *Client) fetch(ctx context.Context, req FetchRequest) (FetchResponse, error) {
	if c.via != nil {
		return c.via.Fetch(ctx, req)
	}
	token := c.login.Token()
	if token == "" {
		return FetchResponse{}, fmt.Errorf("missing token cookie in login info")
	}

	var body io.Reader
	if req.Body != nil {
		body = strings.NewReader(*req.Body)
	}

	httpReq, err := http.NewRequestWithContext(ctx, req.Method, req.URL, body)
	if err != nil {
		return FetchResponse{}, err
	}

	for key, value := range c.baseHeaders(token) {
		httpReq.Header.Set(key, value)
	}
	for key, value := range req.Headers {
		httpReq.Header.Set(key, value)
	}

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return FetchResponse{}, err
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return FetchResponse{}, err
	}

	raw := make(map[string]string, len(TracedHeaders))
	for _, name := range TracedHeaders {
		if v := resp.Header.Get(name); v != "" {
			raw[name] = v
		}
	}
	return FetchResponse{
		OK:         resp.StatusCode >= 200 && resp.StatusCode < 300,
		Status:     resp.StatusCode,
		StatusText: resp.Status,
		Text:       string(data),
		Headers:    FilterHeaders(raw),
	}, nil
}

func (c *Client) baseHeaders(token string) map[string]string {
	userAgent := c.login.UserAgent
	if strings.TrimSpace(userAgent) == "" {
		userAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/146.0.0.0 Safari/537.36"
	}

	chromeMajor := chromeMajorFromUserAgent(userAgent)

	cookieHeader := c.login.CookieHeader()
	if cookieHeader == "" {
		cookieHeader = "token=" + token
	}

	return map[string]string{
		"Accept":             "*/*",
		"Accept-Language":    "en-US,en;q=0.9",
		"Cookie":             cookieHeader,
		"Origin":             "https://app.chitchat.gg",
		"Referer":            c.appURL,
		"Sec-Fetch-Dest":     "empty",
		"Sec-Fetch-Mode":     "cors",
		"Sec-Fetch-Site":     "same-site",
		"User-Agent":         userAgent,
		"sec-ch-ua":          fmt.Sprintf(`"Chromium";v="%s", "Not-A.Brand";v="24", "Google Chrome";v="%s"`, chromeMajor, chromeMajor),
		"sec-ch-ua-mobile":   "?0",
		"sec-ch-ua-platform": `"Windows"`,
	}
}

func chromeMajorFromUserAgent(userAgent string) string {
	match := regexp.MustCompile(`Chrome/(\d+)\.`).FindStringSubmatch(userAgent)
	if len(match) == 2 {
		return match[1]
	}
	return "146"
}
