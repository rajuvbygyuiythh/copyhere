package bot

// Debug / observability layer.
//
//   - apiTracer: sees every finished API request of a worker (via
//     api.Client.SetTrace). Always: tracks ChitChat's x-ratelimit counters
//     and keeps per-status counters for a periodic "[api] summary" line.
//     With Config.DebugAPI: writes every request to
//     data/debug/<profile>_api.log and mirrors writes / errors / slow calls
//     to the live log as "[api] …" lines.
//   - No cookies, tokens or request bodies are ever written; on errors only
//     the first 200 chars of the server's answer are kept.
//   - standingMonitor: re-checks GET /moderation/standing every 30 min and
//     logs when the account's standing changes.

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"chitchatbot/internal/api"
)

const (
	debugDirName       = "debug"
	debugLogMaxBytes   = 5 << 20 // rotate <profile>_api.log at 5 MB
	apiSummaryInterval = 10 * time.Minute
	apiSlowThreshold   = 3 * time.Second
)

type apiTracer struct {
	cfg     Config
	mu      sync.Mutex
	file    *os.File
	path    string
	written int64

	calls     int
	errors    int
	byStatus  map[int]int
	totalTime time.Duration
	slowest   time.Duration
	slowestOp string
	lastSum   time.Time

	// rate limit seen on the last message send (per account)
	sendRemaining int
	sendReset     int
	sendSeenAt    time.Time
}

func newAPITracer(cfg Config) *apiTracer {
	t := &apiTracer{cfg: cfg, byStatus: map[int]int{}, lastSum: time.Now(), sendRemaining: -1, sendReset: -1}
	if cfg.DebugAPI && cfg.DataDir != "" {
		dir := filepath.Join(cfg.DataDir, debugDirName)
		if err := os.MkdirAll(dir, 0o755); err == nil {
			t.path = filepath.Join(dir, safeFileName(cfg.Profile)+"_api.log")
			t.openFile()
		}
	}
	return t
}

func safeFileName(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return "default"
	}
	var b strings.Builder
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_', r == '.':
			b.WriteRune(r)
		default:
			b.WriteRune('_')
		}
	}
	return b.String()
}

