package browser

import (
	"context"
	"errors"
	"fmt"

	"chitchatbot/internal/model"
	"chitchatbot/internal/proxy"
)

type Options struct {
	StartURL    string
	UserDataDir string
	Headless    bool
	// DisablePageRecovery is retained for launch-site compatibility. Rod
	// now never creates or adopts a replacement page after Start returns:
	// a closed page/window stays closed, and the worker/backend Close is
	// responsible for ending the Chrome process.
	DisablePageRecovery bool
	Proxy               *proxy.Entry
	// Timezone / Locale are emulated inside the page (CDP Emulation) so the
	// site sees them instead of the PC's real ones. Empty = no override.
	Timezone string
	Locale   string
	// Stealth removes the automation markers Chrome adds for remote control
	// (navigator.webdriver, "controlled by automated software" bar, WebRTC
	// local-IP leak through a proxy) and installs a small init script.
	Stealth bool
	// LowMemory adds flags that reduce RAM per window (fewer renderer
	// processes, no extensions / background services).
	LowMemory bool
	// WindowSize "W,H" (optional); Stealth picks a realistic one when empty.
	WindowSize string
	// HardwareConcurrency (optional) overrides navigator.hardwareConcurrency
	// so many accounts on one big PC do not all report the same unusual
	// core count (the site receives it in every message's fingerprint).
	HardwareConcurrency int
	// SocketTap (Browser mode) installs the WebSocket tap init script before
	// the app loads, so DrainSocketEvents can read the SPA's own realtime
	// frames (matchUpdate / chatMessage) — the only place match chats and
	// their messages are visible; they never appear in the DM list.
	SocketTap bool
}

// PickHardwareConcurrency returns a common, stable core count for a profile
// name (4/6/8/12/16), so each account keeps the same value across runs.
func PickHardwareConcurrency(profile string) int {
	choices := []int{4, 6, 8, 8, 12, 16}
	var h uint32 = 2166136261
	for i := 0; i < len(profile); i++ {
		h ^= uint32(profile[i])
		h *= 16777619
	}
	return choices[int(h%uint32(len(choices)))]
}

// Inspection is what the site can see from JavaScript; used for the
// "[browser] check:" log line so leaks are visible to the owner.
type Inspection struct {
	UserAgent           string   `json:"userAgent"`
	Platform            string   `json:"platform"`
	Webdriver           bool     `json:"webdriver"`
	Timezone            string   `json:"timezone"`
	Languages           []string `json:"languages"`
	HardwareConcurrency int      `json:"hardwareConcurrency"`
	DeviceMemory        float64  `json:"deviceMemory"`
	Screen              string   `json:"screen"`
	Plugins             int      `json:"plugins"`
	ChromeRuntime       bool     `json:"chromeRuntime"`
}

type FetchRequest struct {
	Method  string
	URL     string
	Headers map[string]string
	Body    *string
}

type FetchResponse struct {
	OK         bool   `json:"ok"`
	Status     int    `json:"status"`
	StatusText string `json:"statusText"`
	Text       string `json:"text"`
	// Headers: selected response headers as seen by the page (lowercase).
	Headers map[string]string `json:"headers,omitempty"`
}

type Backend interface {
	Name() string
	Start(context.Context, Options) error
	CaptureSession(context.Context) (model.LoginInfo, error)
	Fetch(context.Context, FetchRequest) (FetchResponse, error)
	// Navigate loads url in the controlled page (Browser mode: bring the
	// window back to the app after a session expiry).
	Navigate(context.Context, string) error
	// SetCookies injects saved cookies before the page is used, so a profile
	// whose browser folder is empty (fresh-login accounts) is logged in
	// without typing the password again.
	SetCookies(context.Context, []model.Cookie) error
	// Inspect reports what the page's JavaScript sees (fingerprint self-check).
	Inspect(context.Context) (Inspection, error)
	// DrainSocketEvents returns (and clears) the realtime socket frames the
	// in-page tap collected since the last call. Empty when SocketTap was
	// not installed or no socket frame arrived yet.
	DrainSocketEvents(context.Context) ([]string, error)
	Close() error
}

// DefaultWindowSizes are common laptop/desktop sizes; Stealth picks one.
var DefaultWindowSizes = []string{"1366,768", "1536,864", "1440,900", "1600,900", "1920,1080"}

// PickWindowSize returns a deterministic-per-seed realistic window size.
func PickWindowSize(seed string) string {
	if len(DefaultWindowSizes) == 0 {
		return "1366,768"
	}
	sum := 0
	for _, r := range seed {
		sum = (sum*31 + int(r)) & 0x7fffffff
	}
	return DefaultWindowSizes[sum%len(DefaultWindowSizes)]
}

func New(name string) (Backend, error) {
	switch name {
	case "chromedp":
		return &ChromeDPBackend{}, nil
	case "rod":
		return &RodBackend{}, nil
	default:
		return nil, fmt.Errorf("unsupported backend %q", name)
	}
}

var ErrPageUnavailable = errors.New("browser page is not available")
