const statusCards = document.querySelector("#status-cards");
const rangeLabel = document.querySelector("#range-label");
const updatedAtEl = document.querySelector("#updated-at");
const refreshBtn = document.querySelector("#refresh-btn");
const incidentPanel = document.querySelector("#incident-panel");
const incidentList = document.querySelector("#incident-list");
const incidentMeta = document.querySelector("#incident-meta");
const rangeButtons = document.querySelectorAll(".range-button");
const debugPanel = document.querySelector("#debug-panel");
const debugLogEl = document.querySelector("#debug-log");
const debugClearBtn = document.querySelector("#debug-clear");

const REFRESH_INTERVAL = 30_000;
const HISTORY_POINTS = 80;
const RANGE_LABELS = {
  "24h": "Last 24 hours",
  "30d": "Last 30 days",
};
const DEBUG_VERSION = "20251101";
const DEBUG_LIMIT = 120;
const debugBuffer = [];
const debugEnabled = Boolean(debugPanel && debugLogEl);

let currentRange = "24h";

function logDebug(message, data) {
  if (!debugEnabled) {
    return;
  }
  const timestamp = new Date().toISOString();
  debugBuffer.push({ timestamp, message, data });
  while (debugBuffer.length > DEBUG_LIMIT) {
    debugBuffer.shift();
  }
  const lines = debugBuffer.map((entry) => {
    let payload = "";
    if (entry.data !== undefined) {
      try {
        payload = ` ${JSON.stringify(entry.data, replacerSafe)}`;
      } catch (err) {
        payload = ` {"debugError":"${String(err)}"}`;
      }
    }
    return `[${entry.timestamp}] ${entry.message}${payload}`;
  });
  debugLogEl.textContent = lines.join("\n") || "No debug events yet.";
  console.log(`[JobMonitor] ${message}`, data ?? "");
}

function replacerSafe(_key, value) {
  if (value instanceof Date) {
    return value.toISOString();
  }
  return value;
}

if (debugEnabled) {
  debugLogEl.textContent = "";
  logDebug("UI boot", { version: DEBUG_VERSION });
  debugClearBtn?.addEventListener("click", () => {
    debugBuffer.length = 0;
    logDebug("Debug log cleared by user");
  });
}

rangeButtons.forEach((button) => {
  button.addEventListener("click", () => {
    const { range } = button.dataset;
    if (!range || range === currentRange) {
      return;
    }
    refresh(range, "range-switch").catch((err) => console.error("Range switch failed", err));
  });
});

refreshBtn?.addEventListener("click", () => {
  refreshBtn.disabled = true;
  refresh(currentRange, "manual-click")
    .catch((err) => console.error("Manual refresh failed", err))
    .finally(() => (refreshBtn.disabled = false));
});

async function refresh(rangeKey = currentRange, reason = "unspecified") {
  logDebug("Refresh requested", { rangeKey, previousRange: currentRange, reason });
  setActiveRange(rangeKey);
  try {
    const url = `/api/cluster?range=${currentRange}`;
    logDebug("Fetching snapshot", { url });
    const snapshot = await fetchJSON(url);
    logDebug("Snapshot received", {
      nodes: Array.isArray(snapshot?.nodes) ? snapshot.nodes.length : 0,
      generated_at: snapshot?.generated_at,
      range: snapshot?.range || currentRange,
    });
    renderDashboard(snapshot);
  } catch (err) {
    console.error("Refresh failed", err);
    logDebug("Refresh failed", { error: err?.message || String(err) });
    showErrorState(err);
  }
}

function renderDashboard(snapshot) {
  const nodes = snapshot?.nodes || [];
  const generatedAt = snapshot?.generated_at ? new Date(snapshot.generated_at) : null;
  logDebug("Rendering dashboard", {
    nodes: nodes.length,
    generated_at: snapshot?.generated_at,
    range: snapshot?.range || currentRange,
  });
  if (snapshot?.range) {
    setActiveRange(snapshot.range);
  }

  if (generatedAt) {
    updatedAtEl.textContent = `Last updated: ${formatTimestamp(generatedAt)}`;
  } else {
    updatedAtEl.textContent = "Last updated: no data";
  }

  const start = snapshot?.range_start ? new Date(snapshot.range_start) : null;
  const end = snapshot?.range_end ? new Date(snapshot.range_end) : null;
  const rangeLabelText = RANGE_LABELS[currentRange] || "Selected range";
  if (start && end) {
    rangeLabel.textContent = `${rangeLabelText} (${formatRangeDetail(start, end)})`;
  } else {
    rangeLabel.textContent = rangeLabelText;
  }

  statusCards.innerHTML = "";

  if (!nodes.length) {
    statusCards.innerHTML =
      '<div class="empty-state">No data yet. Make sure the monitor has collected at least one sample.</div>';
  } else {
    const sortedNodes = [...nodes].sort((a, b) =>
      getNodeName(a).localeCompare(getNodeName(b), "en-US"),
    );
    sortedNodes.forEach((node) =>
      statusCards.appendChild(renderNodeCard(node, start, end)),
    );
  }

  renderIncidents(nodes);
}

