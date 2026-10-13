package bot

import (
	"math/rand"
	"testing"
	"time"
)

func testPacing() Pacing {
	return Pacing{
		Enabled:         true,
		BotFirstPercent: 30,
		OpenerWaitMin:   45 * time.Second,
		OpenerWaitMax:   120 * time.Second,
		BotOpenDelayMin: 8 * time.Second,
		BotOpenDelayMax: 25 * time.Second,
		ActiveMin:       25 * time.Minute,
		ActiveMax:       40 * time.Minute,
		RestMin:         10 * time.Minute,
		RestMax:         25 * time.Minute,
		ChatsPerHourMax: 30,
		LimitCooldown:   20 * time.Minute,
	}
}

func newTestPacer(cfg Pacing) (*pacer, *time.Time) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	rng := rand.New(rand.NewSource(7))
	p := &pacer{cfg: cfg, rng: rng}
	p.now = func() time.Time { return now }
	p.activeUntil = now.Add(randDuration(rng, cfg.ActiveMin, cfg.ActiveMax))
	return p, &now
}

func TestPacerShiftCycle(t *testing.T) {
	p, now := newTestPacer(testPacing())

	if ok, _, phase, _ := p.gate(); !ok || phase != PhaseActive {
		t.Fatalf("fresh pacer must be active, got ok=%v phase=%s", ok, phase)
	}
	activeLen := p.activeUntil.Sub(*now)
	if activeLen < 25*time.Minute || activeLen > 40*time.Minute {
		t.Fatalf("active window %s outside 25-40min", activeLen)
	}

	// shift ends -> resting
	*now = p.activeUntil.Add(time.Second)
	ok, until, phase, reason := p.gate()
	if ok || phase != PhaseResting || reason != "shift finished" {
		t.Fatalf("expected shift rest, got ok=%v phase=%s reason=%q", ok, phase, reason)
	}
	restLen := until.Sub(*now)
	if restLen < 10*time.Minute || restLen > 25*time.Minute {
		t.Fatalf("rest window %s outside 10-25min", restLen)
	}

	// still resting halfway
	*now = now.Add(restLen / 2)
	if ok, _, phase, _ := p.gate(); ok || phase != PhaseResting {
		t.Fatalf("should still rest, got ok=%v phase=%s", ok, phase)
	}

	// rest over -> active again with a NEW active window
	*now = until.Add(time.Second)
	if ok, _, phase, _ := p.gate(); !ok || phase != PhaseActive {
		t.Fatalf("rest finished: expected active, got ok=%v phase=%s", ok, phase)
	}
	if !p.activeUntil.After(*now) {
		t.Fatalf("new active window not scheduled")
	}
}

func TestPacerHourlyBudget(t *testing.T) {
	cfg := testPacing()
	cfg.ChatsPerHourMax = 3
	cfg.ActiveMin, cfg.ActiveMax = 10*time.Hour, 10*time.Hour // shift never ends in this test
	p, now := newTestPacer(cfg)

	for i := 0; i < 3; i++ {
		if ok, _, _, _ := p.gate(); !ok {
			t.Fatalf("chat %d should be allowed", i+1)
		}
		p.noteChat()
		*now = now.Add(5 * time.Minute)
	}
	ok, until, phase, reason := p.gate()
	if ok || phase != PhaseResting || reason != "hourly chat budget reached" {
		t.Fatalf("budget: ok=%v phase=%s reason=%q", ok, phase, reason)
	}
	// first chat was at T+0, budget frees at T+60min
	if got := until.Sub(*now); got != 45*time.Minute {
		t.Fatalf("budget wait = %s, want 45m", got)
	}
	*now = until.Add(time.Second)
	if ok, _, _, _ := p.gate(); !ok {
		t.Fatal("budget should free after the oldest chat ages out")
	}
}

