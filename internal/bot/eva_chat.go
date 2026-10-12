package bot

import (
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"chitchatbot/internal/model"
)

const (
	evaStageNew           = "NEW"
	evaStageWaitAgeGender = "WAIT_AGE_GENDER"
	evaStageWaitCountry   = "WAIT_COUNTRY"
	evaStageMiddle        = "MIDDLE"
	evaStageEnd           = "END"
	evaMaxReplies         = 8

	// evaSnapPool is the reply pool that shares the bot's snap/tg username.
	// Lines normally contain %username%, which is filled from the dashboard
	// "Usernames" list exactly like Single/Folder sequence messages.
	evaSnapPool = "snap_share.txt"
	// evaSnapTrigger lists user phrases that ask for a snap/insta/tg handle.
	evaSnapTrigger = "snap_request.txt"
	// evaSnapAfterMiddleReplies is how many MIDDLE-stage bot replies happen
	// before the username is shared proactively (if the user never asks).
	evaSnapAfterMiddleReplies = 3
	// evaUsernamePlaceholder mirrors the placeholder used by sequence modes.
	evaUsernamePlaceholder = "%username%"
)

// EVAChatConfig is attached to a worker only when EVAChat Flow is selected.
// The engine is immutable after loading and can safely be shared by workers.
type EVAChatConfig struct {
	DataRoot string
	Engine   *EVAChatEngine
}

// EVAReplyResult describes one decision from the stateful EVAChat engine.
type EVAReplyResult struct {
	Text       string
	SourcePool string
	Ended      bool
	Reason     string
	// HasUsername reports that Text contained %username% and was rendered
	// with an assigned snap/tg username. Callers use it for the existing
	// "snap/tg/user" counter and flagged-user bookkeeping.
	HasUsername bool
	// SnapShared reports that this reply is the snap/tg share line.
	SnapShared bool
}

// EVAReplyOptions carries per-worker facts the shared engine cannot know.
type EVAReplyOptions struct {
	// HasUsernames is true when the worker has at least one snap/tg username
	// configured in the dashboard. Without it a %username% snap line would
	// render empty, so the engine skips the snap share instead.
	HasUsernames bool
}

// EVAChatEngine is a native Go implementation of the active EVA input/output
// flow. It intentionally uses only editable TXT files and neutral defaults;
// it has no Python, local-db, or legacy-rule dependency.
type EVAChatEngine struct {
	dataRoot  string
	inputs    map[string][]string
	outputs   map[string][]string
	countries map[string]string
	scanOrder []string
	maxReply  int
	// snapNeedsUsername is true when any output/snap_share.txt line uses
	// %username%; such a pool is only usable when usernames are configured.
	snapNeedsUsername bool
}

var evaSlang = map[string]string{
	"u": "you", "ur": "your", "yr": "your", "yur": "your",
	"r": "are", "im": "i am", "i'm": "i am", "wats": "what is",
	"wat": "what", "wut": "what", "wbu": "what about you",
	"wby": "what about you", "hbu": "how about you",
}

var (
	evaTokenSplit = regexp.MustCompile(`[^a-z0-9]+`)
	evaAgeGenderA = regexp.MustCompile(`(?i)\b([mf])\s*[-_. ]{0,2}(\d{1,2})\b`)
	evaAgeGenderB = regexp.MustCompile(`(?i)\b(\d{1,2})\s*[-_. ]{0,2}([mf])\b`)
	evaAgeGenderC = regexp.MustCompile(`(?i)\b(?:male|female)\s*(\d{1,2})\b`)
)

var evaPriority = []string{
	"busy_later.txt",
	"how_are_you.txt",
	"your_age.txt",
	"warm_reply.txt",
	"greeting.txt",
	"country.txt",
	"age_gender.txt",
}

var evaRepeatablePools = map[string]bool{
	"follow_up.txt":  true,
	"warm_reply.txt": true,
}

