package bot

// Real-client parity helpers.
//
// Derived from the owner's DevTools captures of the real ChitChat web app
// (Chrome, 2026-10-09). The real client does a few small things our bot did
// not do before; doing them too makes the bot's traffic look like the SPA:
//
//   - POST /match and PATCH /match/disconnect carry a "response-time-id"
//     header: the current user id XOR-encoded with the transport key and a
//     fresh 8-char salt (same scheme as "transport-encoding").
//   - Leaving a chat is PATCH /match/disconnect, then POST /match for the next.
//   - Every partner message is acknowledged with
//     PATCH /users/me/conversations/{id}/read.
//   - On app load the client asks GET /match/active and GET /moderation/standing.
//   - The socket emits 42["presenceSync"] right after connect and every 30 s
//     (see realtime.go).
//
// Everything here is best effort: failures are logged once and never stop
// the worker.

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"strings"
	"sync"
	"time"

	"chitchatbot/internal/api"
)

const transportAlphabet = "0123456789abcdefghijklmnopqrstuvwxyz"

// xorEncode is the obfuscation the ChitChat client uses for its custom
// headers: 8 salt chars followed by hex(payload[i] ^ key[i%len] ^ salt[i%8]).
func xorEncode(key string, payload []byte) string {
	if key == "" {
		key = "0"
	}
	rng := rand.New(rand.NewSource(time.Now().UnixNano()))
	salt := make([]byte, 8)
	for i := range salt {
		salt[i] = transportAlphabet[rng.Intn(len(transportAlphabet))]
	}
	var builder strings.Builder
	builder.Grow(len(salt) + len(payload)*2)
	builder.Write(salt)
	for index, char := range payload {
		value := char ^ key[index%len(key)] ^ salt[index%len(salt)]
		builder.WriteString(fmt.Sprintf("%02x", value))
	}
	return builder.String()
}

// xorDecode reverses xorEncode (used by tests and the lab).
func xorDecode(key string, encoded string) ([]byte, error) {
	if len(encoded) < 8 || len(encoded)%2 != 0 {
		return nil, fmt.Errorf("bad encoded length %d", len(encoded))
	}
	if key == "" {
		key = "0"
	}
	salt := []byte(encoded[:8])
	hexPart := encoded[8:]
	out := make([]byte, 0, len(hexPart)/2)
	for i := 0; i < len(hexPart); i += 2 {
		var v byte
		if _, err := fmt.Sscanf(hexPart[i:i+2], "%02x", &v); err != nil {
			return nil, err
		}
		idx := i / 2
		out = append(out, v^key[idx%len(key)]^salt[idx%len(salt)])
	}
	return out, nil
}

// responseTimeID builds the "response-time-id" header value for the current
// user (the real client sends it on POST /match and PATCH /match/disconnect).
func responseTimeID(key string, currentUserID string) string {
	return xorEncode(key, []byte(strings.TrimSpace(currentUserID)))
}

// matchHeaders are the headers the real client sends on POST /match.
func matchHeaders(cfg Config, currentUserID string) map[string]string {
	headers := map[string]string{
		"accept":       "application/json",
		"content-type": "application/json",
	}
	if id := strings.TrimSpace(currentUserID); id != "" {
		headers["response-time-id"] = responseTimeID(cfg.TransportKey, id)
	}
	return headers
}

// ---- active match tracking (for PATCH /match/disconnect) ----

var (
	activeMatchMu sync.Mutex
	activeMatch   = map[string]string{} // currentUserID -> conversationID
)

// noteActiveMatch remembers that the account is inside a match so the next
// "find a new partner" call can leave it the way the real client does.
func noteActiveMatch(currentUserID, conversationID string) {
	if currentUserID == "" {
		return
	}
	activeMatchMu.Lock()
	defer activeMatchMu.Unlock()
	if conversationID == "" {
		delete(activeMatch, currentUserID)
		return
	}
	activeMatch[currentUserID] = conversationID
}

func takeActiveMatch(currentUserID string) string {
	activeMatchMu.Lock()
	defer activeMatchMu.Unlock()
	id := activeMatch[currentUserID]
	delete(activeMatch, currentUserID)
	return id
}

// disconnectMatch is PATCH /match/disconnect: what the real client sends when
// the user leaves the current chat (before asking for the next one). Best
// effort; the server closes the old match on POST /match anyway.
func disconnectMatch(ctx context.Context, client *api.Client, cfg Config, currentUserID string) {
	if client == nil {
		return
	}
	reqCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	body := "{}"
	resp, err := client.Fetch(reqCtx, api.FetchRequest{
		Method:  "PATCH",
		URL:     cfg.APIBaseURL + "/match/disconnect",
		Headers: matchHeaders(cfg, currentUserID),
		Body:    &body,
	})
	if err != nil {
		logOnce(cfg, "disconnect-err", "[sync] PATCH /match/disconnect failed: %v (ignored)\n", err)
		return
	}
	if !resp.OK {
		logOnce(cfg, fmt.Sprintf("disconnect-%d", resp.Status), "[sync] PATCH /match/disconnect -> %d %s (ignored; real client sends this when leaving a chat)\n", resp.Status, trimForLog(resp.Text))
	}
}

// markConversationRead is PATCH /users/me/conversations/{id}/read — the read
// receipt the real client sends for every partner message it displays.
func markConversationRead(ctx context.Context, client *api.Client, cfg Config, conversationID string) {
	if client == nil || conversationID == "" {
		return
	}
	reqCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	body := "{}"
	resp, err := client.Fetch(reqCtx, api.FetchRequest{
		Method: "PATCH",
		URL:    fmt.Sprintf("%s/users/me/conversations/%s/read", cfg.APIBaseURL, conversationID),
		Headers: map[string]string{
			"accept":       "*/*",
			"content-type": "application/json",
		},
		Body: &body,
	})
	if err != nil {
		logOnce(cfg, "read-err", "[sync] mark-read failed: %v (ignored)\n", err)
		return
	}
	if !resp.OK {
		logOnce(cfg, fmt.Sprintf("read-%d", resp.Status), "[sync] mark-read -> %d %s (ignored)\n", resp.Status, trimForLog(resp.Text))
	}
}

