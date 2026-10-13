package apichat

import (
	"context"
	_ "embed"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

//go:embed assets/persona.txt
var defaultPersona string

//go:embed assets/snap_share.txt
var defaultSnapShare string

//go:embed assets/snap_hint.txt
var defaultSnapHint string

// DataFiles are the editable files seeded into data/api_chat/.
var DataFiles = map[string]string{
	"persona.txt":    defaultPersona,
	"snap_share.txt": defaultSnapShare,
	"snap_hint.txt":  defaultSnapHint,
	RulesFile:        defaultRulesJSON,
}

// Options is everything the dashboard passes to the engine.
type Options struct {
	BaseURL     string
	APIKey      string
	Model       string
	Timeout     time.Duration
	MaxTokens   int
	Temperature float64
	History     int // chat lines kept as model memory (0 = all)
	AgeMin      int
	AgeMax      int
	Countries   []string

	ShareHot       int
	ShareWarm      int
	ShareCold      int
	NoSharePercent int
	HintPercent    int
	NoShareMin     int
	NoShareMax     int
	NoShareStrict  bool
	AutoNoThink    bool

	// RulesPath, when set, is the config_rules.json to load instead of
	// <dataRoot>/config_rules.json (the bot passes FindProjectRules() so the
	// lab and the bot share cmd/apitest/config_rules.json).
	RulesPath string
}

// Defaults mirror apitest.config.example.txt.
func Defaults() Options {
	return Options{
		BaseURL: "https://api.featherless.ai/v1", Model: "deepseek-ai/DeepSeek-V3-0324",
		Timeout: 25 * time.Second, MaxTokens: 1024, Temperature: 0.9, History: 24,
		AgeMin: 19, AgeMax: 25,
		Countries: []string{"germany", "france", "norway", "sweden", "poland", "netherlands", "italy", "spain"},
		ShareHot:  4, ShareWarm: 6, ShareCold: 8,
		NoSharePercent: 30, HintPercent: 60, NoShareMin: 3, NoShareMax: 4,
		AutoNoThink: true,
	}
}

func (o Options) withDefaults() Options {
	d := Defaults()
	if strings.TrimSpace(o.BaseURL) == "" {
		o.BaseURL = d.BaseURL
	}
	o.BaseURL = strings.TrimRight(strings.TrimSpace(o.BaseURL), "/")
	if strings.TrimSpace(o.Model) == "" {
		o.Model = d.Model
	}
	if o.Timeout <= 0 {
		o.Timeout = d.Timeout
	}
	if o.MaxTokens <= 0 {
		o.MaxTokens = d.MaxTokens
	}
	if o.Temperature <= 0 {
		o.Temperature = d.Temperature
	}
	if o.History <= 0 {
		o.History = d.History
	}
	if o.AgeMin < 18 {
		o.AgeMin = d.AgeMin
	}
	if o.AgeMax < o.AgeMin {
		o.AgeMax = o.AgeMin
	}
	if len(o.Countries) == 0 {
		o.Countries = d.Countries
	}
	if o.ShareHot <= 0 {
		o.ShareHot = d.ShareHot
	}
	if o.ShareWarm <= 0 {
		o.ShareWarm = d.ShareWarm
	}
	if o.ShareCold <= 0 {
		o.ShareCold = d.ShareCold
	}
	if o.NoSharePercent < 0 || o.NoSharePercent > 100 {
		o.NoSharePercent = d.NoSharePercent
	}
	if o.HintPercent < 0 || o.HintPercent > 100 {
		o.HintPercent = d.HintPercent
	}
	if o.NoShareMin <= 0 {
		o.NoShareMin = d.NoShareMin
	}
	if o.NoShareMax < o.NoShareMin {
		o.NoShareMax = o.NoShareMin
	}
	return o
}

func (o Options) config() config {
	return config{
		BaseURL: o.BaseURL, Model: o.Model, APIKey: o.APIKey, Timeout: o.Timeout,
		MaxTokens: o.MaxTokens, Temperature: o.Temperature, History: o.History,
		AgeMin: o.AgeMin, AgeMax: o.AgeMax, Countries: o.Countries,
		HintPercent: o.HintPercent, NoSharePercent: o.NoSharePercent,
		NoShareMin: o.NoShareMin, NoShareMax: o.NoShareMax, NoShareStrict: o.NoShareStrict,
		ShareHot: o.ShareHot, ShareWarm: o.ShareWarm, ShareCold: o.ShareCold,
		AutoNoThink: o.AutoNoThink,
	}
}

// Result is one bot turn: the lines to send, in order, plus bookkeeping.
type Result struct {
	Lines     []string
	Turn      int    // bot reply number in this chat (0 = opener)
	Why       string // director reason (status line)
	Mood      string
	Status    string // apitest-style status line for the log
	ShareType string // "direct" / "hint" / "NO-SHARE (...)"
	HintNow   bool
	ShareNow  bool // the handle line is included in Lines
	Username  string
	Ended     bool
	EndReason string
	Fallback  bool  // the model call failed, a built-in line was used
	Err       error // last API error (also when a fallback covered it)
}

// Text returns the first line; Extra the follow-up lines.
func (r Result) Text() string {
	if len(r.Lines) == 0 {
		return ""
	}
	return r.Lines[0]
}

// Extra returns the lines after the first one.
func (r Result) Extra() []string {
	if len(r.Lines) <= 1 {
		return nil
	}
	return append([]string(nil), r.Lines[1:]...)
}

// convo is the in-memory state of one stranger (keyed by account+user id).
type convo struct {
	Age, Country, Name, Major, Year, Tonight, Show, Snack string
	System                                                string
	History                                               []message
	Turns                                                 int
	Director                                              *director
	SnapUser                                              string
	APIErrors                                             int
	LastUsed                                              time.Time
	ShareTurn, HintTurn                                   int
}

// Engine is shared by all workers; it is safe for concurrent use.
type Engine struct {
	opt     Options
	cfg     config
	persona string
	share   *shareDeck
	hint    *shareDeck
	Warns   []string
	// rulesFrom says where the funnel rules came from (file path or defaults).
	rulesFrom string

	mu     sync.Mutex
	convos map[string]*convo
}

const convoTTL = 3 * time.Hour

// New loads persona.txt / snap_share.txt / snap_hint.txt / config_rules.json
// from dataRoot (missing files fall back to the embedded defaults) and
// returns the engine. When config_rules.json exists it overrides the funnel
// fields of opt — the file is the single place those knobs are edited.
func New(dataRoot string, opt Options) (*Engine, error) {
	if strings.TrimSpace(opt.APIKey) == "" {
		return nil, fmt.Errorf("API Chat: the API key is empty")
	}
	var warns []string
	rulesPath := strings.TrimSpace(opt.RulesPath)
	if rulesPath == "" {
		rulesPath = join(dataRoot, RulesFile)
	}
	rules, rerr := LoadRules(rulesPath)
	if rerr == nil {
		opt = rules.Apply(opt)
	} else if !os.IsNotExist(rerr) {
		warns = append(warns, RulesFile+": "+rerr.Error()+" (using built-in rules)")
	}
	opt = opt.withDefaults()
	e := &Engine{opt: opt, cfg: opt.config(), convos: map[string]*convo{}, Warns: warns}
	e.rulesFrom = rulesSource(rulesPath, rerr)

	persona, err := loadPersona(join(dataRoot, "persona.txt"))
	if err != nil {
		e.Warns = append(e.Warns, "persona.txt: "+err.Error()+" (using built-in persona)")
		persona, _ = personaFromText(defaultPersona)
	}
	e.persona = persona
	e.share = e.loadDeck(join(dataRoot, "snap_share.txt"), true, defaultShareLines)
	e.hint = e.loadDeck(join(dataRoot, "snap_hint.txt"), false, defaultHintLines)
	return e, nil
}

func join(dir, name string) string { return filepath.Join(dir, name) }

// EnsureData creates dir and seeds the editable files that are missing
// (persona.txt, snap_share.txt, snap_hint.txt, config_rules.json). Existing
// files are kept. Names in skip are not seeded (the bot skips
// config_rules.json when the project copy in cmd/apitest is used).
func EnsureData(dir string, skip ...string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	for name, content := range DataFiles {
		skipped := false
		for _, s := range skip {
			if s == name {
				skipped = true
			}
		}
		if skipped {
			continue
		}
		path := filepath.Join(dir, name)
		if _, err := os.Stat(path); err == nil {
			continue
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			return err
		}
	}
	return nil
}

func (e *Engine) loadDeck(path string, needPlaceholder bool, fallback []string) *shareDeck {
	styles, warns, err := loadStyleFile(path, needPlaceholder)
	e.Warns = append(e.Warns, warns...)
	switch {
	case err != nil:
		e.Warns = append(e.Warns, fmt.Sprintf("%s not found - using %d built-in lines", path, len(fallback)))
		return newShareDeck(splitShareStyles(fallback), "built-in")
	case len(styles) == 0:
		e.Warns = append(e.Warns, fmt.Sprintf("%s has no usable lines - using %d built-in lines", path, len(fallback)))
		return newShareDeck(splitShareStyles(fallback), "built-in")
	}
	return newShareDeck(styles, filepath.Base(path))
}

// Describe is the one-line summary logged when the bot starts.
func (e *Engine) Describe() string {
	return fmt.Sprintf("API Chat: %s / %s | rules: %s (%s) | %d share styles (%s), %d hint lines (%s)",
		e.opt.BaseURL, e.opt.Model, e.Rules().String(), e.rulesFrom,
		e.share.count(), e.share.name(), e.hint.count(), e.hint.name())
}

// Rules returns the active funnel rules.
func (e *Engine) Rules() Rules {
	o := e.opt
	return Rules{ShareHot: o.ShareHot, ShareWarm: o.ShareWarm, ShareCold: o.ShareCold,
		NoSharePercent: o.NoSharePercent, NoShareMin: o.NoShareMin, NoShareMax: o.NoShareMax,
		NoShareStrict: o.NoShareStrict, HintPercent: o.HintPercent}
}

// Test sends one tiny request and returns the model's answer.
func (e *Engine) Test(ctx context.Context) (string, error) {
	cfg := e.cfg
	if deadline, ok := ctx.Deadline(); ok {
		if d := time.Until(deadline); d > 0 && d < cfg.Timeout {
			cfg.Timeout = d
		}
	}
	cfg.MaxTokens = 64
	reply, _, err := complete(cfg, []message{
		{Role: "system", Content: "reply with one short casual line, lowercase, like a text message."},
		{Role: "user", Content: "hey u there?"},
	})
	if err != nil {
		return "", err
	}
	lines, _ := sanitizeLines(reply, false)
	if len(lines) == 0 {
		return strings.TrimSpace(reply), nil
	}
	return lines[0], nil
}

// Forget drops the in-memory state of one stranger (chat closed / skipped).
func (e *Engine) Forget(key string) {
	e.mu.Lock()
	delete(e.convos, key)
	e.mu.Unlock()
}

// Known reports whether the engine still remembers this stranger.
func (e *Engine) Known(key string) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	_, ok := e.convos[key]
	return ok
}

