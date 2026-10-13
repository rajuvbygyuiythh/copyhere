package bot

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"path/filepath"
	"strings"
	"time"

	"chitchatbot/internal/api"
	"chitchatbot/internal/browser"
	"chitchatbot/internal/model"
	"chitchatbot/internal/proxy"
	"chitchatbot/internal/store"
)

type Config struct {
	AppURL        string
	APIBaseURL    string
	DataDir       string
	Profile       string
	LoginInfoPath string
	Backend       string
	Headless      bool
	// DebugAPI writes every API request to data/debug/<profile>_api.log and
	// mirrors writes / errors to the live log as "[api] …" lines.
	DebugAPI             bool
	BrowserMode          bool            // keep the real browser open; chat through its page
	AttachedBackend      browser.Backend // Browser mode: an already open, logged-in window to adopt (fresh-login handover)
	MatchWhenIdle        bool
	PollMin              time.Duration
	PollMax              time.Duration
	ActionMin            time.Duration
	ActionMax            time.Duration
	MatchReplyTimeoutMin time.Duration
	MatchReplyTimeoutMax time.Duration
	MatchCloseWaitMin    time.Duration
	MatchCloseWaitMax    time.Duration
	ReplyDelayMin        time.Duration
	ReplyDelayMax        time.Duration
	DMLimit              int
	DMOffset             int
	MessageFetchLimit    int
	MessageSequence      []string // Legacy, kept for backwards compatibility
	AvailableSequences   [][]string
	ChatMode             string
	KeywordRules         []KeywordRule
	EVAChat              *EVAChatConfig
	MessageUsernames     []string
	TransportKey         string
	NonceAlphabet        string
	StatusFriend         int
	StatusPendingOut     int
	StatusPendingIn      int
	StatusBlocked        int
	ActionAcceptFriend   int
	ProxyRotator         *proxy.Rotator
	Logf                 LogFunc
	Activityf            LogFunc
	Pacing               Pacing

	pace *pacer // per-worker shift/opener state, created in Run
}

type paths struct {
	ProfileDir      string
	LoginInfo       string
	BotState        string
	RelationshipIDs string
	DMUserIDs       string
	DMConversations string
	AllUserIDs      string
}

var errLoginExpired = errors.New("login expired")

// browserMaxCycleFailures: in Browser mode a failing cycle is retried (the
// window stays open) up to this many times in a row before the worker stops.
const browserMaxCycleFailures = 10