// withReadReceipts relays realtime events and, for every partner
// chatMessage, sends the read receipt in the background (debounced per
// conversation). The relay never blocks the producer for long: it forwards
// on a buffered channel and drops nothing the consumer is willing to take.
func withReadReceipts(ctx context.Context, client *api.Client, cfg Config, currentUserID string, in <-chan realtimeEvent) <-chan realtimeEvent {
	if in == nil || client == nil {
		return in
	}
	out := make(chan realtimeEvent, 32)
	go func() {
		defer close(out)
		lastRead := map[string]time.Time{}
		for {
			select {
			case <-ctx.Done():
				return
			case event, ok := <-in:
				if !ok {
					return
				}
				if event.Type == "matchUpdate" && event.Closed {
					// Partner (or server) closed the match: nothing to disconnect from.
					noteActiveMatch(currentUserID, "")
				}
				if event.Type == "chatMessage" && event.ConversationID != "" && event.UserID != "" && event.UserID != currentUserID {
					now := time.Now()
					if now.Sub(lastRead[event.ConversationID]) > 1500*time.Millisecond {
						lastRead[event.ConversationID] = now
						conv := event.ConversationID
						go func() {
							// Humans need a moment before the message counts as read.
							select {
							case <-ctx.Done():
								return
							case <-time.After(time.Duration(300+rand.Intn(900)) * time.Millisecond):
							}
							markConversationRead(ctx, client, cfg, conv)
						}()
					}
					if len(lastRead) > 256 {
						for k, t := range lastRead {
							if now.Sub(t) > 10*time.Minute {
								delete(lastRead, k)
							}
						}
					}
				}
				select {
				case out <- event:
				case <-ctx.Done():
					return
				}
			}
		}
	}()
	return out
}

// ---- startup calls ----

type moderationStanding struct {
	Standing         string            `json:"standing"`
	LimitedFeatures  []json.RawMessage `json:"limitedFeatures"`
	PendingWarnings  []json.RawMessage `json:"pendingWarnings"`
	ActiveViolations []json.RawMessage `json:"activeViolations"`
}

// logAccountStanding mimics the SPA's load sequence (GET /match/active,
// GET /moderation/standing) and tells the owner if the account carries
// warnings or feature limits before it gets banned.
func logAccountStanding(ctx context.Context, client *api.Client, cfg Config) {
	if client == nil {
		return
	}
	reqCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()

	if resp, err := client.Fetch(reqCtx, api.FetchRequest{Method: "GET", URL: cfg.APIBaseURL + "/match/active"}); err == nil && resp.OK {
		var active struct {
			InQueue bool `json:"inQueue"`
		}
		if json.Unmarshal([]byte(resp.Text), &active) == nil && active.InQueue {
			logln(cfg, "[sync] /match/active: this account is still in the match queue from a previous session.")
		}
	}

	resp, err := client.Fetch(reqCtx, api.FetchRequest{Method: "GET", URL: cfg.APIBaseURL + "/moderation/standing"})
	if err != nil || !resp.OK {
		return
	}
	var standing moderationStanding
	if json.Unmarshal([]byte(resp.Text), &standing) != nil {
		return
	}
	level := strings.ToUpper(strings.TrimSpace(standing.Standing))
	warnings := len(standing.PendingWarnings)
	violations := len(standing.ActiveViolations)
	limited := len(standing.LimitedFeatures)
	if warnings == 0 && violations == 0 && limited == 0 && (level == "" || level == "GOOD" || level == "OK" || level == "NORMAL" || level == "CLEAN" || level == "NONE") {
		logf(cfg, "[standing] %s — no warnings, no limits.\n", orDefault(level, "GOOD"))
		return
	}
	logf(cfg, "[standing] WARN standing=%s pendingWarnings=%d activeViolations=%d limitedFeatures=%d — this account is on ChitChat's radar; consider resting it.\n",
		orDefault(level, "?"), warnings, violations, limited)
	if limited > 0 {
		logf(cfg, "[standing] limitedFeatures: %s\n", trimForLog(string(mustJSON(standing.LimitedFeatures))))
	}
	if violations > 0 {
		logf(cfg, "[standing] activeViolations: %s\n", trimForLog(string(mustJSON(standing.ActiveViolations))))
	}
	if warnings > 0 {
		logf(cfg, "[standing] pendingWarnings: %s\n", trimForLog(string(mustJSON(standing.PendingWarnings))))
	}
}

// ---- small helpers ----

var (
	logOnceMu   sync.Mutex
	logOnceSeen = map[string]bool{}
)

// logOnce logs a message once per (profile, key) so best-effort failures do
// not flood the live log.
func logOnce(cfg Config, key string, format string, args ...interface{}) {
	k := cfg.Profile + "|" + key
	logOnceMu.Lock()
	seen := logOnceSeen[k]
	if !seen {
		logOnceSeen[k] = true
	}
	logOnceMu.Unlock()
	if seen {
		return
	}
	logf(cfg, format, args...)
}

func trimForLog(s string) string {
	s = strings.TrimSpace(strings.ReplaceAll(s, "\n", " "))
	if len(s) > 160 {
		return s[:160] + "…"
	}
	return s
}

func orDefault(s, def string) string {
	if s == "" {
		return def
	}
	return s
}

func mustJSON(v interface{}) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		return []byte("?")
	}
	return b
}
