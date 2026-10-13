package proxy

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestResolveTimezoneExplicitSuffixWins(t *testing.T) {
	e := Entry{Scheme: "http", Host: "1.2.3.4", Port: "8080", Timezone: "America/Chicago"}
	zone, _ := ResolveTimezone(context.Background(), e)
	if zone != "America/Chicago" {
		t.Fatalf("explicit suffix must win without any lookup, got %q", zone)
	}
}

func TestResolveTimezonePrivateProxySkipped(t *testing.T) {
	e := Entry{Scheme: "http", Host: "192.168.1.10", Port: "8080"}
	if zone, _ := ResolveTimezone(context.Background(), e); zone != "" {
		t.Fatalf("private proxy must not be geolocated, got %q", zone)
	}
}

func TestFetchGeoShapes(t *testing.T) {
	cases := []struct {
		name     string
		body     string
		wantZone string
		wantCC   string
	}{
		{"ip-api", `{"status":"success","country":"United States","countryCode":"US","timezone":"America/Chicago"}`, "America/Chicago", "US"},
		{"ipwho", `{"success":true,"country":"Bangladesh","country_code":"BD","timezone":{"id":"Asia/Dhaka","utc":"+06:00"}}`, "Asia/Dhaka", "BD"},
		{"ipapi", `{"country_name":"Germany","country_code":"DE","timezone":"Europe/Berlin"}`, "Europe/Berlin", "DE"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				fmt.Fprint(w, tc.body)
			}))
			defer srv.Close()
			got, err := fetchGeo(context.Background(), srv.Client(), srv.URL)
			if err != nil {
				t.Fatalf("fetchGeo: %v", err)
			}
			if got.Timezone != tc.wantZone || got.CountryCode != tc.wantCC {
				t.Fatalf("got %+v, want zone=%s cc=%s", got, tc.wantZone, tc.wantCC)
			}
		})
	}
}

func TestLookupGeoFallsBackAcrossEndpoints(t *testing.T) {
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "nope", http.StatusBadGateway)
	}))
	defer bad.Close()
	good := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"status":"success","countryCode":"GB","timezone":"Europe/London"}`)
	}))
	defer good.Close()
	got, err := lookupGeoWithClient(context.Background(), http.DefaultClient, []string{bad.URL, good.URL})
	if err != nil || got.Timezone != "Europe/London" || got.CountryCode != "GB" {
		t.Fatalf("got %+v err=%v", got, err)
	}
}

func TestCountryTimezoneFallbackKnown(t *testing.T) {
	for cc, want := range map[string]string{"BD": "Asia/Dhaka", "IN": "Asia/Kolkata", "GB": "Europe/London"} {
		if got := countryTimezone[cc]; got != want {
			t.Fatalf("%s: got %s want %s", cc, got, want)
		}
	}
	if strings.TrimSpace(countryTimezone["US"]) != "America/New_York" {
		t.Fatalf("US fallback changed unexpectedly: %q", countryTimezone["US"])
	}
}