func Run(ctx context.Context, cfg Config) (retErr error) {
	if cfg.Backend != "rod" && cfg.Backend != "chromedp" {
		return fmt.Errorf("backend must be rod or chromedp, got %q", cfg.Backend)
	}

	p := profilePaths(cfg)
	if err := store.EnsureDir(p.ProfileDir); err != nil {
		return err
	}

	login, err := loadSavedLogin(p)
	if err != nil {
		return err
	}
	var bsession *browserSession // non-nil only in Browser mode
	if cfg.BrowserMode {
		bsession, login, err = openBrowserSession(ctx, cfg, p, login)
		if err != nil {
			return err
		}
		defer func() { bsession.saveAndClose(login, closeReason(ctx, retErr)) }()
	} else if login.Token() == "" {
		logln(cfg, "No saved login token found. Opening browser for login.")
		login, err = captureLoginViaBrowser(ctx, cfg, p)
		if err != nil {
			return err
		}
	} else {
		logf(cfg, "Using saved login from %s\n", p.LoginInfo)
	}

	currentUserID := currentUserIDFromLogin(login)
	if currentUserID == "" {
		return fmt.Errorf("could not detect current bot user ID from %s; log in again so user_id can be saved", p.LoginInfo)
	}
	if err := registerBotUser(cfg, currentUserID); err != nil {
		return err
	}
	rng := rand.New(rand.NewSource(time.Now().UnixNano()))
	cfg.pace = newPacer(cfg.Pacing, rng)
	SetWorkerPhase(cfg.Profile, PhaseActive)
	defer ClearWorkerPhase(cfg.Profile)
	if cfg.Pacing.Enabled {
		logf(cfg, "Human pacing on: bot opens %d%% of matches, shift %s-%s active / %s-%s rest, max %d chats/hour\n",
			cfg.Pacing.BotFirstPercent, cfg.Pacing.ActiveMin.Truncate(time.Minute), cfg.Pacing.ActiveMax.Truncate(time.Minute),
			cfg.Pacing.RestMin.Truncate(time.Minute), cfg.Pacing.RestMax.Truncate(time.Minute), cfg.Pacing.ChatsPerHourMax)
	}
	var realtimeEvents <-chan realtimeEvent
	var stopRealtime context.CancelFunc
	// Debug / observability: every request of this worker goes through the
	// tracer (rate-limit tracking, [api] lines, data/debug/<profile>_api.log).
	tracer := newAPITracer(cfg)
	registerTracer(cfg.Profile, tracer)
	defer func() {
		registerTracer(cfg.Profile, nil)
		tracer.Close()
	}()
	if cfg.DebugAPI {
		if tracer.path != "" {
			logf(cfg, "[debug] API trace on → %s\n", tracer.path)
		} else {
			logln(cfg, "[debug] API trace on (live log only; debug folder not writable)")
		}
	}

	var syncClient *api.Client // long-lived client for read receipts / startup calls
	if bsession != nil {
		logln(cfg, "[browser] Realtime websocket is off in Browser mode; the window polls for new messages every few seconds instead.")
		syncClient = tracer.Attach(bsession.client(login))
		realtimeEvents = startBrowserEventPoller(ctx, cfg, syncClient, currentUserID, bsession)
	} else {
		syncClient = tracer.Attach(api.New(login, cfg.AppURL, cfg.ProxyRotator))
		realtimeEvents, stopRealtime = startRealtimeListener(ctx, cfg, login, currentUserID)
	}
	defer func() {
		if stopRealtime != nil {
			stopRealtime()
		}
	}()
	// Real-client parity: read receipts for partner messages + the SPA's
	// load-time calls (/match/active, /moderation/standing).
	realtimeEvents = withReadReceipts(ctx, syncClient, cfg, currentUserID, realtimeEvents)
	startStandingMonitor(ctx, syncClient, cfg, logAccountStanding(ctx, syncClient, cfg))

	logln(cfg, "Login ready. Starting bot loop.")
	browserCycleFailures := 0
	logf(cfg, "Cycle delay: %s to %s\n", cfg.PollMin, cfg.PollMax)
	logf(cfg, "Action delay: %s to %s\n", cfg.ActionMin, cfg.ActionMax)

	lastPhase := PhaseActive
	for {
		var client *api.Client
		if bsession != nil {
			client = tracer.Attach(bsession.client(login))
		} else {
			client = tracer.Attach(api.New(login, cfg.AppURL, cfg.ProxyRotator))
		}

		workDone, err := runCycle(ctx, client, cfg, p, login, currentUserID, rng)
		if err != nil {
			if errors.Is(err, errBrowserGone) {
				logf(cfg, "[browser] %v. Stopping this worker (other accounts keep running).\n", err)
				return err
			}
			if errors.Is(err, errLoginExpired) && bsession != nil && ctx.Err() == nil {
				// Browser mode: do not stop, let the owner log in again in the window.
				login, err = bsession.reloginInWindow(ctx)
				if err != nil {
					return err
				}
				if id := currentUserIDFromLogin(login); id != "" {
					currentUserID = id
				}
				continue
			}
			if errors.Is(err, errAccountBanned) {
				if mErr := markBotUserBanned(cfg, currentUserID, err.Error()); mErr != nil {
					logf(cfg, "[banned] Could not update the account registry: %v\n", mErr)
				}
				logf(cfg, "[banned] ChitChat says this account is banned/flagged (%v). Account marked DEAD; this worker stops. Other accounts keep running.\n", err)
				return err
			}
			if errors.Is(err, errLoginExpired) {
				// If login is expired, stop the worker instead of trying to refresh via browser.
				logln(cfg, "Saved login expired or missing. Stopping worker.")
				return err // This will cause the worker to stop.
			}
			if bsession != nil && ctx.Err() == nil {
				// Browser mode: the window must stay open; a failed cycle is
				// logged and retried instead of stopping the worker.
				browserCycleFailures++
				if browserCycleFailures >= browserMaxCycleFailures {
					logf(cfg, "[browser] %d cycles failed in a row (last: %v). Stopping this worker.\n", browserCycleFailures, err)
					return err
				}
				logf(cfg, "[browser] Cycle failed (%v). Window stays open; retrying after a short wait (%d/%d).\n", err, browserCycleFailures, browserMaxCycleFailures)
				if werr := sleepRandom(ctx, rng, cfg, cfg.PollMin, cfg.PollMax, "Waiting before retry"); werr != nil {
					return werr
				}
				continue
			}
			return err
		}
		browserCycleFailures = 0

		if cfg.MatchWhenIdle && !workDone {
			allowed, until, phase, reason := cfg.pace.gate()
			if phase != lastPhase {
				SetWorkerPhase(cfg.Profile, phase)
				if allowed {
					logln(cfg, "[pacing] Shift active again. Looking for new matches.")
				} else {
					logf(cfg, "[pacing] %s: pausing new matches until %s (DM replies continue).\n", reason, until.Local().Format("15:04:05"))
				}
				lastPhase = phase
			}
			if allowed {
				logln(cfg, "No new messages or relationships to process, attempting to find a new chat partner.")
				chatted, err := matchAndChat(ctx, client, cfg, p, login, currentUserID, rng, realtimeEvents)
				if err != nil {
					if errors.Is(err, errBrowserGone) {
						logf(cfg, "[browser] %v. Stopping this worker (other accounts keep running).\n", err)
						return err
					}
					if errors.Is(err, errAccountBanned) {
						// Dead account: mark it, say it once, stop this worker.
						if mErr := markBotUserBanned(cfg, currentUserID, err.Error()); mErr != nil {
							logf(cfg, "[banned] Could not update the account registry: %v\n", mErr)
						}
						logf(cfg, "[banned] ChitChat says this account is banned/flagged (%v). Account marked DEAD; this worker stops. Other accounts keep running.\n", err)
						return err
					}
					if errors.Is(err, errRateLimited) {
						until := cfg.pace.noteLimited()
						logf(cfg, "[pacing] Platform limit detected. Cooling down until %s.\n", until.Local().Format("15:04:05"))
					} else {
						logf(cfg, "Match and chat failed: %v\n", err)
					}
					// Don't return, just log and continue to next cycle wait.
				}
				if chatted {
					if !evaChatEnabled(cfg) {
						// EVA mode counts a chat only when someone actually spoke
						// (see executeMatchEVAChat); legacy modes count every match.
						cfg.pace.noteChat()
					}
					if stillAllowed, _, _, _ := cfg.pace.gate(); stillAllowed {
						// If we had a match and chatted, we can immediately try for another match
						// instead of waiting for the poll delay (with a short human pause).
						if cfg.Pacing.Enabled {
							if err := sleepRandom(ctx, rng, cfg, matchGapMin, matchGapMax, "[pacing] Short pause before the next match"); err != nil {
								return err
							}
						}
						continue
					}
				}
			}
		}

		if err := waitForNextCycle(ctx, rng, cfg, currentUserID, realtimeEvents); err != nil {
			return err
		}
	}
}

