package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadShareStylesFile(t *testing.T) {
	styles, warns, err := loadShareStyles("snap_share.txt")
	if err != nil {
		t.Fatal(err)
	}
	if len(warns) != 0 {
		t.Fatalf("shipped snap_share.txt must have no bad lines: %v", warns)
	}
	if len(styles) < 20 {
		t.Fatalf("expected 20+ styles, got %d", len(styles))
	}
	two := 0
	for _, st := range styles {
		joined := strings.Join(st, " ")
		if !strings.Contains(joined, usernamePlaceholder) {
			t.Fatalf("style without placeholder: %q", st)
		}
		if strings.Contains(joined, "@") {
			t.Fatalf("styles must never contain @ (platform filter): %q", st)
		}
		if len(st) > 1 {
			two++
		}
	}
	if two == 0 {
		t.Fatal("expected some two-message styles (a | b)")
	}
}

func TestLoadShareStylesParsing(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "snap_share.txt")
	content := "# comment\r\n\r\nadd me %username%\r\nno handle here\r\n  ok | %username% 👻 | \r\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	styles, warns, err := loadShareStyles(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(styles) != 2 {
		t.Fatalf("styles = %v, want 2", styles)
	}
	if len(warns) != 1 || !strings.Contains(warns[0], "line 4") {
		t.Fatalf("warns = %v", warns)
	}
	if len(styles[1]) != 2 || styles[1][0] != "ok" || styles[1][1] != "%username% 👻" {
		t.Fatalf("pipe split wrong: %v", styles[1])
	}
}

func TestShareDeckNoRepeats(t *testing.T) {
	raw := []string{"a %username%", "b %username%", "c %username%", "d %username% | second"}
	deck := newShareDeck(splitShareStyles(raw), "test")
	rng := newRng()

	prev := ""
	for round := 0; round < 25; round++ {
		seen := map[string]bool{}
		for i := 0; i < len(raw); i++ {
			lines := deck.next(rng, "snapqueen")
			key := strings.Join(lines, "|")
			if strings.Contains(key, usernamePlaceholder) || !strings.Contains(key, "snapqueen") {
				t.Fatalf("placeholder not filled: %q", key)
			}
			if seen[key] {
				t.Fatalf("round %d: style repeated before deck exhausted: %q", round, key)
			}
			seen[key] = true
			if key == prev {
				t.Fatalf("same style twice in a row: %q", key)
			}
			prev = key
		}
		if len(seen) != len(raw) {
			t.Fatalf("round %d used %d styles, want %d", round, len(seen), len(raw))
		}
	}
}

func TestShareDeckNilFallsBack(t *testing.T) {
	var deck *shareDeck
	lines := deck.next(newRng(), "x")
	if len(lines) != 1 || !strings.Contains(lines[0], "x") {
		t.Fatalf("nil deck fallback: %v", lines)
	}
	if deck.count() != len(defaultShareLines) || deck.name() != "built-in" {
		t.Fatalf("nil deck count/name: %d %s", deck.count(), deck.name())
	}
}

// --- share types: no-share / hint / direct -------------------------------

func testCfg() config {
	return config{ShareHot: 4, ShareWarm: 6, ShareCold: 8, HintPercent: 60, NoSharePercent: 30, NoShareMin: 3, NoShareMax: 4}
}

// drive runs stranger lines through observe+plan and returns the plans.
func drive(d *director, cfg config, lines []string) []plan {
	var out []plan
	for i, l := range lines {
		d.observe(l)
		out = append(out, d.plan(cfg, i+1, l))
	}
	return out
}

func TestRollDistribution(t *testing.T) {
	cfg := testCfg()
	rng := newRng()
	noShare, hint, direct := 0, 0, 0
	for i := 0; i < 3000; i++ {
		d := newDirector(rng)
		d.roll(cfg)
		switch {
		case d.noShare:
			noShare++
			if d.bailTurn < 3 || d.bailTurn > 4 {
				t.Fatalf("bailTurn %d outside 3-4", d.bailTurn)
			}
		case d.hintMode:
			hint++
		default:
			direct++
		}
	}
	pct := func(n int) float64 { return float64(n) * 100 / 3000 }
	if p := pct(noShare); p < 26 || p > 34 {
		t.Fatalf("no-share %.1f%%, want ~30", p)
	}
	if p := float64(hint) * 100 / float64(hint+direct); p < 56 || p > 64 {
		t.Fatalf("hint share of share-chats %.1f%%, want ~60", p)
	}
}

func TestNoShareChatCloses(t *testing.T) {
	cfg := testCfg()
	d := newDirector(newRng())
	d.roll(cfg)
	d.noShare, d.hintMode, d.bailTurn = true, false, 3
	plans := drive(d, cfg, []string{"hey", "m 23", "u horny?", "wyd"})
	for i, p := range plans[:3] {
		if p.ShareNow || p.HintNow {
			t.Fatalf("reply %d: no-share chat must never share/hint: %+v", i+1, p)
		}
		low := strings.ToLower(p.Instruction)
		if strings.Contains(low, "fun on snap") || strings.Contains(low, "pics on snap") || strings.Contains(low, "got snap") || strings.Contains(low, "always on snap") {
			t.Fatalf("reply %d instruction mentions snap in a no-share chat: %s", i+1, p.Instruction)
		}
	}
	if !plans[2].EndAfter || !d.ended || d.endReason != "no-share chat closed" {
		t.Fatalf("expected the chat to close on reply 3: %+v ended=%v %s", plans[2], d.ended, d.endReason)
	}
}