func (e *Engine) get(key string, rng *rand.Rand, turnsSoFar int) *convo {
	e.mu.Lock()
	defer e.mu.Unlock()
	now := time.Now()
	for k, c := range e.convos {
		if now.Sub(c.LastUsed) > convoTTL {
			delete(e.convos, k)
		}
	}
	c, ok := e.convos[key]
	if !ok {
		c = e.newConvo(rng)
		// bot restarted mid-chat: keep the reply numbering so the funnel
		// does not start over from the greeting
		c.Turns = turnsSoFar
		e.convos[key] = c
	}
	c.LastUsed = now
	return c
}

func (e *Engine) newConvo(rng *rand.Rand) *convo {
	cfg := e.cfg
	c := &convo{
		Age:      fmt.Sprint(cfg.AgeMin + rng.Intn(cfg.AgeMax-cfg.AgeMin+1)),
		Country:  cfg.Countries[rng.Intn(len(cfg.Countries))],
		Director: newDirector(rng),
		LastUsed: time.Now(),
	}
	c.Director.roll(cfg)
	pick := func(xs []string) string { return xs[rng.Intn(len(xs))] }
	c.Name, c.Major, c.Year = pick(lorePool.Names), pick(lorePool.Majors), pick(lorePool.Years)
	c.Tonight, c.Show, c.Snack = pick(lorePool.Tonight), pick(lorePool.Shows), pick(lorePool.Snacks)
	sys := e.persona
	for k, v := range map[string]string{"{age}": c.Age, "{country}": c.Country, "{name}": c.Name, "{major}": c.Major, "{year}": c.Year, "{tonight}": c.Tonight, "{show}": c.Show, "{snack}": c.Snack} {
		sys = strings.ReplaceAll(sys, k, v)
	}
	c.System = sys
	return c
}