func runCycle(
	ctx context.Context,
	client *api.Client,
	cfg Config,
	p paths,
	login model.LoginInfo,
	currentUserID string,
	rng *rand.Rand,
) (bool, error) {
	if login.Token() == "" {
		return false, errLoginExpired
	}

	workDone := false
	relationships, status, err := fetchRelationships(ctx, client, cfg)
	if err != nil {
		return false, err
	}
	if status == 401 || status == 403 {
		return false, errLoginExpired
	}

	pendingUserIDs := extractPendingIncomingUserIDs(relationships, cfg)
	claimablePendingUserIDs, err := claimAllPendingUsers(cfg, currentUserID, pendingUserIDs)
	if err != nil {
		return workDone, err
	}
	acceptedFriend := false

	for index, pendingUserID := range claimablePendingUserIDs {
		logf(cfg, "Accepting pending friend request %d/%d: %s\n", index+1, len(claimablePendingUserIDs), pendingUserID)
		if err := acceptFriend(ctx, client, cfg, pendingUserID); err != nil {
			return workDone, err
		}
		acceptedFriend = true
		workDone = true

		if index < len(claimablePendingUserIDs)-1 {
			if err := sleepRandom(ctx, rng, cfg, cfg.ActionMin, cfg.ActionMax, "Waiting before next friend accept"); err != nil {
				return workDone, err
			}
		}
	}

	if acceptedFriend {
		relationships, status, err = fetchRelationships(ctx, client, cfg)
		if err != nil {
			return workDone, err
		}
		if status == 401 || status == 403 {
			return workDone, errLoginExpired
		}
		pendingUserIDs = extractPendingIncomingUserIDs(relationships, cfg)
		claimablePendingUserIDs, err = filterClaimablePendingUsers(cfg, currentUserID, pendingUserIDs)
		if err != nil {
			return workDone, err
		}
	}

	conversations, status, err := fetchDMConversations(ctx, client, cfg)
	if err != nil {
		return workDone, err
	}
	if status == 401 || status == 403 {
		return workDone, errLoginExpired
	}

	dmTargets := extractDMTargets(conversations, currentUserID)
	dmTargets, err = claimDMTargets(cfg, currentUserID, dmTargets)
	if err != nil {
		return workDone, err
	}
	dmUserIDs := targetUserIDs(dmTargets)

	if err := saveDerivedFiles(p, claimablePendingUserIDs, dmUserIDs, dmTargets); err != nil {
		return workDone, err
	}

	logf(cfg, "Pending incoming IDs: %d\n", len(claimablePendingUserIDs))
	logf(cfg, "DM user IDs: %d\n", len(dmUserIDs))
	logf(cfg, "DM conversations: %d\n", len(dmTargets))

	dmWorkDone, err := processDMAutoResponses(ctx, client, cfg, p, login, currentUserID, dmTargets, rng)
	if err != nil {
		return workDone, err
	}
	if dmWorkDone {
		workDone = true
	}

	if len(claimablePendingUserIDs) == 0 {
		logln(cfg, "No pending incoming friend requests right now.")
	} else if acceptedFriend {
		logf(cfg, "%d pending friend requests remain.\n", len(claimablePendingUserIDs))
	}
	return workDone, nil
}

