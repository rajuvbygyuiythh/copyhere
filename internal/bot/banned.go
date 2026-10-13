package bot

// Banned / flagged account detection.
//
// ChitChat answers /match (and later every write) with HTTP 403 and a body
// like {"statusCode":403,"message":"Your account is banned.","code":"ACCOUNT_BANNED"}
// or {"statusCode":403,"message":"Flagged","error":"Forbidden"}. Such an
// account is useless: retrying every cycle only produces log noise. The
// worker marks it dead in the shared registry (Home / Live Wall "Dead /
// banned" tile) and stops. A later Start simply tries once more; if the flag
// was lifted the account works again, otherwise it stops again with one line.

import (
	"errors"
	"regexp"
	"strings"
	"time"
)

// errAccountBanned is wrapped into match / send errors when the platform
// says the account is banned or flagged.
var errAccountBanned = errors.New("account banned or flagged by platform")

var bannedBodyRe = regexp.MustCompile(`(?i)ACCOUNT_BANNED|ACCOUNT_SUSPENDED|account is banned|account has been banned|account (?:is )?suspended|"message"\s*:\s*"flagged"`)

// isBannedResponse reports whether an HTTP status + body mean "this account
// is banned / flagged" (not a throttle, not a bad request).
func isBannedResponse(status int, body string) bool {
	if status != 403 {
		return false
	}
	return bannedBodyRe.MatchString(body)
}

// bannedReason extracts the platform's message for the log line.
func bannedReason(body string) string {
	body = strings.TrimSpace(body)
	if m := regexp.MustCompile(`"message"\s*:\s*"([^"]*)"`).FindStringSubmatch(body); len(m) == 2 {
		if c := regexp.MustCompile(`"code"\s*:\s*"([^"]*)"`).FindStringSubmatch(body); len(c) == 2 {
			return m[1] + " (" + c[1] + ")"
		}
		return m[1]
	}
	if len(body) > 120 {
		body = body[:120] + "..."
	}
	return body
}

// markBotUserBanned records the account as dead right away (no 10-failure
// threshold) so the dashboard counters update immediately.
func markBotUserBanned(cfg Config, currentUserID, reason string) error {
	currentUserID = strings.TrimSpace(currentUserID)
	if currentUserID == "" {
		return nil
	}
	return updateSharedRegistry(cfg.DataDir, func(registry *sharedRegistry) error {
		now := time.Now().UTC()
		addKnownBotUsersFromProfiles(cfg.DataDir, registry, now)
		record := registry.BotUsers[currentUserID]
		record.Profile = cfg.Profile
		record.SeenAt = now
		record.Status = botStatusDead
		record.LastFailureStatus = 403
		record.LastFailureAt = now
		record.BannedAt = now
		record.BannedReason = reason
		registry.BotUsers[currentUserID] = record
		return nil
	})
}
