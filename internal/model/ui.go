package model

import "strings"

type BrowserOption struct {
	Key         string `json:"key"`
	Label       string `json:"label"`
	Description string `json:"description"`
}

type BrowserSettings struct {
	Profile  string `json:"profile"`
	Backend  string `json:"backend"`
	Headless bool   `json:"headless"`
}

const (
	ChatModeSingle = "single"
	ChatModeFolder = "folder"
	// ChatModeEVAChat is the third dashboard mode. It replaces the old
	// keyword-only responder with the stateful EVAChat Flow engine.
	ChatModeEVAChat = "eva_chat_flow"
	// ChatModeAPIChat is the fourth dashboard mode: the AI (OpenAI-compatible
	// API) writes every line and the apitest "director" decides the funnel
	// (interest tiers, hint / direct / no-share split, snap share from
	// data/api_chat/snap_share.txt). No TXT reply pools are used.
	ChatModeAPIChat = "api_chat"
	// ChatModeKeyword is retained only so existing saved settings can migrate
	// safely. It is never exposed as a selectable dashboard mode.
	ChatModeKeyword = "keyword"
)

type MessageSettings struct {
	ChatMode            string   `json:"chat_mode"`
	Sequence            string   `json:"sequence"`
	Usernames           string   `json:"usernames"`
	FirstMessage        string   `json:"first_message,omitempty"`
	ReplyMessage        string   `json:"reply_message,omitempty"`
	LoadedSequences     []string `json:"loaded_sequences,omitempty"`
	UseLoadedSequences  bool     `json:"use_loaded_sequences"`
	KeywordRulesContent string   `json:"keyword_rules_content,omitempty"`
	KeywordFilePath     string   `json:"keyword_file_path,omitempty"`
	// EVAChat Flow AI compose layer (OpenAI-compatible chat completions).
	// The key is stored only in the local data/ settings file.
	AIEnabled bool   `json:"ai_enabled,omitempty"`
	AIBaseURL string `json:"ai_base_url,omitempty"`
	AIAPIKey  string `json:"ai_api_key,omitempty"`
	AIModel   string `json:"ai_model,omitempty"`
	// API Chat mode (shares ai_base_url / ai_api_key / ai_model above).
	// APISettingsSaved marks that the funnel values below were saved at
	// least once; before that, zero values mean "use defaults".
	APISettingsSaved  bool `json:"api_settings_saved,omitempty"`
	APIShareHot       int  `json:"api_share_hot,omitempty"`
	APIShareWarm      int  `json:"api_share_warm,omitempty"`
	APIShareCold      int  `json:"api_share_cold,omitempty"`
	APINoSharePercent int  `json:"api_no_share_percent"`
	APIHintPercent    int  `json:"api_hint_percent"`
	APINoShareMin     int  `json:"api_no_share_min_replies,omitempty"`
	APINoShareMax     int  `json:"api_no_share_max_replies,omitempty"`
	APINoShareStrict  bool `json:"api_no_share_strict,omitempty"`
}

type ProxySettings struct {
	Enabled  bool   `json:"enabled"`
	Entries  string `json:"entries"`
	Address  string `json:"address,omitempty"`
	Username string `json:"username,omitempty"`
	Password string `json:"password,omitempty"`
}

type GeneralSettings struct {
	PollMinSeconds              int `json:"poll_min_seconds"`
	PollMaxSeconds              int `json:"poll_max_seconds"`
	ActionMinSeconds            int `json:"action_min_seconds"`
	ActionMaxSeconds            int `json:"action_max_seconds"`
	MatchReplyTimeoutMinSeconds int `json:"match_reply_timeout_min_seconds"`
	MatchReplyTimeoutMaxSeconds int `json:"match_reply_timeout_max_seconds"`
	MatchCloseWaitMinSeconds    int `json:"match_close_wait_min_seconds"`
	MatchCloseWaitMaxSeconds    int `json:"match_close_wait_max_seconds"`
	ReplyDelayMinSeconds        int `json:"reply_delay_min_seconds"`
	ReplyDelayMaxSeconds        int `json:"reply_delay_max_seconds"`
	// Live Wall (browser mirror of the Home screen). Zero values keep it
	// enabled on the default port so existing settings need no migration.
	LiveWallDisabled bool `json:"live_wall_disabled,omitempty"`
	LiveWallPort     int  `json:"live_wall_port,omitempty"`
	// Human pacing (opener policy + shift pacing). Zero values are replaced
	// by DefaultPacing() when loaded, so old settings need no migration.
	PacingDisabled       bool `json:"pacing_disabled,omitempty"`
	BotFirstPercent      int  `json:"bot_first_percent,omitempty"`
	OpenerWaitMinSeconds int  `json:"opener_wait_min_seconds,omitempty"`
	OpenerWaitMaxSeconds int  `json:"opener_wait_max_seconds,omitempty"`
	BotOpenDelayMinSecs  int  `json:"bot_open_delay_min_seconds,omitempty"`
	BotOpenDelayMaxSecs  int  `json:"bot_open_delay_max_seconds,omitempty"`
	ActiveMinMinutes     int  `json:"active_min_minutes,omitempty"`
	ActiveMaxMinutes     int  `json:"active_max_minutes,omitempty"`
	RestMinMinutes       int  `json:"rest_min_minutes,omitempty"`
	RestMaxMinutes       int  `json:"rest_max_minutes,omitempty"`
	ChatsPerHourMax      int  `json:"chats_per_hour_max,omitempty"`
	LimitCooldownMinutes int  `json:"limit_cooldown_minutes,omitempty"`
}

