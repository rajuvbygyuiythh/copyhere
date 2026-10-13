// apitest — standalone console chat tester for an OpenAI-compatible API
// (Featherless, OpenAI, Groq, OpenRouter, Gemini, DeepSeek ...).
//
// It is deliberately NOT connected to the main ChitChat Bot code: one file,
// no dependencies, its own go.mod. It contains the "director": a small,
// deterministic funnel brain that reads the stranger's interest and tells the
// model what the next line must do (see PLAN.md). The model only writes the
// words; the director decides mood, double lines, when to ask for snap and
// when to share it.
//
//	apitest.exe            interactive chat (you are the stranger)
//	apitest.exe ping       one raw request, prints status + body
package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// ---------------------------------------------------------------- config ---

type config struct {
	BaseURL      string
	Model        string
	APIKey       string
	Timeout      time.Duration
	MaxTokens    int
	Temperature  float64
	History      int
	AgeMin       int
	AgeMax       int
	Countries    []string
	SnapUsername string
	ShareHot     int // bot reply number at which a HOT user gets the snap
	ShareWarm    int
	ShareCold    int
	ShowPrompt   bool
	ExtraJSON    map[string]any
}

type message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type chatResponse struct {
	Choices []struct {
		Message struct {
			Content          string `json:"content"`
			ReasoningContent string `json:"reasoning_content,omitempty"`
		} `json:"message"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Usage *struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
	} `json:"usage,omitempty"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

// -------------------------------------------------------------- director ---

const (
	tierCold = "COLD"
	tierWarm = "WARM"
	tierHot  = "HOT"
)

var (
	thinkRe     = regexp.MustCompile(`(?s)<think>.*?</think>`)
	forbiddenRe = regexp.MustCompile(`(?i)(@|https?://|www\.|as an ai|language model|i am a bot|i'm a bot)`)
	hornyRe     = regexp.MustCompile(`(?i)\b(h[o0]rny|hrny|s[e3]x|sexy|nudes?|n[o0]des|dick|cock|pussy|boobs?|tits|wet|dtf|f[u*]ck\w*|naked|nsfw|cum|turned on|in the mood|hard rn|send pics?|ur body|strip|hookup|hook up|make ?out|kinky|18\+)\b`)
	flirtyRe    = regexp.MustCompile(`(?i)\b(cute|pretty|beautiful|gorgeous|hot|babe|baby|bae|love|luv|kiss|date|single|gf|bf|boyfriend|girlfriend|miss u|miss you|wanna meet|meet up|ur eyes|ur smile|sweet|crush|flirt\w*|marry|wifey|pics?|photo|selfie)\b`)
	snapAskRe   = regexp.MustCompile(`(?i)(\bsnap\b|\bsc\b|\bsnapchat\b|\bsnp\b|\binsta\b|\big\b|\binstagram\b|\btelegram\b|\btg\b|\bdiscord\b|\bnumber\b|\bwhatsapp\b)`)
	refuseRe    = regexp.MustCompile(`(?i)\b(no|nope|nah|naw|dont have|don't have|do not have|not on|deleted|never|not really)\b`)
	yesRe       = regexp.MustCompile(`(?i)\b(yes+|yea+h*|yep+|yeop|ye|ya|sure|ofc|ok+|okay|k|yup|i do|got it|send|drop|gimme|add me|bet|fr|why not|lets go|let's go)\b`)
	ageRe       = []*regexp.Regexp{regexp.MustCompile(`(?i)\b([mf])[\s\-_. ]{0,2}(\d{1,2})\b`), regexp.MustCompile(`(?i)\b(\d{1,2})[\s\-_. ]{0,2}([mf])\b`), regexp.MustCompile(`(?i)\bi'?m (\d{2})\b`), regexp.MustCompile(`(?i)\b(\d{2}) ?(yo|years? old|yrs)\b`)}
	byeRe       = regexp.MustCompile(`(?i)\b(bye|gtg|g2g|brb|ttyl|cya|gotta go|leaving|later)\b`)
)

var allMoods = []string{"friendly", "playful", "lonely", "flirty", "fun/joke", "curious", "teasing"}
var hotMoods = []string{"flirty", "teasing", "playful"}

var moodHints = map[string]string{
	"friendly": "warm and easy, like texting a new friend",
	"playful":  "light, cheeky, a little sarcastic in a cute way",
	"lonely":   "a bit bored and alone tonight, wants someone to talk to, soft",
	"flirty":   "subtle flirting, compliments back, a little suggestive but never explicit",
	"fun/joke": "make a tiny joke or funny exaggeration about what they said",
	"curious":  "genuinely curious about them, ask one specific thing",
	"teasing":  "tease them playfully about what they said, confident",
}

var shareLines = []string{
	"ok fine, add me on snap 👻 %username%",
	"my snap is %username%, add me rn",
	"sc: %username% .. come find me there",
	"ok u got me lol, snap %username%",
}

type director struct {
	score      int
	tier       string
	hornyHits  int
	flirtyHits int
	underage   bool
	userAge    int
	userGender string
	askedSnap  bool
	askedTurn  int
	nudges     int
	shared     bool
	sharedTurn int
	ended      bool
	endReason  string
	moods      []string
	moodIdx    int
	lastDouble int
	// per-message signals
	userAskedSnap bool
	userRefused   bool
	userQuestion  bool
	userHorny     bool
	userFlirty    bool
	userBye       bool
}

type plan struct {
	Mood        string
	Instruction string
	Double      bool
	ShareNow    bool // append the username line after the model's lead-in
	EndAfter    bool // conversation ends after this reply
	Fixed       string
}

func newDirector(rng *rand.Rand) *director {
	moods := append([]string(nil), allMoods...)
	rng.Shuffle(len(moods), func(i, j int) { moods[i], moods[j] = moods[j], moods[i] })
	return &director{tier: tierCold, moods: moods}
}

// observe scores one stranger message and updates the funnel state.
func (d *director) observe(text string) {
	low := strings.ToLower(text)
	d.userAskedSnap = snapAskRe.MatchString(low)
	d.userQuestion = strings.Contains(low, "?") || regexp.MustCompile(`(?i)\b(wyd|hbu|wbu|how|what|where|why|when|who|r u|are u|do u|u got|got any)\b`).MatchString(low)
	d.userHorny = hornyRe.MatchString(low)
	d.userFlirty = flirtyRe.MatchString(low)
	d.userBye = byeRe.MatchString(low)
	d.userRefused = d.askedSnap && !d.shared && refuseRe.MatchString(low) && !yesRe.MatchString(low)

	if g, a, ok := genderAge(low); ok {
		if d.userAge == 0 {
			d.userAge, d.userGender = a, g
		}
		if a < 18 {
			d.underage = true
		}
	}
	switch {
	case d.userHorny:
		d.score += 3
		d.hornyHits++
	case d.userFlirty:
		d.score += 2
		d.flirtyHits++
	}
	if d.userQuestion {
		d.score++
	}
	if len([]rune(text)) > 25 {
		d.score++
	}
	if strings.ContainsAny(text, "😏😘😍🥵😂❤️🔥") {
		d.score++
	}
	switch {
	case d.hornyHits > 0 || d.score >= 6:
		d.tier = tierHot
	case d.score >= 3:
		d.tier = tierWarm
	default:
		d.tier = tierCold
	}
}

func (d *director) shareTurn(cfg config) int {
	switch d.tier {
	case tierHot:
		return cfg.ShareHot
	case tierWarm:
		return cfg.ShareWarm
	}
	return cfg.ShareCold
}

func (d *director) mood(turn int) string {
	if d.tier == tierHot {
		return hotMoods[(turn/2)%len(hotMoods)]
	}
	return d.moods[(turn/2)%len(d.moods)]
}

// plan decides what bot reply number `turn` must do.
func (d *director) plan(cfg config, turn int, userText string) plan {
	p := plan{Mood: d.mood(turn)}
	base := "reply to exactly what they just said"
	if d.userQuestion {
		base = "they asked you something: answer it directly first, in your own words"
	}

	// hard gates ---------------------------------------------------------
	if d.underage {
		d.ended, d.endReason = true, "underage"
		return plan{Fixed: "oh ur too young for me lol. bye", EndAfter: true}
	}
	if d.userBye && !d.shared {
		d.ended, d.endReason = true, "user left"
		return plan{Mood: p.Mood, Instruction: "they are leaving: say bye casually, one short line, no questions", EndAfter: true}
	}

	// after the share -----------------------------------------------------
	if d.shared {
		if turn-d.sharedTurn >= 2 || d.userBye {
			d.ended, d.endReason = true, "done after share"
			return plan{Mood: p.Mood, Instruction: base + ", then say ttyl and that you'll be on snap. one short line, no question.", EndAfter: true}
		}
		return plan{Mood: p.Mood, Instruction: base + ". remind them casually to add you on snap (do NOT write the username, it was already sent). one short line."}
	}

	// user asks for the snap / says yes after the ask -> share now ---------
	if d.userAskedSnap || (d.askedSnap && yesRe.MatchString(strings.ToLower(userText))) {
		return d.sharePlan(turn, p.Mood, "they want your snap: say something short and playful like 'ok fine' or 'took u long enough'. one line, NO username, no question.")
	}
	// refusal after the ask -> one nudge, then let it go ------------------
	if d.userRefused {
		if d.nudges == 0 {
			d.nudges++
			return plan{Mood: "teasing", Instruction: "they said no to snap: tease them lightly to add you anyway, say u are barely on this app. one short line, NO username.", Double: false}
		}
		d.ended, d.endReason = true, "refused twice"
		return plan{Mood: "friendly", Instruction: "they really don't want snap: no worries, say bye nicely. one short line.", EndAfter: true}
	}

	target := d.shareTurn(cfg)
	switch {
	case turn >= target:
		// share turn: the model writes the lead-in, we append the username
		return d.sharePlan(turn, p.Mood, base+". then say u are moving to snap because this app is boring / u barely check it. one short line, NO username, no question.")
	case turn == target-1 && !d.askedSnap:
		d.askedSnap, d.askedTurn = true, turn
		if d.lastDouble == turn-1 {
			p.Instruction = base + " and in the same line ask if they have snapchat ('u got snap?' style). one line."
			return p
		}
		p.Instruction = base + ". then ask if they have snapchat (just 'u got snap?' style). two lines: first the answer, second the question."
		p.Double = true
		d.lastDouble = turn
		return p
	}

	// middle chat, step by step -------------------------------------------
	switch {
	case d.userHorny:
		p.Instruction = base + ". they are being horny: tease back, playful and confident, suggestive but NOT explicit, don't shut them down. hint it would be more fun on snap, but do NOT ask for snap yet."
	case d.userFlirty:
		p.Instruction = base + ". they are flirting: flirt back, give a small compliment, keep them chasing."
	case turn == 1:
		p.Instruction = base + ". greet back casually and ask one quick thing about them (age / where from / wyd)."
	case turn == 2 && d.userAge == 0:
		p.Instruction = base + ". if they didn't say their age/gender yet, ask asl style (like 'f 21, u?') with your persona age."
	default:
		p.Instruction = base + ". then keep the chat going with ONE short question that fits what they said. mood: " + moodHints[p.Mood] + "."
	}
	// double lines: roughly every 3rd turn, after horny/flirty lines, never twice in a row
	if (d.userHorny || d.userFlirty || turn%3 == 0) && d.lastDouble != turn-1 && turn > 1 {
		p.Double = true
		d.lastDouble = turn
		p.Instruction += " write TWO lines: first the reaction/answer, second the question."
	}
	return p
}

func (d *director) sharePlan(turn int, mood, instruction string) plan {
	d.shared, d.sharedTurn = true, turn
	d.askedSnap = true
	d.lastDouble = turn
	return plan{Mood: mood, Instruction: instruction, ShareNow: true}
}

func (d *director) status(cfg config, turn int) string {
	switch {
	case d.ended:
		return fmt.Sprintf("ENDED (%s)", d.endReason)
	case d.shared:
		return fmt.Sprintf("tier=%s score=%d | snap SHARED on turn %d", d.tier, d.score, d.sharedTurn)
	}
	return fmt.Sprintf("tier=%s score=%d | snap at reply %d (asked=%v)", d.tier, d.score, d.shareTurn(cfg), d.askedSnap)
}

// ---------------------------------------------------------------- session ---

type session struct {
	Age      int
	Country  string
	System   string
	History  []message
	Turns    int
	Director *director
}

func newSession(cfg config, personaTemplate string, rng *rand.Rand) *session {
	s := &session{
		Age:      cfg.AgeMin + rng.Intn(cfg.AgeMax-cfg.AgeMin+1),
		Country:  cfg.Countries[rng.Intn(len(cfg.Countries))],
		Director: newDirector(rng),
	}
	sys := strings.ReplaceAll(personaTemplate, "{age}", strconv.Itoa(s.Age))
	sys = strings.ReplaceAll(sys, "{country}", s.Country)
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

func main() {
	dir := exeDir()
	cfg, err := loadConfig(filepath.Join(dir, "apitest.config.txt"))
	if err != nil {
		fatal(err)
	}
	personaTemplate, err := loadPersona(filepath.Join(dir, "persona.txt"))
	if err != nil {
		fatal(err)
	}
	mode := "chat"
	if len(os.Args) > 1 {
		mode = strings.ToLower(os.Args[1])
	}
	switch mode {
	case "ping":
		ping(cfg)
	case "chat":
		chatLoop(cfg, personaTemplate)
	default:
		fmt.Println("usage: apitest.exe [chat|ping]")
		os.Exit(2)
	}
}

func chatLoop(cfg config, personaTemplate string) {
	rng := rand.New(rand.NewSource(time.Now().UnixNano()))
	s := newSession(cfg, personaTemplate, rng)
	printBanner(cfg, s)

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
		switch strings.ToLower(text) {
		case "/quit", "/exit", "/q":
			return
		case "/new":
			s = newSession(cfg, personaTemplate, rng)
			fmt.Printf("--- new chat: persona f %d from %s | moods: %s ---\n", s.Age, s.Country, strings.Join(s.Director.moods, " > "))
			continue
		case "/prompt":
			cfg.ShowPrompt = !cfg.ShowPrompt
			fmt.Printf("--- show prompt: %v ---\n", cfg.ShowPrompt)
			continue
		case "/system":
			fmt.Println("--- system prompt ---\n" + s.System + "\n---------------------")
			continue
		case "/state":
			fmt.Printf("--- %s | user %s%d | horny %d flirty %d | turn %d ---\n", s.Director.status(cfg, s.Turns+1), s.Director.userGender, s.Director.userAge, s.Director.hornyHits, s.Director.flirtyHits, s.Turns)
			continue
		case "/history":
			for i, m := range s.History {
				fmt.Printf("%2d %-9s %s\n", i+1, m.Role+":", m.Content)
			}
			continue
		case "/help":
			printHelp()
			continue
		}

		if s.Director.ended {
			fmt.Println("     (chat ended: " + s.Director.endReason + " — the bot would not reply anymore. /new for a new stranger)")
			continue
		}

		d := s.Director
		d.observe(text)
		turn := s.Turns + 1
		p := d.plan(cfg, turn, text)
		s.History = append(s.History, message{Role: "user", Content: text})

		var lines []string
		info := ""
		took := 0.0
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
			reply, callInfo, err := complete(cfg, msgs)
			took = time.Since(start).Seconds()
			info = callInfo
			if err != nil {
				fmt.Printf("eva> (no reply)\n     ERROR after %.1fs: %v\n", took, err)
				fmt.Printf("     plan was: mood=%s double=%v share=%v | %s\n", p.Mood, p.Double, p.ShareNow, p.Instruction)
				continue
			}
			var warn string
			lines, warn = sanitizeLines(reply, p.Double)
			if warn != "" {
				fmt.Printf("     WARN: %s\n     raw: %q\n", warn, strings.TrimSpace(reply))
			}
		}
		if p.ShareNow {
			user := cfg.SnapUsername
			if user == "" {
				user = "yoursnapname"
			}
			lines = append(lines, strings.ReplaceAll(shareLines[rng.Intn(len(shareLines))], "%username%", user))
		}
		for _, line := range lines {
			fmt.Printf("eva> %s\n", line)
			s.History = append(s.History, message{Role: "assistant", Content: line})
		}
		s.Turns++
		fmt.Printf("     [%.1fs%s] reply %d | mood %s | %s", took, info, turn, p.Mood, d.status(cfg, turn+1))
		if p.Double {
			fmt.Print(" | double")
		}
		if p.EndAfter {
			fmt.Print(" | END")
		}
		fmt.Println()
	}
}

// ---------------------------------------------------------------- API ---

func complete(cfg config, msgs []message) (string, string, error) {
	status, body, err := rawCall(cfg, msgs, cfg.MaxTokens)
	if err != nil {
		return "", "", err
	}
	var parsed chatResponse
	if jsonErr := json.Unmarshal(body, &parsed); jsonErr != nil {
		return "", "", fmt.Errorf("HTTP %d, non-JSON body: %s", status, trim(string(body), 300))
	}
	if status >= 400 {
		msg := trim(string(body), 300)
		if parsed.Error != nil && parsed.Error.Message != "" {
			msg = parsed.Error.Message
		}
		return "", "", fmt.Errorf("HTTP %d: %s", status, msg)
	}
	if len(parsed.Choices) == 0 {
		return "", "", errors.New("no choices in response")
	}
	info := ""
	if parsed.Usage != nil {
		info = fmt.Sprintf(", %d+%d tok", parsed.Usage.PromptTokens, parsed.Usage.CompletionTokens)
	}
	choice := parsed.Choices[0]
	if fr := choice.FinishReason; fr != "" && fr != "stop" {
		info += ", finish=" + fr
	}
	content := choice.Message.Content
	if strings.TrimSpace(thinkRe.ReplaceAllString(content, "")) == "" {
		reasoning := strings.TrimSpace(choice.Message.ReasoningContent)
		if reasoning == "" {
			reasoning = strings.TrimSpace(content)
		}
		hint := "model spent all tokens THINKING and never wrote the reply"
		if reasoning != "" {
			hint += fmt.Sprintf(" (reasoning %d chars: %q)", len(reasoning), trim(reasoning, 160))
		}
		hint += "\n     FIX: raise MAX_TOKENS (e.g. 1024) or disable thinking with EXTRA_JSON in apitest.config.txt, or use a non-reasoning model (e.g. meta-llama/Llama-3.3-70B-Instruct)"
		return "", "", errors.New(hint)
	}
	return content, info, nil
}

func rawCall(cfg config, msgs []message, maxTokens int) (int, []byte, error) {
	fields := map[string]any{
		"model":       cfg.Model,
		"messages":    msgs,
		"max_tokens":  maxTokens,
		"temperature": cfg.Temperature,
	}
	for k, v := range cfg.ExtraJSON {
		fields[k] = v
	}
	payload, err := json.Marshal(fields)
	if err != nil {
		return 0, nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), cfg.Timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, cfg.BaseURL+"/chat/completions", bytes.NewReader(payload))
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+cfg.APIKey)
	resp, err := (&http.Client{Timeout: cfg.Timeout}).Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if err != nil {
		return resp.StatusCode, nil, err
	}
	return resp.StatusCode, body, nil
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