func loadSavedLogin(p paths) (model.LoginInfo, error) {
	var login model.LoginInfo
	ok, err := store.LoadJSON(p.LoginInfo, &login)
	if err != nil {
		return model.LoginInfo{}, err
	}
	if !ok {
		return model.LoginInfo{}, nil
	}
	return login, nil
}

func match(ctx context.Context, client *api.Client, cfg Config, currentUserID string) error {
	// Real client: leave the current chat first (PATCH /match/disconnect),
	// then ask for the next partner. Both calls carry "response-time-id".
	if previous := takeActiveMatch(currentUserID); previous != "" {
		disconnectMatch(ctx, client, cfg, currentUserID)
	}
	body := `{"withInterests":false,"restart":true}`
	resp, err := client.Fetch(ctx, api.FetchRequest{
		Method:  "POST",
		URL:     cfg.APIBaseURL + "/match",
		Headers: matchHeaders(cfg, currentUserID),
		Body:    &body,
	})
	if err != nil {
		return fmt.Errorf("match request failed: %w", err)
	}

	if resp.Status == 429 {
		return fmt.Errorf("match request returned 429: %w", errRateLimited)
	}
	if isBannedResponse(resp.Status, resp.Text) {
		return fmt.Errorf("match request returned 403 %s: %w", bannedReason(resp.Text), errAccountBanned)
	}
	if !resp.OK {
		return fmt.Errorf("match request returned non-OK status %d: %s", resp.Status, resp.Text)
	}

	logf(cfg, "Match request successful: %d %s\n", resp.Status, resp.Text)
	return nil
}

// closeReason describes why a browser-mode worker is ending (for the log).
func closeReason(ctx context.Context, err error) string {
	switch {
	case ctx.Err() != nil:
		return "Stop pressed"
	case err == nil:
		return "worker finished"
	case errors.Is(err, errBrowserGone):
		return "window closed or unreachable"
	case errors.Is(err, errAccountBanned):
		return "account banned"
	default:
		return "worker error: " + err.Error()
	}
}

func currentUserIDFromLogin(login model.LoginInfo) string {
	return login.CurrentUserID()
}

func waitForNextCycle(
	ctx context.Context,
	rng *rand.Rand,
	cfg Config,
	currentUserID string,
	realtimeEvents <-chan realtimeEvent,
) error {
	if realtimeEvents == nil {
		return sleepRandom(ctx, rng, cfg, cfg.PollMin, cfg.PollMax, "Waiting before next cycle")
	}

	if cfg.PollMax < cfg.PollMin {
		cfg.PollMax = cfg.PollMin
	}

	delay := cfg.PollMin
	if cfg.PollMax > cfg.PollMin {
		delay += time.Duration(rng.Int63n(int64(cfg.PollMax - cfg.PollMin)))
	}

	logf(cfg, "Waiting before next cycle %s...\n", delay.Truncate(time.Second))
	timer := time.NewTimer(delay)
	defer timer.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
			return nil
		case event := <-realtimeEvents:
			if shouldWakeForRealtimeEvent(currentUserID, event) {
				logf(cfg, "[realtime] waking cycle for conversation=%s event=%s\n", event.ConversationID, event.Type)
				return nil
			}
		}
	}
}

