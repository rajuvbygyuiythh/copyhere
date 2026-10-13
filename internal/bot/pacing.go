package bot

import (
	"errors"
	"math/rand"
	"sync"
	"time"
)

// Human pacing for matched chats.
//
// Two problems this solves:
//
//  1. ChitChat throttles an account after roughly 40-60 minutes of non-stop
//     chatting. The pacer runs every worker in shifts (active window -> rest
//     window, both randomised per account) and caps matched chats per rolling
//     hour. A 429 / limit response puts the account into an immediate cooldown
//     instead of counting towards "dead".
//  2. A bot that opens every single match within a second is the loudest bot
//     signal there is. The opener policy makes the *user* speak first most of
//     the time (default 70%); the bot only replies. When the bot does open it
//     waits a human delay first. A silent user is skipped (next match).
//
// Resting only pauses NEW matches - DM replies keep flowing.

// Pacing is the per-worker configuration (copied from GeneralSettings).
type Pacing struct {
	Enabled         bool
	BotFirstPercent int
	OpenerWaitMin   time.Duration
	OpenerWaitMax   time.Duration
	BotOpenDelayMin time.Duration
	BotOpenDelayMax time.Duration
	ActiveMin       time.Duration
	ActiveMax       time.Duration
	RestMin         time.Duration
	RestMax         time.Duration
	ChatsPerHourMax int
	LimitCooldown   time.Duration
}

// errRateLimited is returned by API helpers when the platform answers 429.
var errRateLimited = errors.New("rate limited by platform")

// Worker phases exported to the dashboard / Live Wall.
const (
	PhaseActive  = "active"
	PhaseResting = "resting"
	PhaseLimited = "limited"
)

var workerPhases sync.Map // profile -> phase string

// SetWorkerPhase records the current phase of a worker (profile).
func SetWorkerPhase(profile string, phase string) {
	if profile == "" {
		return
	}
	workerPhases.Store(profile, phase)
}

// ClearWorkerPhase forgets a worker (called when it stops).
func ClearWorkerPhase(profile string) {
	workerPhases.Delete(profile)
}

// ClearAllWorkerPhases forgets every worker (bot stopped).
func ClearAllWorkerPhases() {
	workerPhases.Range(func(key, _ any) bool {
		workerPhases.Delete(key)
		return true
	})
}

// RestingWorkerCount returns how many workers are resting or limited.
func RestingWorkerCount() int {
	count := 0
	workerPhases.Range(func(_, value any) bool {
		if phase, _ := value.(string); phase == PhaseResting || phase == PhaseLimited {
			count++
		}
		return true
	})
	return count
}

// pacer is the per-worker shift / budget state (in memory; a restart simply
// begins a fresh shift, which is what a human reopening the app looks like).
type pacer struct {
	mu          sync.Mutex
	cfg         Pacing
	rng         *rand.Rand
	activeUntil time.Time
	restUntil   time.Time
	limitUntil  time.Time
	chatTimes   []time.Time
	now         func() time.Time
}

func newPacer(cfg Pacing, rng *rand.Rand) *pacer {
	p := &pacer{cfg: cfg, rng: rng, now: time.Now}
	p.activeUntil = p.now().Add(randDuration(rng, cfg.ActiveMin, cfg.ActiveMax))
	return p
}

func randDuration(rng *rand.Rand, lo, hi time.Duration) time.Duration {
	if hi < lo {
		hi = lo
	}
	if hi <= lo || rng == nil {
		return lo
	}
	return lo + time.Duration(rng.Int63n(int64(hi-lo)))
}

// gate decides whether the worker may look for a NEW match right now.
// It returns (allowed, wait-until, phase, reason).
func (p *pacer) gate() (bool, time.Time, string, string) {
	if p == nil || !p.cfg.Enabled {
		return true, time.Time{}, PhaseActive, ""
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	now := p.now()

	if now.Before(p.limitUntil) {
		return false, p.limitUntil, PhaseLimited, "platform limit cooldown"
	}
	if now.Before(p.restUntil) {
		return false, p.restUntil, PhaseResting, "shift rest"
	}
	if !p.restUntil.IsZero() && !now.Before(p.restUntil) {
		// rest finished -> new active window
		p.restUntil = time.Time{}
		p.activeUntil = now.Add(randDuration(p.rng, p.cfg.ActiveMin, p.cfg.ActiveMax))
	}
	if !now.Before(p.activeUntil) {
		p.restUntil = now.Add(randDuration(p.rng, p.cfg.RestMin, p.cfg.RestMax))
		return false, p.restUntil, PhaseResting, "shift finished"
	}

	// rolling-hour chat budget
	p.pruneLocked(now)
	if p.cfg.ChatsPerHourMax > 0 && len(p.chatTimes) >= p.cfg.ChatsPerHourMax {
		until := p.chatTimes[0].Add(time.Hour)
		return false, until, PhaseResting, "hourly chat budget reached"
	}
	return true, time.Time{}, PhaseActive, ""
}

func (p *pacer) pruneLocked(now time.Time) {
	cut := now.Add(-time.Hour)
	i := 0
	for i < len(p.chatTimes) && p.chatTimes[i].Before(cut) {
		i++
	}
	if i > 0 {
		p.chatTimes = append([]time.Time(nil), p.chatTimes[i:]...)
	}
}

// noteChat records one matched chat against the hourly budget.
func (p *pacer) noteChat() {
	if p == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.chatTimes = append(p.chatTimes, p.now())
}

// noteLimited starts (or extends) the platform-limit cooldown.
func (p *pacer) noteLimited() time.Time {
	if p == nil {
		return time.Time{}
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	cooldown := p.cfg.LimitCooldown
	if cooldown <= 0 {
		cooldown = 20 * time.Minute
	}
	// jitter 100-150% so limited accounts do not all wake together
	cooldown += time.Duration(p.rng.Int63n(int64(cooldown/2) + 1))
	until := p.now().Add(cooldown)
	if until.After(p.limitUntil) {
		p.limitUntil = until
	}
	return p.limitUntil
}

// botOpensFirst rolls the opener dice for one match.
func (p *pacer) botOpensFirst() bool {
	if p == nil || !p.cfg.Enabled {
		return true
	}
	if p.cfg.BotFirstPercent >= 100 {
		return true
	}
	if p.cfg.BotFirstPercent <= 0 {
		return false
	}
	return p.rng.Intn(100) < p.cfg.BotFirstPercent
}

func (p *pacer) openerWait() time.Duration {
	return randDuration(p.rng, p.cfg.OpenerWaitMin, p.cfg.OpenerWaitMax)
}

func (p *pacer) botOpenDelay() time.Duration {
	if p == nil || !p.cfg.Enabled {
		return 0
	}
	return randDuration(p.rng, p.cfg.BotOpenDelayMin, p.cfg.BotOpenDelayMax)
}

// humanReplyDelay is used when the dashboard reply delay is 0: a short
// read-and-type pause that grows with the reply length (capped), so replies
// never land in the same instant the user's message arrived.
func humanReplyDelay(rng *rand.Rand, reply string) time.Duration {
	base := 1500 * time.Millisecond
	perChar := 55 * time.Millisecond
	d := base + time.Duration(len(reply))*perChar
	if d > 9*time.Second {
		d = 9 * time.Second
	}
	jitter := time.Duration(rng.Int63n(int64(1200 * time.Millisecond)))
	return d + jitter
}
