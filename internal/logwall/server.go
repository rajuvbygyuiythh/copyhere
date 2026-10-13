// Package logwall serves the "Live Wall": a local-only web page that mirrors
// the dashboard Home screen (8 counters + live chat activity log) in a
// full-screen, auto-theming sci-fi / hacking style, meant to be opened in a
// normal PC browser (or a second monitor) while the bot runs.
//
// It never touches sessions, cookies or credentials: it only receives the
// same model.LogEntry values the dashboard already shows and the same
// model.HomeStats the dashboard already computes.
package logwall

import (
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"chitchatbot/internal/model"
)

//go:embed assets/wall.html
var assets embed.FS

// DefaultPort is the first port tried when none is configured.
const DefaultPort = 47321

// StatsFunc returns the current Home counters (same values as the dashboard).
type StatsFunc func() (model.HomeStats, error)

// HistoryFunc returns the most recent log entries (oldest first is fine, the
// page sorts by time).
type HistoryFunc func(limit int) ([]model.LogEntry, error)

// Options configures a Server.
type Options struct {
	// Port to listen on (127.0.0.1 only). 0 => DefaultPort. If busy, the
	// next 20 ports are tried.
	Port int
	// BackgroundDir holds optional user images (png/jpg/jpeg/webp/gif) that
	// the page shows as a faint, shadowed background slideshow.
	BackgroundDir string
	// Stats is polled every StatsInterval while at least one browser is
	// connected.
	Stats StatsFunc
	// History is used to pre-fill the page when it first loads.
	History HistoryFunc
	// StatsInterval defaults to 3s (same cadence as the dashboard).
	StatsInterval time.Duration
}

type event struct {
	Type string `json:"type"`
	Data any    `json:"data,omitempty"`
}

// Server is a tiny HTTP + Server-Sent-Events hub.
type Server struct {
	opts Options

	mu        sync.Mutex
	listener  net.Listener
	httpSrv   *http.Server
	url       string
	running   bool
	startedAt time.Time
	clients   map[chan []byte]struct{}
	closed    bool
	stopStats chan struct{}
}

// New creates a server; nothing listens until Start is called.
func New(opts Options) *Server {
	if opts.Port <= 0 {
		opts.Port = DefaultPort
	}
	if opts.StatsInterval <= 0 {
		opts.StatsInterval = 3 * time.Second
	}
	return &Server{
		opts:    opts,
		clients: map[chan []byte]struct{}{},
	}
}

// Start begins listening (idempotent) and returns the page URL.
func (s *Server) Start() (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return "", errors.New("live wall server is closed")
	}
	if s.listener != nil {
		return s.url, nil
	}

	var (
		ln  net.Listener
		err error
	)
	for i := 0; i < 20; i++ {
		port := s.opts.Port + i
		ln, err = net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
		if err == nil {
			s.url = fmt.Sprintf("http://127.0.0.1:%d/", port)
			break
		}
	}
	if ln == nil {
		return "", fmt.Errorf("live wall: no free port near %d: %w", s.opts.Port, err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/", s.handleIndex)
	mux.HandleFunc("/api/snapshot", s.handleSnapshot)
	mux.HandleFunc("/events", s.handleEvents)
	mux.HandleFunc("/bg/", s.handleBackground)

	s.listener = ln
	s.startedAt = time.Now()
	s.httpSrv = &http.Server{
		Handler:           s.guard(mux),
		ReadHeaderTimeout: 10 * time.Second,
	}
	s.stopStats = make(chan struct{})
	go func() { _ = s.httpSrv.Serve(ln) }()
	go s.statsLoop(s.stopStats)
	return s.url, nil
}

// URL returns the page URL ("" if not started).
func (s *Server) URL() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.url
}

// Started reports whether the server is listening.
func (s *Server) Started() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.listener != nil
}

// Close stops the server and disconnects every browser.
func (s *Server) Close() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	srv := s.httpSrv
	stop := s.stopStats
	for ch := range s.clients {
		close(ch)
		delete(s.clients, ch)
	}
	s.mu.Unlock()

	if stop != nil {
		close(stop)
	}
	if srv != nil {
		return srv.Close()
	}
	return nil
}

// Publish pushes one log entry to every connected page.
func (s *Server) Publish(entry model.LogEntry) {
	s.broadcast(event{Type: "log", Data: entry})
}

// SetRunning tells the pages whether the bot is running.
func (s *Server) SetRunning(running bool) {
	s.mu.Lock()
	s.running = running
	s.mu.Unlock()
	s.broadcast(event{Type: "running", Data: running})
}

