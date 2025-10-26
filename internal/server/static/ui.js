const statusCards = document.querySelector("#status-cards");
const rangeLabel = document.querySelector("#range-label");
const updatedAtEl = document.querySelector("#updated-at");
const refreshBtn = document.querySelector("#refresh-btn");
const incidentPanel = document.querySelector("#incident-panel");
const incidentList = document.querySelector("#incident-list");
const incidentMeta = document.querySelector("#incident-meta");

const REFRESH_INTERVAL = 30_000;
const HISTORY_POINTS = 80;

refreshBtn?.addEventListener("click", () => {
  refreshBtn.disabled = true;
  refresh()
    .catch((err) => console.error("Manual refresh failed", err))
    .finally(() => (refreshBtn.disabled = false));
});

async function refresh() {
  try {
    const snapshot = await fetchJSON("/api/cluster");
    renderDashboard(snapshot);
  } catch (err) {
    console.error("Refresh failed", err);
    showErrorState(err);
  }
}

function renderDashboard(snapshot) {
  const nodes = snapshot?.nodes || [];
  const generatedAt = snapshot?.generated_at ? new Date(snapshot.generated_at) : null;

  if (generatedAt) {
    updatedAtEl.textContent = `Last updated: ${formatTimestamp(generatedAt)}`;
  } else {
    updatedAtEl.textContent = "Last updated: no data";
  }

  const range = deriveRange(nodes);
  if (range) {
    rangeLabel.textContent = `Data range: ${formatRange(range.start, range.end)}`;
  } else {
    rangeLabel.textContent = "No history available";
  }

  statusCards.innerHTML = "";

  if (!nodes.length) {
    statusCards.innerHTML =
      '<div class="empty-state">No data yet. Make sure the monitor has collected at least one sample.</div>';
  } else {
    const sortedNodes = [...nodes].sort((a, b) =>
      getNodeName(a).localeCompare(getNodeName(b), "en-US"),
    );
    sortedNodes.forEach((node) => statusCards.appendChild(renderNodeCard(node)));
  }

  renderIncidents(nodes);
}

function renderNodeCard(node) {
  const info = node.node || {};
  const nodeName = info.name || info.id || "Nieznany serwer";
  const nodeId = info.id || "unknown";
  const services = buildServiceData(node);
  const nodeUptime = computeNodeUptime(services);
  const uptimeClass = uptimeLevel(nodeUptime);
  const uptimeLabel = Number.isFinite(nodeUptime)
    ? `${nodeUptime.toFixed(2)}% uptime`
    : "No data";

  const updatedAt = node.updated_at ? new Date(node.updated_at) : null;
  const sourceLabel = node.source === "local" ? "lokalny" : "peer";

  const card = document.createElement("article");
  card.className = "status-card";

  const head = document.createElement("div");
  head.className = "card-head";
  head.innerHTML = `
    <div class="card-title">
      <span class="service-name">${nodeName}</span>
      <span class="meta-text">ID: ${nodeId} - ${sourceLabel}</span>
    </div>
    <span class="uptime-pill ${uptimeClass}">${uptimeLabel}</span>
  `;
  card.appendChild(head);

  const meta = document.createElement("div");
  meta.className = "card-meta";
  meta.appendChild(createMetaBadge("Services", `<span>${services.length}</span>`));
  if (updatedAt) {
    meta.appendChild(
      createMetaBadge("Last sync", `<span>${formatTimestamp(updatedAt)}</span>`),
    );
  }
  if (node.status?.timestamp) {
    meta.appendChild(
      createMetaBadge(
        "Latest sample",
        `<span>${formatTimestamp(new Date(node.status.timestamp))}</span>`,
      ),
    );
  }
  card.appendChild(meta);

  const list = document.createElement("div");
  list.className = "service-list";
  if (!services.length) {
    const placeholder = document.createElement("div");
    placeholder.className = "meta-text";
    placeholder.textContent = "No history collected for this node.";
    list.appendChild(placeholder);
  } else {
    services.forEach((service) => list.appendChild(renderServiceRow(service)));
  }
  card.appendChild(list);

  if (node.error) {
    const errorBox = document.createElement("div");
    errorBox.className = "card-error";
    errorBox.textContent = node.error;
    card.appendChild(errorBox);
  }

  return card;
}