func TestPacerLimitCooldownWinsOverEverything(t *testing.T) {
	p, now := newTestPacer(testPacing())
	until := p.noteLimited()
	cool := until.Sub(*now)
	if cool < 20*time.Minute || cool > 30*time.Minute {
		t.Fatalf("cooldown %s outside 20-30min (20min + up to 50%% jitter)", cool)
	}
	if ok, _, phase, _ := p.gate(); ok || phase != PhaseLimited {
		t.Fatalf("limited pacer must not match: ok=%v phase=%s", ok, phase)
	}
	// extending: a second 429 never shortens the cooldown
	second := p.noteLimited()
	if second.Before(until) {
		t.Fatal("second limit shortened the cooldown")
	}
	*now = second.Add(time.Second)
	if ok, _, _, _ := p.gate(); !ok {
		t.Fatal("cooldown over: should be active")
	}
}

func TestOpenerPolicyRatio(t *testing.T) {
	p, _ := newTestPacer(testPacing())
	botFirst := 0
	const n = 10000
	for i := 0; i < n; i++ {
		if p.botOpensFirst() {
			botFirst++
		}
	}
	ratio := float64(botFirst) / n
	if ratio < 0.27 || ratio > 0.33 {
		t.Fatalf("bot-first ratio %.3f, want ~0.30", ratio)
	}

	p.cfg.BotFirstPercent = 0
	if p.botOpensFirst() {
		t.Fatal("0%% must never open")
	}
	p.cfg.BotFirstPercent = 100
	if !p.botOpensFirst() {
		t.Fatal("100%% must always open")
	}

	// disabled pacing keeps the legacy behaviour: bot always opens, no delay
	off := &pacer{cfg: Pacing{Enabled: false}, rng: rand.New(rand.NewSource(1)), now: time.Now}
	if !off.botOpensFirst() || off.botOpenDelay() != 0 {
		t.Fatal("disabled pacing must behave like before (bot opens instantly)")
	}
	if ok, _, _, _ := off.gate(); !ok {
		t.Fatal("disabled pacing must never rest")
	}
	var nilPacer *pacer
	if ok, _, _, _ := nilPacer.gate(); !ok || !nilPacer.botOpensFirst() {
		t.Fatal("nil pacer must be a no-op")
	}
}

func TestOpenerWaitAndDelays(t *testing.T) {
	p, _ := newTestPacer(testPacing())
	for i := 0; i < 200; i++ {
		if w := p.openerWait(); w < 45*time.Second || w > 120*time.Second {
			t.Fatalf("opener wait %s outside 45-120s", w)
		}
		if d := p.botOpenDelay(); d < 8*time.Second || d > 25*time.Second {
			t.Fatalf("bot open delay %s outside 8-25s", d)
		}
	}
	rng := rand.New(rand.NewSource(3))
	short := humanReplyDelay(rng, "ok")
	long := humanReplyDelay(rng, "this is a much longer reply that a person would need some seconds to type out fully")
	if short < 1500*time.Millisecond || short > 4*time.Second {
		t.Fatalf("short reply delay %s", short)
	}
	if long < short || long > 11*time.Second {
		t.Fatalf("long reply delay %s (short %s)", long, short)
	}
}

func TestWorkerPhaseRegistry(t *testing.T) {
	ClearAllWorkerPhases()
	SetWorkerPhase("a", PhaseActive)
	SetWorkerPhase("b", PhaseResting)
	SetWorkerPhase("c", PhaseLimited)
	SetWorkerPhase("", PhaseLimited) // ignored
	if got := RestingWorkerCount(); got != 2 {
		t.Fatalf("resting count %d, want 2", got)
	}
	ClearWorkerPhase("b")
	if got := RestingWorkerCount(); got != 1 {
		t.Fatalf("after clear: %d, want 1", got)
	}
	ClearAllWorkerPhases()
	if got := RestingWorkerCount(); got != 0 {
		t.Fatalf("after clear all: %d", got)
	}
}
