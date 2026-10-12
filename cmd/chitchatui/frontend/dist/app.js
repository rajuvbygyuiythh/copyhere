const state = {
  snapshot: null,
  currentTab: "home",
};

function backend() {
  return window.go && window.go.main && window.go.main.App;
}

async function invoke(method, ...args) {
  const api = backend();
  if (!api || typeof api[method] !== "function") {
    throw new Error(`Wails binding not ready: ${method}`);
  }
  return api[method](...args);
}

function runtimeEvents() {
  return window.runtime || {};
}

function setRunning(running) {
  const pill = document.getElementById("running-pill");
  pill.textContent = running ? "Running" : "Stopped";
  pill.className = `pill ${running ? "running" : "stopped"}`;
  const cleanButton = document.getElementById("clean-profiles");
  if (cleanButton) {
    cleanButton.disabled = !!running;
    cleanButton.title = running ? "Stop the bot before cleaning profile data." : "";
  }
}

function renderLogs(logs) {
  const logList = document.getElementById("log-list");
  logList.innerHTML = "";
  const visibleLogs = (logs || []).filter(isVisibleHomeLog);

  if (!visibleLogs.length) {
    const empty = document.createElement("div");
    empty.className = "log-entry";
    empty.innerHTML = `<div class="log-time">-</div><div class="log-level activity">ACTIVITY</div><div class="log-message">No chat activity yet.</div>`;
    logList.appendChild(empty);
    return;
  }

  visibleLogs.forEach((entry) => appendLog(entry, false));
  logList.scrollTop = logList.scrollHeight;
}

function appendLog(entry, scroll = true) {
  if (!isVisibleHomeLog(entry)) {
    return;
  }

  const logList = document.getElementById("log-list");
  const row = document.createElement("div");
  row.className = "log-entry";

  const level = String(entry.level || "info").toLowerCase();
  row.innerHTML = `
    <div class="log-time">${escapeHtml(entry.time || "")}</div>
    <div class="log-level ${level}">${escapeHtml(level)}</div>
    <div class="log-message">${escapeHtml(entry.message || "")}</div>
  `;

  logList.insertBefore(row, logList.firstChild);
  if (scroll) {
    logList.scrollTop = 0;
  }
}

function isVisibleHomeLog(entry) {
  const level = String(entry && entry.level ? entry.level : "info").toLowerCase();
  return level === "activity";
}

function renderBrowserOptions(options, selected) {
  const root = document.getElementById("browser-options");
  root.innerHTML = "";

  options.forEach((option) => {
    const wrapper = document.createElement("label");
    wrapper.className = `browser-choice ${option.key === selected ? "active" : ""}`;
    wrapper.innerHTML = `
      <div class="browser-choice-title">
        <input type="radio" name="browser-backend" value="${escapeHtml(option.key)}" ${option.key === selected ? "checked" : ""}>
        <span>${escapeHtml(option.label)}</span>
      </div>
      <div class="browser-choice-description">${escapeHtml(option.description)}</div>
    `;
    root.appendChild(wrapper);
  });

  root.querySelectorAll('input[name="browser-backend"]').forEach((input) => {
    input.addEventListener("change", () => {
      root.querySelectorAll(".browser-choice").forEach((choice) => choice.classList.remove("active"));
      input.closest(".browser-choice").classList.add("active");
    });
  });
}

function renderHomeStats(stats) {
  const home = stats || {};
  document.getElementById("stat-bot-accounts").textContent = String(home.bot_accounts || 0);
  document.getElementById("stat-running-accounts").textContent = String(home.running_accounts || 0);
  document.getElementById("stat-user-send").textContent = String(home.user_send_count || 0);
  document.getElementById("stat-traffic-users").textContent = String(home.traffic_users || 0);
  document.getElementById("stat-snap-tg-user").textContent = String(home.snap_tg_sent_count || 0);
  document.getElementById("stat-messages-sent").textContent = String(home.messages_sent || 0);
  document.getElementById("stat-dead-accounts").textContent = String(home.dead_accounts || 0);
  document.getElementById("stat-skipped-accounts").textContent = String(home.skipped_accounts || 0);
}