// LoadEVAChatEngine validates and loads the user-editable TXT data pack.
func LoadEVAChatEngine(dataRoot string) (*EVAChatEngine, error) {
	inputDir := filepath.Join(dataRoot, "input")
	outputDir := filepath.Join(dataRoot, "output")

	inputs, err := loadEVATextTree(inputDir, true)
	if err != nil {
		return nil, fmt.Errorf("load EVAChat input rules: %w", err)
	}
	outputs, err := loadEVATextTree(outputDir, false)
	if err != nil {
		return nil, fmt.Errorf("load EVAChat reply pools: %w", err)
	}

	for _, required := range []string{"greeting.txt"} {
		if len(inputs[required]) == 0 || len(outputs[required]) == 0 {
			return nil, fmt.Errorf("EVAChat requires input/%s and output/%s with at least one line", required, required)
		}
	}
	for _, required := range []string{"follow_up.txt", "closer.txt"} {
		if len(outputs[required]) == 0 {
			return nil, fmt.Errorf("EVAChat requires output/%s with at least one line", required)
		}
	}

	countries, err := loadEVACountries(filepath.Join(dataRoot, "countries.txt"))
	if err != nil {
		return nil, err
	}
	if len(countries) == 0 {
		countries = map[string]string{
			"usa": "usa", "canada": "canada", "uk": "uk", "bangladesh": "bangladesh", "india": "india",
		}
	}

	known := make(map[string]bool, len(inputs))
	for name := range inputs {
		known[name] = true
	}
	order := make([]string, 0, len(inputs))
	for _, name := range evaPriority {
		if known[name] {
			order = append(order, name)
			delete(known, name)
		}
	}
	custom := make([]string, 0, len(known))
	for name := range known {
		custom = append(custom, name)
	}
	sort.Strings(custom)
	order = append(order, custom...)

	snapNeedsUsername := false
	for _, line := range outputs[evaSnapPool] {
		if strings.Contains(line, evaUsernamePlaceholder) {
			snapNeedsUsername = true
			break
		}
	}

	return &EVAChatEngine{
		dataRoot:          dataRoot,
		inputs:            inputs,
		outputs:           outputs,
		countries:         countries,
		scanOrder:         order,
		maxReply:          evaMaxReplies,
		snapNeedsUsername: snapNeedsUsername,
	}, nil
}

// SnapShareAvailable reports whether the loaded pack can share a snap/tg
// username for a worker with (or without) configured usernames.
func (e *EVAChatEngine) SnapShareAvailable(hasUsernames bool) bool {
	if e == nil || len(e.outputs[evaSnapPool]) == 0 {
		return false
	}
	return hasUsernames || !e.snapNeedsUsername
}

func loadEVATextTree(root string, skipComments bool) (map[string][]string, error) {
	if info, err := os.Stat(root); err != nil {
		return nil, err
	} else if !info.IsDir() {
		return nil, fmt.Errorf("%s is not a directory", root)
	}

	loaded := make(map[string][]string)
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || !strings.EqualFold(filepath.Ext(path), ".txt") {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		key := filepath.ToSlash(rel)
		lines, err := loadEVALines(path, skipComments)
		if err != nil {
			return err
		}
		if len(lines) > 0 {
			loaded[key] = lines
		}
		return nil
	})
	return loaded, err
}

func loadEVALines(path string, skipComments bool) ([]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	lines := strings.Split(strings.ReplaceAll(string(data), "\r", ""), "\n")
	result := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || (skipComments && strings.HasPrefix(line, "#")) {
			continue
		}
		result = append(result, line)
	}
	return result, nil
}

func loadEVACountries(path string) (map[string]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]string{}, nil
		}
		return nil, fmt.Errorf("read EVAChat countries: %w", err)
	}
	countries := make(map[string]string)
	for _, line := range strings.Split(strings.ReplaceAll(string(data), "\r", ""), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") || !strings.Contains(line, "=") {
			continue
		}
		key, value, _ := strings.Cut(line, "=")
		key = strings.ToLower(strings.TrimSpace(key))
		value = strings.ToLower(strings.TrimSpace(value))
		if key != "" && value != "" {
			countries[key] = value
		}
	}
	return countries, nil
}

// NewEVAChatSession returns a fully initialized conversation state.
// InputCategories returns the loaded trigger-category count/list for UI logs.
func (e *EVAChatEngine) InputCategories() []string {
	if e == nil {
		return nil
	}
	categories := make([]string, 0, len(e.inputs))
	for category := range e.inputs {
		categories = append(categories, category)
	}
	sort.Strings(categories)
	return categories
}