var openers = []string{"hey", "heyy", "hi", "hii", "hey u", "hey :)", "hi there", "heyy wyd", "hey, bored?", "hii how r u"}

// Start is the bot-first opener (pacing decided the bot speaks first).
// It costs no API call: a short casual hi from a small pool.
func (e *Engine) Start(key string, rng *rand.Rand) Result {
	c := e.get(key, rng, 0)
	line := openers[rng.Intn(len(openers))]
	c.History = append(c.History, message{Role: "assistant", Content: line})
	return Result{Lines: []string{line}, Turn: 0, Why: "opener", Mood: "friendly", ShareType: c.Director.shareType(), Status: "opener | type " + c.Director.shareType()}
}

// fallbackLines keep the bot talking when the model call fails.
var fallbackLines = []string{"lol wait what", "haha ok", "hmm ok", "lol same", "wait im confused lol", "haha ur funny", "ok ok lol", "true lol"}

// Reply handles one incoming stranger message. username is the snap handle
// assigned to this stranger (sticky per chat); it is only used on the share turn.
func (e *Engine) Reply(key, incoming, username string, rng *rand.Rand, turnsSoFar int) Result {
	c := e.get(key, rng, turnsSoFar)
	d := c.Director
	if username != "" {
		c.SnapUser = username
	}
	if d.ended {
		return Result{Ended: true, EndReason: d.endReason, Why: "already ended", ShareType: d.shareType()}
	}
	text := strings.TrimSpace(incoming)
	if text == "" {
		return Result{Why: "empty message", ShareType: d.shareType()}
	}
	d.observe(text)
	turn := c.Turns + 1
	p := d.plan(e.cfg, turn, text)
	c.History = append(c.History, message{Role: "user", Content: text})

	res := Result{Turn: turn, Why: p.Why, Mood: p.Mood, ShareType: d.shareType(), Username: c.SnapUser}
	var lines []string
	took := 0.0
	info := ""
	if p.Fixed != "" {
		lines = []string{p.Fixed}
	} else {
		msgs := e.buildMessages(c, p, turn)
		start := time.Now()
		reply, ci, err := complete(e.cfg, msgs)
		took = time.Since(start).Seconds()
		info = ci.Text
		if err != nil {
			res.Err = err
			res.Fallback = true
			c.APIErrors++
			if c.APIErrors >= 2 && !p.ShareNow && !p.HintNow {
				// second failure in one chat: leave politely instead of babbling
				d.ended, d.endReason = true, "api errors"
				res.Ended, res.EndReason = true, d.endReason
				lines = []string{"ugh my wifi is dying, ttyl"}
			} else {
				lines = []string{fallbackLines[rng.Intn(len(fallbackLines))]}
			}
		} else {
			var warn string
			lines, warn = sanitizeLines(reply, p.Double)
			if warn != "" {
				info += " | warn: " + warn
			}
			if problem := replyProblem(lines, d, text, c.History); problem != "" {
				fix := fmt.Sprintf("your previous draft was rejected: %s. rewrite it. their message was: %q. answer it first, keep the same mood and format, say something new.", problem, text)
				msgs = append(msgs, message{Role: "system", Content: fix})
				start2 := time.Now()
				reply2, ci2, err2 := complete(e.cfg, msgs)
				took += time.Since(start2).Seconds()
				if err2 == nil {
					info = ci2.Text + " | retry: " + problem
					lines, _ = sanitizeLines(reply2, p.Double)
				}
			}
			lines = polish(lines, c.History)
			if len(lines) == 0 {
				res.Fallback = true
				lines = []string{fallbackLines[rng.Intn(len(fallbackLines))]}
			}
		}
	}
	if p.HintNow {
		e.mu.Lock()
		hintLines := e.hint.next(rng, "")
		e.mu.Unlock()
		lines = append(lines, hintLines...)
		c.HintTurn = turn
		res.HintNow = true
	}
	if p.ShareNow {
		user := orDefault(c.SnapUser, "")
		if user == "" {
			// no handle configured: never send a broken line, close instead
			d.ended, d.endReason = true, "no username configured"
			res.Ended, res.EndReason = true, d.endReason
			lines = append(lines, "ugh gotta go, ttyl")
		} else {
			e.mu.Lock()
			shareLines := e.share.next(rng, user)
			e.mu.Unlock()
			lines = append(lines, shareLines...)
			if c.ShareTurn == 0 {
				c.ShareTurn = turn
			}
			res.ShareNow = true
		}
	}
	for _, line := range lines {
		c.History = append(c.History, message{Role: "assistant", Content: line})
	}
	c.Turns++
	if p.EndAfter || d.ended {
		res.Ended = true
		if res.EndReason == "" {
			res.EndReason = d.endReason
		}
	}
	res.Lines = lines
	status := fmt.Sprintf("[%.1fs%s] reply %d | mood %s | %s | %s", took, info, turn, p.Mood, p.Why, d.status(e.cfg, turn+1))
	if p.Double {
		status += " | double"
	}
	if res.Fallback {
		status += " | FALLBACK"
	}
	if res.Ended {
		status += " | END"
	}
	res.Status = status
	return res
}

