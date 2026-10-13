package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"chitchatbot/internal/browser"

	"github.com/wailsapp/wails/v2/pkg/runtime"

	"chitchatbot/internal/bot"
	"chitchatbot/internal/license"
	"chitchatbot/internal/logwall"
	"chitchatbot/internal/model"
	"chitchatbot/internal/proxy"
	"chitchatbot/internal/store"
	"chitchatbot/internal/uistore"
	"chitchatbot/internal/workerplan"
)

type App struct {
	ctx            context.Context
	dataDir        string
	uiStore        *uistore.Store
	mu             sync.Mutex
	running        bool
	runID          uint64
	botCancel      context.CancelFunc
	activeWorkers  map[string]bool
	licenseKey     string
	licenseInvalid bool
	licenseReason  string
	licenseTimer   *time.Ticker
	licenseDone    chan bool
	liveWall       *logwall.Server
}

func NewApp(dataDir string) (*App, error) {
	uiStore, err := uistore.Open(dataDir)
	if err != nil {
		return nil, err
	}

	app := &App{
		dataDir:       dataDir,
		uiStore:       uiStore,
		activeWorkers: map[string]bool{},
	}

	if err := app.seedDefaults(); err != nil {
		_ = uiStore.Close()
		return nil, err
	}

	key, err := license.GenerateHardwareKey()
	if err != nil {
		app.licenseInvalid = true
		app.licenseReason = "Could not read hardware identifiers."
	} else {
		valid, reason := license.VerifyLicense(key)
		if !valid {
			app.licenseInvalid = true
			app.licenseKey = key
			app.licenseReason = reason
		} else {
			app.licenseKey = key
		}
	}

	return app, nil
}

func (a *App) startup(ctx context.Context) {
	a.ctx = ctx

	if a.licenseInvalid {
		runtime.WindowSetSize(a.ctx, 500, 350)
		runtime.WindowCenter(a.ctx)
		return
	}

	a.licenseTimer = time.NewTicker(24 * time.Hour)
	a.licenseDone = make(chan bool)

	go func() {
		for {
			select {
			case <-a.licenseDone:
				return
			case <-a.licenseTimer.C:
				v, r := license.VerifyLicense(a.licenseKey)
				if !v {
					a.mu.Lock()
					a.licenseInvalid = true
					a.licenseReason = r
					a.mu.Unlock()
					a.emit("ui:license_invalid", map[string]string{"key": a.licenseKey, "reason": r})
					runtime.WindowSetSize(a.ctx, 500, 350)
					runtime.WindowCenter(a.ctx)
					return
				}
			}
		}
	}()

	a.log("info", "Wails UI ready.")
}

func (a *App) shutdown(ctx context.Context) {
	_ = a.StopBot()
	a.mu.Lock()
	wall := a.liveWall
	a.mu.Unlock()
	if wall != nil {
		_ = wall.Close()
	}
	if a.licenseTimer != nil {
		a.licenseTimer.Stop()
	}
	if a.licenseDone != nil {
		close(a.licenseDone)
	}
	if a.uiStore != nil {
		_ = a.uiStore.Close()
	}
}

func (a *App) GetInitialState() (model.UISnapshot, error) {
	browserSettings, err := a.uiStore.LoadBrowserSettings()
	if err != nil {
		return model.UISnapshot{}, err
	}

	messageSettings, err := a.uiStore.LoadMessageSettings()
	if err != nil {
		return model.UISnapshot{}, err
	}

	proxySettings, err := a.uiStore.LoadProxySettings()
	if err != nil {
		return model.UISnapshot{}, err
	}

	generalSettings, err := a.uiStore.LoadGeneralSettings()
	if err != nil {
		return model.UISnapshot{}, err
	}

	logs, err := a.uiStore.ListLogs(200)
	if err != nil {
		return model.UISnapshot{}, err
	}

	a.mu.Lock()
	running := a.running
	runningAccounts := len(a.activeWorkers)
	isLicenseInvalid := a.licenseInvalid
	licenseKey := a.licenseKey
	licenseReason := a.licenseReason
	a.mu.Unlock()

	homeStats, err := a.homeStats(browserSettings, messageSettings, runningAccounts)
	if err != nil {
		return model.UISnapshot{}, err
	}

	return model.UISnapshot{
		Running:        running,
		LicenseInvalid: isLicenseInvalid,
		LicenseKey:     licenseKey,
		LicenseReason:  licenseReason,
		Home:           homeStats,
		Browser:        browserSettings,
		Message:        messageSettings,
		Proxy:          proxySettings,
		General:        generalSettings,
		BrowserOptions: model.DefaultBrowserOptions(),
		Logs:           logs,
	}, nil
}

