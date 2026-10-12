package proxy

import (
	"fmt"
	"net/url"
	"strings"
	"sync/atomic"
)

type Entry struct {
	Scheme   string
	Host     string
	Port     string
	Username string
	Password string
}

type Rotator struct {
	entries []Entry
	next    atomic.Uint64
}

func ParseList(raw string) ([]Entry, error) {
	lines := strings.Split(strings.ReplaceAll(raw, "\r", ""), "\n")
	result := make([]Entry, 0, len(lines))

	for index, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		entry, err := ParseLine(line)
		if err != nil {
			return nil, fmt.Errorf("proxy line %d: %w", index+1, err)
		}

		result = append(result, entry)
	}

	return result, nil
}

func ParseLine(line string) (Entry, error) {
	line = strings.TrimSpace(line)
	if strings.Contains(line, "://") {
		parsed, err := url.Parse(line)
		if err != nil {
			return Entry{}, fmt.Errorf("invalid proxy URL: %w", err)
		}

		scheme, err := normalizeScheme(parsed.Scheme)
		if err != nil {
			return Entry{}, err
		}

		host := strings.TrimSpace(parsed.Hostname())
		port := strings.TrimSpace(parsed.Port())
		if host == "" || port == "" {
			return Entry{}, fmt.Errorf("expected %s://host:port", scheme)
		}

		entry := Entry{
			Scheme: scheme,
			Host:   host,
			Port:   port,
		}
		if parsed.User != nil {
			entry.Username = parsed.User.Username()
			entry.Password, _ = parsed.User.Password()
		}
		return entry, nil
	}

	parts := strings.Split(strings.TrimSpace(line), ":")
	switch len(parts) {
	case 2:
		if parts[0] == "" || parts[1] == "" {
			return Entry{}, fmt.Errorf("expected ip:port")
		}
		return Entry{
			Scheme: "http",
			Host:   parts[0],
			Port:   parts[1],
		}, nil
	case 4:
		if parts[0] == "" || parts[1] == "" || parts[2] == "" || parts[3] == "" {
			return Entry{}, fmt.Errorf("expected ip:port:user:pass")
		}
		return Entry{
			Scheme:   "http",
			Host:     parts[0],
			Port:     parts[1],
			Username: parts[2],
			Password: parts[3],
		}, nil
	default:
		return Entry{}, fmt.Errorf("expected ip:port or ip:port:user:pass")
	}
}

func NewRotator(entries []Entry) *Rotator {
	if len(entries) == 0 {
		return nil
	}

	cloned := make([]Entry, len(entries))
	copy(cloned, entries)

	return &Rotator{entries: cloned}
}

func (r *Rotator) Next() (Entry, bool) {
	if r == nil || len(r.entries) == 0 {
		return Entry{}, false
	}

	index := int((r.next.Add(1) - 1) % uint64(len(r.entries)))
	return r.entries[index], true
}

func (r *Rotator) Len() int {
	if r == nil {
		return 0
	}
	return len(r.entries)
}

func (e Entry) Address() string {
	return e.Host + ":" + e.Port
}

func (e Entry) BrowserURL() string {
	return e.SchemeOrDefault() + "://" + e.Address()
}

func (e Entry) TransportURL() *url.URL {
	result := &url.URL{
		Scheme: e.SchemeOrDefault(),
		Host:   e.Address(),
	}
	if e.HasAuth() {
		result.User = url.UserPassword(e.Username, e.Password)
	}
	return result
}

func (e Entry) HasAuth() bool {
	return e.Username != "" || e.Password != ""
}

func (e Entry) SupportsUDP() bool {
	switch e.SchemeOrDefault() {
	case "socks5":
		return true
	default:
		return false
	}
}

func (e Entry) SchemeOrDefault() string {
	scheme, err := normalizeScheme(e.Scheme)
	if err != nil {
		return "http"
	}
	return scheme
}

func (e Entry) Label() string {
	return e.SchemeOrDefault() + "://" + e.Address()
}

func normalizeScheme(value string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", "http":
		return "http", nil
	case "socks5":
		return "socks5", nil
	default:
		return "", fmt.Errorf("unsupported proxy scheme %q", value)
	}
}