// PacingDefaults are the human-pacing values used when a field is 0.
// ChitChat throttles an account after roughly 40-60 minutes of non-stop
// chatting, so a shift is kept well below that.
type PacingDefaults struct {
	BotFirstPercent      int
	OpenerWaitMinSeconds int
	OpenerWaitMaxSeconds int
	BotOpenDelayMinSecs  int
	BotOpenDelayMaxSecs  int
	ActiveMinMinutes     int
	ActiveMaxMinutes     int
	RestMinMinutes       int
	RestMaxMinutes       int
	ChatsPerHourMax      int
	LimitCooldownMinutes int
}

func DefaultPacing() PacingDefaults {
	return PacingDefaults{
		BotFirstPercent:      30,
		OpenerWaitMinSeconds: 45,
		OpenerWaitMaxSeconds: 120,
		BotOpenDelayMinSecs:  8,
		BotOpenDelayMaxSecs:  25,
		ActiveMinMinutes:     25,
		ActiveMaxMinutes:     40,
		RestMinMinutes:       10,
		RestMaxMinutes:       25,
		ChatsPerHourMax:      30,
		LimitCooldownMinutes: 20,
	}
}

// ApplyPacingDefaults fills zero pacing fields and fixes inverted ranges.
func (g *GeneralSettings) ApplyPacingDefaults() {
	d := DefaultPacing()
	if g.BotFirstPercent <= 0 && g.OpenerWaitMinSeconds <= 0 && g.OpenerWaitMaxSeconds <= 0 {
		g.BotFirstPercent = d.BotFirstPercent
	}
	if g.BotFirstPercent > 100 {
		g.BotFirstPercent = 100
	}
	if g.OpenerWaitMinSeconds <= 0 {
		g.OpenerWaitMinSeconds = d.OpenerWaitMinSeconds
	}
	if g.OpenerWaitMaxSeconds < g.OpenerWaitMinSeconds {
		g.OpenerWaitMaxSeconds = max(g.OpenerWaitMinSeconds, d.OpenerWaitMaxSeconds)
	}
	if g.BotOpenDelayMinSecs <= 0 {
		g.BotOpenDelayMinSecs = d.BotOpenDelayMinSecs
	}
	if g.BotOpenDelayMaxSecs < g.BotOpenDelayMinSecs {
		g.BotOpenDelayMaxSecs = max(g.BotOpenDelayMinSecs, d.BotOpenDelayMaxSecs)
	}
	if g.ActiveMinMinutes <= 0 {
		g.ActiveMinMinutes = d.ActiveMinMinutes
	}
	if g.ActiveMaxMinutes < g.ActiveMinMinutes {
		g.ActiveMaxMinutes = max(g.ActiveMinMinutes, d.ActiveMaxMinutes)
	}
	if g.RestMinMinutes <= 0 {
		g.RestMinMinutes = d.RestMinMinutes
	}
	if g.RestMaxMinutes < g.RestMinMinutes {
		g.RestMaxMinutes = max(g.RestMinMinutes, d.RestMaxMinutes)
	}
	if g.ChatsPerHourMax <= 0 {
		g.ChatsPerHourMax = d.ChatsPerHourMax
	}
	if g.LimitCooldownMinutes <= 0 {
		g.LimitCooldownMinutes = d.LimitCooldownMinutes
	}
}

type HomeStats struct {
	BotAccounts     int `json:"bot_accounts"`
	RunningAccounts int `json:"running_accounts"`
	UserSendCount   int `json:"user_send_count"`
	DeadAccounts    int `json:"dead_accounts"`
	TrafficUsers    int `json:"traffic_users"`
	MessagesSent    int `json:"messages_sent"`
	SnapTgSentCount int `json:"snap_tg_sent_count"`
	SkippedAccounts int `json:"skipped_accounts"`
	UserFirstCount  int `json:"user_first_count"`
	BotFirstCount   int `json:"bot_first_count"`
	RestingAccounts int `json:"resting_accounts"`
}

