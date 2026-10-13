package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"chitchatbot/internal/logwall"
	"chitchatbot/internal/model"
	"github.com/wailsapp/wails/v2/pkg/runtime"
)

// Live Wall = the dashboard Home screen (8 counters + chat activity log)
// mirrored to a local-only web page (http://127.0.0.1:<port>/) that any PC
// browser can show full-screen with auto-rotating sci-fi themes. The page
// receives exactly the same LogEntry / HomeStats values the dashboard uses;
// nothing else from the data folder is exposed.

const liveWallFolderName = "logwall"

func (a *App) liveWallDir() string {
	return filepath.Join(a.dataDir, liveWallFolderName)
}

func (a *App) currentLiveWall() *logwall.Server {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.liveWall
}

// liveWallStats computes the Home counters exactly like GetInitialState does.
func (a *App) liveWallStats() (model.HomeStats, error) {
	browserSettings, err := a.uiStore.LoadBrowserSettings()
	if err != nil {
		return model.HomeStats{}, err
	}
	messageSettings, err := a.uiStore.LoadMessageSettings()
	if err != nil {
		return model.HomeStats{}, err
	}
	a.mu.Lock()
	runningAccounts := len(a.activeWorkers)
	a.mu.Unlock()
	return a.homeStats(browserSettings, messageSettings, runningAccounts)
}

// ensureLiveWall creates (once) and starts the local server, returning its URL.
func (a *App) ensureLiveWall() (string, error) {
	settings, err := a.uiStore.LoadGeneralSettings()
	if err != nil {
		return "", err
	}
	_ = os.MkdirAll(a.liveWallDir(), 0o755)

	a.mu.Lock()
	if a.liveWall == nil {
		a.liveWall = logwall.New(logwall.Options{
			Port:          settings.LiveWallPort,
			BackgroundDir: a.liveWallDir(),
			Stats:         a.liveWallStats,
			History:       a.uiStore.ListLogs,
		})
	}
	wall := a.liveWall
	a.mu.Unlock()

	url, err := wall.Start()
	if err != nil {
		return "", err
	}
	// make sure the page knows the current state right away
	a.mu.Lock()
	running := a.running
	a.mu.Unlock()
	wall.SetRunning(running)
	return url, nil
}

// autoOpenLiveWall runs on every bot start (in its own goroutine): starts the
// server if needed and opens the default browser unless the wall is disabled
// in Settings or a page is already connected.
func (a *App) autoOpenLiveWall() {
	settings, err := a.uiStore.LoadGeneralSettings()
	if err != nil || settings.LiveWallDisabled {
		return
	}
	url, err := a.ensureLiveWall()
	if err != nil {
		a.log("error", fmt.Sprintf("Live Wall could not start: %v", err))
		return
	}
	if wall := a.currentLiveWall(); wall != nil && wall.ClientCount() > 0 {
		a.log("info", fmt.Sprintf("Live Wall already open in browser (%s).", url))
		return
	}
	a.openURL(url)
	a.log("info", fmt.Sprintf("Live Wall opened in browser: %s", url))
}

func (a *App) openURL(url string) {
	if a.ctx != nil {
		runtime.BrowserOpenURL(a.ctx, url)
		return
	}
	_ = exec.Command("rundll32", "url.dll,FileProtocolHandler", url).Start()
}

// OpenLiveWall is bound to the dashboard "Live Wall" button: it starts the
// server if needed, opens the browser and returns the URL.
func (a *App) OpenLiveWall() (string, error) {
	url, err := a.ensureLiveWall()
	if err != nil {
		a.log("error", fmt.Sprintf("Live Wall could not start: %v", err))
		return "", err
	}
	a.openURL(url)
	a.log("info", fmt.Sprintf("Live Wall opened in browser: %s", url))
	return url, nil
}

// OpenLiveWallFolder opens data\logwall in Explorer; any png/jpg dropped there
// becomes a faint, shadowed background on the Live Wall (slideshow if several).
func (a *App) OpenLiveWallFolder() (string, error) {
	dir := a.liveWallDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	if err := exec.Command("explorer.exe", dir).Start(); err != nil {
		return "", err
	}
	a.log("info", fmt.Sprintf("Live Wall background folder: %s (drop png/jpg photos here)", dir))
	return dir, nil
}
