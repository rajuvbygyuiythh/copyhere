package bot

// Browser mode has no Go websocket, but the whole match-chat flow
// (executeMatch*Sequence) is event driven: it waits on the realtimeEvent
// channel for the partner's reply and for "conversation closed". This file
// provides that channel in Browser mode by polling the API through the
// window: conversations list every few seconds, messages only for the
// conversations that changed. Produced events use the same shapes as
// realtime.go so logic.go needs no changes:
//   - matchUpdate (a new conversation appeared → Target filled, like the
//     socket's match notification), emitted BEFORE its first messages
//   - chatMessage  (partner's new message; bot's own messages are skipped)
//   - matchUpdate Closed=true (a conversation vanished from the list)

import (
	"context"
	"math/rand"
	"sort"
	"time"

	"chitchatbot/internal/api"
	"chitchatbot/internal/model"
)

type browserPollConversation struct {
	updatedAt string
	unread    int
	seen      map[string]bool
	seeded    bool // true once the message list has been read at least once
	partner   model.DMTarget
}

// browserEventPoller turns polling through the window into realtimeEvents.
type browserEventPoller struct {
	cfg           Config
	client        *api.Client
	currentUserID string
	session       *browserSession // may be nil in tests
	minInterval   time.Duration
	maxInterval   time.Duration
	errorBackoff  time.Duration
	known         map[string]*browserPollConversation
	initial       bool
	goneLogged    bool
}

func newBrowserEventPoller(cfg Config, client *api.Client, currentUserID string, session *browserSession) *browserEventPoller {
	return &browserEventPoller{
		cfg:           cfg,
		client:        client,
		currentUserID: currentUserID,
		session:       session,
		minInterval:   3500 * time.Millisecond,
		maxInterval:   5 * time.Second,
		errorBackoff:  10 * time.Second,
		known:         make(map[string]*browserPollConversation),
		initial:       true,
	}
}

// startBrowserEventPoller runs the poller until ctx ends and returns its
// event channel (buffered, so a busy consumer never blocks a poll).
func startBrowserEventPoller(ctx context.Context, cfg Config, client *api.Client, currentUserID string, session *browserSession) <-chan realtimeEvent {
	poller := newBrowserEventPoller(cfg, client, currentUserID, session)
	events := make(chan realtimeEvent, 64)
	go poller.run(ctx, events)
	return events
}

func (p *browserEventPoller) run(ctx context.Context, events chan<- realtimeEvent) {
	defer close(events)
	rng := rand.New(rand.NewSource(time.Now().UnixNano()))
	delay := time.Duration(0)
	for {
		if delay > 0 {
			timer := time.NewTimer(delay)
			select {
			case <-ctx.Done():
				timer.Stop()
				return
			case <-timer.C:
			}
		}
		delay = p.minInterval
		if p.maxInterval > p.minInterval {
			delay += time.Duration(rng.Int63n(int64(p.maxInterval - p.minInterval)))
		}
		if p.session != nil && p.session.paused() {
			continue // re-login in progress: leave the window alone
		}
		if ctx.Err() != nil {
			return
		}
		batch, ok := p.poll(ctx)
		if !ok {
			if ctx.Err() != nil {
				return
			}
			delay = p.errorBackoff
			continue
		}
		for _, event := range batch {
			select {
			case events <- event:
			case <-ctx.Done():
				return
			}
		}
	}
}

// poll performs one round. ok=false means "nothing usable this round"
// (error, 401 during re-login, browser gone) and asks for a backoff.
func (p *browserEventPoller) poll(ctx context.Context) ([]realtimeEvent, bool) {
	conversations, status, err := fetchDMConversations(ctx, p.client, p.cfg)
	if err != nil {
		if isBrowserGoneError(err) && !p.goneLogged && ctx.Err() == nil {
			p.goneLogged = true
			logf(p.cfg, "[browser] Message poller: %v\n", err)
		}
		return nil, false
	}
	if status >= 400 {
		return nil, false
	}

	present := make(map[string]bool, len(conversations))
	var batch []realtimeEvent
	for _, conv := range conversations {
		if conv.ID == "" {
			continue
		}
		present[conv.ID] = true
		state, exists := p.known[conv.ID]
		if !exists {
			state = &browserPollConversation{
				updatedAt: conv.UpdatedAt,
				unread:    conv.UnreadMessageCount,
				seen:      make(map[string]bool),
			}
			if targets := extractDMTargets([]model.DMConversation{conv}, p.currentUserID); len(targets) > 0 {
				state.partner = targets[0]
			}
			p.known[conv.ID] = state
			if p.initial {
				// conversations that already existed at start: read lazily,
				// only when they change.
				continue
			}
			if state.partner.UserID != "" {
				batch = append(batch, realtimeEvent{
					Type:           "matchUpdate",
					ConversationID: conv.ID,
					UserID:         state.partner.UserID,
					Username:       state.partner.Username,
					Target:         state.partner,
				})
			}
			batch = append(batch, p.readMessages(ctx, conv.ID, state, "")...)
			continue
		}
		changed := conv.UpdatedAt != state.updatedAt || conv.UnreadMessageCount > state.unread
		if !changed {
			continue
		}
		since := ""
		if !state.seeded {
			since = state.updatedAt // first read of an old conversation: only what came after start
		}
		state.updatedAt = conv.UpdatedAt
		state.unread = conv.UnreadMessageCount
		batch = append(batch, p.readMessages(ctx, conv.ID, state, since)...)
	}

	// A conversation that vanished while the list is not full = closed.
	if len(conversations) < p.cfg.DMLimit {
		for id, state := range p.known {
			if present[id] {
				continue
			}
			delete(p.known, id)
			if p.initial {
				continue
			}
			batch = append(batch, realtimeEvent{
				Type:           "matchUpdate",
				ConversationID: id,
				UserID:         state.partner.UserID,
				Username:       state.partner.Username,
				Closed:         true,
				ClosedBy:       state.partner.Username,
				Target:         state.partner,
			})
		}
	}
	p.initial = false
	return batch, true
}

// readMessages fetches the conversation and returns chatMessage events for
// the partner's messages not yet seen (and newer than since, if given).
func (p *browserEventPoller) readMessages(ctx context.Context, conversationID string, state *browserPollConversation, since string) []realtimeEvent {
	messages, status, err := fetchConversationMessages(ctx, p.client, p.cfg, conversationID)
	if err != nil || status >= 400 {
		return nil
	}
	sort.SliceStable(messages, func(i, j int) bool { return messages[i].CreatedAt < messages[j].CreatedAt })
	firstRead := !state.seeded
	state.seeded = true

	var out []realtimeEvent
	for _, msg := range messages {
		if msg.ID == "" || state.seen[msg.ID] {
			continue
		}
		state.seen[msg.ID] = true
		if msg.Author.ID == "" || msg.Author.ID == p.currentUserID {
			continue
		}
		if firstRead && since != "" && msg.CreatedAt != "" && msg.CreatedAt <= since {
			continue
		}
		out = append(out, realtimeEvent{
			Type:           "chatMessage",
			ConversationID: conversationID,
			UserID:         msg.Author.ID,
			Username:       state.partner.Username,
			MessageID:      msg.ID,
			Content:        msg.Content,
			CreatedAt:      msg.CreatedAt,
			Target:         state.partner,
		})
	}
	return out
}