func (a *App) homeStats(
	browserSettings model.BrowserSettings,
	messageSettings model.MessageSettings,
	runningAccounts int,
) (model.HomeStats, error) {
	workers, err := workerplan.DiscoverProfileWorkers(a.dataDir, browserSettings.Profile)
	if err != nil {
		return model.HomeStats{}, err
	}

	accountCounts, err := bot.BotAccountStatusCounts(a.dataDir)
	if err != nil {
		return model.HomeStats{}, err
	}

	counters, err := a.countStateCounters()
	if err != nil {
		return model.HomeStats{}, err
	}

	botAccounts := len(workers)
	if runningAccounts+accountCounts.Dead > botAccounts {
		botAccounts = runningAccounts + accountCounts.Dead
	}

	return model.HomeStats{
		BotAccounts:     botAccounts,
		RunningAccounts: runningAccounts,
		UserSendCount:   len(messageSettings.UsernameLines()),
		DeadAccounts:    accountCounts.Dead,
		TrafficUsers:    counters.TrafficUsers,
		MessagesSent:    counters.MessagesSent,
		SnapTgSentCount: counters.SnapTgSentCount,
		SkippedAccounts: counters.SkippedAccounts,
		UserFirstCount:  counters.UserFirstCount,
		BotFirstCount:   counters.BotFirstCount,
		RestingAccounts: bot.RestingWorkerCount(),
	}, nil
}

func pacingFromSettings(g model.GeneralSettings) bot.Pacing {
	g.ApplyPacingDefaults()
	sec := func(v int) time.Duration { return time.Duration(v) * time.Second }
	mins := func(v int) time.Duration { return time.Duration(v) * time.Minute }
	return bot.Pacing{
		Enabled:         !g.PacingDisabled,
		BotFirstPercent: g.BotFirstPercent,
		OpenerWaitMin:   sec(g.OpenerWaitMinSeconds),
		OpenerWaitMax:   sec(g.OpenerWaitMaxSeconds),
		BotOpenDelayMin: sec(g.BotOpenDelayMinSecs),
		BotOpenDelayMax: sec(g.BotOpenDelayMaxSecs),
		ActiveMin:       mins(g.ActiveMinMinutes),
		ActiveMax:       mins(g.ActiveMaxMinutes),
		RestMin:         mins(g.RestMinMinutes),
		RestMax:         mins(g.RestMaxMinutes),
		ChatsPerHourMax: g.ChatsPerHourMax,
		LimitCooldown:   mins(g.LimitCooldownMinutes),
	}
}

// stateCounters are the Home counters that live in the per-profile
// bot_state.json files; they are summed in a single pass.
type stateCounters struct {
	MessagesSent    int
	TrafficUsers    int
	SnapTgSentCount int
	SkippedAccounts int
	UserFirstCount  int
	BotFirstCount   int
}

func (a *App) countStateCounters() (stateCounters, error) {
	var c stateCounters
	statePaths, err := filepath.Glob(filepath.Join(a.dataDir, "profiles", "*", "bot_state.json"))
	if err != nil {
		return c, err
	}

	counts := map[string]int{}
	uniqueUsers := map[string]bool{}
	for _, statePath := range statePaths {
		var state model.BotState
		ok, err := store.LoadJSON(statePath, &state)
		if err != nil {
			return c, err
		}
		if !ok {
			continue
		}

		for accountID, accountState := range state {
			for userID, count := range accountState.SentMessageCounts {
				if count <= 0 {
					continue
				}

				key := accountID + "\x00" + userID
				if count > counts[key] {
					counts[key] = count
				}
				uniqueUsers[userID] = true
			}
			c.SnapTgSentCount += accountState.SnapTgSentCount
			c.SkippedAccounts += accountState.SkippedUsersCount
			c.UserFirstCount += accountState.UserFirstCount
			c.BotFirstCount += accountState.BotFirstCount
		}
	}

	for _, count := range counts {
		c.MessagesSent += count
	}
	c.TrafficUsers = len(uniqueUsers)
	return c, nil
}

func (a *App) setWorkerRunning(profile string, running bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.activeWorkers == nil {
		a.activeWorkers = map[string]bool{}
	}
	if running {
		a.activeWorkers[profile] = true
		return
	}
	delete(a.activeWorkers, profile)
}

func (a *App) clearActiveWorkers() {
	a.mu.Lock()
	a.activeWorkers = map[string]bool{}
	a.mu.Unlock()
}

func (a *App) SaveBrowserSettings(settings model.BrowserSettings) error {
	if strings.TrimSpace(settings.Profile) == "" {
		settings.Profile = "default"
	}
	if err := a.uiStore.SaveBrowserSettings(settings); err != nil {
		return err
	}
	a.log("info", fmt.Sprintf("Browser settings saved. Backend=%s Profile=%s", settings.Backend, settings.Profile))
	return nil
}