func TestNoShareUserAsks(t *testing.T) {
	cfg := testCfg()
	d := newDirector(newRng())
	d.roll(cfg)
	d.noShare, d.hintMode, d.bailTurn = true, false, 4
	plans := drive(d, cfg, []string{"hey", "whats ur snap"})
	if !plans[1].ShareNow || !d.shared {
		t.Fatalf("stranger asked -> share even in a no-share chat: %+v", plans[1])
	}

	cfg.NoShareStrict = true
	d = newDirector(newRng())
	d.roll(cfg)
	d.noShare, d.hintMode, d.bailTurn = true, false, 4
	plans = drive(d, cfg, []string{"hey", "whats ur snap"})
	if plans[1].ShareNow || d.shared || plans[1].Why != "no-share chat -> decline" {
		t.Fatalf("strict: must decline: %+v", plans[1])
	}
}

func TestHintThenUserBites(t *testing.T) {
	cfg := testCfg()
	d := newDirector(newRng())
	d.roll(cfg)
	d.noShare, d.hintMode = false, true
	d.tier, d.score = tierHot, 6 // HOT: share at 4 -> hint at 3
	plans := drive(d, cfg, []string{"hey", "m 22", "ur cute", "whats ur snap"})
	if !plans[2].HintNow || plans[2].ShareNow || plans[2].Why != "hint" {
		t.Fatalf("reply 3 should be the hint: %+v", plans[2])
	}
	if !plans[3].ShareNow || d.sharedTurn != 4 {
		t.Fatalf("reply 4 should share after they asked: %+v", plans[3])
	}
	if !strings.HasPrefix(plans[3].Why, "user wants it") {
		t.Fatalf("why = %q", plans[3].Why)
	}
}

func TestHintIgnoredSharesNextReply(t *testing.T) {
	cfg := testCfg()
	d := newDirector(newRng())
	d.roll(cfg)
	d.noShare, d.hintMode = false, true
	d.tier, d.score = tierHot, 6
	plans := drive(d, cfg, []string{"hey", "m 22", "ur cute", "im from texas lol"})
	if !plans[2].HintNow {
		t.Fatalf("reply 3 should be the hint: %+v", plans[2])
	}
	if !plans[3].ShareNow || plans[3].Why != "hint missed -> direct share" {
		t.Fatalf("reply 4 should share directly: %+v", plans[3])
	}
}

func TestHintYesShares(t *testing.T) {
	cfg := testCfg()
	d := newDirector(newRng())
	d.roll(cfg)
	d.noShare, d.hintMode = false, true
	d.tier, d.score = tierHot, 6
	plans := drive(d, cfg, []string{"hey", "m 22", "ur cute", "ya"})
	if !plans[3].ShareNow {
		t.Fatalf("'ya' after the hint should share: %+v", plans[3])
	}
}

func TestDirectUnchanged(t *testing.T) {
	cfg := testCfg()
	d := newDirector(newRng())
	d.roll(cfg)
	d.noShare, d.hintMode = false, false
	d.tier, d.score = tierHot, 6
	plans := drive(d, cfg, []string{"hey", "m 22", "ur cute", "lol"})
	if plans[2].HintNow || plans[2].Why != "ask snap" {
		t.Fatalf("direct: reply 3 should ask snap: %+v", plans[2])
	}
	if !plans[3].ShareNow || plans[3].Why != "share turn" {
		t.Fatalf("direct: reply 4 should share: %+v", plans[3])
	}
}

func TestHintFile(t *testing.T) {
	styles, warns, err := loadStyleFile("snap_hint.txt", false)
	if err != nil || len(warns) != 0 {
		t.Fatal(err, warns)
	}
	if len(styles) < 15 {
		t.Fatalf("expected 15+ hints, got %d", len(styles))
	}
	for _, st := range styles {
		j := strings.ToLower(strings.Join(st, " "))
		if strings.Contains(j, usernamePlaceholder) || strings.Contains(j, "add me") || strings.Contains(j, "http") {
			t.Fatalf("hint must not carry the handle / 'add me' / links: %q", st)
		}
	}
}

func TestUsernameDeck(t *testing.T) {
	snapNames = newShareDeck([][]string{{"a"}, {"b"}, {"c"}}, "usernames")
	defer func() { snapNames = nil }()
	cfg := testCfg()
	rng := newRng()
	seen := map[string]int{}
	for i := 0; i < 30; i++ {
		seen[nextSnapUser(cfg, rng)]++
	}
	if len(seen) != 3 || seen["a"] != 10 || seen["b"] != 10 || seen["c"] != 10 {
		t.Fatalf("deck should rotate evenly: %v", seen)
	}
	cfg.SnapUsernames = []string{"only"}
	snapNames = nil
	if nextSnapUser(cfg, rng) != "only" {
		t.Fatal("config list fallback")
	}
}
