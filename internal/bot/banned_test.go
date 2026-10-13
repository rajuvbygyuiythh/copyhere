package bot

import (
	"errors"
	"fmt"
	"testing"
)

func TestIsBannedResponse(t *testing.T) {
	cases := []struct {
		status int
		body   string
		want   bool
	}{
		{403, `{"statusCode":403,"path":"/match","message":"Your account is banned.","code":"ACCOUNT_BANNED"}`, true},
		{403, `{"statusCode":403,"path":"/match","message":"Flagged","error":"Forbidden"}`, true},
		{403, `{"statusCode":403,"message":"Forbidden resource","error":"Forbidden"}`, false},
		{429, `{"statusCode":429,"message":"Too Many Requests"}`, false},
		{200, `ACCOUNT_BANNED`, false},
	}
	for _, c := range cases {
		if got := isBannedResponse(c.status, c.body); got != c.want {
			t.Errorf("isBannedResponse(%d, %s) = %v, want %v", c.status, c.body, got, c.want)
		}
	}
	if r := bannedReason(`{"statusCode":403,"message":"Your account is banned.","code":"ACCOUNT_BANNED"}`); r != "Your account is banned. (ACCOUNT_BANNED)" {
		t.Fatalf("reason: %q", r)
	}
	if r := bannedReason(`{"statusCode":403,"message":"Flagged","error":"Forbidden"}`); r != "Flagged" {
		t.Fatalf("reason: %q", r)
	}
	wrapped := fmt.Errorf("match request returned 403 Flagged: %w", errAccountBanned)
	if !errors.Is(wrapped, errAccountBanned) {
		t.Fatal("wrap")
	}
}

func TestMarkBotUserBannedCountsDead(t *testing.T) {
	cfg := Config{DataDir: t.TempDir(), Profile: "acc1"}
	if err := registerBotUser(cfg, "bot-1"); err != nil {
		t.Fatal(err)
	}
	before, _ := BotAccountStatusCounts(cfg.DataDir)
	if err := markBotUserBanned(cfg, "bot-1", "Flagged"); err != nil {
		t.Fatal(err)
	}
	after, _ := BotAccountStatusCounts(cfg.DataDir)
	if after.Dead != before.Dead+1 {
		t.Fatalf("dead count %d -> %d", before.Dead, after.Dead)
	}
}
