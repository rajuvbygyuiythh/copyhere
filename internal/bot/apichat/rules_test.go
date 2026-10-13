package apichat

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRulesFileOverridesOptions(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, RulesFile), []byte(`{"share_hot":2,"share_warm":3,"share_cold":5,"no_share_percent":50,"hint_percent":10,"no_share_min_replies":2,"no_share_max_replies":2,"no_share_strict":true}`), 0o644); err != nil {
		t.Fatal(err)
	}
	opt := Defaults()
	opt.APIKey = "k"
	e, err := New(dir, opt)
	if err != nil {
		t.Fatal(err)
	}
	r := e.Rules()
	if r.ShareHot != 2 || r.ShareWarm != 3 || r.ShareCold != 5 || r.NoSharePercent != 50 || r.HintPercent != 10 || r.NoShareMin != 2 || r.NoShareMax != 2 || !r.NoShareStrict {
		t.Fatalf("rules not applied: %+v", r)
	}
	if e.cfg.ShareHot != 2 || !e.cfg.NoShareStrict {
		t.Fatalf("director config not updated: %+v", e.cfg)
	}
}

func TestRulesMissingAndBroken(t *testing.T) {
	dir := t.TempDir()
	if err := EnsureData(dir); err != nil {
		t.Fatal(err)
	}
	_ = os.Remove(filepath.Join(dir, RulesFile))
	opt := Defaults()
	opt.APIKey = "k"
	e, err := New(dir, opt)
	if err != nil || len(e.Warns) != 0 || e.Rules() != DefaultRules() {
		t.Fatalf("missing file must mean defaults without warnings: %v %v %+v", err, e.Warns, e.Rules())
	}
	if err := os.WriteFile(filepath.Join(dir, RulesFile), []byte(`{broken`), 0o644); err != nil {
		t.Fatal(err)
	}
	e, err = New(dir, opt)
	if err != nil || len(e.Warns) != 1 || e.Rules() != DefaultRules() {
		t.Fatalf("broken file must warn and keep defaults: %v %v", err, e.Warns)
	}
}

func TestEmbeddedRulesParse(t *testing.T) {
	dir := t.TempDir()
	if err := EnsureData(dir); err != nil {
		t.Fatal(err)
	}
	r, err := LoadRules(filepath.Join(dir, RulesFile))
	if err != nil || r != DefaultRules() {
		t.Fatalf("embedded config_rules.json must equal DefaultRules: %v %+v", err, r)
	}
}
