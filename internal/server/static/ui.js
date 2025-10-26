const statusCards = document.querySelector("#status-cards");
const rangeLabel = document.querySelector("#range-label");
const updatedAtEl = document.querySelector("#updated-at");
const refreshBtn = document.querySelector("#refresh-btn");
const incidentPanel = document.querySelector("#incident-panel");
const incidentList = document.querySelector("#incident-list");
const incidentMeta = document.querySelector("#incident-meta");

const REFRESH_INTERVAL = 30_000;

refreshBtn?.addEventListener("click", () => {
  refreshBtn.disabled = true;
  refresh().finally(() => {
    refreshBtn.disabled = false;
  });
});

async function refresh() {
  try {
    const [latest, uptime, history] = await Promise.all([
      fetchJSON("/api/status"),
      fetchJSON("/api/uptime"),
      fetchJSON("/api/history"),
    ]);
    renderDashboard(latest, uptime, history);
  } catch (err) {
    console.error("Refresh failed", err);
  }
}

function renderDashboard(latest, uptimeStats, historyEntries) {
  const latestTimestamp = latest?.timestamp ? new Date(latest.timestamp) : null;
  if (latestTimestamp) {
    updatedAtEl.textContent = `Ostatnia aktualizacja: ${formatTimestamp(latestTimestamp)}`;
  } else {
    updatedAtEl.textContent = "Ostatnia aktualizacja: brak danych";
  }

  const sortedHistory = [...(historyEntries || [])].sort((a, b) =>
    new Date(a.timestamp) - new Date(b.timestamp),
  );
  if (sortedHistory.length) {
    const first = new Date(sortedHistory[0].timestamp);
    const last = new Date(sortedHistory[sortedHistory.length - 1].timestamp);
    rangeLabel.textContent = `Zakres: ${formatRange(first, last)}`;
  } else {
    rangeLabel.textContent = "Brak historii do wyświetlenia";
  }

  const uptimeMap = new Map(uptimeStats.map((item) => [item.id, item]));
  const latestMap = new Map((latest.checks || []).map((check) => [check.id, check]));
  const historyMap = buildHistoryMap(sortedHistory);

  const serviceIds = new Set([
    ...uptimeMap.keys(),
    ...latestMap.keys(),
    ...historyMap.keys(),
  ]);

  statusCards.innerHTML = "";

  if (serviceIds.size === 0) {
    statusCards.innerHTML =
      '<div class="empty-state">Brak danych. Upewnij się, że monitor zebrał pierwsze próbki.</div>';
    renderIncidents([]);
    return;
  }

  const services = [...serviceIds].map((id) => {
    const latestCheck = latestMap.get(id);
    const uptime = uptimeMap.get(id);
    const history = historyMap.get(id) || [];
    const name =
      latestCheck?.name ?? uptime?.name ?? history[history.length - 1]?.name ?? id;
    return { id, name, latestCheck, uptime, history };
  });

  services.sort((a, b) => a.name.localeCompare(b.name, "pl"));

  for (const service of services) {
    statusCards.appendChild(renderCard(service, latestTimestamp));
  }

  renderIncidents((latest.checks || []).filter((check) => !check.ok));
}

function renderCard(service, latestTimestamp) {
  const { id, name, latestCheck, uptime, history } = service;
  const card = document.createElement("article");
  card.className = "status-card";

  const uptimeValue = resolveUptime(uptime, history);
  const uptimeClass = uptimeLevel(uptimeValue);
  const uptimeLabel = Number.isFinite(uptimeValue)
    ? `${uptimeValue.toFixed(2)}% uptime`
    : "Brak danych";

  const chipInfo = resolveStateChip(latestCheck);
  const failingCount = uptime?.failing ?? history.filter((h) => !h.ok).length;
  const totalChecks = uptime?.total_checks ?? history.length;

  const head = document.createElement("div");
  head.className = "card-head";
  head.innerHTML = `
    <div class="card-title">
      <span class="service-name">${name}</span>
      <span class="meta-text">Id: ${id}</span>
    </div>
    <span class="uptime-pill ${uptimeClass}">${uptimeLabel}</span>
  `;
  card.appendChild(head);

  const meta = document.createElement("div");
  meta.className = "card-meta";

  meta.appendChild(createMetaBadge("Stan", `<span class="state-chip ${chipInfo.className}">${chipInfo.label}</span>`));
  if (latestTimestamp) {
    meta.appendChild(
      createMetaBadge(
        "Ostatni pomiar",
        `<span>${formatTimestamp(latestTimestamp)}</span>`,
      ),
    );
  }
  if (Number.isFinite(totalChecks) && totalChecks > 0) {
    meta.appendChild(createMetaBadge("Łącznie prób", `<span>${totalChecks}</span>`));
  }
  if (failingCount > 0) {
    meta.appendChild(
      createMetaBadge(
        "Niepowodzenia",
        `<span class="meta-text" style="color:#a02f22;font-weight:600;">${failingCount}</span>`,
      ),
    );
  }
  card.appendChild(meta);

  const timeline = document.createElement("div");
  timeline.className = "timeline";
  const points = history.slice(-80); // show last 80 samples
  if (points.length === 0) {
    const placeholder = document.createElement("div");
    placeholder.className = "meta-text";
    placeholder.textContent = "Brak historii dla tej usługi.";
    timeline.appendChild(placeholder);
  } else {
    for (const point of points) {
      const dot = document.createElement("span");
      dot.className = `timeline-dot ${stateToClass(point.state, point.ok)}`;
      const tooltipState = point.state ? point.state : point.ok ? "active" : "unknown";
      const errorSuffix = point.error ? ` • ${point.error}` : "";
      dot.title = `${formatTooltip(point.timestamp)} — ${tooltipState}${errorSuffix}`;
      timeline.appendChild(dot);
    }
  }
  card.appendChild(timeline);

  if (latestCheck?.error) {
    const errorBox = document.createElement("div");
    errorBox.className = "card-error";
    errorBox.textContent = latestCheck.error;
    card.appendChild(errorBox);
  }

  return card;
}