func (a *App) RunFreshBrowsers(count int) error {
	if count < 1 {
		return fmt.Errorf("fresh browser count must be at least 1")
	}
	if count > 5 {
		return fmt.Errorf("fresh browser count cannot be more than 5")
	}

	browserSettings, err := a.uiStore.LoadBrowserSettings()
	if err != nil {
		return err
	}

	proxyRotator, proxyCount, err := a.loadProxyRotator()
	if err != nil {
		return err
	}

	profileName := safeProfileName(browserSettings.Profile)
	profileDir := filepath.Join(a.dataDir, "profiles", profileName)
	if err := store.EnsureDir(profileDir); err != nil {
		return err
	}

	a.log("info", fmt.Sprintf(
		"Starting %d fresh %s browser(s) for profile '%s'.",
		count,
		browserSettings.Backend,
		profileName,
	))
	a.log("info", fmt.Sprintf("Fresh login JSON files will be saved under %s", profileDir))
	if proxyCount > 0 {
		a.log("info", fmt.Sprintf("Fresh browsers will use %d proxy line(s) in rotation.", proxyCount))
	}

	for i := 1; i <= count; i++ {
		index := i
		var selectedProxy *proxy.Entry
		if proxyRotator != nil {
			if nextProxy, ok := proxyRotator.Next(); ok {
				selectedProxy = &nextProxy
			}
		}
		go a.runFreshBrowser(profileDir, profileName, browserSettings, selectedProxy, index, count)
	}

	return nil
}

func (a *App) SaveMessageSettings(settings model.MessageSettings) error {
	existing, _ := a.uiStore.LoadMessageSettings()
	if len(settings.LoadedSequences) == 0 && len(existing.LoadedSequences) > 0 {
		settings.LoadedSequences = existing.LoadedSequences
	}
	// Keyword fields are retained in the stored JSON only for backward
	// compatibility. The selectable third mode now uses EVAChat Flow.
	switch settings.ChatMode {
	case model.ChatModeEVAChat, model.ChatModeKeyword:
		settings.ChatMode = model.ChatModeEVAChat
		settings.UseLoadedSequences = false
	case model.ChatModeAPIChat:
		settings.UseLoadedSequences = false
	case model.ChatModeFolder:
		settings.UseLoadedSequences = true
	case model.ChatModeSingle:
		settings.UseLoadedSequences = false
	default:
		if settings.UseLoadedSequences && len(settings.LoadedSequences) > 0 {
			settings.ChatMode = model.ChatModeFolder
			settings.UseLoadedSequences = true
		} else {
			settings.ChatMode = model.ChatModeSingle
			settings.UseLoadedSequences = false
		}
	}
	if err := a.uiStore.SaveMessageSettings(settings); err != nil {
		return err
	}
	a.log("info", fmt.Sprintf("Message settings saved. Mode=%s", settings.EffectiveChatMode()))
	return nil
}

func (a *App) SaveProxySettings(settings model.ProxySettings) error {
	if settings.Enabled {
		entries, err := proxy.ParseList(settings.Entries)
		if err != nil {
			return err
		}
		if len(entries) == 0 {
			return fmt.Errorf("proxy list is enabled but empty")
		}
	}

	if err := a.uiStore.SaveProxySettings(settings); err != nil {
		return err
	}

	if settings.Enabled {
		entries, _ := proxy.ParseList(settings.Entries)
		a.log("info", fmt.Sprintf("Proxy settings saved. %d proxy line(s) enabled for Rod, ChromeDP, and Go request rotation.", len(entries)))
	} else {
		a.log("info", "Proxy settings saved. Proxy use is disabled.")
	}
	return nil
}

func (a *App) SaveGeneralSettings(settings model.GeneralSettings) error {
	if err := a.uiStore.SaveGeneralSettings(settings); err != nil {
		return err
	}
	settings.ApplyPacingDefaults()
	a.log("info", fmt.Sprintf(
		"Timing settings saved. Poll=%ds-%ds Action=%ds-%ds | Pacing %s: bot-first %d%%, user wait %d-%ds, shift %d-%dmin / rest %d-%dmin, %d chats/h",
		settings.PollMinSeconds,
		settings.PollMaxSeconds,
		settings.ActionMinSeconds,
		settings.ActionMaxSeconds,
		map[bool]string{true: "off", false: "on"}[settings.PacingDisabled],
		settings.BotFirstPercent,
		settings.OpenerWaitMinSeconds, settings.OpenerWaitMaxSeconds,
		settings.ActiveMinMinutes, settings.ActiveMaxMinutes,
		settings.RestMinMinutes, settings.RestMaxMinutes,
		settings.ChatsPerHourMax,
	))
	return nil
}