function renderServiceRow(service) {
  const row = document.createElement("div");
  row.className = "service-row";

  const head = document.createElement("div");
  head.className = "service-row-head";

  const title = document.createElement("div");
  title.className = "service-row-title";
  title.innerHTML = `
    <span class="service-label">${service.name}</span>
    <span class="meta-text">ID: ${service.id}</span>
  `;
  head.appendChild(title);

  const meta = document.createElement("div");
  meta.className = "service-row-meta";

  const stateChip = resolveStateChip(service.latestCheck);
  meta.appendChild(
    createMetaBadge("Stan", `<span class="state-chip ${stateChip.className}">${stateChip.label}</span>`),
  );

  if (service.metric && Number.isFinite(service.metric.uptime_percent)) {
    const className = uptimeLevel(service.metric.uptime_percent);
    meta.appendChild(
      createMetaBadge(
        "Uptime",
        `<span class="uptime-stat ${className}">${service.metric.uptime_percent.toFixed(
          2,
        )}%</span>`,
      ),
    );
    meta.appendChild(
      createMetaBadge(
        "Checks",
        `<span>${service.metric.total_checks} (${service.metric.passing}/${service.metric.failing})</span>`,
      ),
    );
  } else {
    meta.appendChild(createMetaBadge("Uptime", "<span>-</span>"));
  }

  head.appendChild(meta);
  row.appendChild(head);

  const timeline = document.createElement("div");
  timeline.className = "timeline";
  const points = service.history.slice(-HISTORY_POINTS);
  if (!points.length) {
    const placeholder = document.createElement("div");
    placeholder.className = "meta-text";
    placeholder.textContent = "No historical data.";
    timeline.appendChild(placeholder);
  } else {
    points.forEach((point) => {
      const dot = document.createElement("span");
      dot.className = `timeline-dot ${stateToClass(point.state, point.ok)}`;
      const errorSuffix = point.error ? ` EUR ${point.error}` : "";
      dot.title = `${formatTooltip(point.timestamp)} EUR" ${point.state}${errorSuffix}`;
      timeline.appendChild(dot);
    });
  }
  row.appendChild(timeline);

  if (service.latestCheck?.error) {
    const errorBox = document.createElement("div");
    errorBox.className = "card-error";
    errorBox.textContent = service.latestCheck.error;
    row.appendChild(errorBox);
  }

  return row;
}

function buildServiceData(node) {
  const metricsMap = new Map((node.services || []).map((svc) => [svc.id, svc]));
  const latestMap = new Map(
    (node.status?.checks || []).map((check) => [check.id, check]),
  );

  const historyMap = new Map();
  (node.history || []).forEach((entry) => {
    const timestamp = entry.timestamp;
    (entry.checks || []).forEach((check) => {
      const list = historyMap.get(check.id) || [];
      list.push({
        id: check.id,
        name: check.name || check.id,
        ok: Boolean(check.ok),
        state: check.state || (check.ok ? "active" : "unknown"),
        error: check.error,
        timestamp,
      });
      historyMap.set(check.id, list);
    });
  });

  const ids = new Set([
    ...metricsMap.keys(),
    ...latestMap.keys(),
    ...historyMap.keys(),
  ]);

  const services = [];
  ids.forEach((id) => {
    const metric = metricsMap.get(id);
    const latestCheck = latestMap.get(id);
    const history = (historyMap.get(id) || []).sort(
      (a, b) => new Date(a.timestamp) - new Date(b.timestamp),
    );
    let name =
      metric?.name ||
      latestCheck?.name ||
      (history.length ? history[history.length - 1].name : null) ||
      id;
    services.push({ id, name, metric, latestCheck, history });
  });

  services.sort((a, b) => a.name.localeCompare(b.name, "en-US"));
  return services;
}

