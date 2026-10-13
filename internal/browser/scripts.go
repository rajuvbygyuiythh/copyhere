package browser

import "fmt"

const storageFunction = `() => {
  const copy = (storage) => {
    const data = {};
    for (let index = 0; index < storage.length; index += 1) {
      const key = storage.key(index);
      data[key] = storage.getItem(key);
    }
    return data;
  };

  return {
    local_storage: copy(window.localStorage),
    session_storage: copy(window.sessionStorage),
  };
}`

const fingerprintFunction = `() => ({
  userAgent: navigator.userAgent,
  language: navigator.language,
  colorDepth: String(screen.colorDepth),
  screenResolution: String(screen.width) + "x" + String(screen.height),
  timeZone: Intl.DateTimeFormat().resolvedOptions().timeZone,
  addBehavior: String("addBehavior" in document.documentElement),
  openDatabase: String("openDatabase" in window),
  plugins: Array.from(navigator.plugins).map((plugin) => plugin.name).join(","),
  canvas: (() => {
    const canvas = document.createElement("canvas");
    const context = canvas.getContext("2d");
    if (context) {
      context.fillText("BF", 2, 2);
    }
    return canvas.toDataURL().slice(-50);
  })(),
  webgl: (() => {
    const context = document.createElement("canvas").getContext("webgl");
    if (!context) {
      return "~";
    }
    return String(context.getParameter(context.VENDOR)) + "~" + String(context.getParameter(context.RENDERER));
  })(),
  touchSupport: String("ontouchstart" in window),
  hardwareConcurrency: String(navigator.hardwareConcurrency || "unknown"),
})`

const fetchFunction = `async (request) => {
  const options = {
    method: request.method,
    credentials: "include",
    headers: request.headers || {},
  };

  if (Object.prototype.hasOwnProperty.call(request, "body")) {
    options.body = request.body;
  }

  try {
    const response = await fetch(request.url, options);
    const text = await response.text();
    const headers = {};
    try {
      const keep = ["x-ratelimit-limit", "x-ratelimit-remaining", "x-ratelimit-reset", "retry-after", "content-type", "cf-ray", "x-envoy-upstream-service-time"];
      response.headers.forEach((value, name) => {
        if (keep.indexOf(String(name).toLowerCase()) !== -1) { headers[String(name).toLowerCase()] = value; }
      });
    } catch (e) {}
    return {
      ok: response.ok,
      status: response.status,
      statusText: response.statusText,
      text,
      headers,
    };
  } catch (error) {
    return {
      ok: false,
      status: 0,
      statusText: "FETCH_ERROR",
      text: String(error),
    };
  }
}`

// stealthInitScript runs before any page script (Page.addScriptToEvaluateOnNewDocument).
// Minimal and consistent on purpose: only the markers that scream "automation".
const stealthInitScript = `(() => {
  try {
    if (navigator.webdriver) {
      Object.defineProperty(Object.getPrototypeOf(navigator), "webdriver", { get: () => false, configurable: true });
    }
  } catch (e) {}
  try {
    if (!window.chrome) { window.chrome = {}; }
    if (!window.chrome.runtime) { window.chrome.runtime = { connect: () => {}, sendMessage: () => {} }; }
  } catch (e) {}
  try {
    if (!navigator.languages || navigator.languages.length === 0) {
      Object.defineProperty(navigator, "languages", { get: () => ["en-US", "en"], configurable: true });
    }
  } catch (e) {}
  try {
    if (navigator.plugins && navigator.plugins.length === 0) {
      const fake = [{ name: "PDF Viewer" }, { name: "Chrome PDF Viewer" }, { name: "Chromium PDF Viewer" }];
      Object.defineProperty(navigator, "plugins", { get: () => fake, configurable: true });
    }
  } catch (e) {}
  try {
    const originalQuery = window.navigator.permissions && window.navigator.permissions.query;
    if (originalQuery) {
      window.navigator.permissions.query = (parameters) =>
        parameters && parameters.name === "notifications"
          ? Promise.resolve({ state: Notification.permission, onchange: null })
          : originalQuery.call(window.navigator.permissions, parameters);
    }
  } catch (e) {}
})();`

// stealthScript returns the init script for the given options (the constant
// stealth part plus per-profile overrides).
func stealthScript(options Options) string {
	if options.HardwareConcurrency <= 0 {
		return stealthInitScript
	}
	return fmt.Sprintf(`(() => { try { Object.defineProperty(navigator, "hardwareConcurrency", { get: () => %d, configurable: true }); } catch (e) {} })();
`, options.HardwareConcurrency) + stealthInitScript
}

// inspectFunction returns what the page's JavaScript sees.
const inspectFunction = `() => ({
  userAgent: navigator.userAgent,
  platform: navigator.platform,
  webdriver: !!navigator.webdriver,
  timezone: (Intl.DateTimeFormat().resolvedOptions() || {}).timeZone || "",
  languages: Array.from(navigator.languages || []),
  hardwareConcurrency: navigator.hardwareConcurrency || 0,
  deviceMemory: navigator.deviceMemory || 0,
  screen: String(screen.width) + "x" + String(screen.height),
  plugins: (navigator.plugins || []).length,
  chromeRuntime: !!(window.chrome && window.chrome.runtime),
})`