func (a *App) ClearLogs() error {
	if err := a.uiStore.ClearLogs(); err != nil {
		return err
	}
	a.emit("ui:logs_cleared", true)
	if wall := a.currentLiveWall(); wall != nil {
		wall.LogsCleared()
	}
	a.log("info", "Logs cleared.")
	return nil
}

func (a *App) ClearLoadedMessages() error {
	settings, err := a.uiStore.LoadMessageSettings()
	if err != nil {
		return err
	}
	settings.LoadedSequences = nil
	settings.UseLoadedSequences = false
	if settings.ChatMode == model.ChatModeFolder {
		settings.ChatMode = model.ChatModeSingle
	}
	return a.uiStore.SaveMessageSettings(settings)
}

func (a *App) LoadMessageFolder() ([]string, error) {
	dir, err := runtime.OpenDirectoryDialog(a.ctx, runtime.OpenDialogOptions{
		Title: "Select Folder with Chat Patterns",
	})
	if err != nil {
		return nil, err
	}
	if dir == "" {
		return nil, nil
	}

	files, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}

	var sequences []string
	for _, f := range files {
		if !f.IsDir() && strings.HasSuffix(strings.ToLower(f.Name()), ".txt") {
			content, err := os.ReadFile(filepath.Join(dir, f.Name()))
			if err != nil {
				continue
			}
			seq := strings.TrimSpace(string(content))
			if seq != "" {
				sequences = append(sequences, seq)
			}
		}
	}

	if len(sequences) == 0 {
		return nil, errors.New("no valid .txt files found in the selected folder")
	}

	settings, err := a.uiStore.LoadMessageSettings()
	if err != nil {
		return nil, err
	}

	settings.ChatMode = model.ChatModeFolder
	settings.LoadedSequences = sequences
	settings.UseLoadedSequences = true
	if err := a.uiStore.SaveMessageSettings(settings); err != nil {
		return nil, err
	}

	return sequences, nil
}

// OpenEVAChatDataFolder bootstraps the editable EVAChat data pack, validates
// it, and opens the local folder. It never imports arbitrary keyword files.
func (a *App) OpenEVAChatDataFolder() (string, error) {
	dataRoot, err := bot.EnsureEVAChatData(a.dataDir)
	if err != nil {
		return "", err
	}
	engine, err := bot.LoadEVAChatEngine(dataRoot)
	if err != nil {
		return "", err
	}
	if err := exec.Command("explorer.exe", dataRoot).Start(); err != nil {
		return "", err
	}
	summary := fmt.Sprintf("EVAChat data folder ready: %s (%d trigger categories, %d reply pools)", dataRoot, len(engine.InputCategories()), len(engine.OutputPools()))
	a.log("info", summary)
	return summary, nil
}

// OpenAPIChatDataFolder seeds data/api_chat/ (persona.txt, snap_share.txt,
// snap_hint.txt) and opens it in Explorer.
func (a *App) OpenAPIChatDataFolder() (string, error) {
	dataRoot, err := bot.EnsureAPIChatData(a.dataDir)
	if err != nil {
		return "", err
	}
	summary := fmt.Sprintf("API Chat data folder ready: %s (config_rules.json, persona.txt, snap_share.txt, snap_hint.txt)", dataRoot)
	a.log("info", summary)
	if openErr := exec.Command("explorer.exe", dataRoot).Start(); openErr != nil {
		a.log("warn", fmt.Sprintf("Could not open folder: %v", openErr))
	}
	return summary, nil
}

// TestAPIChat sends one small request through the API Chat engine (unsaved
// panel values are used, so the owner can test before saving).
func (a *App) TestAPIChat(settings model.MessageSettings) (string, error) {
	dataRoot, err := bot.EnsureAPIChatData(a.dataDir)
	if err != nil {
		return "", err
	}
	engine, err := bot.LoadAPIChat(dataRoot, settings)
	if err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	answer, err := engine.Test(ctx)
	if err != nil {
		a.log("error", fmt.Sprintf("API Chat test failed: %v", err))
		return "", err
	}
	summary := fmt.Sprintf("API OK -> %q | %s", answer, engine.Describe())
	for _, w := range engine.Warns {
		summary += " | WARN: " + w
	}
	a.log("info", summary)
	return summary, nil
}

