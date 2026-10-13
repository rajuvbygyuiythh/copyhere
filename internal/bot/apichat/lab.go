package apichat

// lab.go — the apitest console lab (interactive chat, ping, scenario and
// compare runs). It used to be a separate module (apitest/); it now lives in
// the main project and shares the director / persona / deck code in brain.go
// with the bot. Entry point: cmd/apitest -> LabMain().

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

var (
	shareStyles *shareDeck // loaded once in main from snap_share.txt (handle lines)
	hintStyles  *shareDeck // snap_hint.txt (👻 hints, no handle)
	snapNames   *shareDeck // snap_usernames.txt + SNAP_USERNAME (one handle per chat)
)

// nextSnapUser picks the handle for one chat: shuffled deck over all
// configured usernames so the same string is not in every conversation.
func nextSnapUser(cfg config, rng *rand.Rand) string {
	if snapNames != nil && snapNames.count() > 0 {
		return snapNames.next(rng, "")[0]
	}
	if len(cfg.SnapUsernames) > 0 {
		return cfg.SnapUsernames[rng.Intn(len(cfg.SnapUsernames))]
	}
	return orDefault(cfg.SnapUsername, "yoursnapname")
}

// initShareStyles loads snap_share.txt, snap_hint.txt and snap_usernames.txt
// next to the exe, falling back to the built-in lists with a warning.
func initShareStyles(dir string, cfg *config) {
	shareStyles = loadDeck(filepath.Join(dir, "snap_share.txt"), true, defaultShareLines)
	hintStyles = loadDeck(filepath.Join(dir, "snap_hint.txt"), false, defaultHintLines)

	// usernames: file lines + SNAP_USERNAME list, de-duplicated, "@" stripped
	seen := map[string]bool{}
	var names [][]string
	add := func(u string) {
		u = strings.TrimPrefix(strings.TrimSpace(u), "@")
		if u != "" && !seen[u] {
			seen[u] = true
			names = append(names, []string{u})
		}
	}
	if styles, _, err := loadStyleFile(filepath.Join(dir, "snap_usernames.txt"), false); err == nil {
		for _, st := range styles {
			add(strings.Join(st, "")) // a username never contains "|"
		}
	}
	for _, u := range cfg.SnapUsernames {
		add(u)
	}
	cfg.SnapUsernames = cfg.SnapUsernames[:0]
	for _, n := range names {
		cfg.SnapUsernames = append(cfg.SnapUsernames, n[0])
	}
	if len(names) > 0 {
		cfg.SnapUsername = names[0][0]
		snapNames = newShareDeck(names, "usernames")
	} else {
		fmt.Println("     WARN: no snap username set (SNAP_USERNAME= in apitest.config.txt or snap_usernames.txt) - using 'yoursnapname'")
	}
}

func loadDeck(path string, needPlaceholder bool, fallback []string) *shareDeck {
	styles, warns, err := loadStyleFile(path, needPlaceholder)
	for _, w := range warns {
		fmt.Println("     WARN: " + w)
	}
	name := filepath.Base(path)
	switch {
	case err != nil:
		fmt.Printf("     WARN: %s not found - using %d built-in lines\n", path, len(fallback))
		return newShareDeck(splitShareStyles(fallback), "built-in")
	case len(styles) == 0:
		fmt.Printf("     WARN: %s has no usable lines - using %d built-in lines\n", path, len(fallback))
		return newShareDeck(splitShareStyles(fallback), "built-in")
	}
	return newShareDeck(styles, name)
}

type session struct {
	Age      int
	Country  string
	Name     string
	Major    string
	Year     string
	Tonight  string
	Show     string
	Snack    string
	System   string
	History  []message
	Turns    int
	Director *director
	SnapUser string // the handle used in this chat (one per chat from the username deck)
}