function setChatMode(mode) {
  const validMode = (mode === "folder" || mode === "eva_chat_flow") ? mode : "single";
  ["single", "folder", "evachat"].forEach((m) => {
    const radio = document.getElementById(`chat-mode-${m}`);
    const label = document.getElementById(`mode-option-${m}`);
    const section = document.getElementById(`section-${m}-mode`);
    if (radio) radio.checked = ((m === "evachat" ? "eva_chat_flow" : m) === validMode);
    if (label) label.classList.toggle("active", (m === "evachat" ? "eva_chat_flow" : m) === validMode);
    if (section) section.style.display = ((m === "evachat" ? "eva_chat_flow" : m) === validMode) ? "block" : "none";
  });
  const useLoaded = document.getElementById("use-loaded-sequences");
  if (useLoaded) useLoaded.checked = (validMode === "folder");
}

function fillForms(snapshot) {
  document.getElementById("browser-profile").value = snapshot.browser.profile || "default";
  document.getElementById("browser-headless").checked = !!snapshot.browser.headless;
  
  const currentMode = snapshot.message.chat_mode || ((snapshot.message.use_loaded_sequences && snapshot.message.loaded_sequences && snapshot.message.loaded_sequences.length > 0) ? "folder" : "single");
  setChatMode(currentMode);

  document.getElementById("loaded-sequences-status").textContent = snapshot.message.loaded_sequences ? `${snapshot.message.loaded_sequences.length} sequences loaded` : "0 sequences loaded";
  document.getElementById("message-sequence").value = snapshot.message.sequence || "";
  const evaStatus = document.getElementById("evachat-status");
  if (evaStatus) evaStatus.textContent = currentMode === "eva_chat_flow" ? "EVAChat Flow will load the editable data/eva_chat TXT pack when the bot starts." : "TXT data is initialized when EVAChat Flow starts.";
  document.getElementById("message-usernames").value = snapshot.message.usernames || "";
  document.getElementById("proxy-enabled").checked = !!snapshot.proxy.enabled;
  document.getElementById("proxy-entries").value = snapshot.proxy.entries || "";
  document.getElementById("poll-min").value = snapshot.general.poll_min_seconds;
  document.getElementById("poll-max").value = snapshot.general.poll_max_seconds;
  document.getElementById("action-min").value = snapshot.general.action_min_seconds;
  document.getElementById("action-max").value = snapshot.general.action_max_seconds;
  document.getElementById("match-reply-timeout-min").value = snapshot.general.match_reply_timeout_min_seconds || 60;
  document.getElementById("match-reply-timeout-max").value = snapshot.general.match_reply_timeout_max_seconds || 60;
  document.getElementById("match-close-wait-min").value = snapshot.general.match_close_wait_min_seconds || 0;
  document.getElementById("match-close-wait-max").value = snapshot.general.match_close_wait_max_seconds || 0;
  document.getElementById("reply-delay-min").value = snapshot.general.reply_delay_min_seconds || 0;
  document.getElementById("reply-delay-max").value = snapshot.general.reply_delay_max_seconds || 0;
  renderBrowserOptions(snapshot.browser_options || [], snapshot.browser.backend || "rod");
  renderHomeStats(snapshot.home);
  setRunning(!!snapshot.running);
  if (snapshot.license_invalid) {
    const overlay = document.getElementById('license-overlay');
    overlay.style.display = 'flex';
    document.getElementById('license-message').textContent = snapshot.license_reason || "Your license is invalid or expired.";
    document.getElementById('license-key-input').value = snapshot.license_key || "UNKNOWN";
    return;
  }

  document.querySelector('.shell').classList.add('visible');

  const messageLineCount = String(snapshot.message.sequence || "")
    .split(/\r?\n/)
    .map((line) => line.trim())
    .filter(Boolean).length;
  document.getElementById("home-summary").textContent =
    `Showing bot logs | Backend: ${snapshot.browser.backend} | Profiles auto-start | Message steps: ${messageLineCount}`;
}

function escapeHtml(value) {
  return String(value)
    .replaceAll("&", "&amp;")
    .replaceAll("<", "&lt;")
    .replaceAll(">", "&gt;")
    .replaceAll('"', "&quot;")
    .replaceAll("'", "&#39;");
}

