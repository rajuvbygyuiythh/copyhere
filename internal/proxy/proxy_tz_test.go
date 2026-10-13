package proxy

import "testing"

func TestParseLineTimezoneSuffix(t *testing.T) {
	cases := map[string]struct {
		host, user, tz string
	}{
		"1.2.3.4:8080":                                    {"1.2.3.4", "", ""},
		"1.2.3.4:8080:u:p|tz=America/Chicago":             {"1.2.3.4", "u", "America/Chicago"},
		"1.2.3.4:8080:u:p tz=America/Los_Angeles":         {"1.2.3.4", "u", "America/Los_Angeles"},
		"socks5://u:p@5.6.7.8:1080|timezone=Europe/Paris": {"5.6.7.8", "u", "Europe/Paris"},
		"http://9.9.9.9:3128 |tz= America/Denver ":        {"9.9.9.9", "", "America/Denver"},
	}
	for line, want := range cases {
		e, err := ParseLine(line)
		if err != nil {
			t.Fatalf("%q: %v", line, err)
		}
		if e.Host != want.host || e.Username != want.user || e.Timezone != want.tz {
			t.Fatalf("%q: got host=%s user=%s tz=%s", line, e.Host, e.Username, e.Timezone)
		}
	}
	e, _ := ParseLine("1.2.3.4:8080|tz=America/Chicago")
	if e.BrowserURL() != "http://1.2.3.4:8080" {
		t.Fatalf("tz must not leak into the browser proxy url: %s", e.BrowserURL())
	}
	if e.Label() != "http://1.2.3.4:8080 (tz America/Chicago)" {
		t.Fatalf("label: %s", e.Label())
	}
}
