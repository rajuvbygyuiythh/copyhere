package bot

import (
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"chitchatbot/internal/model"
)

func testEVAEngine(t *testing.T) *EVAChatEngine {
	t.Helper()
	root, err := EnsureEVAChatData(t.TempDir())
	if err != nil {
		t.Fatalf("EnsureEVAChatData: %v", err)
	}
	engine, err := LoadEVAChatEngine(root)
	if err != nil {
		t.Fatalf("LoadEVAChatEngine: %v", err)
	}
	return engine
}

func TestEVAChatStarterPackAndFlow(t *testing.T) {
	engine := testEVAEngine(t)
	rng := rand.New(rand.NewSource(7))
	state := NewEVAChatSession()

	opener := engine.Start(&state, rng)
	if opener.Text == "" || opener.SourcePool != "greeting.txt" {
		t.Fatalf("unexpected opener: %#v", opener)
	}
	if !state.Started || state.Stage != evaStageWaitAgeGender {
		t.Fatalf("unexpected state after opener: %#v", state)
	}

	ageReply := engine.Reply("m21", &state, rng)
	if ageReply.SourcePool != "age_gender.txt" || state.Stage != evaStageWaitCountry {
		t.Fatalf("age/gender should advance country stage: reply=%#v state=%#v", ageReply, state)
	}

	countryReply := engine.Reply("bangladesh", &state, rng)
	if countryReply.SourcePool != "follow_up.txt" || state.Stage != evaStageMiddle {
		t.Fatalf("country should enter middle stage: reply=%#v state=%#v", countryReply, state)
	}

	smallTalk := engine.Reply("how r u today", &state, rng)
	if smallTalk.SourcePool != "how_are_you.txt" || smallTalk.Text == "" {
		t.Fatalf("expected how-are-you category: %#v", smallTalk)
	}
}

func TestEVAChatMinorGateEndsConversation(t *testing.T) {
	engine := testEVAEngine(t)
	rng := rand.New(rand.NewSource(9))
	state := NewEVAChatSession()
	_ = engine.Start(&state, rng)

	result := engine.Reply("m15", &state, rng)
	if !result.Ended || result.SourcePool != "closer.txt" || !state.Blocked || !state.Ended {
		t.Fatalf("minor gate must close safely: result=%#v state=%#v", result, state)
	}
	if later := engine.Reply("hello", &state, rng); later.Text != "" || !later.Ended {
		t.Fatalf("ended session must not send another reply: %#v", later)
	}
}

func TestEVAChatNormalizesSlangAndAvoidsSpentPool(t *testing.T) {
	if !evaMatchesTrigger("wats ur age?", "what is your age") {
		t.Fatal("slang-normalized trigger did not match")
	}

	engine := testEVAEngine(t)
	rng := rand.New(rand.NewSource(3))
	state := NewEVAChatSession()
	_ = engine.Start(&state, rng)
	state.Stage = evaStageMiddle

	// greeting.txt was already used by Start; a later greeting cannot reuse it.
	result := engine.Reply("hello", &state, rng)
	if result.SourcePool == "greeting.txt" || result.Text == "" {
		t.Fatalf("spent pool should fall back without silence: %#v", result)
	}
}

func TestEVAChatRuntimeAdapterPersistsSession(t *testing.T) {
	engine := testEVAEngine(t)
	cfg := Config{ChatMode: model.ChatModeEVAChat, EVAChat: &EVAChatConfig{Engine: engine}}
	state := &model.AccountState{}
	rng := rand.New(rand.NewSource(11))

	opener := evaChatStart(cfg, state, "user-1", rng)
	if opener.Text == "" {
		t.Fatal("adapter did not create an opener")
	}
	stored := state.EVAChatSessions["user-1"]
	if !stored.Started || stored.Stage != evaStageWaitAgeGender {
		t.Fatalf("adapter did not persist opener state: %#v", stored)
	}

	reply := evaChatReply(cfg, state, "user-1", "m21", rng)
	if reply.SourcePool != "age_gender.txt" {
		t.Fatalf("adapter routed wrong reply: %#v", reply)
	}
	stored = state.EVAChatSessions["user-1"]
	if stored.Stage != evaStageWaitCountry || !stored.HasUserAge {
		t.Fatalf("adapter did not persist reply state: %#v", stored)
	}
}

func TestEVAChatLoadRejectsMissingRequiredPool(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "input"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "output"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "input", "greeting.txt"), []byte("hi\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadEVAChatEngine(root); err == nil {
		t.Fatal("expected missing required output pool to be rejected")
	}
}

