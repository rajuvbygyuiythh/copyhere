package proxy

// Auto timezone from the proxy's exit country.
//
// There is deliberately no dashboard timezone setting any more: before a
// browser window is launched, the bot asks an IP-geolocation service —
// THROUGH the window's own proxy, so the answer describes the exit IP the
// site will see — which country/timezone that proxy exits in, and the
// window then emulates exactly that IANA timezone. Priority:
//
//  1. an explicit "|tz=Zone" suffix on the proxy line (manual override),
//  2. the timezone reported for the proxy exit IP (ip-api / ipwho / ipapi),
//  3. a country -> primary-timezone fallback when only a country is known,
//  4. "" (no override: the window keeps the PC's own timezone) when there
//     is no proxy or every lookup fails — the caller logs which happened.
//
// Results are cached per proxy for the life of the process, so starting
// many windows costs one lookup per distinct proxy.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	_ "time/tzdata" // validate IANA names even on Windows builds without system tzdata
)

// GeoResult is what a lookup learned about a proxy exit IP.
type GeoResult struct {
	Country     string
	CountryCode string
	Timezone    string
	Source      string
}

// geoEndpoints are tried in order. Package-level var so tests can point
// them at a local server.
var geoEndpoints = []string{
	"http://ip-api.com/json/?fields=status,message,country,countryCode,timezone,query",
	"https://ipwho.is/?fields=success,message,country,country_code,timezone",
	"https://ipapi.co/json/",
}

type cachedGeo struct {
	zone    string
	country string
}

var (
	geoCacheMu sync.Mutex
	geoCache   = map[string]cachedGeo{}
)

// ResolveTimezone returns the IANA timezone the browser behind this proxy
// should pretend to be in ("" = no override), plus the exit country code
// when known. It never blocks longer than ~10s and never fails hard: any
// error yields the fallback chain described above.
func ResolveTimezone(ctx context.Context, e Entry) (zone string, countryCode string) {
	if explicit := strings.TrimSpace(e.Timezone); explicit != "" {
		return explicit, ""
	}
	host := strings.TrimSpace(e.Host)
	if host == "" {
		return "", ""
	}
	if ip := net.ParseIP(host); ip != nil && (ip.IsPrivate() || ip.IsLoopback() || ip.IsUnspecified()) {
		return "", "" // a LAN proxy says nothing about the exit country
	}

	key := e.SchemeOrDefault() + "://" + e.Address() + "|" + e.Username
	geoCacheMu.Lock()
	if hit, ok := geoCache[key]; ok {
		geoCacheMu.Unlock()
		return hit.zone, hit.country
	}
	geoCacheMu.Unlock()

	lookupCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	result, _ := lookupGeo(lookupCtx, e)
	zone = result.Timezone
	if zone == "" && result.CountryCode != "" {
		zone = countryTimezone[strings.ToUpper(result.CountryCode)]
	}
	if zone != "" {
		if _, err := time.LoadLocation(zone); err != nil {
			zone = ""
		}
	}
	geoCacheMu.Lock()
	geoCache[key] = cachedGeo{zone: zone, country: result.CountryCode}
	geoCacheMu.Unlock()
	return zone, result.CountryCode
}

// lookupGeo queries the geo endpoints through the proxy itself.
func lookupGeo(ctx context.Context, e Entry) (GeoResult, error) {
	client := &http.Client{
		Timeout: 10 * time.Second,
		Transport: &http.Transport{
			Proxy:               http.ProxyURL(e.TransportURL()),
			TLSHandshakeTimeout: 6 * time.Second,
		},
	}
	return lookupGeoWithClient(ctx, client, geoEndpoints)
}

func lookupGeoWithClient(ctx context.Context, client *http.Client, endpoints []string) (GeoResult, error) {
	var lastErr error
	for _, endpoint := range endpoints {
		result, err := fetchGeo(ctx, client, endpoint)
		if err == nil && (result.Timezone != "" || result.CountryCode != "") {
			return result, nil
		}
		if err != nil {
			lastErr = err
		}
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("no geolocation endpoint answered")
	}
	return GeoResult{}, lastErr
}