func newSession(cfg config, personaTemplate string, rng *rand.Rand) *session {
	s := &session{
		Age:      cfg.AgeMin + rng.Intn(cfg.AgeMax-cfg.AgeMin+1),
		Country:  cfg.Countries[rng.Intn(len(cfg.Countries))],
		Director: newDirector(rng),
		SnapUser: nextSnapUser(cfg, rng),
	}
	s.Director.roll(cfg)
	pick := func(xs []string) string { return xs[rng.Intn(len(xs))] }
	s.Name, s.Major, s.Year = pick(lorePool.Names), pick(lorePool.Majors), pick(lorePool.Years)
	s.Tonight, s.Show, s.Snack = pick(lorePool.Tonight), pick(lorePool.Shows), pick(lorePool.Snacks)
	sys := strings.ReplaceAll(personaTemplate, "{age}", strconv.Itoa(s.Age))
	sys = strings.ReplaceAll(sys, "{country}", s.Country)
	for k, v := range map[string]string{"{name}": s.Name, "{major}": s.Major, "{year}": s.Year, "{tonight}": s.Tonight, "{show}": s.Show, "{snack}": s.Snack} {
		sys = strings.ReplaceAll(sys, k, v)
	}
	s.System = sys
	return s
}

func buildMessages(s *session, cfg config, p plan, turn int) []message {
	msgs := []message{{Role: "system", Content: s.System}}
	h := s.History
	if cfg.History > 0 && len(h) > cfg.History {
		h = h[len(h)-cfg.History:]
	}
	msgs = append(msgs, h...)
	note := fmt.Sprintf("[director note for your reply #%d — follow it, never mention it]\nmood right now: %s (%s).\nwhat this message must do: %s\n", turn, p.Mood, moodHints[p.Mood], p.Instruction)
	if facts, dontAsk := s.Director.known(); facts != "" {
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

// ------------------------------------------------------------------- main ---

// LabMain runs the console lab (os.Args: chat | ping | scenario <name|all> | compare <scenario>).
func LabMain() {
	dir := exeDir()
	cfg, err := loadConfig(filepath.Join(dir, "apitest.config.txt"))
	if err != nil {
		fatal(err)
	}
	// config_rules.json (same file format the bot uses in data/api_chat/)
	// overrides the SHARE_* / NO_SHARE_* / HINT_PERCENT keys when present.
	rulesPath := filepath.Join(dir, RulesFile)
	if rules, rerr := LoadRules(rulesPath); rerr == nil {
		rules.applyConfig(&cfg)
		fmt.Printf("rules: %s -> %s\n", RulesFile, rules.String())
	} else if !os.IsNotExist(rerr) {
		fmt.Printf("WARN %s: %v (using apitest.config.txt values)\n", RulesFile, rerr)
	}
	personaTemplate, err := loadPersona(filepath.Join(dir, "persona.txt"))
	if err != nil {
		fatal(err)
	}
	initShareStyles(dir, &cfg)
	mode := "chat"
	if len(os.Args) > 1 {
		mode = strings.ToLower(os.Args[1])
	}
	switch mode {
	case "ping":
		ping(cfg)
	case "chat":
		chatLoop(cfg, personaTemplate)
	case "compare":
		name := ""
		if len(os.Args) > 2 {
			name = strings.ToLower(os.Args[2])
		}
		compareRun(cfg, personaTemplate, name)
	case "scenario", "scenarios", "run":
		name := ""
		if len(os.Args) > 2 {
			name = strings.ToLower(os.Args[2])
		}
		scenarioRun(cfg, personaTemplate, name)
	default:
		fmt.Println("usage: apitest.exe [chat|ping|scenario <name|all>|compare <scenario>]")
		os.Exit(2)
	}
}

type stats struct {
	Replies, Lines, Words, Retries, Warns, Doubles int
	PromptTok, ComplTok                            int
	Seconds                                        float64
	ShareTurn                                      int
	ShareStyle                                     string
	HintTurn                                       int
	HintStyle                                      string
}

type runner struct {
	cfg     config
	persona string
	rng     *rand.Rand
	s       *session
	st      stats
	logf    *os.File
}

func newRunner(cfg config, personaTemplate string) *runner {
	r := &runner{cfg: cfg, persona: personaTemplate, rng: rand.New(rand.NewSource(time.Now().UnixNano()))}
	r.s = newSession(cfg, personaTemplate, r.rng)
	if f, err := os.OpenFile(filepath.Join(exeDir(), "apitest.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644); err == nil {
		r.logf = f
	}
	r.logLine("=== new chat %s | model %s | persona f %d %s name %s ===", time.Now().Format("2006-01-02 15:04:05"), cfg.Model, r.s.Age, r.s.Country, r.s.Name)
	return r
}

func (r *runner) close() {
	if r.logf != nil {
		r.logf.Close()
		r.logf = nil
	}
}

func (r *runner) logLine(format string, a ...any) {
	if r.logf != nil {
		fmt.Fprintf(r.logf, format+"\n", a...)
	}
}

func (r *runner) reset() {
	r.summary()
	r.s = newSession(r.cfg, r.persona, r.rng)
	r.st = stats{}
	r.logLine("=== new chat %s | persona f %d %s name %s ===", time.Now().Format("15:04:05"), r.s.Age, r.s.Country, r.s.Name)
}

func (r *runner) summary() {
	if r.st.Replies == 0 {
		return
	}
	share := "not shared"
	if r.st.ShareTurn > 0 {
		share = fmt.Sprintf("shared on reply %d as %s (%q)", r.st.ShareTurn, r.s.SnapUser, r.st.ShareStyle)
	}
	if r.st.HintTurn > 0 {
		share += fmt.Sprintf(" | hint on reply %d (%q)", r.st.HintTurn, r.st.HintStyle)
	}
	share += " | type " + r.s.Director.shareType()
	avgW := float64(r.st.Words) / float64(max(r.st.Lines, 1))
	line := fmt.Sprintf("--- summary: %d replies | %s | tier %s | retries %d | warns %d | doubles %d | avg %.1f words/line | %.1fs avg | %d+%d tokens | %s ---",
		r.st.Replies, share, r.s.Director.tier, r.st.Retries, r.st.Warns, r.st.Doubles, avgW, r.st.Seconds/float64(r.st.Replies), r.st.PromptTok, r.st.ComplTok, r.s.Director.status(r.cfg, r.s.Turns+1))
	fmt.Println(line)
	r.logLine("%s", line)
}

// step handles one stranger message end to end and prints the bot reply.
func (r *runner) step(text string) {
	s, cfg, d := r.s, r.cfg, r.s.Director
	if d.ended {
		fmt.Println("     (chat ended: " + d.endReason + " — the bot would not reply anymore. /new for a new stranger)")
		return
	}
	d.observe(text)
	turn := s.Turns + 1
	p := d.plan(cfg, turn, text)
	s.History = append(s.History, message{Role: "user", Content: text})
	r.logLine("you> %s", text)

	var lines []string
	info := ""
	took := 0.0
	retries := 0
	if p.Fixed != "" {
		lines = []string{p.Fixed}
	} else {
		msgs := buildMessages(s, cfg, p, turn)
		if cfg.ShowPrompt {
			fmt.Println("--- request messages ---")
			for _, m := range msgs {
				fmt.Printf("[%s] %s\n", m.Role, m.Content)
			}
			fmt.Println("------------------------")
		}
		start := time.Now()
		reply, ci, err := complete(cfg, msgs)
		took = time.Since(start).Seconds()
		info = ci.Text
		r.st.PromptTok += ci.Prompt
		r.st.ComplTok += ci.Compl
		if err != nil {
			fmt.Printf("eva> (no reply)\n     ERROR after %.1fs: %v\n", took, err)
			fmt.Printf("     plan was: mood=%s double=%v share=%v | %s\n", p.Mood, p.Double, p.ShareNow, p.Instruction)
			r.logLine("ERROR: %v", err)
			return
		}
		var warn string
		lines, warn = sanitizeLines(reply, p.Double)
		if warn != "" {
			r.st.Warns++
			fmt.Printf("     WARN: %s\n     raw: %q\n", warn, strings.TrimSpace(reply))
			r.logLine("WARN: %s | raw %q", warn, strings.TrimSpace(reply))
		}
		if problem := replyProblem(lines, d, text, s.History); problem != "" {
			retries++
			r.st.Retries++
			fmt.Printf("     RETRY: %s -> asking the model once more\n     first try: %q\n", problem, strings.Join(lines, " / "))
			r.logLine("RETRY: %s | first try %q", problem, strings.Join(lines, " / "))
			fix := fmt.Sprintf("your previous draft was rejected: %s. rewrite it. their message was: %q. answer it first, keep the same mood and format, say something new.", problem, text)
			msgs = append(msgs, message{Role: "system", Content: fix})
			start2 := time.Now()
			reply2, ci2, err2 := complete(cfg, msgs)
			took += time.Since(start2).Seconds()
			r.st.PromptTok += ci2.Prompt
			r.st.ComplTok += ci2.Compl
			if err2 == nil {
				info = ci2.Text
				lines, warn = sanitizeLines(reply2, p.Double)
				if warn != "" {
					fmt.Printf("     WARN: %s\n     raw: %q\n", warn, strings.TrimSpace(reply2))
				}
				if problem2 := replyProblem(lines, d, text, s.History); problem2 != "" {
					fmt.Printf("     STILL: %s (kept anyway — the real bot would fall back to a TXT line)\n", problem2)
					r.logLine("STILL: %s", problem2)
				}
			}
		}
		lines = polish(lines, s.History)
	}
	if p.HintNow {
		hintLines := hintStyles.next(r.rng, "")
		lines = append(lines, hintLines...)
		r.st.HintTurn = turn
		r.st.HintStyle = strings.Join(hintLines, " | ")
	}
	if p.ShareNow {
		shareLines := shareStyles.next(r.rng, r.s.SnapUser)
		lines = append(lines, shareLines...)
		if r.st.ShareTurn == 0 {
			r.st.ShareTurn = turn
			r.st.ShareStyle = strings.Join(shareLines, " | ")
		}
	}
	for _, line := range lines {
		fmt.Printf("eva> %s\n", line)
		s.History = append(s.History, message{Role: "assistant", Content: line})
		r.st.Lines++
		r.st.Words += len(strings.Fields(line))
		r.logLine("eva> %s", line)
	}
	s.Turns++
	r.st.Replies++
	r.st.Seconds += took
	if p.Double {
		r.st.Doubles++
	}
	status := fmt.Sprintf("[%.1fs%s] reply %d | mood %s | %s | %s", took, info, turn, p.Mood, p.Why, d.status(cfg, turn+1))
	if p.Double {
		status += " | double"
	}
	if p.EndAfter {
		status += " | END"
	}
	fmt.Println("     " + status)
	r.logLine("     %s", status)
}

func chatLoop(cfg config, personaTemplate string) {
	r := newRunner(cfg, personaTemplate)
	defer r.close()
	defer r.summary()
	printBanner(cfg, r.s)

	in := bufio.NewScanner(os.Stdin)
	in.Buffer(make([]byte, 0, 64*1024), 64*1024)
	for {
		fmt.Print("you> ")
		if !in.Scan() {
			return
		}
		text := strings.TrimSpace(in.Text())
		if text == "" {
			continue
		}
		s := r.s
		switch strings.ToLower(text) {
		case "/quit", "/exit", "/q":
			return
		case "/new":
			r.reset()
			r.s.Director.roll(r.cfg)
			fmt.Printf("--- new chat: persona f %d from %s (%s) | type %s | moods: %s ---\n", r.s.Age, r.s.Country, r.s.Name, r.s.Director.shareType(), strings.Join(r.s.Director.moods, " > "))
			continue
		case "/prompt":
			r.cfg.ShowPrompt = !r.cfg.ShowPrompt
			fmt.Printf("--- show prompt: %v ---\n", r.cfg.ShowPrompt)
			continue
		case "/system":
			fmt.Println("--- system prompt ---\n" + s.System + "\n---------------------")
			continue
		case "/state":
			facts, _ := s.Director.known()
			fmt.Printf("--- %s | knows: %q | horny %d flirty %d | turn %d | me: %s, %s %s, %s ---\n", s.Director.status(cfg, s.Turns+1), facts, s.Director.hornyHits, s.Director.flirtyHits, s.Turns, s.Name, s.Year, s.Major, s.Tonight)
			continue
		case "/history":
			for i, m := range s.History {
				fmt.Printf("%2d %-9s %s\n", i+1, m.Role+":", m.Content)
			}
			continue
		case "/summary":
			r.summary()
			continue
		case "/share", "/snap":
			fmt.Printf("--- this chat: type %s, username %s ---\n", s.Director.shareType(), s.SnapUser)
			fmt.Printf("--- %d share styles from %s, 5 previews (deck order, no repeats until all used) ---\n", shareStyles.count(), shareStyles.name())
			for i := 0; i < 5; i++ {
				fmt.Printf("  %s\n", strings.Join(shareStyles.next(r.rng, s.SnapUser), "  ||  "))
			}
			fmt.Printf("--- %d hint styles from %s, 5 previews ---\n", hintStyles.count(), hintStyles.name())
			for i := 0; i < 5; i++ {
				fmt.Printf("  %s\n", strings.Join(hintStyles.next(r.rng, ""), "  ||  "))
			}
			continue
		case "/help":
			printHelp()
			continue
		}
		r.step(text)
	}
}

// scenarioRun feeds scripted stranger lines (scenarios.txt) through the same pipeline.
func scenarioRun(cfg config, personaTemplate string, name string) {
	scen, err := loadScenarios(filepath.Join(exeDir(), "scenarios.txt"))
	if err != nil {
		fatal(err)
	}
	var names []string
	if name == "" || name == "all" {
		for n := range scen {
			names = append(names, n)
		}
		sort.Strings(names)
	} else {
		if _, ok := scen[name]; !ok {
			var have []string
			for n := range scen {
				have = append(have, n)
			}
			sort.Strings(have)
			fatal(fmt.Errorf("scenario %q not in scenarios.txt (have: %s)", name, strings.Join(have, ", ")))
		}
		names = []string{name}
	}
	r := newRunner(cfg, personaTemplate)
	for i, n := range names {
		if i > 0 {
			r.reset()
		}
		forceShareType(r.s.Director, r.cfg, n)
		fmt.Printf("\n################ scenario: %s  (persona f %d %s, name %s, type %s) ################\n", n, r.s.Age, r.s.Country, r.s.Name, r.s.Director.shareType())
		r.logLine("### scenario %s (type %s)", n, r.s.Director.shareType())
		for _, line := range scen[n] {
			if r.s.Director.ended {
				fmt.Printf("you> %s\n     (chat already ended: %s)\n", line, r.s.Director.endReason)
				break
			}
			fmt.Printf("you> %s\n", line)
			r.step(line)
		}
	}
	r.summary()
	r.close()
	fmt.Println("\n(full transcript appended to apitest.log — send that file for review)")
}

// forceShareType makes scenario runs deterministic: "hint*" scenarios play the
// hint type, "noshare*" the no-share type, everything else the direct share.
func forceShareType(d *director, cfg config, scenario string) {
	d.roll(cfg)
	d.noShare, d.hintMode = false, false
	switch {
	case strings.HasPrefix(scenario, "hint"):
		d.hintMode = true
	case strings.HasPrefix(scenario, "noshare"):
		d.noShare = true
	}
}

// compareRun plays ONE scenario on every model in COMPARE_MODELS and prints a table.
func compareRun(cfg config, personaTemplate string, name string) {
	if len(cfg.Models) == 0 {
		fatal(errors.New("set COMPARE_MODELS=modelA,modelB,... in apitest.config.txt first"))
	}
	if name == "" {
		name = "warm"
	}
	scen, err := loadScenarios(filepath.Join(exeDir(), "scenarios.txt"))
	if err != nil {
		fatal(err)
	}
	lines, ok := scen[name]
	if !ok {
		fatal(fmt.Errorf("scenario %q not in scenarios.txt", name))
	}
	type row struct {
		model string
		st    stats
		errs  int
		end   string
	}
	var rows []row
	for _, m := range cfg.Models {
		c := cfg
		c.Model = m
		r := newRunner(c, personaTemplate)
		forceShareType(r.s.Director, c, name)
		fmt.Printf("\n################ model: %s | scenario: %s | type %s ################\n", m, name, r.s.Director.shareType())
		r.logLine("### compare model %s scenario %s", m, name)
		errs := 0
		for _, line := range lines {
			if r.s.Director.ended {
				break
			}
			fmt.Printf("you> %s\n", line)
			before := r.st.Replies
			r.step(line)
			if r.st.Replies == before {
				errs++
				if errs >= 2 {
					fmt.Println("     (two errors in a row - skipping this model)")
					break
				}
			}
		}
		r.summary()
		r.close()
		rows = append(rows, row{m, r.st, errs, r.s.Director.status(c, r.s.Turns+1)})
	}
	fmt.Println("\n================ compare: scenario " + name + " ================")
	fmt.Printf("%-45s %7s %6s %7s %5s %9s %7s %8s  %s\n", "model", "replies", "share", "retries", "warns", "words/ln", "avg s", "tokens", "end state")
	for _, r := range rows {
		share := "-"
		if r.st.ShareTurn > 0 {
			share = strconv.Itoa(r.st.ShareTurn)
		}
		avgW, avgS := 0.0, 0.0
		if r.st.Lines > 0 {
			avgW = float64(r.st.Words) / float64(r.st.Lines)
		}
		if r.st.Replies > 0 {
			avgS = r.st.Seconds / float64(r.st.Replies)
		}
		state := r.end
		if r.errs > 0 {
			state = fmt.Sprintf("%d errors | %s", r.errs, state)
		}
		fmt.Printf("%-45s %7d %6s %7d %5d %9.1f %7.1f %8d  %s\n", trim(r.model, 45), r.st.Replies, share, r.st.Retries, r.st.Warns, avgW, avgS, r.st.PromptTok+r.st.ComplTok, trim(state, 40))
	}
	fmt.Println("lower retries/warns + short words/ln + share on the expected reply = better model for this job. transcripts in apitest.log")
}

func loadScenarios(path string) (map[string][]string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("scenarios.txt missing next to apitest.exe: %w", err)
	}
	out := map[string][]string{}
	cur := ""
	for _, raw := range strings.Split(string(b), "\n") {
		l := strings.TrimSpace(raw)
		if l == "" || strings.HasPrefix(l, "#") {
			continue
		}
		if strings.HasPrefix(l, "[") && strings.HasSuffix(l, "]") {
			cur = strings.ToLower(strings.TrimSpace(l[1 : len(l)-1]))
			continue
		}
		if cur != "" {
			out[cur] = append(out[cur], l)
		}
	}
	if len(out) == 0 {
		return nil, errors.New("scenarios.txt has no [sections]")
	}
	return out, nil
}

// ---------------------------------------------------------------- API ---

func noThinkLabel(cfg config) string {
	if !cfg.AutoNoThink {
		return "off (AUTO_NOTHINK=0)"
	}
	f := noThinkFields(cfg.Model)
	if f == nil {
		return "not needed for this model"
	}
	b, _ := json.Marshal(f)
	return "auto " + string(b)
}

func ping(cfg config) {
	fmt.Printf("POST %s/chat/completions  model=%s\n", cfg.BaseURL, cfg.Model)
	start := time.Now()
	status, body, err := rawCall(cfg, []message{
		{Role: "system", Content: "reply with exactly: pong"},
		{Role: "user", Content: "ping"},
	}, cfg.MaxTokens)
	fmt.Printf("took %.1fs\n", time.Since(start).Seconds())
	if err != nil {
		fmt.Println("ERROR:", err)
		os.Exit(1)
	}
	fmt.Println("HTTP", status)
	fmt.Println(prettyJSON(body))
	var parsed chatResponse
	if json.Unmarshal(body, &parsed) == nil && len(parsed.Choices) > 0 {
		said := strings.TrimSpace(parsed.Choices[0].Message.Content)
		fmt.Printf("\nmodel said: %q\n", said)
		if said == "" {
			fmt.Println("RESULT: API OK but EMPTY reply -> reasoning model used all tokens thinking.")
			fmt.Println("        -> set MAX_TOKENS=1024 or EXTRA_JSON to disable thinking, or pick a non-reasoning model")
			return
		}
		fmt.Println("RESULT: API OK")
		return
	}
	if status >= 400 {
		msg := "see body above"
		if parsed.Error != nil && parsed.Error.Message != "" {
			msg = parsed.Error.Message
		}
		fmt.Printf("RESULT: API ERROR HTTP %d - %s\n", status, msg)
		switch status {
		case 401, 403:
			fmt.Println("        -> API_KEY wrong / missing (apitest.config.txt)")
		case 404:
			fmt.Println("        -> BASE_URL or MODEL id wrong")
		case 429:
			fmt.Println("        -> rate / concurrency limit of your plan - wait and retry")
		}
		os.Exit(1)
	}
	fmt.Println("RESULT: API answered but no choices -> check model id")
	os.Exit(1)
}

// ------------------------------------------------------------- sanitize ---

func loadConfig(path string) (config, error) {
	cfg := config{
		AutoNoThink: true,
		BaseURL:     "https://api.featherless.ai/v1",
		Model:       "deepseek-ai/DeepSeek-V3-0324",
		Timeout:     60 * time.Second,
		MaxTokens:   1024,
		Temperature: 0.9,
		History:     30,
		AgeMin:      20,
		AgeMax:      25,
		Countries:   []string{"uk", "germany", "france", "italy", "spain", "netherlands", "sweden", "poland", "ireland", "norway"},
		ShareHot:    4,
		HintPercent: 60, NoSharePercent: 30, NoShareMin: 3, NoShareMax: 4,
		ShareWarm: 6,
		ShareCold: 8,
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return cfg, fmt.Errorf("%s not found (apitest.bat creates it) - fill in API_KEY first", filepath.Base(path))
		}
		return cfg, err
	}
	for _, line := range strings.Split(strings.ReplaceAll(string(data), "\r", ""), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") || !strings.Contains(line, "=") {
			continue
		}
		key, value, _ := strings.Cut(line, "=")
		key = strings.ToUpper(strings.TrimSpace(key))
		value = strings.TrimSpace(value)
		intVal := func(min int) (int, bool) {
			n, err := strconv.Atoi(value)
			return n, err == nil && n >= min
		}
		switch key {
		case "BASE_URL":
			cfg.BaseURL = strings.TrimRight(value, "/")
		case "MODEL":
			cfg.Model = value
		case "API_KEY":
			cfg.APIKey = value
		case "SNAP_USERNAME", "SNAP_USERNAMES":
			for _, u := range strings.Split(value, ",") {
				if u = strings.TrimPrefix(strings.TrimSpace(u), "@"); u != "" {
					cfg.SnapUsernames = append(cfg.SnapUsernames, u)
				}
			}
			if len(cfg.SnapUsernames) > 0 {
				cfg.SnapUsername = cfg.SnapUsernames[0]
			}
		case "HINT_PERCENT":
			if n, ok := intVal(0); ok && n <= 100 {
				cfg.HintPercent = n
			}
		case "NO_SHARE_PERCENT":
			if n, ok := intVal(0); ok && n <= 100 {
				cfg.NoSharePercent = n
			}
		case "NO_SHARE_MIN_REPLIES":
			if n, ok := intVal(1); ok {
				cfg.NoShareMin = n
			}
		case "NO_SHARE_MAX_REPLIES":
			if n, ok := intVal(1); ok {
				cfg.NoShareMax = n
			}
		case "NO_SHARE_STRICT":
			cfg.NoShareStrict = value == "1" || strings.EqualFold(value, "true")
		case "TIMEOUT_SEC":
			if n, ok := intVal(1); ok {
				cfg.Timeout = time.Duration(n) * time.Second
			}
		case "MAX_TOKENS":
			if n, ok := intVal(1); ok {
				cfg.MaxTokens = n
			}
		case "TEMPERATURE":
			if f, err := strconv.ParseFloat(value, 64); err == nil {
				cfg.Temperature = f
			}
		case "HISTORY":
			if n, ok := intVal(0); ok {
				cfg.History = n
			}
		case "AGE_MIN":
			if n, ok := intVal(18); ok {
				cfg.AgeMin = n
			}
		case "AGE_MAX":
			if n, ok := intVal(18); ok {
				cfg.AgeMax = n
			}
		case "SHARE_HOT":
			if n, ok := intVal(2); ok {
				cfg.ShareHot = n
			}
		case "SHARE_WARM":
			if n, ok := intVal(2); ok {
				cfg.ShareWarm = n
			}
		case "SHARE_COLD":
			if n, ok := intVal(2); ok {
				cfg.ShareCold = n
			}
		case "EXTRA_JSON":
			var extra map[string]any
			if err := json.Unmarshal([]byte(value), &extra); err != nil {
				return cfg, fmt.Errorf("EXTRA_JSON is not valid JSON: %v", err)
			}
			cfg.ExtraJSON = extra
		case "AUTO_NOTHINK":
			cfg.AutoNoThink = value != "0" && !strings.EqualFold(value, "false") && !strings.EqualFold(value, "off")
		case "COMPARE_MODELS", "MODELS":
			for _, m := range strings.Split(value, ",") {
				if m = strings.TrimSpace(m); m != "" {
					cfg.Models = append(cfg.Models, m)
				}
			}
		case "COUNTRIES":
			var list []string
			for _, c := range strings.Split(value, ",") {
				if c = strings.TrimSpace(strings.ToLower(c)); c != "" {
					list = append(list, c)
				}
			}
			if len(list) > 0 {
				cfg.Countries = list
			}
		}
	}
	if cfg.AgeMax < cfg.AgeMin {
		cfg.AgeMax = cfg.AgeMin
	}
	if cfg.APIKey == "" || strings.Contains(cfg.APIKey, "your-") {
		return cfg, errors.New("API_KEY is empty in apitest.config.txt - paste your key and run again")
	}
	if cfg.Model == "" || cfg.BaseURL == "" {
		return cfg, errors.New("MODEL / BASE_URL must not be empty")
	}
	return cfg, nil
}

