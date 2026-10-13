package bot

// API Chat mode — the fourth dashboard mode.
//
// The whole conversation is written by the AI; the funnel (interest tiers,
// hint / direct / no-share split, when the snap is shared) is decided by the
// apitest "director" ported to internal/bot/apichat. This file is the bridge:
// it reuses the EVAChat plumbing in logic.go (opener policy, pacing, extra
// lines, counters) and only swaps the brain. The TXT engine is untouched.

import (
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"time"

	"chitchatbot/internal/bot/apichat"
	"chitchatbot/internal/model"
)

// APIChatDataDirName is the editable folder under data/ (persona.txt,
// snap_share.txt, snap_hint.txt, config_rules.json).
const APIChatDataDirName = "api_chat"

// EnsureAPIChatData seeds data/api_chat/ and returns its path.
func EnsureAPIChatData(dataDir string) (string, error) {
	dir := filepath.Join(dataDir, APIChatDataDirName)
	if err := apichat.EnsureData(dir); err != nil {
		return "", err
	}
	return dir, nil
}

// APIChatOptions maps dashboard settings to engine options: only the API
// connection comes from the dashboard; the funnel rules are read by the
// engine from data/api_chat/config_rules.json.
func APIChatOptions(settings model.MessageSettings) apichat.Options {
	opt := apichat.Defaults()
	opt.BaseURL = strings.TrimSpace(settings.AIBaseURL)
	opt.APIKey = strings.TrimSpace(settings.AIAPIKey)
	opt.Model = strings.TrimSpace(settings.AIModel)
	return opt
}

// LoadAPIChat builds the shared engine for all workers.
func LoadAPIChat(dataRoot string, settings model.MessageSettings) (*apichat.Engine, error) {
	return apichat.New(dataRoot, APIChatOptions(settings))
}

// apiChatEnabled reports that this worker runs the API Chat brain.
func apiChatEnabled(cfg Config) bool {
	return cfg.ChatMode == model.ChatModeAPIChat && cfg.EVAChat != nil && cfg.EVAChat.API != nil
}

func apiChatKey(cfg Config, userID string) string {
	return cfg.Profile + "|" + userID
}

// apiChatUsername hands out one dashboard username per stranger from a
// per-account shuffled deck (every handle once before any repeats) and
// records it in AssignedUsernames so counters / flagged-user bookkeeping
// keep working exactly like the other modes.
func apiChatUsername(cfg Config, accountState *model.AccountState, userID string, rng *rand.Rand) string {
	if accountState.AssignedUsernames == nil {
		accountState.AssignedUsernames = make(map[string]string)
	}
	if assigned := accountState.AssignedUsernames[userID]; assigned != "" {
		return assigned
	}
	names := make([]string, 0, len(cfg.MessageUsernames))
	for _, u := range cfg.MessageUsernames {
		if u = strings.TrimPrefix(strings.TrimSpace(u), "@"); u != "" {
			names = append(names, u)
		}
	}
	if len(names) == 0 {
		return ""
	}
	if len(accountState.APIUsernameOrder) != len(names) || accountState.APIUsernameCursor >= len(names) {
		accountState.APIUsernameOrder = rng.Perm(len(names))
		accountState.APIUsernameCursor = 0
	}
	assigned := names[accountState.APIUsernameOrder[accountState.APIUsernameCursor]]
	accountState.APIUsernameCursor++
	accountState.AssignedUsernames[userID] = assigned
	return assigned
}

// apiChatStart is the bot-first opener.
func apiChatStart(cfg Config, accountState *model.AccountState, userID string, rng *rand.Rand) EVAReplyResult {
	session := evaChatSession(accountState, userID)
	res := cfg.EVAChat.API.Start(apiChatKey(cfg, userID), rng)
	session.Started = true
	session.Stage = "API"
	accountState.EVAChatSessions[userID] = session
	return EVAReplyResult{Text: res.Text(), Extra: res.Extra(), SourcePool: "api", Reason: res.Status}
}

// apiChatReply handles one incoming message through the director + API.
func apiChatReply(cfg Config, accountState *model.AccountState, userID, incoming string, rng *rand.Rand) EVAReplyResult {
	session := evaChatSession(accountState, userID)
	username := apiChatUsername(cfg, accountState, userID, rng)
	res := cfg.EVAChat.API.Reply(apiChatKey(cfg, userID), evaLastLine(incoming), username, rng, session.BotReplyCount)

	session.Started = true
	session.Stage = "API"
	if res.Turn > 0 {
		session.BotReplyCount = res.Turn
	}
	if res.Ended {
		session.Ended = true
		cfg.EVAChat.API.Forget(apiChatKey(cfg, userID))
	}
	if res.ShareNow {
		session.SnapPivoted = true
		switch {
		case strings.HasPrefix(res.Why, "user wants it"):
			accountState.APIUserAsked++
		case strings.HasPrefix(res.ShareType, "hint"):
			accountState.APIShareHint++
		default:
			accountState.APIShareDirect++
		}
		if strings.HasPrefix(res.ShareType, "hint") && strings.HasPrefix(res.Why, "user wants it") {
			accountState.APIHintBite++
		}
	}
	if res.Ended && res.EndReason == "no-share chat closed" {
		accountState.APINoShareClose++
	}
	if res.Err != nil {
		accountState.APIErrors++
		apiChatLogError(cfg, res.Err)
	}
	accountState.EVAChatSessions[userID] = session

	out := EVAReplyResult{
		Text:        res.Text(),
		Extra:       res.Extra(),
		SourcePool:  "api",
		Reason:      res.Status,
		Ended:       res.Ended,
		SnapShared:  res.ShareNow,
		HasUsername: res.ShareNow && username != "" && strings.Contains(strings.Join(res.Lines, " "), username),
	}
	return out
}

// apiChatLogError appends API failures to data/api_chat/errors.log so the
// owner can see rate limits / wrong model ids without the dashboard log.
func apiChatLogError(cfg Config, err error) {
	if cfg.DataDir == "" || err == nil {
		return
	}
	path := filepath.Join(cfg.DataDir, APIChatDataDirName, "errors.log")
	f, ferr := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if ferr != nil {
		return
	}
	defer f.Close()
	_, _ = fmt.Fprintf(f, "%s %s: %v\n", time.Now().Format("2006-01-02 15:04:05"), cfg.Profile, err)
}