func TestEVAChatSharesSnapWhenUserAsks(t *testing.T) {
	engine := testEVAEngine(t)
	if !engine.SnapShareAvailable(true) {
		t.Fatal("starter pack should provide output/snap_share.txt")
	}
	if engine.SnapShareAvailable(false) {
		t.Fatal("snap share must be unavailable when no usernames are configured and the pool uses %username%")
	}

	cfg := Config{
		ChatMode:         model.ChatModeEVAChat,
		EVAChat:          &EVAChatConfig{Engine: engine},
		MessageUsernames: []string{"snap_demo_user"},
	}
	state := &model.AccountState{}
	rng := rand.New(rand.NewSource(5))

	opener := evaChatStart(cfg, state, "user-2", rng)
	if opener.HasUsername {
		t.Fatalf("opener must not carry a username: %#v", opener)
	}
	_ = evaChatReply(cfg, state, "user-2", "f22", rng)
	_ = evaChatReply(cfg, state, "user-2", "canada", rng)

	share := evaChatReply(cfg, state, "user-2", "whats your snap", rng)
	if !share.SnapShared || share.SourcePool != evaSnapPool || !share.HasUsername {
		t.Fatalf("direct snap request should share the username: %#v", share)
	}
	if !strings.Contains(share.Text, "snap_demo_user") || strings.Contains(share.Text, evaUsernamePlaceholder) {
		t.Fatalf("%%username%% was not rendered: %q", share.Text)
	}
	stored := state.EVAChatSessions["user-2"]
	if !stored.SnapShared || state.AssignedUsernames["user-2"] != "snap_demo_user" {
		t.Fatalf("snap share state not persisted: session=%#v assigned=%#v", stored, state.AssignedUsernames)
	}

	closing := evaChatReply(cfg, state, "user-2", "ok added you", rng)
	if !closing.Ended || closing.SourcePool != "closer.txt" || closing.SnapShared {
		t.Fatalf("after sharing, the next reply must close politely: %#v", closing)
	}
	if again := evaChatReply(cfg, state, "user-2", "snap?", rng); again.Text != "" || again.SnapShared {
		t.Fatalf("username must never be shared twice: %#v", again)
	}
}

func TestEVAChatSharesSnapProactivelyAfterMiddleReplies(t *testing.T) {
	engine := testEVAEngine(t)
	cfg := Config{
		ChatMode:         model.ChatModeEVAChat,
		EVAChat:          &EVAChatConfig{Engine: engine},
		MessageUsernames: []string{"proactive_user"},
	}
	state := &model.AccountState{}
	rng := rand.New(rand.NewSource(13))

	_ = evaChatStart(cfg, state, "user-3", rng)
	_ = evaChatReply(cfg, state, "user-3", "m25", rng)
	_ = evaChatReply(cfg, state, "user-3", "india", rng)

	var share EVAReplyResult
	for i := 0; i < evaSnapAfterMiddleReplies+1; i++ {
		share = evaChatReply(cfg, state, "user-3", "haha nice", rng)
		if share.SnapShared {
			break
		}
	}
	if !share.SnapShared || !strings.Contains(share.Text, "proactive_user") {
		t.Fatalf("expected a proactive snap share after %d middle replies: %#v", evaSnapAfterMiddleReplies, share)
	}
	if got := state.EVAChatSessions["user-3"].MiddleReplies; got != evaSnapAfterMiddleReplies {
		t.Fatalf("middle reply counter = %d, want %d", got, evaSnapAfterMiddleReplies)
	}
}

func TestEVAChatSharesSnapWhenUserLeaves(t *testing.T) {
	engine := testEVAEngine(t)
	cfg := Config{
		ChatMode:         model.ChatModeEVAChat,
		EVAChat:          &EVAChatConfig{Engine: engine},
		MessageUsernames: []string{"leaving_user"},
	}
	state := &model.AccountState{}
	rng := rand.New(rand.NewSource(17))

	_ = evaChatStart(cfg, state, "user-4", rng)
	_ = evaChatReply(cfg, state, "user-4", "f19", rng)
	_ = evaChatReply(cfg, state, "user-4", "uk", rng)

	share := evaChatReply(cfg, state, "user-4", "gtg brb", rng)
	if !share.SnapShared || !strings.Contains(share.Text, "leaving_user") {
		t.Fatalf("user leaving before share should receive the username: %#v", share)
	}
}

func TestEVAChatSkipsSnapWithoutUsernames(t *testing.T) {
	engine := testEVAEngine(t)
	cfg := Config{ChatMode: model.ChatModeEVAChat, EVAChat: &EVAChatConfig{Engine: engine}}
	state := &model.AccountState{}
	rng := rand.New(rand.NewSource(19))

	_ = evaChatStart(cfg, state, "user-5", rng)
	_ = evaChatReply(cfg, state, "user-5", "m30", rng)
	_ = evaChatReply(cfg, state, "user-5", "usa", rng)

	for i := 0; i < evaMaxReplies; i++ {
		result := evaChatReply(cfg, state, "user-5", "whats your snap", rng)
		if result.SnapShared || result.HasUsername || strings.Contains(result.Text, evaUsernamePlaceholder) {
			t.Fatalf("snap share must be skipped when no usernames exist: %#v", result)
		}
		if result.Ended {
			break
		}
	}
	if state.EVAChatSessions["user-5"].SnapShared {
		t.Fatal("session must not be marked as shared without usernames")
	}
}