function selectedBackend() {
  const selected = document.querySelector('input[name="browser-backend"]:checked');
  return selected ? selected.value : "rod";
}

async function refreshState() {
  const snapshot = await invoke("GetInitialState");
  state.snapshot = snapshot;
  fillForms(snapshot);
  renderLogs(snapshot.logs || []);
}

async function autoRefreshStats() {
  try {
    const snapshot = await invoke("GetInitialState");
    renderHomeStats(snapshot.home);
  } catch (err) {
    // ignore
  }
}

function activateTab(tabName) {
  state.currentTab = tabName;

  document.querySelectorAll(".nav-button").forEach((button) => {
    button.classList.toggle("active", button.dataset.tab === tabName);
  });
  document.querySelectorAll(".tab-panel").forEach((panel) => {
    panel.classList.toggle("active", panel.dataset.panel === tabName);
  });
}

function bindTabs() {
  document.querySelectorAll(".nav-button").forEach((button) => {
    button.addEventListener("click", () => activateTab(button.dataset.tab));
  });
}

function bindForms() {
  document.getElementById("browser-form").addEventListener("submit", async (event) => {
    event.preventDefault();
    await invoke("SaveBrowserSettings", {
      profile: document.getElementById("browser-profile").value.trim() || "default",
      backend: selectedBackend(),
      headless: document.getElementById("browser-headless").checked,
    });
    await refreshState();
  });

  document.querySelectorAll('input[name="chat-mode"]').forEach((radio) => {
    radio.addEventListener("change", (e) => {
      setChatMode(e.target.value);
    });
  });

  ["single", "folder", "evachat"].forEach((mode) => {
    const label = document.getElementById(`mode-option-${mode}`);
    if (label) {
      label.addEventListener("click", () => {
        setChatMode(mode === "evachat" ? "eva_chat_flow" : mode);
      });
    }
  });

  document.getElementById("load-folder-btn").addEventListener("click", async () => {
    const summary = document.getElementById("home-summary");
    try {
      const sequences = await invoke("LoadMessageFolder");
      if (sequences && sequences.length > 0) {
        summary.textContent = `Loaded ${sequences.length} message sequences from folder.`;
        setChatMode("folder");
        await refreshState();
        setChatMode("folder");
      }
    } catch (error) {
      summary.textContent = error.message || String(error);
    }
  });

  document.getElementById("clean-loaded-btn").addEventListener("click", async () => {
    const summary = document.getElementById("home-summary");
    try {
      await invoke("ClearLoadedMessages");
      summary.textContent = "Loaded sequences cleared from DB.";
      setChatMode("single");
      await refreshState();
      setChatMode("single");
    } catch (error) {
      summary.textContent = error.message || String(error);
    }
  });

  document.getElementById("open-evachat-folder-btn").addEventListener("click", async () => {
    const summary = document.getElementById("home-summary");
    try {
      const result = await invoke("OpenEVAChatDataFolder");
      if (result) summary.textContent = result;
    } catch (error) {
      if (summary) summary.textContent = error.message || String(error);
    }
  });

  document.getElementById("message-form").addEventListener("submit", async (event) => {
    event.preventDefault();
    const selectedMode = document.querySelector('input[name="chat-mode"]:checked')?.value || "single";
    await invoke("SaveMessageSettings", {
      chat_mode: selectedMode,
      use_loaded_sequences: selectedMode === "folder",
      sequence: document.getElementById("message-sequence").value,
      usernames: document.getElementById("message-usernames").value,
    });
    await refreshState();
    setChatMode(selectedMode);
  });

  document.getElementById("proxy-form").addEventListener("submit", async (event) => {
    event.preventDefault();
    const summary = document.getElementById("home-summary");
    try {
      await invoke("SaveProxySettings", {
        enabled: document.getElementById("proxy-enabled").checked,
        entries: document.getElementById("proxy-entries").value,
      });
      await refreshState();
    } catch (error) {
      summary.textContent = error.message || String(error);
    }
  });

  document.getElementById("general-form").addEventListener("submit", async (event) => {
    event.preventDefault();
    await invoke("SaveGeneralSettings", {
      poll_min_seconds: Number(document.getElementById("poll-min").value),
      poll_max_seconds: Number(document.getElementById("poll-max").value),
      action_min_seconds: Number(document.getElementById("action-min").value),
      action_max_seconds: Number(document.getElementById("action-max").value),
      match_reply_timeout_min_seconds: Number(document.getElementById("match-reply-timeout-min").value),
      match_reply_timeout_max_seconds: Number(document.getElementById("match-reply-timeout-max").value),
      match_close_wait_min_seconds: Number(document.getElementById("match-close-wait-min").value),
      match_close_wait_max_seconds: Number(document.getElementById("match-close-wait-max").value),
      reply_delay_min_seconds: Number(document.getElementById("reply-delay-min").value),
      reply_delay_max_seconds: Number(document.getElementById("reply-delay-max").value),
    });
    await refreshState();
  });
}

