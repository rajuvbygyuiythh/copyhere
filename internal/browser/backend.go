package browser

import (
	"context"
	"errors"
	"fmt"

	"chitchatbot/internal/model"
	"chitchatbot/internal/proxy"
)

type Options struct {
	StartURL            string
	UserDataDir         string
	Headless            bool
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
