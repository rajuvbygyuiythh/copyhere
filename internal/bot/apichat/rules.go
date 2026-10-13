package apichat

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// RulesFile is the editable funnel file (bot: data/api_chat/, lab: cmd/apitest/).
const RulesFile = "config_rules.json"

//go:embed assets/config_rules.json
var defaultRulesJSON string

// Rules are the funnel / anti-spam knobs. They live in config_rules.json so
// the dashboard stays clean and the same file controls the bot and the lab.
type Rules struct {
	ShareHot       int  `json:"share_hot"`
	ShareWarm      int  `json:"share_warm"`
	ShareCold      int  `json:"share_cold"`
	NoSharePercent int  `json:"no_share_percent"`
	NoShareMin     int  `json:"no_share_min_replies"`
	NoShareMax     int  `json:"no_share_max_replies"`
	NoShareStrict  bool `json:"no_share_strict"`
	HintPercent    int  `json:"hint_percent"`
}

// DefaultRules mirror the embedded config_rules.json.
func DefaultRules() Rules {
	d := Defaults()
	return Rules{ShareHot: d.ShareHot, ShareWarm: d.ShareWarm, ShareCold: d.ShareCold,
		NoSharePercent: d.NoSharePercent, NoShareMin: d.NoShareMin, NoShareMax: d.NoShareMax,
		NoShareStrict: d.NoShareStrict, HintPercent: d.HintPercent}
}

// LoadRules reads path. A missing file returns the defaults and os.ErrNotExist
// (caller decides whether to seed); a broken file returns the defaults and
// the parse error. Values are clamped like Options.withDefaults.
func LoadRules(path string) (Rules, error) {
	r := DefaultRules()
	data, err := os.ReadFile(path)
	if err != nil {
		return r, err
	}
	if err := json.Unmarshal(data, &r); err != nil {
		return DefaultRules(), fmt.Errorf("%s: %w", path, err)
	}
	return r.clamped(), nil
}

func (r Rules) clamped() Rules {
	d := DefaultRules()
	if r.ShareHot <= 0 {
		r.ShareHot = d.ShareHot
	}
	if r.ShareWarm <= 0 {
		r.ShareWarm = d.ShareWarm
	}
	if r.ShareCold <= 0 {
		r.ShareCold = d.ShareCold
	}
	if r.NoSharePercent < 0 || r.NoSharePercent > 100 {
		r.NoSharePercent = d.NoSharePercent
	}
	if r.HintPercent < 0 || r.HintPercent > 100 {
		r.HintPercent = d.HintPercent
	}
	if r.NoShareMin <= 0 {
		r.NoShareMin = d.NoShareMin
	}
	if r.NoShareMax < r.NoShareMin {
		r.NoShareMax = r.NoShareMin
	}
	return r
}

// Apply copies the rules into the engine options.
func (r Rules) Apply(o Options) Options {
	o.ShareHot, o.ShareWarm, o.ShareCold = r.ShareHot, r.ShareWarm, r.ShareCold
	o.NoSharePercent, o.HintPercent = r.NoSharePercent, r.HintPercent
	o.NoShareMin, o.NoShareMax, o.NoShareStrict = r.NoShareMin, r.NoShareMax, r.NoShareStrict
	return o
}

// applyConfig copies the rules into the lab's config struct.
func (r Rules) applyConfig(c *config) {
	c.ShareHot, c.ShareWarm, c.ShareCold = r.ShareHot, r.ShareWarm, r.ShareCold
	c.NoSharePercent, c.HintPercent = r.NoSharePercent, r.HintPercent
	c.NoShareMin, c.NoShareMax, c.NoShareStrict = r.NoShareMin, r.NoShareMax, r.NoShareStrict
}

// String is the one-line summary shown in logs / Test API.
func (r Rules) String() string {
	strict := "off"
	if r.NoShareStrict {
		strict = "on"
	}
	return fmt.Sprintf("share at HOT %d / WARM %d / COLD %d | no-snap %d%% (leave after %d-%d replies, strict %s) | hint first %d%%",
		r.ShareHot, r.ShareWarm, r.ShareCold, r.NoSharePercent, r.NoShareMin, r.NoShareMax, strict, r.HintPercent)
}

// ProjectRulesRel is the one shared rules file inside the project tree.
var ProjectRulesRel = filepath.Join("cmd", "apitest", RulesFile)

// FindProjectRules looks for cmd/apitest/config_rules.json by walking up
// (max 6 levels) from the working directory and from the executable's
// directory, so the bot (build/bin/chitchatui.exe, started from the project
// root or double-clicked) and the lab (cmd/apitest/apitest.exe) find the SAME
// file. Returns "" when the project tree is not around (exe copied away).
func FindProjectRules() string {
	var starts []string
	if wd, err := os.Getwd(); err == nil {
		starts = append(starts, wd)
	}
	if exe, err := os.Executable(); err == nil {
		starts = append(starts, filepath.Dir(exe))
	}
	for _, start := range starts {
		dir := start
		for i := 0; i < 6; i++ {
			candidate := filepath.Join(dir, ProjectRulesRel)
			if st, err := os.Stat(candidate); err == nil && !st.IsDir() {
				return candidate
			}
			parent := filepath.Dir(dir)
			if parent == dir {
				break
			}
			dir = parent
		}
	}
	return ""
}

// rulesSource describes where the active rules came from (for logs).
func rulesSource(path string, err error) string {
	switch {
	case err == nil:
		return path
	case os.IsNotExist(err):
		return "built-in defaults (" + path + " missing)"
	default:
		return "built-in defaults (" + strings.TrimSpace(err.Error()) + ")"
	}
}