func (e *Engine) buildMessages(c *convo, p plan, turn int) []message {
	msgs := []message{{Role: "system", Content: c.System}}
	h := c.History
	if e.cfg.History > 0 && len(h) > e.cfg.History {
		h = h[len(h)-e.cfg.History:]
	}
	msgs = append(msgs, h...)
	note := fmt.Sprintf("[director note for your reply #%d — follow it, never mention it]\nmood right now: %s (%s).\nwhat this message must do: %s\n", turn, p.Mood, moodHints[p.Mood], p.Instruction)
	if facts, dontAsk := c.Director.known(); facts != "" {
		note += fmt.Sprintf("what they already told you: %s. NEVER ask again for: %s. a real person remembers.\n", facts, strings.Join(dontAsk, ", "))
	}
	if p.Double {
		note += "format: exactly TWO short lines separated by a newline, each max 8 words.\n"
	} else {
		note += "format: ONE short line, max 10 words. no newline.\n"
	}
	note += "never leave their question unanswered. never ask two questions. never write a username or @."
	msgs = append(msgs, message{Role: "system", Content: note})
	return msgs
}

// personaFromText strips comments like loadPersona does, for the embedded copy.
func personaFromText(data string) (string, error) {
	var lines []string
	for _, line := range strings.Split(strings.ReplaceAll(data, "\r", ""), "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		lines = append(lines, strings.TrimRight(line, " "))
	}
	text := strings.TrimSpace(strings.Join(lines, "\n"))
	if text == "" {
		return "", fmt.Errorf("persona is empty")
	}
	return text, nil
}