function createMetaBadge(label, valueHTML) {
  const wrapper = document.createElement("span");
  wrapper.className = "meta-badge";
  wrapper.innerHTML = `<span>${label}:</span>${valueHTML}`;
  return wrapper;
}

function resolveUptime(uptime, history) {
  if (uptime && Number.isFinite(uptime.uptime_percent)) {
    return uptime.uptime_percent;
  }
  if (!history.length) {
    return Number.NaN;
  }
  const ok = history.filter((h) => h.ok).length;
  return (ok / history.length) * 100;
}

function uptimeLevel(value) {
  if (!Number.isFinite(value)) {
    return "";
  }
  if (value >= 99.5) return "";
  if (value >= 95) return "medium";
  return "low";
}

function resolveStateChip(latestCheck) {
  if (!latestCheck) {
    return { label: "Brak danych", className: "unknown" };
  }
  const state = latestCheck.state ? latestCheck.state.toLowerCase() : "";
  if (latestCheck.ok || state === "active" || state === "running") {
    return { label: state || "active", className: "" };
  }
  if (["activating", "deactivating", "reloading"].includes(state)) {
    return { label: state, className: "warning" };
  }
  if (!state) {
    return { label: "nieznany", className: "unknown" };
  }
  return { label: state, className: "error" };
}

function buildHistoryMap(historyEntries) {
  const map = new Map();
  for (const entry of historyEntries) {
    const timestamp = entry.timestamp;
    for (const check of entry.checks || []) {
      const arr = map.get(check.id) || [];
      arr.push({
        id: check.id,
        name: check.name,
        ok: Boolean(check.ok),
        state: check.state || (check.ok ? "active" : "unknown"),
        error: check.error,
        timestamp,
      });
      map.set(check.id, arr);
    }
  }
  return map;
}

function stateToClass(state, ok) {
  const normalized = (state || "").toLowerCase();
  if (ok || normalized === "active" || normalized === "running") {
    return "state-success";
  }
  if (["activating", "deactivating", "reloading", "maintenance"].includes(normalized)) {
    return "state-warning";
  }
  if (!normalized || normalized === "unknown") {
    return "state-unknown";
  }
  return "state-error";
}

function renderIncidents(failingChecks) {
  if (!failingChecks.length) {
    incidentPanel.classList.remove("active");
    incidentList.innerHTML = "";
    incidentMeta.textContent = "";
    return;
  }
  incidentPanel.classList.add("active");
  incidentList.innerHTML = "";
  failingChecks.forEach((check) => {
    const item = document.createElement("li");
    item.className = "incident-item";
    item.innerHTML = `
      <strong>${check.name}</strong>
      <span>${check.state ?? "brak stanu"} — ${check.error ?? "Brak szczegółów"}</span>
    `;
    incidentList.appendChild(item);
  });
  incidentMeta.textContent = `${failingChecks.length} element(y) wymagają uwagi`;
}

function formatTimestamp(date) {
  return date.toLocaleString("pl-PL", {
    year: "numeric",
    month: "short",
    day: "numeric",
    hour: "2-digit",
    minute: "2-digit",
  });
}

function formatTooltip(isoDate) {
  const date = new Date(isoDate);
  return date.toLocaleString("pl-PL", {
    month: "short",
    day: "2-digit",
    hour: "2-digit",
    minute: "2-digit",
  });
}

function formatRange(start, end) {
  const fmt = new Intl.DateTimeFormat("pl-PL", {
    month: "short",
    year: "numeric",
  });
  const startLabel = fmt.format(start);
  const endLabel = fmt.format(end);
  return startLabel === endLabel ? startLabel : `${startLabel} – ${endLabel}`;
}

async function fetchJSON(url) {
  const res = await fetch(url, { cache: "no-store" });
  if (!res.ok) {
    throw new Error(`Request ${url} failed with status ${res.status}`);
  }
  return res.json();
}

refresh();
setInterval(() => {
  refresh().catch((err) => console.error("Scheduled refresh failed", err));
}, REFRESH_INTERVAL);