// fetchGeo reads one endpoint and extracts country/timezone in whichever
// shape that service uses (timezone as string or as {id: "…"} object).
func fetchGeo(ctx context.Context, client *http.Client, endpoint string) (GeoResult, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return GeoResult{}, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64)")
	resp, err := client.Do(req)
	if err != nil {
		return GeoResult{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return GeoResult{}, fmt.Errorf("%s: HTTP %d", endpoint, resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if err != nil {
		return GeoResult{}, err
	}
	var raw map[string]any
	if err := json.Unmarshal(body, &raw); err != nil {
		return GeoResult{}, fmt.Errorf("%s: %w", endpoint, err)
	}
	if status, _ := raw["status"].(string); status == "fail" {
		return GeoResult{}, fmt.Errorf("%s: %v", endpoint, raw["message"])
	}
	if success, ok := raw["success"].(bool); ok && !success {
		return GeoResult{}, fmt.Errorf("%s: %v", endpoint, raw["message"])
	}
	out := GeoResult{Source: endpoint}
	out.Country, _ = raw["country"].(string)
	if out.Country == "" {
		out.Country, _ = raw["country_name"].(string)
	}
	out.CountryCode, _ = raw["countryCode"].(string)
	if out.CountryCode == "" {
		out.CountryCode, _ = raw["country_code"].(string)
	}
	out.CountryCode = strings.ToUpper(strings.TrimSpace(out.CountryCode))
	switch tz := raw["timezone"].(type) {
	case string:
		out.Timezone = strings.TrimSpace(tz)
	case map[string]any:
		out.Timezone, _ = tz["id"].(string)
		out.Timezone = strings.TrimSpace(out.Timezone)
	}
	return out, nil
}

// countryTimezone maps an ISO country code to its primary IANA timezone.
// Only used when a service reports a country but no timezone; for
// multi-zone countries this is the most populous zone (the geo services
// above normally return the exit IP's exact zone, which wins first).
var countryTimezone = map[string]string{
	"AD": "Europe/Andorra", "AE": "Asia/Dubai", "AF": "Asia/Kabul", "AG": "America/Antigua",
	"AL": "Europe/Tirane", "AM": "Asia/Yerevan", "AO": "Africa/Luanda", "AR": "America/Argentina/Buenos_Aires",
	"AT": "Europe/Vienna", "AU": "Australia/Sydney", "AZ": "Asia/Baku", "BA": "Europe/Sarajevo",
	"BB": "America/Barbados", "BD": "Asia/Dhaka", "BE": "Europe/Brussels", "BG": "Europe/Sofia",
	"BH": "Asia/Bahrain", "BR": "America/Sao_Paulo", "BS": "America/Nassau", "BW": "Africa/Gaborone",
	"BY": "Europe/Minsk", "BZ": "America/Belize", "CA": "America/Toronto", "CH": "Europe/Zurich",
	"CL": "America/Santiago", "CN": "Asia/Shanghai", "CO": "America/Bogota", "CR": "America/Costa_Rica",
	"CU": "America/Havana", "CY": "Asia/Nicosia", "CZ": "Europe/Prague", "DE": "Europe/Berlin",
	"DK": "Europe/Copenhagen", "DO": "America/Santo_Domingo", "DZ": "Africa/Algiers", "EC": "America/Guayaquil",
	"EE": "Europe/Tallinn", "EG": "Africa/Cairo", "ES": "Europe/Madrid", "ET": "Africa/Addis_Ababa",
	"FI": "Europe/Helsinki", "FR": "Europe/Paris", "GB": "Europe/London", "GE": "Asia/Tbilisi",
	"GH": "Africa/Accra", "GR": "Europe/Athens", "GT": "America/Guatemala", "HK": "Asia/Hong_Kong",
	"HN": "America/Tegucigalpa", "HR": "Europe/Zagreb", "HU": "Europe/Budapest", "ID": "Asia/Jakarta",
	"IE": "Europe/Dublin", "IL": "Asia/Jerusalem", "IN": "Asia/Kolkata", "IQ": "Asia/Baghdad",
	"IR": "Asia/Tehran", "IS": "Atlantic/Reykjavik", "IT": "Europe/Rome", "JM": "America/Jamaica",
	"JO": "Asia/Amman", "JP": "Asia/Tokyo", "KE": "Africa/Nairobi", "KG": "Asia/Bishkek",
	"KH": "Asia/Phnom_Penh", "KR": "Asia/Seoul", "KW": "Asia/Kuwait", "KZ": "Asia/Almaty",
	"LA": "Asia/Vientiane", "LB": "Asia/Beirut", "LK": "Asia/Colombo", "LT": "Europe/Vilnius",
	"LU": "Europe/Luxembourg", "LV": "Europe/Riga", "LY": "Africa/Tripoli", "MA": "Africa/Casablanca",
	"MD": "Europe/Chisinau", "ME": "Europe/Podgorica", "MK": "Europe/Skopje", "MM": "Asia/Yangon",
	"MN": "Asia/Ulaanbaatar", "MT": "Europe/Malta", "MU": "Indian/Mauritius", "MV": "Indian/Maldives",
	"MX": "America/Mexico_City", "MY": "Asia/Kuala_Lumpur", "NG": "Africa/Lagos", "NI": "America/Managua",
	"NL": "Europe/Amsterdam", "NO": "Europe/Oslo", "NP": "Asia/Kathmandu", "NZ": "Pacific/Auckland",
	"OM": "Asia/Muscat", "PA": "America/Panama", "PE": "America/Lima", "PH": "Asia/Manila",
	"PK": "Asia/Karachi", "PL": "Europe/Warsaw", "PR": "America/Puerto_Rico", "PT": "Europe/Lisbon",
	"PY": "America/Asuncion", "QA": "Asia/Qatar", "RO": "Europe/Bucharest", "RS": "Europe/Belgrade",
	"RU": "Europe/Moscow", "SA": "Asia/Riyadh", "SE": "Europe/Stockholm", "SG": "Asia/Singapore",
	"SI": "Europe/Ljubljana", "SK": "Europe/Bratislava", "SN": "Africa/Dakar", "SV": "America/El_Salvador",
	"SY": "Asia/Damascus", "TH": "Asia/Bangkok", "TN": "Africa/Tunis", "TR": "Europe/Istanbul",
	"TT": "America/Port_of_Spain", "TW": "Asia/Taipei", "TZ": "Africa/Dar_es_Salaam", "UA": "Europe/Kyiv",
	"UG": "Africa/Kampala", "US": "America/New_York", "UY": "America/Montevideo", "UZ": "Asia/Tashkent",
	"VE": "America/Caracas", "VN": "Asia/Ho_Chi_Minh", "ZA": "Africa/Johannesburg", "ZM": "Africa/Lusaka",
	"ZW": "Africa/Harare",
}