// TestEVAChatAI sends one small request to the configured AI API (unsaved
// dashboard values are used) and returns the model's answer.
func (a *App) TestEVAChatAI(settings model.MessageSettings) (string, error) {
	settings.AIEnabled = true
	dataRoot, err := bot.EnsureEVAChatData(a.dataDir)
	if err != nil {
		return "", err
	}
	ai, err := bot.LoadEVAAI(dataRoot, settings)
	if err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	answer, err := ai.Test(ctx)
	if err != nil {
		a.log("error", fmt.Sprintf("EVAChat AI test failed: %v", err))
		return "", err
	}
	summary := fmt.Sprintf("AI OK (%s / %s) -> %q", ai.BaseURL, ai.Model, answer)
	a.log("info", summary)
	return summary, nil
}

func (a *App) CleanProfiles() error {
	a.mu.Lock()
	running := a.running
	a.mu.Unlock()

	if running {
		return fmt.Errorf("stop the bot before cleaning profile data")
	}

	if err := a.removeProfileData(); err != nil {
		return err
	}

	a.clearActiveWorkers()

	a.log("info", "Profile data cleaned.")
	return nil
}

func (a *App) StartBot() error {
	a.mu.Lock()
	if a.running {
		a.mu.Unlock()
		a.log("info", "Bot is already running.")
		return nil
	}

	ctx, cancel := context.WithCancel(context.Background())
	a.runID++
	runID := a.runID
	a.running = true
	a.botCancel = cancel
	a.mu.Unlock()

	a.emitStatus(true)
	a.log("activity", "bot started wait to process")
	go a.autoOpenLiveWall()

	configs, err := a.buildBotConfigs()
	if err != nil {
		a.mu.Lock()
		a.running = false
		a.botCancel = nil
		if a.runID == runID {
			a.runID++
		}
		a.mu.Unlock()
		a.emitStatus(false)
		return err
	}

	a.log("info", fmt.Sprintf("Starting %d worker(s) across all discovered profiles.", len(configs)))

	var wg sync.WaitGroup
	wg.Add(len(configs))

	for _, cfg := range configs {
		workerCfg := cfg
		profileName := workerCfg.Profile
		a.setWorkerRunning(profileName, true)
		go func() {
			defer func() {
				a.setWorkerRunning(profileName, false)
				wg.Done()
			}()

			err := bot.Run(ctx, workerCfg)
			if errors.Is(err, context.Canceled) {
				a.log("info", fmt.Sprintf("[%s] Worker stopped.", profileName))
				return
			}
			if err != nil {
				a.log("error", fmt.Sprintf("[%s] Worker stopped with error: %v", profileName, err))
				return
			}
			a.log("info", fmt.Sprintf("[%s] Worker finished.", profileName))
		}()
	}

	go func() {
		wg.Wait()

		a.mu.Lock()
		if a.runID == runID {
			a.running = false
			a.botCancel = nil
			a.runID++
			a.mu.Unlock()
			a.log("info", "All bot workers stopped.")
			a.emitStatus(false)
			return
		}
		a.mu.Unlock()
	}()

	return nil
}

func (a *App) StopBot() error {
	a.mu.Lock()
	cancel := a.botCancel
	if cancel == nil {
		a.mu.Unlock()
		return nil
	}
	a.botCancel = nil
	a.running = false
	a.mu.Unlock()

	cancel()
	a.clearActiveWorkers()
	bot.ClearAllWorkerPhases()
	a.log("info", "Stopping all bot workers.")
	a.emitStatus(false)
	return nil
}

func (a *App) seedDefaults() error {
	if _, err := a.uiStore.LoadBrowserSettings(); err != nil {
		return err
	}
	if _, err := a.uiStore.LoadMessageSettings(); err != nil {
		return err
	}
	if _, err := a.uiStore.LoadProxySettings(); err != nil {
		return err
	}
	if _, err := a.uiStore.LoadGeneralSettings(); err != nil {
		return err
	}
	return nil
}

func (a *App) removeProfileData() error {
	dataDir, err := filepath.Abs(a.dataDir)
	if err != nil {
		return err
	}

	targets := []string{
		filepath.Join(a.dataDir, "profiles"),
		filepath.Join(a.dataDir, "tmp"),
		filepath.Join(a.dataDir, "fresh-runs"),
		filepath.Join(a.dataDir, "bot_registry.json"),
	}

	for _, target := range targets {
		targetAbs, err := filepath.Abs(target)
		if err != nil {
			return err
		}
		if !pathInside(dataDir, targetAbs) {
			return fmt.Errorf("refusing to clean path outside data directory: %s", targetAbs)
		}
		if err := os.RemoveAll(targetAbs); err != nil {
			return err
		}
	}

	return nil
}

func pathInside(parent string, child string) bool {
	parent = filepath.Clean(parent)
	child = filepath.Clean(child)
	if parent == child {
		return false
	}
	return strings.HasPrefix(child, parent+string(os.PathSeparator))
}