// sanitizeLines applies the rules the real bot would enforce and returns the
// line(s) to send (1, or 2 when a double line was requested).
func sanitizeLines(raw string, double bool) ([]string, string) {
	text := strings.TrimSpace(thinkRe.ReplaceAllString(raw, ""))
	var warns []string
	if strings.Contains(raw, "<think>") {
		warns = append(warns, "model emitted <think> reasoning (stripped)")
	}
	var lines []string
	for _, l := range strings.Split(text, "\n") {
		l = strings.TrimSpace(strings.Trim(strings.TrimSpace(l), "\"'“”`"))
		l = strings.TrimLeft(l, "-•*0123456789. ")
		for _, prefix := range []string{"you:", "me:", "eva:", "bot:", "line 1:", "line 2:", "line1:", "line2:"} {
			if strings.HasPrefix(strings.ToLower(l), prefix) {
				l = strings.TrimSpace(l[len(prefix):])
			}
		}
		if l != "" {
			lines = append(lines, strings.ToLower(l))
		}
	}
	if len(lines) == 0 {
		return []string{"(empty)"}, "empty reply -> bot would use a TXT fallback line"
	}
	want := 1
	if double {
		want = 2
	}
	if len(lines) > want {
		warns = append(warns, fmt.Sprintf("model wrote %d lines, kept %d", len(lines), want))
		lines = lines[:want]
	}
	for _, l := range lines {
		if len([]rune(l)) > 120 {
			warns = append(warns, "line too long (>120 chars) -> bot would fall back")
		}
		if n := len(strings.Fields(l)); n > 14 {
			warns = append(warns, fmt.Sprintf("%d words (target <=10)", n))
		}
		if forbiddenRe.MatchString(l) {
			warns = append(warns, "contains @ / link / ai-talk -> bot would fall back")
		}
		if strings.Count(l, "?") > 1 {
			warns = append(warns, "two questions in one line")
		}
	}
	return lines, strings.Join(warns, "; ")
}

