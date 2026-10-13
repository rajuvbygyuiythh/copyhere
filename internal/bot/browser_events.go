package bot

// Browser mode has no Go websocket, but the whole match-chat flow
// (executeMatch*Sequence) is event driven: it waits on the realtimeEvent
// channel for the partner's reply and for "conversation closed". This file
// provides that channel in Browser mode from three sources, all producing
// the same event shapes as realtime.go so logic.go needs no changes:
//
//  1. Socket tap (primary for match chats): the SPA's own socket.io frames,
//     captured inside the page by the WebSocket tap (browser/scripts.go)
//     and parsed with parseRealtimeEvent — identical payloads to the
//     classic Go websocket. Match chats NEED this (or source 2): a matched
//     anonymous conversation never appears in the DM conversation list
//     (the site's own sidebar shows "No conversations yet" during a match),
//     which is why the old DM-only poller never saw a match and the bot
//     sat on "Waiting for a new conversation..." forever.
//  2. GET /match/active (fallback for match chats): polled every round;
//     a newly active conversation emits matchUpdate, then its messages
//     are read through GET …/conversations/{id}/messages like a DM.
//  3. DM list poll (friend/DM conversations): conversation list every few
//     seconds, messages only for conversations that changed.
//
// Outgoing messages need no new path: sendDMMessage already POSTs
// …/conversations/{id}/messages, executed inside the page in Browser mode
// (browserExecutor), once a source above has supplied the conversation id.
//
// Events:
//   - matchUpdate (a new conversation appeared → Target filled, like the
//     socket's match notification), emitted BEFORE its first messages
//   - chatMessage  (partner's new message; bot's own messages are skipped)
//   - matchUpdate Closed=true (a conversation vanished / match ended)

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

	// tapDrain, when set, returns the SPA socket frames captured in-page.
	tapDrain func(ctx context.Context) ([]string, error)
	// activeID is the match conversation last seen via /match/active.
	activeID string
	// seenMsgs dedupes partner messages across the tap and HTTP sources;
	// announced dedupes matchUpdate emissions per conversation.
	seenMsgs  map[string]bool
	announced map[string]bool
}

func newBrowserEventPoller(cfg Config, client *api.Client, currentUserID string, session *browserSession) *browserEventPoller {
	p := &browserEventPoller{
		cfg:           cfg,
		client:        client,
		currentUserID: currentUserID,
		session:       session,
		minInterval:   3500 * time.Millisecond,
		maxInterval:   5 * time.Second,
		errorBackoff:  10 * time.Second,
		known:         make(map[string]*browserPollConversation),
		initial:       true,
		seenMsgs:      make(map[string]bool),
		announced:     make(map[string]bool),
	}
	if session != nil && session.backend != nil {
		p.tapDrain = session.backend.DrainSocketEvents
	}
	return p
}

// emitMatchOnce appends a matchUpdate for target unless one was already
// emitted for that conversation by another source.
func (p *browserEventPoller) emitMatchOnce(batch []realtimeEvent, target model.DMTarget) []realtimeEvent {
	if target.ConversationID == "" || target.UserID == "" {
		return batch
	}
	if p.announced == nil {
		p.announced = make(map[string]bool)
	}
	if p.announced[target.ConversationID] {
		return batch
	}
	p.announced[target.ConversationID] = true
	return append(batch, realtimeEvent{
		Type:           "matchUpdate",
		ConversationID: target.ConversationID,
		UserID:         target.UserID,
		Username:       target.Username,
		Target:         target,
	})
}

// emitClosed appends a closed matchUpdate and forgets the conversation.
func (p *browserEventPoller) emitClosed(batch []realtimeEvent, target model.DMTarget, closedBy string) []realtimeEvent {
	if target.ConversationID == "" {
		return batch
	}
	delete(p.announced, target.ConversationID)
	if p.activeID == target.ConversationID {
		p.activeID = ""
	}
	return append(batch, realtimeEvent{
		Type:           "matchUpdate",
		ConversationID: target.ConversationID,
		UserID:         target.UserID,
		Username:       target.Username,
		Closed:         true,
		ClosedBy:       closedBy,
		Target:         target,
	})
}