// OutputPools returns the loaded reply-pool count/list for UI logs.
func (e *EVAChatEngine) OutputPools() []string {
	if e == nil {
		return nil
	}
	pools := make([]string, 0, len(e.outputs))
	for pool := range e.outputs {
		pools = append(pools, pool)
	}
	sort.Strings(pools)
	return pools
}

func NewEVAChatSession() model.EVAChatSession {
	return model.EVAChatSession{
		Stage:       evaStageNew,
		PoolsUsed:   make(map[string]bool),
		UsedReplies: make(map[string][]string),
	}
}

// Start opens a new conversation using output/greeting.txt.
func (e *EVAChatEngine) Start(state *model.EVAChatSession, rng *rand.Rand) EVAReplyResult {
	if e == nil {
		return EVAReplyResult{Reason: "EVAChat engine unavailable"}
	}
	e.normalizeState(state)
	if state.Ended {
		return EVAReplyResult{Ended: true, Reason: "conversation already ended"}
	}
	if state.Started {
		return EVAReplyResult{Reason: "conversation already started"}
	}

	state.Started = true
	state.Stage = evaStageWaitAgeGender
	return e.pick("greeting.txt", state, rng, "conversation opener")
}

// Reply processes one incoming message and mutates only the supplied session.
// It assumes usernames are configured; workers use ReplyWithOptions.
func (e *EVAChatEngine) Reply(message string, state *model.EVAChatSession, rng *rand.Rand) EVAReplyResult {
	return e.ReplyWithOptions(message, state, rng, EVAReplyOptions{HasUsernames: true})
}

// ReplyWithOptions is Reply with per-worker facts such as whether a snap/tg
// username list exists.
func (e *EVAChatEngine) ReplyWithOptions(message string, state *model.EVAChatSession, rng *rand.Rand, opts EVAReplyOptions) EVAReplyResult {
	if e == nil {
		return EVAReplyResult{Reason: "EVAChat engine unavailable"}
	}
	e.normalizeState(state)
	if state.Ended || state.Stage == evaStageEnd {
		state.Ended = true
		state.Stage = evaStageEnd
		return EVAReplyResult{Ended: true, Reason: "conversation already ended"}
	}
	if !state.Started {
		return e.Start(state, rng)
	}

	message = evaLastLine(message)
	if message == "" {
		return e.pick("follow_up.txt", state, rng, "empty incoming message")
	}

	gender, age, hasAge := evaAgeGender(message)
	if gender != "" && state.UserGender == "" {
		state.UserGender = gender
	}
	if hasAge && !state.HasUserAge {
		state.UserAge = age
		state.HasUserAge = true
	}
	if country := e.countryFromText(message); country != "" {
		state.UserCountry = country
	}
	if hasAge && age < 18 {
		state.Blocked = true
	}
	if state.Blocked {
		return e.finish(state, rng, "age gate")
	}
	// Once the snap/tg username has gone out, the next user message closes
	// the flow politely instead of continuing small talk.
	if state.SnapShared {
		return e.finish(state, rng, "snap shared; closing")
	}
	snapAvailable := e.SnapShareAvailable(opts.HasUsernames)
	if state.BotReplyCount >= e.maxReply {
		return e.finish(state, rng, "reply cap")
	}
	// Share the username before the cap closes the chat so a long
	// conversation never ends without the snap/tg line.
	if snapAvailable && state.BotReplyCount >= e.maxReply-1 && state.Stage != evaStageNew {
		return e.shareSnap(state, rng, "reply cap reached before share")
	}
	// A direct "snap?/insta?/tg?" request is honoured at any stage after the
	// opener, even before age/country were captured.
	if snapAvailable && state.Stage != evaStageNew && e.matchesCategory(message, evaSnapTrigger) {
		return e.shareSnap(state, rng, "user asked for snap/tg")
	}

	switch state.Stage {
	case evaStageNew:
		return e.Start(state, rng)
	case evaStageWaitAgeGender:
		if hasAge {
			state.Stage = evaStageWaitCountry
			state.Misses = 0
			return e.pick("age_gender.txt", state, rng, "captured age or gender")
		}
		if e.matchesCategory(message, "country.txt") {
			state.Stage = evaStageWaitCountry
			state.Misses = 0
			return e.pick("country.txt", state, rng, "country question")
		}
		state.Misses++
		if state.Misses >= 2 {
			state.Stage = evaStageWaitCountry
			state.Misses = 0
			return e.pick("country.txt", state, rng, "age step exhausted")
		}
		return e.pick("age_ask.txt", state, rng, "waiting for age or gender")
	case evaStageWaitCountry:
		if state.UserCountry != "" && !e.matchesCategory(message, "country.txt") {
			state.Stage = evaStageMiddle
			return e.pick("follow_up.txt", state, rng, "captured country")
		}
		if e.matchesCategory(message, "country.txt") {
			return e.pick("country.txt", state, rng, "country question")
		}
		state.Stage = evaStageMiddle
		return e.pick("follow_up.txt", state, rng, "country stage fallback")
	default:
		return e.replyMiddle(message, state, rng, snapAvailable)
	}
}