func captureLoginViaBrowser(ctx context.Context, cfg Config, p paths) (model.LoginInfo, error) {
	backend, err := browser.New(cfg.Backend)
	if err != nil {
		return model.LoginInfo{}, err
	}
	defer backend.Close()

	userDataDir := filepath.Join(p.ProfileDir, "browser-data", cfg.Backend)
	if err := store.EnsureDir(userDataDir); err != nil {
		return model.LoginInfo{}, err
	}

	logf(cfg, "Opening %s browser for login with profile %q\n", cfg.Backend, cfg.Profile)
	logf(cfg, "Browser data: %s\n", userDataDir)

	var selectedProxy *proxy.Entry
	if cfg.ProxyRotator != nil {
		if nextProxy, ok := cfg.ProxyRotator.Next(); ok {
			selectedProxy = &nextProxy
			logf(cfg, "Using proxy for browser login: %s\n", nextProxy.Label())
		}
	}
	resolveWindowTimezone(ctx, cfg, selectedProxy)

	if err := backend.Start(ctx, browserWindowOptions(cfg, userDataDir, selectedProxy)); err != nil {
		return model.LoginInfo{}, err
	}

	return waitForLogin(ctx, backend, cfg, p)
}

func waitForLogin(ctx context.Context, backend browser.Backend, cfg Config, p paths) (model.LoginInfo, error) {
	waitingForTokenLogged := false
	waitingForSessionLogged := false

	for {
		login, err := backend.CaptureSession(ctx)
		if err != nil {
			return model.LoginInfo{}, err
		}

		token := login.Token()
		if token != "" {
			ready, authErr := browser.SessionAuthenticated(ctx, backend, cfg.APIBaseURL)
			if authErr == nil && ready {
				login.UserID = login.CurrentUserID()
				if login.UserID == "" {
					return model.LoginInfo{}, fmt.Errorf("login detected, but current bot user ID could not be read from token or browser storage")
				}
				login.Profile = cfg.Profile
				login.Backend = cfg.Backend
				login.SavedAt = time.Now().UTC()
				if err := store.SaveJSON(p.LoginInfo, login); err != nil {
					return model.LoginInfo{}, err
				}
				logf(cfg, "Login detected. Session saved to %s\n", p.LoginInfo)
				return login, nil
			}
		}

		if token == "" && !waitingForTokenLogged {
			logln(cfg, "Waiting for token cookie. Finish login in the browser window.")
			waitingForTokenLogged = true
		}

		if token != "" && !waitingForSessionLogged {
			logln(cfg, "Token detected, but the session is still unauthorized. Finish the full login in the browser window.")
			waitingForSessionLogged = true
		}

		select {
		case <-ctx.Done():
			return model.LoginInfo{}, ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
}

func profilePaths(cfg Config) paths {
	profileDir := filepath.Join(cfg.DataDir, "profiles", safeName(cfg.Profile))
	loginInfoPath := filepath.Join(profileDir, "login_info.json")
	if strings.TrimSpace(cfg.LoginInfoPath) != "" {
		loginInfoPath = cfg.LoginInfoPath
	}
	return paths{
		ProfileDir:      profileDir,
		LoginInfo:       loginInfoPath,
		BotState:        filepath.Join(profileDir, "bot_state.json"),
		RelationshipIDs: filepath.Join(profileDir, "relationships_ids.json"),
		DMUserIDs:       filepath.Join(profileDir, "dm_user_ids.json"),
		DMConversations: filepath.Join(profileDir, "dm_conversations.json"),
		AllUserIDs:      filepath.Join(profileDir, "all_user_ids.json"),
	}
}

func safeName(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return "default"
	}
	name = strings.ReplaceAll(name, "..", "_")
	name = strings.NewReplacer("\\", "_", "/", "_", ":", "_", "*", "_", "?", "_", "\"", "_", "<", "_", ">", "_", "|", "_").Replace(name)
	return name
}