// pollTap drains the in-page socket tap and converts the SPA's frames with
// the classic realtime parser.
func (p *browserEventPoller) pollTap(ctx context.Context) []realtimeEvent {
	if p.tapDrain == nil {
		return nil
	}
	frames, err := p.tapDrain(ctx)
	if err != nil || len(frames) == 0 {
		return nil
	}
	var batch []realtimeEvent
	for _, frame := range frames {
		event, ok, err := parseRealtimeEvent(frame, p.currentUserID)
		if err != nil || !ok {
			continue
		}
		switch event.Type {
		case "matchUpdate":
			if event.Closed {
				target := event.Target
				if target.ConversationID == "" {
					target.ConversationID = event.ConversationID
				}
				batch = p.emitClosed(batch, target, event.ClosedBy)
				continue
			}
			if event.Target.UserID == "" {
				continue
			}
			if p.known[event.Target.ConversationID] == nil {
				p.known[event.Target.ConversationID] = &browserPollConversation{seen: make(map[string]bool), partner: event.Target}
			}
			// Exempt the match conversation from the DM-vanish close check.
			p.activeID = event.Target.ConversationID
			before := len(batch)
			batch = p.emitMatchOnce(batch, event.Target)
			if len(batch) > before {
				logf(p.cfg, "[browser] Match detected via the page socket: %s (%s), conversation %s\n", event.Target.Username, event.Target.UserID, event.Target.ConversationID)
			}
		case "chatMessage":
			if event.UserID == "" || event.UserID == p.currentUserID || event.ConversationID == "" {
				continue
			}
			if event.MessageID != "" {
				if p.seenMsgs == nil {
					p.seenMsgs = make(map[string]bool)
				}
				if p.seenMsgs[event.MessageID] {
					continue
				}
				p.seenMsgs[event.MessageID] = true
			}
			if state := p.known[event.ConversationID]; state != nil {
				if event.Username == "" {
					event.Username = state.partner.Username
				}
				event.Target = state.partner
				if event.MessageID != "" {
					state.seen[event.MessageID] = true
				}
			}
			batch = append(batch, event)
		case "messageEdited":
			batch = append(batch, event)
		}
	}
	return batch
}

// pollActive reads GET /match/active and turns a newly active match into a
// matchUpdate plus chatMessage events read over HTTP.
func (p *browserEventPoller) pollActive(ctx context.Context) []realtimeEvent {
	st, status, err := fetchActiveMatch(ctx, p.client, p.cfg, p.currentUserID)
	if err != nil || status >= 400 {
		return nil
	}
	var batch []realtimeEvent
	if st.HasConversation && st.Closed {
		target := st.Target
		delete(p.known, target.ConversationID)
		return p.emitClosed(batch, target, st.ClosedBy)
	}
	if !st.HasConversation {
		// Back in the queue while a match was active = the match ended.
		if p.activeID != "" && st.HasQueueState && st.InQueue {
			target := model.DMTarget{ConversationID: p.activeID}
			if state := p.known[p.activeID]; state != nil {
				target = state.partner
			}
			delete(p.known, p.activeID)
			batch = p.emitClosed(batch, target, target.Username)
		}
		return batch
	}

	target := st.Target
	state := p.known[target.ConversationID]
	if state == nil {
		state = &browserPollConversation{seen: make(map[string]bool), partner: target}
		p.known[target.ConversationID] = state
	} else if state.partner.UserID == "" {
		state.partner = target
	}
	if p.activeID != target.ConversationID {
		p.activeID = target.ConversationID
		if p.initial {
			// A match that was already active when the worker started:
			// seed its messages silently; the next POST /match replaces it.
			_ = p.readMessages(ctx, target.ConversationID, state, "")
			return batch
		}
		before := len(batch)
		batch = p.emitMatchOnce(batch, target)
		if len(batch) > before {
			logf(p.cfg, "[browser] Match detected via /match/active: %s (%s), conversation %s\n", target.Username, target.UserID, target.ConversationID)
		}
	}
	batch = append(batch, p.readMessages(ctx, target.ConversationID, state, "")...)
	return batch
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
	// Sources 1+2 first: match chats are invisible in the DM list below.
	batch := p.pollTap(ctx)
	batch = append(batch, p.pollActive(ctx)...)

	conversations, status, err := fetchDMConversations(ctx, p.client, p.cfg)
	if err != nil {
		if isBrowserGoneError(err) && !p.goneLogged && ctx.Err() == nil {
			p.goneLogged = true
			logf(p.cfg, "[browser] Message poller: %v\n", err)
		}
		if len(batch) > 0 {
			p.initial = false
			return batch, true
		}
		return nil, false
	}
	if status >= 400 {
		if len(batch) > 0 {
			p.initial = false
			return batch, true
		}
		return nil, false
	}

	present := make(map[string]bool, len(conversations))
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
			batch = p.emitMatchOnce(batch, state.partner)
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
	// The active match conversation is exempt: it is not a DM conversation,
	// so it is "absent" here in every round; its end is reported by the
	// socket tap / /match/active instead.
	if len(conversations) < p.cfg.DMLimit {
		for id, state := range p.known {
			if present[id] || id == p.activeID {
				continue
			}
			delete(p.known, id)
			if p.initial {
				continue
			}
			batch = p.emitClosed(batch, state.partner, state.partner.Username)
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
		if p.seenMsgs == nil {
			p.seenMsgs = make(map[string]bool)
		}
		if p.seenMsgs[msg.ID] {
			continue // already delivered by the socket tap
		}
		p.seenMsgs[msg.ID] = true
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