func (t *apiTracer) openFile() {
	if t.path == "" {
		return
	}
	if info, err := os.Stat(t.path); err == nil && info.Size() > debugLogMaxBytes {
		_ = os.Rename(t.path, t.path+".1")
	}
	f, err := os.OpenFile(t.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	if info, err := f.Stat(); err == nil {
		t.written = info.Size()
	}
	t.file = f
}

// Close flushes the debug file (called when the worker ends).
func (t *apiTracer) Close() {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.summaryLocked(true)
	if t.file != nil {
		_ = t.file.Close()
		t.file = nil
	}
}

// Attach installs the tracer on a client and returns it.
func (t *apiTracer) Attach(client *api.Client) *api.Client {
	if t == nil || client == nil {
		return client
	}
	return client.SetTrace(t.handle)
}

// apiPath shortens a URL for logs: method-agnostic path with ids masked.
func apiPath(rawURL string) string {
	path := rawURL
	if i := strings.Index(path, "://"); i >= 0 {
		path = path[i+3:]
		if j := strings.Index(path, "/"); j >= 0 {
			path = path[j:]
		} else {
			path = "/"
		}
	}
	if i := strings.Index(path, "?"); i >= 0 {
		path = path[:i]
	}
	parts := strings.Split(path, "/")
	for i, p := range parts {
		if len(p) == 24 && isHex(p) {
			parts[i] = p[:4] + "…" + p[len(p)-3:]
		}
	}
	return strings.Join(parts, "/")
}

func isHex(s string) bool {
	for _, r := range s {
		if !((r >= '0' && r <= '9') || (r >= 'a' && r <= 'f') || (r >= 'A' && r <= 'F')) {
			return false
		}
	}
	return s != ""
}

func (t *apiTracer) handle(ev api.TraceEvent) {
	t.mu.Lock()
	defer t.mu.Unlock()

	t.calls++
	t.totalTime += ev.Duration
	if ev.Err != nil {
		t.errors++
	} else {
		t.byStatus[ev.Status]++
	}
	path := apiPath(ev.URL)
	if ev.Duration > t.slowest {
		t.slowest = ev.Duration
		t.slowestOp = ev.Method + " " + path
	}

	remaining, limit, reset := ev.Response.RateLimit()
	isSend := ev.Method == "POST" && strings.HasSuffix(path, "/messages")
	if isSend && remaining >= 0 {
		t.sendRemaining, t.sendReset, t.sendSeenAt = remaining, reset, time.Now()
	}

	line := fmt.Sprintf("%s %s %s", time.Now().Format("15:04:05.000"), ev.Method, path)
	switch {
	case ev.Err != nil:
		line += " ERR " + trimForLog(ev.Err.Error())
	default:
		line += fmt.Sprintf(" %d", ev.Status)
	}
	line += fmt.Sprintf(" %dms", ev.Duration.Milliseconds())
	if remaining >= 0 {
		line += fmt.Sprintf(" rl=%d/%d reset=%ds", remaining, limit, reset)
	}
	if ev.Via {
		line += " via=browser"
	}
	if ev.Err == nil && ev.Status >= 400 {
		line += " body=" + trimForLog(ev.Response.Text)
	}

	if t.file != nil {
		n, err := t.file.WriteString(line + "\n")
		if err == nil {
			t.written += int64(n)
			if t.written > debugLogMaxBytes {
				_ = t.file.Close()
				t.file = nil
				t.openFile()
			}
		}
	}

	// Live log: always errors / slow calls / low quota; with DebugAPI also
	// every write (POST/PATCH/PUT/DELETE). GET polls stay in the file only.
	show := ev.Err != nil || ev.Status >= 400 || ev.Duration > apiSlowThreshold
	if t.cfg.DebugAPI && ev.Method != "GET" {
		show = true
	}
	if isSend && remaining == 0 {
		logf(t.cfg, "[ratelimit] message quota used up (%d/%d); ChitChat resets it in %ds. Pausing sends until then.\n", remaining, limit, reset)
	}
	if show && (t.cfg.DebugAPI || ev.Err != nil || ev.Status >= 400) {
		logf(t.cfg, "[api] %s\n", strings.TrimPrefix(line, line[:13]))
	}

	if time.Since(t.lastSum) >= apiSummaryInterval {
		t.summaryLocked(false)
	}
}

// summaryLocked logs the per-account API summary (callers hold t.mu).
func (t *apiTracer) summaryLocked(final bool) {
	if t.calls == 0 {
		return
	}
	if !t.cfg.DebugAPI && !final && t.errors == 0 && t.byStatus[429] == 0 && t.byStatus[403] == 0 {
		// Quiet account, no debug: skip the periodic line.
		t.lastSum = time.Now()
		return
	}
	statuses := make([]int, 0, len(t.byStatus))
	for s := range t.byStatus {
		statuses = append(statuses, s)
	}
	sort.Ints(statuses)
	parts := make([]string, 0, len(statuses))
	for _, s := range statuses {
		parts = append(parts, fmt.Sprintf("%d×%d", s, t.byStatus[s]))
	}
	avg := time.Duration(0)
	if t.calls > 0 {
		avg = t.totalTime / time.Duration(t.calls)
	}
	label := "summary"
	if final {
		label = "final summary"
	}
	logf(t.cfg, "[api] %s: %d calls (%s), %d transport errors, avg %dms, slowest %s %dms\n",
		label, t.calls, strings.Join(parts, " "), t.errors, avg.Milliseconds(), t.slowestOp, t.slowest.Milliseconds())
	if t.file != nil {
		_, _ = t.file.WriteString(fmt.Sprintf("%s SUMMARY calls=%d statuses=%s errors=%d avg=%dms\n",
			time.Now().Format("15:04:05.000"), t.calls, strings.Join(parts, ","), t.errors, avg.Milliseconds()))
	}
	t.lastSum = time.Now()
}

// SendQuotaWait returns how long the caller should wait before the next
// message send according to the last x-ratelimit headers (0 = go ahead).
func (t *apiTracer) SendQuotaWait() time.Duration {
	if t == nil {
		return 0
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.sendRemaining != 0 || t.sendReset < 0 {
		return 0
	}
	until := t.sendSeenAt.Add(time.Duration(t.sendReset)*time.Second + 500*time.Millisecond)
	if wait := time.Until(until); wait > 0 {
		return wait
	}
	return 0
}

// ---- per-account tracer registry (so sendDMMessage can consult quota) ----

var (
	tracersMu sync.Mutex
	tracers   = map[string]*apiTracer{} // profile -> tracer
)

func registerTracer(profile string, t *apiTracer) {
	tracersMu.Lock()
	defer tracersMu.Unlock()
	if t == nil {
		delete(tracers, profile)
		return
	}
	tracers[profile] = t
}

func tracerFor(profile string) *apiTracer {
	tracersMu.Lock()
	defer tracersMu.Unlock()
	return tracers[profile]
}

// waitForSendQuota pauses before a message send when the previous send
// answered x-ratelimit-remaining: 0 (10 sends per 3 s on ChitChat).
func waitForSendQuota(ctx context.Context, cfg Config) error {
	t := tracerFor(cfg.Profile)
	if t == nil {
		return nil
	}
	wait := t.SendQuotaWait()
	if wait <= 0 {
		return nil
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(wait):
		return nil
	}
}

// ---- standing monitor ----

const standingRecheckInterval = 30 * time.Minute

// startStandingMonitor re-reads /moderation/standing periodically and logs
// only when something changed (new warning, limit, violation).
func startStandingMonitor(ctx context.Context, client *api.Client, cfg Config, initial string) {
	if client == nil {
		return
	}
	go func() {
		last := initial
		ticker := time.NewTicker(standingRecheckInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
			current, ok := fetchStandingSummary(ctx, client, cfg)
			if !ok || current == last {
				continue
			}
			logf(cfg, "[standing] CHANGED: %s  (was: %s)\n", current, last)
			last = current
		}
	}()
}