func (e *EVAChatEngine) replyMiddle(message string, state *model.EVAChatSession, rng *rand.Rand, snapAvailable bool) EVAReplyResult {
	if e.matchesCategory(message, "busy_later.txt") {
		// The user is leaving: share the username now if it never went out,
		// otherwise say goodbye.
		if snapAvailable {
			return e.shareSnap(state, rng, "user leaving before share")
		}
		result := e.pick("busy_later.txt", state, rng, "user is leaving")
		state.Stage = evaStageEnd
		state.Ended = true
		result.Ended = true
		return result
	}
	if snapAvailable && state.MiddleReplies >= evaSnapAfterMiddleReplies {
		return e.shareSnap(state, rng, fmt.Sprintf("after %d middle replies", state.MiddleReplies))
	}
	state.MiddleReplies++
	for _, category := range e.scanOrder {
		if category == "busy_later.txt" || category == "country.txt" || category == "age_gender.txt" || category == evaSnapTrigger {
			continue
		}
		if e.matchesCategory(message, category) {
			return e.pick(category, state, rng, "category match")
		}
	}
	return e.pick("follow_up.txt", state, rng, "no category match")
}

// shareSnap sends one line from output/snap_share.txt exactly once per user.
// The returned Text still contains %username%; the worker adapter renders it
// with the per-user assigned username (see evaChatRender).
func (e *EVAChatEngine) shareSnap(state *model.EVAChatSession, rng *rand.Rand, reason string) EVAReplyResult {
	e.normalizeState(state)
	lines := e.outputs[evaSnapPool]
	if len(lines) == 0 || state.SnapShared {
		return e.pick("follow_up.txt", state, rng, reason+"; snap pool unavailable")
	}
	choice := lines[0]
	if rng != nil && len(lines) > 1 {
		choice = lines[rng.Intn(len(lines))]
	}
	state.SnapShared = true
	state.PoolsUsed[evaSnapPool] = true
	state.UsedReplies[evaSnapPool] = append(state.UsedReplies[evaSnapPool], choice)
	state.LastPool = evaSnapPool
	state.BotReplyCount++
	return EVAReplyResult{
		Text:        choice,
		SourcePool:  evaSnapPool,
		Reason:      reason,
		SnapShared:  true,
		HasUsername: strings.Contains(choice, evaUsernamePlaceholder),
	}
}

func (e *EVAChatEngine) finish(state *model.EVAChatSession, rng *rand.Rand, reason string) EVAReplyResult {
	if state.Ended {
		return EVAReplyResult{Ended: true, Reason: reason}
	}
	result := e.pick("closer.txt", state, rng, reason)
	state.Stage = evaStageEnd
	state.Ended = true
	result.Ended = true
	return result
}

