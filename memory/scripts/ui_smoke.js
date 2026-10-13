// UI smoke: loads dist/index.html + app.js in jsdom with a fake Wails bridge, checks the Browser form round-trip.
// Needs: cd /tmp && npm install jsdom@24   (registry.npmjs.org is reachable). Run: node memory/scripts/ui_smoke.js
const { JSDOM } = require(process.env.JSDOM_DIR || "/tmp/node_modules/jsdom");
const fs = require("fs");
const dir = "/home/user/copyhere/cmd/chitchatui/frontend/dist/";
const html = fs.readFileSync(dir + "index.html", "utf8");
const calls = [];
const snapshot = {
  browser: { profile: "default", backend: "rod", headless: false, browser_mode: true },
  browser_options: [{ id: "rod", label: "Rod" }, { id: "chromedp", label: "ChromeDP" }],
  general: {}, home: {}, logs: [], running: false, license_key: "", license_invalid: false, license_reason: "",
  message: { chat_mode: "api_chat", sequence: [], loaded_sequences: [], use_loaded_sequences: false, usernames: [], ai_api_key: "", ai_base_url: "", ai_model: "" },
  proxy: { enabled: false, entries: [] },
};
const dom = new JSDOM(html, { runScripts: "outside-only", url: "http://localhost/" });
const { window } = dom;
window.go = { main: { App: new Proxy({}, { get: (_, name) => (...args) => { calls.push([name, args]);
  if (name === "GetInitialState") return Promise.resolve(JSON.parse(JSON.stringify(snapshot)));
  return Promise.resolve(null); } }) } };
window.runtime = { EventsOn: () => {} };
const errors = [];
window.addEventListener("error", (e) => errors.push(String(e.error || e.message)));
try { window.eval(fs.readFileSync(dir + "app.js", "utf8")); } catch (e) { errors.push("eval: " + e.stack); }
window.document.dispatchEvent(new window.Event("DOMContentLoaded"));
setTimeout(async () => {
  const d = window.document;
  const cb = d.getElementById("browser-mode");
  console.log("checkbox exists:", !!cb, "checked from snapshot:", cb && cb.checked);
  cb.checked = false;
  d.getElementById("browser-form").dispatchEvent(new window.Event("submit", { cancelable: true }));
  await new Promise(r => setTimeout(r, 50));
  const save = calls.find(c => c[0] === "SaveBrowserSettings");
  console.log("SaveBrowserSettings payload:", JSON.stringify(save && save[1][0]));
  console.log("errors:", errors.length ? errors : "none");
  process.exit(errors.length || !cb || !save || save[1][0].browser_mode !== false ? 1 : 0);
}, 300);