func printBanner(cfg config, s *session) {
	fmt.Println("==========================================================")
	fmt.Println(" apitest - chat with the AI persona (you are the stranger)")
	fmt.Printf(" api:     %s\n model:   %s\n thinking-off: %s\n", cfg.BaseURL, cfg.Model, noThinkLabel(cfg))
	fmt.Printf(" persona: f %d from %s, name %s (only told if asked)   (persona.txt, /new = re-roll)\n", s.Age, s.Country, s.Name)
	fmt.Printf(" snap at: HOT reply %d | WARM %d | COLD %d   username: %s\n", cfg.ShareHot, cfg.ShareWarm, cfg.ShareCold, orDefault(cfg.SnapUsername, "(set SNAP_USERNAME)"))
	fmt.Printf(" styles:  %d share lines (%s) | %d hint lines (%s) | %d usernames   (/share = preview)\n", shareStyles.count(), shareStyles.name(), hintStyles.count(), hintStyles.name(), max(len(cfg.SnapUsernames), 1))
	fmt.Printf(" mix:     %d%% chats NO snap (close after %d-%d replies) | of the rest %d%% hint first, %d%% direct\n", cfg.NoSharePercent, cfg.NoShareMin, cfg.NoShareMax, cfg.HintPercent, 100-cfg.HintPercent)
	fmt.Printf(" moods:   %s\n", strings.Join(s.Director.moods, " > "))
	fmt.Println(" type a message, or /help for commands")
	fmt.Println("==========================================================")
}

func printHelp() {
	fmt.Println(" /new      new chat with a new random persona + mood order")
	fmt.Println(" /state    interest tier / score / when the snap will be shared")
	fmt.Println(" /system   show the current system prompt")
	fmt.Println(" /prompt   toggle printing the full request (incl. director note) before each call")
	fmt.Println(" /history  show the conversation memory sent to the model")
	fmt.Println(" /summary  stats of this chat so far (retries, tokens, share turn)")
	fmt.Println(" /share    this chat's share type + previews from snap_share.txt / snap_hint.txt")
	fmt.Println(" /quit     exit")
}

func exeDir() string {
	if exe, err := os.Executable(); err == nil {
		return filepath.Dir(exe)
	}
	wd, _ := os.Getwd()
	return wd
}

func prettyJSON(b []byte) string {
	var out bytes.Buffer
	if json.Indent(&out, b, "", "  ") != nil {
		return trim(string(b), 2000)
	}
	return trim(out.String(), 4000)
}

func fatal(err error) {
	fmt.Println("ERROR:", err)
	os.Exit(1)
}