function computeNodeUptime(services) {
  const values = services
    .map((svc) => svc.metric?.uptime_percent)
    .filter((value) => Number.isFinite(value));
  if (!values.length) {
    return Number.NaN;
  }
  const sum = values.reduce((acc, value) => acc + value, 0);
  return sum / values.length;
}

function deriveRange(nodes) {
  let start = null;
  let end = null;
  nodes.forEach((node) => {
    (node.history || []).forEach((entry) => {
      const ts = new Date(entry.timestamp);
      if (!start || ts < start) start = ts;
      if (!end || ts > end) end = ts;
    });
  });
  if (!start || !end) {
    return null;
  }
  return { start, end };
}

function renderIncidents(nodes) {
  const incidents = [];
  nodes.forEach((node) => {
    const nodeName = getNodeName(node);
    if (node.error) {
      incidents.push({
        title: `${nodeName} - sync error`,
        details: node.error,
      });
    }
    (node.status?.checks || [])
      .filter((check) => !check.ok)
      .forEach((check) => {
        incidents.push({
          title: `${nodeName} / ${check.name || check.id}`,
          details: `${check.state || "no state"} - ${check.error || "no details"}`,
        });
      });
  });

  if (!incidents.length) {
    incidentPanel.classList.remove("active");
    incidentList.innerHTML = "";
    incidentMeta.textContent = "";
    return;
  }

  incidentPanel.classList.add("active");
  incidentList.innerHTML = "";
  incidents.forEach((item) => {
    const el = document.createElement("li");
    el.className = "incident-item";
    el.innerHTML = `<strong>${item.title}</strong><span>${item.details}</span>`;
    incidentList.appendChild(el);
  });
  incidentMeta.textContent = `${incidents.length} item(s) require attention`;
}

function getNodeName(node) {
  return node?.node?.name || node?.node?.id || "Unknown server";
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
    return { label: "no data", className: "unknown" };
  }
  const state = (latestCheck.state || "").toLowerCase();
  if (latestCheck.ok || state === "active" || state === "running") {
    return { label: state || "active", className: "" };
  }
  if (["activating", "deactivating", "reloading"].includes(state)) {
    return { label: state, className: "warning" };
  }
  if (!state) {
    return { label: "unknown", className: "unknown" };
  }
  return { label: state, className: "error" };
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

function createMetaBadge(label, valueHTML) {
  const wrapper = document.createElement("span");
  wrapper.className = "meta-badge";
  wrapper.innerHTML = `<span>${label}:</span>${valueHTML}`;
  return wrapper;
}

function formatTimestamp(date) {
  return date.toLocaleString("en-US", {
    year: "numeric",
    month: "short",
    day: "numeric",
    hour: "2-digit",
    minute: "2-digit",
  });
}

function formatTooltip(isoDate) {
  const date = new Date(isoDate);
  return date.toLocaleString("en-US", {
    month: "short",
    day: "2-digit",
    hour: "2-digit",
    minute: "2-digit",
  });
}

function formatRange(start, end) {
  const formatter = new Intl.DateTimeFormat("en-US", {
    month: "short",
    year: "numeric",
  });
  const startLabel = formatter.format(start);
  const endLabel = formatter.format(end);
  return startLabel === endLabel ? startLabel : `${startLabel} - ${endLabel}`;
}

async function fetchJSON(url) {
  const res = await fetch(url, { cache: "no-store" });
  if (!res.ok) {
    throw new Error(`Request ${url} failed with status ${res.status}`);
  }
  return res.json();
}

function showErrorState(err) {
  statusCards.innerHTML = `<div class="empty-state">Error fetching data: ${
    err?.message || err
  }</div>`;
}

refresh();
setInterval(() => {
  refresh().catch((err) => console.error("Scheduled refresh failed", err));
}, REFRESH_INTERVAL);