func (e *EVAChatEngine) pick(pool string, state *model.EVAChatSession, rng *rand.Rand, reason string) EVAReplyResult {
	e.normalizeState(state)
	actualPool := pool
	if !evaRepeatablePools[pool] && state.PoolsUsed[pool] && pool != "closer.txt" {
		actualPool = "follow_up.txt"
		reason += "; pool already used"
	}
	lines := e.outputs[actualPool]
	if len(lines) == 0 && actualPool != "follow_up.txt" {
		actualPool = "follow_up.txt"
		lines = e.outputs[actualPool]
	}
	if len(lines) == 0 {
		return EVAReplyResult{Reason: reason + "; no reply pool available"}
	}

	used := state.UsedReplies[actualPool]
	fresh := make([]string, 0, len(lines))
	for _, line := range lines {
		if !containsString(used, line) {
			fresh = append(fresh, line)
		}
	}
	if len(fresh) == 0 {
		fresh = append(fresh, lines...)
	}
	choice := fresh[0]
	if rng != nil && len(fresh) > 1 {
		choice = fresh[rng.Intn(len(fresh))]
	}

	state.UsedReplies[actualPool] = append(state.UsedReplies[actualPool], choice)
	if !evaRepeatablePools[actualPool] {
		state.PoolsUsed[actualPool] = true
	}
	state.LastPool = actualPool
	state.BotReplyCount++

	return EVAReplyResult{
		Text:        choice,
		SourcePool:  actualPool,
		Reason:      reason,
		HasUsername: strings.Contains(choice, evaUsernamePlaceholder),
	}
}

func (e *EVAChatEngine) normalizeState(state *model.EVAChatSession) {
	if state.Stage == "" {
		state.Stage = evaStageNew
	}
	if state.PoolsUsed == nil {
		state.PoolsUsed = make(map[string]bool)
	}
	if state.UsedReplies == nil {
		state.UsedReplies = make(map[string][]string)
	}
}

func (e *EVAChatEngine) matchesCategory(message, category string) bool {
	triggers := e.inputs[category]
	for _, trigger := range triggers {
		if evaMatchesTrigger(message, trigger) {
			return true
		}
	}
	return false
}

func evaMatchesTrigger(message, trigger string) bool {
	raw := strings.ToLower(strings.TrimSpace(message))
	trigger = strings.ToLower(strings.TrimSpace(trigger))
	if raw == "" || trigger == "" {
		return false
	}
	simpleMessage := evaSimple(raw)
	simpleTrigger := evaSimple(trigger)
	if raw == trigger || simpleMessage == simpleTrigger {
		return true
	}
	if len(simpleTrigger) >= 3 && evaWholePhraseContains(simpleMessage, simpleTrigger) {
		return true
	}

	normalizedMessage := evaNormalizeText(raw)
	normalizedTrigger := evaNormalizeText(trigger)
	if normalizedTrigger != "" && (normalizedMessage == normalizedTrigger ||
		(len(normalizedTrigger) >= 3 && evaWholePhraseContains(normalizedMessage, normalizedTrigger))) {
		return true
	}

	triggerTokens := meaningfulTokens(evaNormalizeTokens(trigger))
	messageTokens := make(map[string]bool)
	for _, token := range evaNormalizeTokens(message) {
		messageTokens[token] = true
	}
	if len(triggerTokens) >= 2 {
		for _, token := range triggerTokens {
			if !messageTokens[token] {
				return false
			}
		}
		return true
	}
	return false
}

func evaSimple(value string) string {
	return strings.Join(evaNormalizeTokens(value), " ")
}

func evaNormalizeText(value string) string {
	return strings.Join(evaNormalizeTokens(value), " ")
}

func evaNormalizeTokens(value string) []string {
	raw := evaTokenSplit.Split(strings.ToLower(value), -1)
	result := make([]string, 0, len(raw))
	for _, token := range raw {
		if token == "" {
			continue
		}
		if expanded, ok := evaSlang[token]; ok {
			result = append(result, strings.Fields(expanded)...)
			continue
		}
		result = append(result, token)
	}
	return result
}

func meaningfulTokens(tokens []string) []string {
	result := make([]string, 0, len(tokens))
	seen := make(map[string]bool)
	for _, token := range tokens {
		if len(token) < 3 || seen[token] {
			continue
		}
		seen[token] = true
		result = append(result, token)
	}
	return result
}