function bindActions() {
  document.getElementById("start-bot").addEventListener("click", async () => {
    await invoke("StartBot");
    await refreshState();
  });

  document.getElementById("stop-bot").addEventListener("click", async () => {
    await invoke("StopBot");
    await refreshState();
  });

  document.getElementById("clean-profiles").addEventListener("click", async () => {
    const summary = document.getElementById("home-summary");
    try {
      await invoke("CleanProfiles");
      await refreshState();
      summary.textContent = "Profile data cleaned. Login profiles and worker ownership data were removed.";
    } catch (error) {
      summary.textContent = error.message || String(error);
    }
  });

  document.getElementById("refresh-state").addEventListener("click", async () => {
    await refreshState();
  });

  document.getElementById("clear-logs").addEventListener("click", async () => {
    await invoke("ClearLogs");
    await refreshState();
  });

  document.getElementById("run-fresh-browsers").addEventListener("click", async () => {
    const count = Number(document.getElementById("fresh-browser-count").value);
    const summary = document.getElementById("home-summary");
    if (!Number.isInteger(count) || count < 1 || count > 5) {
      summary.textContent = "Fresh browser count must be between 1 and 5.";
      return;
    }

    try {
      summary.textContent = `Launching ${count} fresh browser(s)...`;
      await invoke("RunFreshBrowsers", count);
    } catch (error) {
      summary.textContent = error.message || String(error);
    }
  });

}

function bindEvents() {
  const runtime = runtimeEvents();

  if (typeof runtime.EventsOn === "function") {
    runtime.EventsOn("ui:log", (entry) => {
      appendLog(entry);
    });
    runtime.EventsOn("ui:running", (running) => {
      setRunning(!!running);
    });
    runtime.EventsOn("ui:logs_cleared", () => {
      renderLogs([]);
    });
    runtime.EventsOn("ui:license_invalid", (data) => {
      document.querySelector('.shell').classList.remove('visible');
      const overlay = document.getElementById('license-overlay');
      overlay.style.display = 'flex';
      document.getElementById('license-message').textContent = data.reason || "Your license became invalid or expired.";
      document.getElementById('license-key-input').value = data.key || "UNKNOWN";
    });
  }

  const copyBtn = document.getElementById('copy-license-btn');
  if (copyBtn) {
    copyBtn.addEventListener('click', () => {
      const input = document.getElementById('license-key-input');
      input.select();
      input.setSelectionRange(0, 99999);
      navigator.clipboard.writeText(input.value).then(() => {
        const originalText = copyBtn.textContent;
        copyBtn.textContent = 'Copied!';
        setTimeout(() => copyBtn.textContent = originalText, 2000);
      });
    });
  }
}

async function bootstrap() {
  bindTabs();
  bindForms();
  bindActions();
  bindEvents();
  activateTab("home");

  let attempts = 0;
  while (attempts < 50) {
    attempts += 1;
    try {
      await refreshState();
      setInterval(autoRefreshStats, 3000);
      return;
    } catch (error) {
      await new Promise((resolve) => setTimeout(resolve, 200));
    }
  }

  document.getElementById("home-summary").textContent = "Failed to connect to Wails bindings.";
}

window.addEventListener("DOMContentLoaded", () => {
  bootstrap().catch((error) => {
    document.getElementById("home-summary").textContent = error.message || String(error);
  });
});