func (a *App) buildBotConfigs() ([]bot.Config, error) {
	browserSettings, err := a.uiStore.LoadBrowserSettings()
	if err != nil {
		return nil, err
	}

	messageSettings, err := a.uiStore.LoadMessageSettings()
	if err != nil {
		return nil, err
	}

	generalSettings, err := a.uiStore.LoadGeneralSettings()
	if err != nil {
		return nil, err
	}

	proxyEntries, err := a.loadProxyEntries()
	if err != nil {
		return nil, err
	}

	var evaChatConfig *bot.EVAChatConfig
	if messageSettings.EffectiveChatMode() == model.ChatModeEVAChat {
		dataRoot, err := bot.EnsureEVAChatData(a.dataDir)
		if err != nil {
			return nil, err
		}
		engine, err := bot.LoadEVAChatEngine(dataRoot)
		if err != nil {
			return nil, err
		}
		ai, err := bot.LoadEVAAI(dataRoot, messageSettings)
		if err != nil {
			return nil, err
		}
		evaChatConfig = &bot.EVAChatConfig{DataRoot: dataRoot, Engine: engine, AI: ai}
		a.log("info", fmt.Sprintf("EVAChat Flow ready: %d trigger categories and %d reply pools loaded from %s. %s", len(engine.InputCategories()), len(engine.OutputPools()), dataRoot, ai.Describe()))
	}
	if messageSettings.EffectiveChatMode() == model.ChatModeAPIChat {
		dataRoot, err := bot.EnsureAPIChatData(a.dataDir)
		if err != nil {
			return nil, err
		}
		apiEngine, err := bot.LoadAPIChat(dataRoot, messageSettings)
		if err != nil {
			return nil, fmt.Errorf("API Chat: %w (Message -> API Chat -> fill base URL, model and API key)", err)
		}
		for _, w := range apiEngine.Warns {
			a.log("warn", "API Chat: "+w)
		}
		evaChatConfig = &bot.EVAChatConfig{DataRoot: dataRoot, API: apiEngine}
		a.log("info", fmt.Sprintf("%s | files: %s", apiEngine.Describe(), dataRoot))
	}

	profiles, err := workerplan.DiscoverProfileWorkers(a.dataDir, browserSettings.Profile)
	if err != nil {
		return nil, err
	}

	assignments := workerplan.AssignProfileWorkers(profiles, proxyEntries)
	configs := make([]bot.Config, 0, len(assignments))

	for _, assignment := range assignments {
		cfg := bot.DefaultConfig(
			a.dataDir,
			assignment.Profile,
			browserSettings.Backend,
			browserSettings.Headless,
		)
		cfg.LoginInfoPath = assignment.LoginInfoPath
		cfg.ChatMode = messageSettings.EffectiveChatMode()
		if cfg.ChatMode == model.ChatModeEVAChat || cfg.ChatMode == model.ChatModeAPIChat {
			cfg.EVAChat = evaChatConfig
			// EVAChat has its own persisted stage state. Keep MessageSequence
			// empty so sequence code cannot accidentally drive this mode.
			cfg.MessageSequence = nil
			cfg.AvailableSequences = nil
		} else if cfg.ChatMode == model.ChatModeFolder && len(messageSettings.LoadedSequences) > 0 {
			cfg.AvailableSequences = make([][]string, len(messageSettings.LoadedSequences))
			for i, seqStr := range messageSettings.LoadedSequences {
				cfg.AvailableSequences[i] = model.MessageSequenceLines(seqStr)
			}
		} else {
			cfg.AvailableSequences = [][]string{messageSettings.SequenceLines()}
		}
		cfg.MessageUsernames = messageSettings.UsernameLines()
		cfg.MatchWhenIdle = true
		cfg.PollMin = time.Duration(generalSettings.PollMinSeconds) * time.Second
		cfg.PollMax = time.Duration(generalSettings.PollMaxSeconds) * time.Second
		cfg.ActionMin = time.Duration(generalSettings.ActionMinSeconds) * time.Second
		cfg.ActionMax = time.Duration(generalSettings.ActionMaxSeconds) * time.Second
		cfg.MatchReplyTimeoutMin = time.Duration(generalSettings.MatchReplyTimeoutMinSeconds) * time.Second
		cfg.MatchReplyTimeoutMax = time.Duration(generalSettings.MatchReplyTimeoutMaxSeconds) * time.Second
		cfg.MatchCloseWaitMin = time.Duration(generalSettings.MatchCloseWaitMinSeconds) * time.Second
		cfg.MatchCloseWaitMax = time.Duration(generalSettings.MatchCloseWaitMaxSeconds) * time.Second
		cfg.ReplyDelayMin = time.Duration(generalSettings.ReplyDelayMinSeconds) * time.Second
		cfg.ReplyDelayMax = time.Duration(generalSettings.ReplyDelayMaxSeconds) * time.Second
		cfg.Pacing = pacingFromSettings(generalSettings)

		profileName := assignment.Profile
		proxyLabel := "direct"
		if assignment.HasProxy {
			assignedProxy := assignment.Proxy
			cfg.ProxyRotator = proxy.NewRotator([]proxy.Entry{assignedProxy})
			proxyLabel = assignedProxy.Label()
		}

		cfg.Logf = func(format string, args ...any) {
			message := fmt.Sprintf(strings.TrimSuffix(format, "\n"), args...)
			a.log("info", fmt.Sprintf("[%s] %s", profileName, message))
		}
		cfg.Activityf = func(format string, args ...any) {
			message := fmt.Sprintf(strings.TrimSuffix(format, "\n"), args...)
			a.log("activity", message)
		}

		if assignment.LoginInfoPath != "" {
			a.log("info", fmt.Sprintf("Prepared worker for user/profile %s using %s from %s.", profileName, proxyLabel, assignment.LoginInfoPath))
		} else {
			a.log("info", fmt.Sprintf("Prepared worker for profile %s using %s.", profileName, proxyLabel))
		}
		configs = append(configs, cfg)
	}

	return configs, nil
}