function renderNodeCard(node, rangeStart, rangeEnd) {
  const info = node.node || {};
  const nodeName = info.name || info.id || "Unknown server";
  const nodeId = info.id || "unknown";
  const services = buildServiceData(node, rangeStart, rangeEnd);
  const nodeUptime = computeNodeUptime(services);
  const uptimeClass = uptimeLevel(nodeUptime);
  const uptimeLabel = Number.isFinite(nodeUptime)
    ? `${nodeUptime.toFixed(2)}% uptime`
    : "No data";

  const updatedAt = node.updated_at ? new Date(node.updated_at) : null;
  const sourceLabel = node.source === "local" ? "local" : "peer";

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
  if (node.connectivity) {
    meta.appendChild(createConnectivityBadge(node.connectivity));
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
    if (service.metric.missing) {
      meta.appendChild(createMetaBadge("Missing", `<span>${service.metric.missing}</span>`));
    }
  } else {
    meta.appendChild(createMetaBadge("Uptime", "<span>-</span>"));
  }

  head.appendChild(meta);
  row.appendChild(head);

  const timeline = document.createElement("div");
  timeline.className = "timeline";
  const segments = service.timeline || [];
  if (!segments.length) {
    const placeholder = document.createElement("div");
    placeholder.className = "meta-text";
    placeholder.textContent = "No historical data.";
    timeline.appendChild(placeholder);
  } else {
    segments.slice(-HISTORY_POINTS).forEach((segment) => {
      const dot = document.createElement("span");
      dot.className = `timeline-dot ${segment.className}`;
      dot.title = segment.tooltip;
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

function buildServiceData(node, rangeStart, rangeEnd) {
  const targetMap = new Map((node.targets || []).map((t) => [t.id, t]));
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
    ...targetMap.keys(),
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
      targetMap.get(id)?.name ||
      metric?.name ||
      latestCheck?.name ||
      (history.length ? history[history.length - 1].name : null) ||
      id;
    const intervalMs = getIntervalMs(node, history);
    const filledHistory = fillMissingHistory(
      history,
      intervalMs,
      rangeStart,
      rangeEnd,
      id,
      name,
    );
    const timeline = buildTimelineSegments(
      filledHistory,
      rangeStart,
      rangeEnd,
      HISTORY_POINTS,
    );
    services.push({
      id,
      name,
      metric,
      latestCheck,
      history: filledHistory,
      timeline,
    });
  });

  services.sort((a, b) => a.name.localeCompare(b.name, "en-US"));
  return services;
}

function getIntervalMs(node, history) {
  const minutes = Number(node?.node?.interval_minutes);
  if (Number.isFinite(minutes) && minutes > 0) {
    return minutes * 60_000;
  }
  return estimateIntervalFromHistory(history);
}

function estimateIntervalFromHistory(history) {
  if (!history || history.length < 2) {
    return 5 * 60_000;
  }
  const diffs = [];
  for (let i = 1; i < history.length; i += 1) {
    const prev = new Date(history[i - 1].timestamp).getTime();
    const curr = new Date(history[i].timestamp).getTime();
    const diff = curr - prev;
    if (diff > 0) {
      diffs.push(diff);
    }
  }
  if (!diffs.length) {
    return 5 * 60_000;
  }
  diffs.sort((a, b) => a - b);
  const mid = Math.floor(diffs.length / 2);
  const median =
    diffs.length % 2 === 0
      ? (diffs[mid - 1] + diffs[mid]) / 2
      : diffs[mid];
  return Math.max(median, 60_000);
}

function fillMissingHistory(history, intervalMs, rangeStart, rangeEnd, id, name) {
  if (!Array.isArray(history)) {
    history = [];
  }
  if (!intervalMs || !Number.isFinite(intervalMs) || intervalMs <= 0) {
    return history;
  }

  const startMs = rangeStart instanceof Date ? rangeStart.getTime() : null;
  const endMs = rangeEnd instanceof Date ? rangeEnd.getTime() : null;
  const sorted = [...history].sort(
    (a, b) => new Date(a.timestamp) - new Date(b.timestamp),
  );

  const createMissing = (ts) => ({
    id,
    name,
    ok: false,
    state: "missing",
    error: "no data",
    timestamp: new Date(ts).toISOString(),
    synthetic: true,
  });

  const withinRange = (ts) => {
    if (startMs != null && ts < startMs) {
      return false;
    }
    if (endMs != null && ts > endMs) {
      return false;
    }
    return true;
  };

  if (!sorted.length) {
    if (startMs != null && endMs != null) {
      const filled = [];
      for (let ts = startMs; ts <= endMs; ts += intervalMs) {
        filled.push(createMissing(ts));
      }
      return filled;
    }
    return [];
  }

  const filled = [];
  const threshold = intervalMs * 1.5;

  const pushMissingSlots = (fromTs, toTs) => {
    let ts = fromTs + intervalMs;
    while (ts < toTs - intervalMs * 0.25) {
      if (!withinRange(ts)) {
        if (endMs != null && ts > endMs) {
          break;
        }
        ts += intervalMs;
        continue;
      }
      filled.push(createMissing(ts));
      ts += intervalMs;
    }
  };

  const firstTs = new Date(sorted[0].timestamp).getTime();
  if (startMs != null && firstTs - startMs > intervalMs * 0.5) {
    pushMissingSlots(startMs - intervalMs, firstTs);
  }

  let prevTs = firstTs;
  filled.push(sorted[0]);
  for (let i = 1; i < sorted.length; i += 1) {
    const currentTs = new Date(sorted[i].timestamp).getTime();
    if (currentTs - prevTs > threshold) {
      pushMissingSlots(prevTs, currentTs);
    }
    filled.push(sorted[i]);
    prevTs = currentTs;
  }

  if (endMs != null && endMs - prevTs > intervalMs * 0.5) {
    pushMissingSlots(prevTs, endMs + intervalMs);
  }

  return filled;
}

function buildTimelineSegments(history, rangeStart, rangeEnd, segmentsCount) {
  if (!segmentsCount || segmentsCount <= 0) {
    return [];
  }

  const sorted = [...history].sort(
    (a, b) => new Date(a.timestamp) - new Date(b.timestamp),
  );
  const startMs =
    rangeStart instanceof Date && !Number.isNaN(rangeStart.getTime())
      ? rangeStart.getTime()
      : (sorted.length
      ? new Date(sorted[0].timestamp).getTime()
      : Date.now() - segmentsCount * 60_000);
  let endMs =
    rangeEnd instanceof Date && !Number.isNaN(rangeEnd.getTime())
      ? rangeEnd.getTime()
      : (sorted.length
      ? new Date(sorted[sorted.length - 1].timestamp).getTime()
      : startMs + segmentsCount * 60_000);

  if (endMs <= startMs) {
    endMs = startMs + segmentsCount * 60_000;
  }

  const bucketSize = (endMs - startMs) / segmentsCount;
  if (!Number.isFinite(bucketSize) || bucketSize <= 0) {
    return [];
  }

  const segments = [];
  let index = 0;
  for (let i = 0; i < segmentsCount; i += 1) {
    const bucketStart = startMs + bucketSize * i;
    const bucketEnd = i === segmentsCount - 1 ? endMs : bucketStart + bucketSize;

    while (
      index < sorted.length &&
      new Date(sorted[index].timestamp).getTime() < bucketStart
    ) {
      index += 1;
    }

    let cursor = index;
    const bucketEntries = [];
    while (cursor < sorted.length) {
      const ts = new Date(sorted[cursor].timestamp).getTime();
      if (ts >= bucketEnd) {
        break;
      }
      bucketEntries.push(sorted[cursor]);
      cursor += 1;
    }
    index = cursor;

    const bucket = determineBucketState(bucketEntries);
    const tooltip = formatBucketTooltip(bucketStart, bucketEnd, bucket.label);
    segments.push({
      className: `state-${bucket.className}`,
      tooltip,
    });
  }

  return segments;
}

function determineBucketState(entries) {
  if (!entries || !entries.length) {
    return { className: "missing", label: "No data" };
  }
  let hasError = false;
  let hasWarning = false;
  let hasSuccess = false;
  let hasMissing = false;

  entries.forEach((entry) => {
    const state = (entry.state || "").toLowerCase();
    const hasErrorState =
      !entry.ok &&
      (state === "inactive" ||
        state === "failed" ||
        state === "degraded" ||
        (!state && entry?.error));

    if (hasErrorState) {
      hasError = true;
      return;
    }
    if (entry.ok || state === "active" || state === "running") {
      hasSuccess = true;
      return;
    }
    if (state === "missing" || entry.synthetic) {
      hasMissing = true;
      return;
    }
    if (["activating", "deactivating", "reloading", "maintenance"].includes(state)) {
      hasWarning = true;
      return;
    }
    if (!state || state === "unknown") {
      hasMissing = true;
      return;
    }
    hasError = true;
  });

  if (hasError) {
    return { className: "error", label: "Unavailable" };
  }
  if (hasMissing) {
    return { className: "missing", label: "No data" };
  }
  if (hasWarning) {
    return { className: "warning", label: "Transitioning" };
  }
  if (hasSuccess) {
    return { className: "success", label: "Operational" };
  }
  return { className: "missing", label: "No data" };
}

function formatBucketTooltip(startMs, endMs, label) {
  const fmt = new Intl.DateTimeFormat("en-US", {
    month: "short",
    day: "2-digit",
    hour: "2-digit",
    minute: "2-digit",
  });
  const start = fmt.format(new Date(startMs));
  const end = fmt.format(new Date(endMs));
  return `${start} - ${end}: ${label}`;
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
    if (node.connectivity && !node.connectivity.ok) {
      incidents.push({
        title: `${nodeName} - connectivity`,
        details:
          node.connectivity.error ||
          `No response from ${node.connectivity.target || "probe target"}`,
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
  logDebug("Incidents recalculated", { count: incidents.length });

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
  if (state === "missing") {
    return { label: "missing data", className: "warning" };
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
  if (normalized === "missing") {
    return "state-missing";
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

function createConnectivityBadge(connectivity) {
  const state = resolveConnectivityState(connectivity);
  logDebug("Connectivity sample", {
    target: connectivity?.target,
    ok: connectivity?.ok,
    latency_ms: connectivity?.latency_ms,
    checked_at: connectivity?.checked_at,
  });
  const wrapper = document.createElement("span");
  wrapper.className = "meta-badge connectivity";

  const label = document.createElement("span");
  label.textContent = "Connectivity:";
  wrapper.appendChild(label);

  const value = document.createElement("span");
  value.className = state.className;
  value.textContent = state.label;
  wrapper.appendChild(value);

  if (state.detail) {
    const detail = document.createElement("span");
    detail.className = "connectivity-detail";
    detail.textContent = state.detail;
    wrapper.appendChild(detail);
  }

  if (state.tooltip) {
    wrapper.title = state.tooltip;
  }

  return wrapper;
}

function resolveConnectivityState(connectivity) {
  if (!connectivity) {
    return {
      label: "unknown",
      className: "status-unknown",
      detail: "",
      tooltip: "",
    };
  }

  const checkedAt = connectivity.checked_at ? new Date(connectivity.checked_at) : null;
  const tooltipParts = [];
  if (checkedAt) {
    tooltipParts.push(`Checked: ${formatTimestamp(checkedAt)}`);
  }
  if (connectivity.error) {
    tooltipParts.push(`Error: ${connectivity.error}`);
  }

  let label = "pending";
  let className = "status-unknown";
  let detail = "";

  if (connectivity.ok) {
    label = "online";
    className = "status-ok";
    if (Number.isFinite(connectivity.latency_ms)) {
      detail = `${connectivity.latency_ms} ms`;
    }
  } else if (connectivity.error) {
    label = "offline";
    className = "status-error";
    detail = connectivity.error;
  }

  return {
    label,
    className,
    detail,
    tooltip: tooltipParts.join("\n"),
  };
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

function formatRangeDetail(start, end) {
  const fmt = new Intl.DateTimeFormat("en-US", {
    month: "short",
    day: "2-digit",
    hour: "2-digit",
    minute: "2-digit",
  });
  return `${fmt.format(start)} – ${fmt.format(end)}`;
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

refresh(currentRange, "initial-load");
setInterval(() => {
  refresh(currentRange, "scheduled").catch((err) =>
    console.error("Scheduled refresh failed", err),
  );
}, REFRESH_INTERVAL);

function setActiveRange(range) {
  if (range) {
    currentRange = range;
  }
  rangeButtons.forEach((button) => {
    button.classList.toggle("active", button.dataset.range === currentRange);
  });
}

