package bot

import (
	"math/rand"
	"os"
	"path/filepath"
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
