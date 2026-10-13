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