type LogEntry struct {
	Time    string `json:"time"`
	Level   string `json:"level"`
	Message string `json:"message"`
}

type UISnapshot struct {
	Running        bool            `json:"running"`
	LicenseInvalid bool            `json:"license_invalid"`
	LicenseKey     string          `json:"license_key"`
	LicenseReason  string          `json:"license_reason"`
	Home           HomeStats       `json:"home"`
	Browser        BrowserSettings `json:"browser"`
	Message        MessageSettings `json:"message"`
	Proxy          ProxySettings   `json:"proxy"`
	General        GeneralSettings `json:"general"`
	BrowserOptions []BrowserOption `json:"browser_options"`
	Logs           []LogEntry      `json:"logs"`
}

func DefaultBrowserOptions() []BrowserOption {
	return []BrowserOption{
		{
			Key:         "rod",
			Label:       "Rod",
			Description: "Uses Rod for login capture with a persistent browser profile.",
		},
		{
			Key:         "chromedp",
			Label:       "ChromeDP",
			Description: "Uses Chrome DevTools Protocol through chromedp.",
		},
	}
}

func DefaultBrowserSettings() BrowserSettings {
	return BrowserSettings{
		Profile:  "default",
		Backend:  "rod",
		Headless: false,
	}
}

func DefaultMessageSettings() MessageSettings {
	return NewMessageSettings("hello how are you\nok i am checking. wait a bit.", "")
}

func DefaultProxySettings() ProxySettings {
	return ProxySettings{}
}

func DefaultGeneralSettings() GeneralSettings {
	g := GeneralSettings{
		PollMinSeconds:              20,
		PollMaxSeconds:              45,
		ActionMinSeconds:            20,
		ActionMaxSeconds:            45,
		MatchReplyTimeoutMinSeconds: 60,
		MatchReplyTimeoutMaxSeconds: 60,
		MatchCloseWaitMinSeconds:    0,
		MatchCloseWaitMaxSeconds:    0,
		ReplyDelayMinSeconds:        0,
		ReplyDelayMaxSeconds:        0,
	}
	g.ApplyPacingDefaults()
	return g
}

func NewMessageSettings(sequence string, usernames string) MessageSettings {
	settings := MessageSettings{
		ChatMode:  ChatModeSingle,
		Sequence:  NormalizeMessageSequence(sequence),
		Usernames: NormalizeUsernameList(usernames),
	}

	lines := settings.SequenceLines()
	if len(lines) > 0 {
		settings.FirstMessage = lines[0]
	}
	if len(lines) > 1 {
		settings.ReplyMessage = lines[1]
	}

	return settings
}

func (s MessageSettings) EffectiveChatMode() string {
	switch s.ChatMode {
	case ChatModeEVAChat:
		return ChatModeEVAChat
	case ChatModeAPIChat:
		return ChatModeAPIChat
	case ChatModeKeyword:
		// v1.0 stored the third mode as "keyword". Migrate it at read time
		// rather than leaving existing users with a dead mode after upgrade.
		return ChatModeEVAChat
	case ChatModeFolder:
		return ChatModeFolder
	case ChatModeSingle:
		return ChatModeSingle
	default:
		if s.UseLoadedSequences && len(s.LoadedSequences) > 0 {
			return ChatModeFolder
		}
		return ChatModeSingle
	}
}

func (s MessageSettings) SequenceLines() []string {
	return MessageSequenceLines(s.Sequence)
}

func (s MessageSettings) UsernameLines() []string {
	return UsernameLines(s.Usernames)
}

func MessageSequenceLines(sequence string) []string {
	normalized := NormalizeMessageSequence(sequence)
	if normalized == "" {
		return nil
	}
	return strings.Split(normalized, "\n")
}

func NormalizeMessageSequence(sequence string) string {
	return normalizeLineList(sequence)
}

func UsernameLines(usernames string) []string {
	normalized := NormalizeUsernameList(usernames)
	if normalized == "" {
		return nil
	}
	return strings.Split(normalized, "\n")
}

func NormalizeUsernameList(usernames string) string {
	return normalizeLineList(usernames)
}

func normalizeLineList(value string) string {
	lines := strings.Split(strings.ReplaceAll(value, "\r", ""), "\n")
	result := make([]string, 0, len(lines))

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		result = append(result, line)
	}

	return strings.Join(result, "\n")
}

func LegacyMessageSequence(firstMessage string, replyMessage string) string {
	lines := make([]string, 0, 2)

	firstMessage = strings.TrimSpace(firstMessage)
	if firstMessage != "" {
		lines = append(lines, firstMessage)
	}

	replyMessage = strings.TrimSpace(replyMessage)
	if replyMessage != "" {
		lines = append(lines, replyMessage)
	}

	return strings.Join(lines, "\n")
}