func safeProfileName(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return "default"
	}
	name = strings.ReplaceAll(name, "..", "_")
	name = strings.NewReplacer("\\", "_", "/", "_", ":", "_", "*", "_", "?", "_", "\"", "_", "<", "_", ">", "_", "|", "_").Replace(name)
	return name
}

func (a *App) runFreshBrowser(profileDir string, profileName string, settings model.BrowserSettings, selectedProxy *proxy.Entry, index int, total int) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Keep disposable browser data outside the repo so wails dev file watching
	// doesn't restart the app when a fresh browser run creates a new directory.
	browserTempDir, err := os.MkdirTemp("", fmt.Sprintf("chitchat-fresh-browser-%s-%02d-", profileName, index))
	if err != nil {
		a.log("error", fmt.Sprintf("[%s fresh %d/%d] Failed to create temp browser dir: %v", profileName, index, total, err))
		return
	}

	userDataDir := filepath.Join(browserTempDir, "user-data")
	if err := store.EnsureDir(userDataDir); err != nil {
		a.log("error", fmt.Sprintf("[%s fresh %d/%d] Failed to create temp browser dir: %v", profileName, index, total, err))
		return
	}

	backend, err := browser.New(settings.Backend)
	if err != nil {
		a.log("error", fmt.Sprintf("[%s fresh %d/%d] Failed to create browser backend: %v", profileName, index, total, err))
		return
	}
	defer func() {
		_ = backend.Close()
		a.removeFreshBrowserData(browserTempDir, profileName, index, total)
	}()

	a.log("info", fmt.Sprintf(
		"[%s fresh %d/%d] Opening brand-new %s browser. Old profile data is not used.",
		profileName,
		index,
		total,
		settings.Backend,
	))
	if selectedProxy != nil {
		a.log("info", fmt.Sprintf("[%s fresh %d/%d] Using proxy %s", profileName, index, total, selectedProxy.Label()))
	}

	if err := backend.Start(ctx, browser.Options{
		StartURL:            "https://app.chitchat.gg/",
		UserDataDir:         userDataDir,
		Headless:            settings.Headless,
		DisablePageRecovery: true,
		Proxy:               selectedProxy,
	}); err != nil {
		a.log("error", fmt.Sprintf("[%s fresh %d/%d] Browser start failed: %v", profileName, index, total, err))
		return
	}

	login, err := a.captureFreshLogin(ctx, backend, settings.Backend, profileName, index, total)
	if err != nil {
		if errors.Is(err, browser.ErrPageUnavailable) || errors.Is(err, context.Canceled) {
			a.log("info", fmt.Sprintf("[%s fresh %d/%d] Fresh browser was closed before login was saved.", profileName, index, total))
			return
		}
		a.log("error", fmt.Sprintf("[%s fresh %d/%d] Fresh login capture failed: %v", profileName, index, total, err))
		return
	}

	login.Profile = profileName
	login.Backend = settings.Backend
	login.UserID = login.CurrentUserID()
	login.SavedAt = time.Now().UTC()
	loginPath := a.freshLoginInfoPath(profileDir, login, index)

	if err := store.SaveJSON(loginPath, login); err != nil {
		a.log("error", fmt.Sprintf("[%s fresh %d/%d] Failed to save login info: %v", profileName, index, total, err))
		return
	}

	a.log("info", fmt.Sprintf(
		"[%s fresh %d/%d] Login saved to %s. Temporary browser data will be removed after close.",
		profileName,
		index,
		total,
		loginPath,
	))
}