// LogsCleared tells the pages to wipe their history.
func (s *Server) LogsCleared() {
	s.broadcast(event{Type: "logs_cleared"})
}

// ClientCount returns the number of connected browsers.
func (s *Server) ClientCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.clients)
}

func (s *Server) broadcast(ev event) {
	payload, err := json.Marshal(ev)
	if err != nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for ch := range s.clients {
		select {
		case ch <- payload:
		default:
			// slow client: drop this event rather than block the bot
		}
	}
}

func (s *Server) statsLoop(stop chan struct{}) {
	ticker := time.NewTicker(s.opts.StatsInterval)
	defer ticker.Stop()
	for {
		select {
		case <-stop:
			return
		case <-ticker.C:
			if s.ClientCount() == 0 || s.opts.Stats == nil {
				continue
			}
			stats, err := s.opts.Stats()
			if err != nil {
				continue
			}
			s.broadcast(event{Type: "stats", Data: stats})
		}
	}
}

// guard only answers requests addressed to the loopback host (blocks DNS
// rebinding tricks) and sets no-cache headers.
func (s *Server) guard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host := r.Host
		if h, _, err := net.SplitHostPort(host); err == nil {
			host = h
		}
		if host != "127.0.0.1" && host != "localhost" && host != "::1" {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		next.ServeHTTP(w, r)
	})
}

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	page, err := assets.ReadFile("assets/wall.html")
	if err != nil {
		http.Error(w, "page missing", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(page)
}

type snapshot struct {
	Running     bool             `json:"running"`
	StartedAt   string           `json:"started_at"`
	Stats       model.HomeStats  `json:"stats"`
	Logs        []model.LogEntry `json:"logs"`
	Backgrounds []string         `json:"backgrounds"`
	BgDir       string           `json:"bg_dir"`
}

func (s *Server) handleSnapshot(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	running := s.running
	started := s.startedAt
	s.mu.Unlock()

	snap := snapshot{
		Running:     running,
		StartedAt:   started.UTC().Format(time.RFC3339),
		Backgrounds: s.backgroundNames(),
		BgDir:       s.opts.BackgroundDir,
	}
	if s.opts.Stats != nil {
		if stats, err := s.opts.Stats(); err == nil {
			snap.Stats = stats
		}
	}
	if s.opts.History != nil {
		if logs, err := s.opts.History(300); err == nil {
			snap.Logs = logs
		}
	}
	if snap.Logs == nil {
		snap.Logs = []model.LogEntry{}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(snap)
}

func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}

	ch := make(chan []byte, 256)
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		http.Error(w, "closed", http.StatusGone)
		return
	}
	s.clients[ch] = struct{}{}
	running := s.running
	s.mu.Unlock()

	defer func() {
		s.mu.Lock()
		if _, still := s.clients[ch]; still {
			delete(s.clients, ch)
			close(ch)
		}
		s.mu.Unlock()
	}()

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)

	hello, _ := json.Marshal(event{Type: "running", Data: running})
	_, _ = fmt.Fprintf(w, "retry: 1500\ndata: %s\n\n", hello)
	flusher.Flush()

	heartbeat := time.NewTicker(15 * time.Second)
	defer heartbeat.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case <-heartbeat.C:
			_, _ = fmt.Fprint(w, ": ping\n\n")
			flusher.Flush()
		case payload, ok := <-ch:
			if !ok {
				return
			}
			_, _ = fmt.Fprintf(w, "data: %s\n\n", payload)
			flusher.Flush()
		}
	}
}

var imageExt = map[string]string{
	".png":  "image/png",
	".jpg":  "image/jpeg",
	".jpeg": "image/jpeg",
	".webp": "image/webp",
	".gif":  "image/gif",
}

func (s *Server) backgroundNames() []string {
	names := []string{}
	if s.opts.BackgroundDir == "" {
		return names
	}
	entries, err := os.ReadDir(s.opts.BackgroundDir)
	if err != nil {
		return names
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if _, ok := imageExt[strings.ToLower(filepath.Ext(e.Name()))]; ok {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	return names
}

func (s *Server) handleBackground(w http.ResponseWriter, r *http.Request) {
	name := filepath.Base(strings.TrimPrefix(r.URL.Path, "/bg/"))
	ctype, ok := imageExt[strings.ToLower(filepath.Ext(name))]
	if !ok || name == "." || name == "/" || s.opts.BackgroundDir == "" {
		http.NotFound(w, r)
		return
	}
	full := filepath.Join(s.opts.BackgroundDir, name)
	data, err := os.ReadFile(full)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", ctype)
	w.Header().Set("Cache-Control", "max-age=60")
	_, _ = w.Write(data)
}
