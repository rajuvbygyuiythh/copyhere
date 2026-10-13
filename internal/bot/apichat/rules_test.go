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

func TestRulesPathOptionAndProjectLookup(t *testing.T) {
	root := t.TempDir()
	proj := filepath.Join(root, "cmd", "apitest")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(proj, RulesFile), []byte(`{"share_hot":1,"share_warm":2,"share_cold":3,"no_share_percent":5,"hint_percent":7,"no_share_min_replies":1,"no_share_max_replies":1}`), 0o644); err != nil {
		t.Fatal(err)
	}
	// explicit path wins over <dataRoot>/config_rules.json
	data := t.TempDir()
	if err := EnsureData(data); err != nil {
		t.Fatal(err)
	}
	opt := Defaults()
	opt.APIKey, opt.RulesPath = "k", filepath.Join(proj, RulesFile)
	e, err := New(data, opt)
	if err != nil || e.Rules().ShareHot != 1 || e.Rules().HintPercent != 7 {
		t.Fatalf("RulesPath not used: %v %+v", err, e.Rules())
	}
	// lookup walks up from the working directory (build/bin like the exe)
	deep := filepath.Join(root, "build", "bin")
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatal(err)
	}
	wd, _ := os.Getwd()
	defer os.Chdir(wd)
	if err := os.Chdir(deep); err != nil {
		t.Fatal(err)
	}
	got := FindProjectRules()
	want := filepath.Join(proj, RulesFile)
	if gotR, _ := filepath.EvalSymlinks(got); gotR != func() string { w, _ := filepath.EvalSymlinks(want); return w }() {
		t.Fatalf("FindProjectRules = %q, want %q", got, want)
	}
	// skip list keeps the data folder free of a second rules file
	data2 := t.TempDir()
	if err := EnsureData(data2, RulesFile); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(data2, RulesFile)); !os.IsNotExist(err) {
		t.Fatal("config_rules.json must not be seeded when skipped")
	}
}