func (a *App) captureFreshLogin(
	ctx context.Context,
	backend browser.Backend,
	backendName string,
	profileName string,
	index int,
	total int,
) (model.LoginInfo, error) {
	waitingForTokenLogged := false
	waitingForSessionLogged := false

	for {
		login, err := backend.CaptureSession(ctx)
		if err != nil {
			return model.LoginInfo{}, err
		}

		token := login.Token()
		if token != "" {
			ready, authErr := browser.SessionAuthenticated(ctx, backend, "https://api.chitchat.gg")
			if authErr == nil && ready {
				login.UserID = login.CurrentUserID()
				if login.UserID == "" {
					return model.LoginInfo{}, fmt.Errorf("login detected, but current bot user ID could not be read from token or browser storage")
				}
				return login, nil
			}
		}

		if token == "" && !waitingForTokenLogged {
			a.log("info", fmt.Sprintf(
				"[%s fresh %d/%d] Waiting for login token in the new %s browser window.",
				profileName,
				index,
				total,
				backendName,
			))
			waitingForTokenLogged = true
		}

		if token != "" && !waitingForSessionLogged {
			a.log("info", fmt.Sprintf(
				"[%s fresh %d/%d] Token detected, but the session is still unauthorized. Finish the full login in the %s browser window.",
				profileName,
				index,
				total,
				backendName,
			))
			waitingForSessionLogged = true
		}

		select {
		case <-ctx.Done():
			return model.LoginInfo{}, ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
}

func (a *App) freshLoginInfoPath(loginDir string, login model.LoginInfo, index int) string {
	userID := sanitizeFreshFileSegment(login.CurrentUserID())
	if userID == "" {
		userID = fmt.Sprintf("browser-%02d", index)
	}

	stamp := fmt.Sprintf("%s-%09d", login.SavedAt.UTC().Format("20060102-150405"), login.SavedAt.UTC().Nanosecond())
	filename := fmt.Sprintf("login_info_%s_%s_%02d.json", userID, stamp, index)
	return filepath.Join(loginDir, filename)
}

func sanitizeFreshFileSegment(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}

	value = strings.ReplaceAll(value, "..", "_")
	value = strings.NewReplacer("\\", "_", "/", "_", ":", "_", "*", "_", "?", "_", "\"", "_", "<", "_", ">", "_", "|", "_", " ", "_").Replace(value)
	return strings.Trim(value, "._")
}

func (a *App) removeFreshBrowserData(tempDir string, profileName string, index int, total int) {
	var lastErr error
	for attempt := 0; attempt < 5; attempt++ {
		if err := os.RemoveAll(tempDir); err == nil {
			a.log("info", fmt.Sprintf(
				"[%s fresh %d/%d] Removed temporary browser data from %s",
				profileName,
				index,
				total,
				tempDir,
			))
			return
		} else {
			lastErr = err
			time.Sleep(300 * time.Millisecond)
		}
	}

	if lastErr != nil {
		a.log("warn", fmt.Sprintf(
			"[%s fresh %d/%d] Could not remove temporary browser data from %s: %v",
			profileName,
			index,
			total,
			tempDir,
			lastErr,
		))
	}
}

func (a *App) loadProxyRotator() (*proxy.Rotator, int, error) {
	entries, err := a.loadProxyEntries()
	if err != nil {
		return nil, 0, err
	}
	if len(entries) == 0 {
		return nil, 0, nil
	}
	return proxy.NewRotator(entries), len(entries), nil
}

func (a *App) loadProxyEntries() ([]proxy.Entry, error) {
	proxySettings, err := a.uiStore.LoadProxySettings()
	if err != nil {
		return nil, err
	}
	if !proxySettings.Enabled {
		return nil, nil
	}

	entries, err := proxy.ParseList(proxySettings.Entries)
	if err != nil {
		return nil, err
	}
	if len(entries) == 0 {
		return nil, fmt.Errorf("proxy list is enabled but empty")
	}

	return entries, nil
}

func (a *App) emitStatus(running bool) {
	a.emit("ui:running", running)
	if wall := a.currentLiveWall(); wall != nil {
		wall.SetRunning(running)
	}
}

func (a *App) emit(name string, data any) {
	if a.ctx == nil {
		return
	}
	runtime.EventsEmit(a.ctx, name, data)
}

func (a *App) log(level string, message string) {
	entry, err := a.uiStore.AppendLog(level, message)
	if err == nil {
		a.emit("ui:log", entry)
		if wall := a.currentLiveWall(); wall != nil {
			wall.Publish(entry)
		}
	}
}