func evaWholePhraseContains(message, phrase string) bool {
	messageTokens := strings.Fields(message)
	phraseTokens := strings.Fields(phrase)
	if len(phraseTokens) == 0 || len(phraseTokens) > len(messageTokens) {
		return false
	}
	for start := 0; start <= len(messageTokens)-len(phraseTokens); start++ {
		matches := true
		for index := range phraseTokens {
			if messageTokens[start+index] != phraseTokens[index] {
				matches = false
				break
			}
		}
		if matches {
			return true
		}
	}
	return false
}

func evaLastLine(message string) string {
	lines := strings.Split(strings.ReplaceAll(message, "\r", ""), "\n")
	for index := len(lines) - 1; index >= 0; index-- {
		if line := strings.TrimSpace(lines[index]); line != "" {
			return line
		}
	}
	return ""
}

func evaAgeGender(message string) (string, int, bool) {
	for _, re := range []*regexp.Regexp{evaAgeGenderA, evaAgeGenderB} {
		match := re.FindStringSubmatch(message)
		if len(match) != 3 {
			continue
		}
		age := 0
		if _, err := fmt.Sscanf(match[2], "%d", &age); err == nil && age >= 13 && age <= 99 {
			return strings.ToLower(match[1]), age, true
		}
		if _, err := fmt.Sscanf(match[1], "%d", &age); err == nil && age >= 13 && age <= 99 {
			return strings.ToLower(match[2]), age, true
		}
	}
	match := evaAgeGenderC.FindStringSubmatch(message)
	if len(match) == 2 {
		age := 0
		if _, err := fmt.Sscanf(match[1], "%d", &age); err == nil && age >= 13 && age <= 99 {
			return "", age, true
		}
	}
	return "", 0, false
}

func (e *EVAChatEngine) countryFromText(message string) string {
	normalized := " " + evaNormalizeText(message) + " "
	keys := make([]string, 0, len(e.countries))
	for key := range e.countries {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool { return len(keys[i]) > len(keys[j]) })
	for _, key := range keys {
		if strings.Contains(normalized, " "+evaNormalizeText(key)+" ") {
			return e.countries[key]
		}
	}
	return ""
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func evaChatEnabled(cfg Config) bool {
	return cfg.ChatMode == model.ChatModeEVAChat && cfg.EVAChat != nil && cfg.EVAChat.Engine != nil
}

func evaChatSession(accountState *model.AccountState, userID string) model.EVAChatSession {
	if accountState.EVAChatSessions == nil {
		accountState.EVAChatSessions = make(map[string]model.EVAChatSession)
	}
	session, ok := accountState.EVAChatSessions[userID]
	if !ok {
		session = NewEVAChatSession()
	}
	return session
}

func evaChatStart(cfg Config, accountState *model.AccountState, userID string, rng *rand.Rand) EVAReplyResult {
	session := evaChatSession(accountState, userID)
	result := cfg.EVAChat.Engine.Start(&session, rng)
	accountState.EVAChatSessions[userID] = session
	return evaChatRender(cfg, accountState, userID, rng, result)
}

func evaChatReply(cfg Config, accountState *model.AccountState, userID, incoming string, rng *rand.Rand) EVAReplyResult {
	session := evaChatSession(accountState, userID)
	opts := EVAReplyOptions{HasUsernames: len(cfg.MessageUsernames) > 0}
	result := cfg.EVAChat.Engine.ReplyWithOptions(incoming, &session, rng, opts)
	accountState.EVAChatSessions[userID] = session
	return evaChatRender(cfg, accountState, userID, rng, result)
}

// evaChatRender fills %username% in an EVAChat reply with the same per-user
// snap/tg username assignment used by Single/Folder sequence modes, so the
// dashboard "snap/tg/user" counter and flagged-user tracking keep working.
func evaChatRender(cfg Config, accountState *model.AccountState, userID string, rng *rand.Rand, result EVAReplyResult) EVAReplyResult {
	if !result.HasUsername || !strings.Contains(result.Text, evaUsernamePlaceholder) {
		result.HasUsername = false
		return result
	}
	assigned := getOrAssignUsername(cfg, accountState, userID, rng)
	result.Text = renderMessageTemplate(result.Text, rng, assigned)
	result.HasUsername = assigned != "" && strings.Contains(result.Text, assigned)
	return result
}