func genderAge(low string) (string, int, bool) {
	for i, re := range ageRe {
		m := re.FindStringSubmatch(low)
		if len(m) < 2 {
			continue
		}
		var g, a string
		switch i {
		case 0:
			g, a = m[1], m[2]
		case 1:
			a, g = m[1], m[2]
		default:
			a = m[1]
		}
		n, err := strconv.Atoi(a)
		if err == nil && n >= 13 && n <= 99 {
			return g, n, true
		}
	}
	return "", 0, false
}

// -------------------------------------------------------------- config ---

func loadConfig(path string) (config, error) {
	cfg := config{
		BaseURL:     "https://api.featherless.ai/v1",
		Model:       "meta-llama/Llama-3.3-70B-Instruct",
		Timeout:     60 * time.Second,
		MaxTokens:   1024,
		Temperature: 0.9,
		History:     30,
		AgeMin:      20,
		AgeMax:      25,
		Countries:   []string{"uk", "germany", "france", "italy", "spain", "netherlands", "sweden", "poland", "ireland", "norway"},
		ShareHot:    4,
		ShareWarm:   6,
		ShareCold:   8,
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
		case "SNAP_USERNAME":
			cfg.SnapUsername = strings.TrimPrefix(value, "@")
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

func loadPersona(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("persona.txt: %w", err)
	}
	var lines []string
	for _, line := range strings.Split(strings.ReplaceAll(string(data), "\r", ""), "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		lines = append(lines, strings.TrimRight(line, " "))
	}
	text := strings.TrimSpace(strings.Join(lines, "\n"))
	if text == "" {
		return "", errors.New("persona.txt is empty")
	}
	return text, nil
}

// --------------------------------------------------------------- misc ---

func printBanner(cfg config, s *session) {
	fmt.Println("==========================================================")
	fmt.Println(" apitest - chat with the AI persona (you are the stranger)")
	fmt.Printf(" api:     %s\n model:   %s\n", cfg.BaseURL, cfg.Model)
	fmt.Printf(" persona: f %d from %s   (persona.txt, /new = re-roll)\n", s.Age, s.Country)
	fmt.Printf(" snap at: HOT reply %d | WARM %d | COLD %d   username: %s\n", cfg.ShareHot, cfg.ShareWarm, cfg.ShareCold, orDefault(cfg.SnapUsername, "(set SNAP_USERNAME)"))
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
	fmt.Println(" /quit     exit")
}

func orDefault(v, d string) string {
	if strings.TrimSpace(v) == "" {
		return d
	}
	return v
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

func trim(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) > n {
		return s[:n] + "..."
	}
	return s
}

func fatal(err error) {
	fmt.Println("ERROR:", err)
	os.Exit(1)
}
