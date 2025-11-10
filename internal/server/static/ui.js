const statusCards = document.querySelector("#status-cards");
const rangeLabel = document.querySelector("#range-label");
const updatedAtEl = document.querySelector("#updated-at");
const refreshBtn = document.querySelector("#refresh-btn");
const incidentPanel = document.querySelector("#incident-panel");
const incidentList = document.querySelector("#incident-list");
const incidentMeta = document.querySelector("#incident-meta");
const rangeButtons = document.querySelectorAll(".range-button");
const viewButtons = document.querySelectorAll("[data-view]");
const dashboardSections = document.querySelectorAll("[data-dashboard-section]");
const overviewPanel = document.querySelector("#overview-panel");
const overviewGrid = document.querySelector("#overview-grid");
const overviewMeta = document.querySelector("#overview-meta");
const connectionBanner = document.querySelector("#connection-banner");
const debugPanel = document.querySelector("#debug-panel");
const debugLogEl = document.querySelector("#debug-log");
const debugClearBtn = document.querySelector("#debug-clear");

const REFRESH_INTERVAL = 30_000;
const OVERVIEW_REFRESH_INTERVAL = 60_000;
const HISTORY_POINTS = 80;
const MAX_TIMELINE_DETAILS = 3;
const RANGE_LABELS = {
  "24h": "Last 24 hours",
  "30d": "Last 30 days",
};
const OVERVIEW_LIMIT = 9;
const OVERVIEW_BUCKET_COUNT = 3;
const DEBUG_VERSION = "20251108";
const SVG_NS = "http://www.w3.org/2000/svg";
const DEBUG_LIMIT = 120;
const debugBuffer = [];
const debugEnabled = Boolean(debugPanel && debugLogEl);

let currentRange = "24h";
let currentViewMode = "dashboard";
let overviewSocket = null;
let overviewReconnectTimer = null;
let overviewFallbackTimer = null;

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

viewButtons.forEach((button) => {
  button.addEventListener("click", () => {
    const { view } = button.dataset;
    if (!view || view === currentViewMode) {
      return;
    }
    setActiveView(view);
  });
});
setActiveView(currentViewMode);

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
  card.appendChild(meta);

  const connectivityData = buildConnectivityData(node, rangeStart, rangeEnd);
  if (connectivityData) {
    card.appendChild(renderConnectivitySection(connectivityData));
  }

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
  const labelRow = document.createElement("div");
  labelRow.className = "service-label-row";
  const label = document.createElement("span");
  label.className = "service-label";
  label.textContent = service.name;
  labelRow.appendChild(label);
  if (service.url) {
    const link = document.createElement("a");
    link.className = "service-link";
    link.href = service.url;
    link.target = "_blank";
    link.rel = "noopener noreferrer";
    const labelText = service.name || service.id;
    link.setAttribute("aria-label", `Open ${labelText} in a new tab`);
    link.title = `Open ${labelText} in a new tab`;
    link.appendChild(createExternalLinkIcon());
    labelRow.appendChild(link);
  }
  title.appendChild(labelRow);
  const metaText = document.createElement("span");
  metaText.className = "meta-text";
  metaText.textContent = `ID: ${service.id}`;
  title.appendChild(metaText);
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

function createExternalLinkIcon() {
  const svg = document.createElementNS(SVG_NS, "svg");
  svg.setAttribute("viewBox", "0 0 20 20");
  svg.setAttribute("aria-hidden", "true");
  svg.setAttribute("focusable", "false");
  svg.classList.add("service-link-icon");
  const path = document.createElementNS(SVG_NS, "path");
  path.setAttribute(
    "d",
    "M13 3h4v4h-2V5.41l-7.59 7.6-1.42-1.42 7.6-7.59H13V3zm3 8v6H4V4h6V2H4a2 2 0 0 0-2 2v12c0 1.1.9 2 2 2h12a2 2 0 0 0 2-2v-6h-2z",
  );
  path.setAttribute("fill", "currentColor");
  svg.appendChild(path);
  return svg;
}

function renderConnectivitySection(data) {
  if (!data) {
    return null;
  }
  const section = document.createElement("div");
  section.className = "connectivity-section";

  const head = document.createElement("div");
  head.className = "connectivity-head";

  const left = document.createElement("div");
  left.className = "connectivity-head-left";
  const title = document.createElement("span");
  title.className = "section-title";
  title.textContent = `Connectivity (${data.rangeLabel})`;
  left.appendChild(title);

  const summary = document.createElement("span");
  summary.className = "meta-text connectivity-summary";
  summary.textContent = data.summary;
  left.appendChild(summary);
  head.appendChild(left);

  const right = document.createElement("div");
  right.className = "connectivity-head-right";
  if (data.latestChip) {
    const chip = document.createElement("span");
    chip.className = `state-chip ${data.latestChip.className}`;
    chip.textContent = data.latestChip.label;
    right.appendChild(chip);
  }
  if (data.latestLatency) {
    const latency = document.createElement("span");
    latency.className = "meta-text connectivity-latency";
    latency.textContent = data.latestLatency;
    right.appendChild(latency);
  }
  head.appendChild(right);
  section.appendChild(head);

  const timeline = document.createElement("div");
  timeline.className = "timeline connectivity-timeline";
  if (!Array.isArray(data.timeline) || !data.timeline.length) {
    const placeholder = document.createElement("div");
    placeholder.className = "meta-text";
    placeholder.textContent = "No connectivity data.";
    timeline.appendChild(placeholder);
  } else {
    data.timeline.slice(-HISTORY_POINTS).forEach((segment) => {
      const dot = document.createElement("span");
      dot.className = `timeline-dot ${segment.className}`;
      dot.title = segment.tooltip;
      timeline.appendChild(dot);
    });
  }
  section.appendChild(timeline);

  return section;
}

function buildConnectivityData(node, rangeStart, rangeEnd) {
  const historyRaw = Array.isArray(node.connectivity_history)
    ? node.connectivity_history
    : [];
  const latest = node.connectivity || null;
  const timelinePointsRaw =
    node.connectivity_timeline || node.connectivityTimeline || null;
  if (!historyRaw.length && !latest) {
    return null;
  }

  const history = historyRaw
    .map((entry) => {
      const timestamp = entry?.checked_at || entry?.timestamp;
      if (!timestamp) {
        return null;
      }
      const iso = new Date(timestamp).toISOString();
      if (Number.isNaN(new Date(iso).getTime())) {
        return null;
      }
      const ok = Boolean(entry.ok);
      const state = ok ? "online" : entry.error ? "offline" : "unknown";
      return {
        id: "connectivity",
        name: "Connectivity",
        ok,
        state,
        error: entry.error,
        latency_ms: entry.latency_ms,
        timestamp: iso,
      };
    })
    .filter(Boolean);

  if (latest?.checked_at) {
    const isoLatest = new Date(latest.checked_at).toISOString();
    if (!Number.isNaN(new Date(isoLatest).getTime())) {
      const exists = history.some((item) => item.timestamp === isoLatest);
      if (!exists) {
        history.push({
          id: "connectivity",
          name: "Connectivity",
          ok: Boolean(latest.ok),
          state: latest.ok ? "online" : latest.error ? "offline" : "unknown",
          error: latest.error,
          latency_ms: latest.latency_ms,
          timestamp: isoLatest,
        });
      }
    }
  }

  if (!history.length) {
    return null;
  }

  history.sort((a, b) => new Date(a.timestamp) - new Date(b.timestamp));

  let windowStart = rangeStart instanceof Date ? rangeStart : null;
  let windowEnd = rangeEnd instanceof Date ? rangeEnd : null;
  if (
    !(windowStart instanceof Date) ||
    Number.isNaN(windowStart?.getTime()) ||
    !(windowEnd instanceof Date) ||
    Number.isNaN(windowEnd?.getTime())
  ) {
    windowEnd = new Date();
    windowStart = new Date(windowEnd.getTime() - 24 * 60 * 60 * 1000);
  }

  let filledHistory = history;
  let timeline = mapTimelinePoints(timelinePointsRaw);
  if (!timeline.length) {
    const intervalMs = getConnectivityIntervalMs(node, history);
    filledHistory = fillMissingHistory(
      history,
      intervalMs,
      windowStart,
      windowEnd,
      "connectivity",
      "Connectivity",
    );
    timeline = buildTimelineSegments(
      filledHistory,
      windowStart,
      windowEnd,
      HISTORY_POINTS,
    );
  }
  const stats = computeConnectivityStats(filledHistory);
  if (stats) {
    logDebug("Connectivity stats", stats);
  }
  const summary = formatConnectivitySummary(stats);
  if (!timeline.length && (!stats || !stats.total)) {
    return null;
  }

  const latestChip = resolveStateChip(
    latest
      ? {
          ok: Boolean(latest.ok),
          state: latest.ok ? "online" : latest.error ? "offline" : "unknown",
        }
      : { ok: false, state: "missing" },
  );
  const latencyLabel = formatConnectivityLatency(latest);
  const rangeLabel =
    windowStart && windowEnd
      ? formatRangeDetail(windowStart, windowEnd)
      : RANGE_LABELS[currentRange] || "Selected range";

  return {
    timeline,
    summary,
    stats,
    rangeLabel,
    latestChip,
    latestLatency: latencyLabel,
  };
}

function computeConnectivityStats(history) {
  if (!Array.isArray(history) || !history.length) {
    return null;
  }
  let ok = 0;
  let down = 0;
  let missing = 0;
  history.forEach((entry) => {
    if (entry.synthetic && entry.state === "missing") {
      missing += 1;
      return;
    }
    if (entry.ok) {
      ok += 1;
      return;
    }
    down += 1;
  });
  const total = ok + down + missing;
  const uptime = total > 0 ? (ok / total) * 100 : NaN;
  return {
    total,
    ok,
    down,
    missing,
    uptime: Number.isFinite(uptime) ? uptime : null,
  };
}

function formatConnectivitySummary(stats) {
  if (!stats || !stats.total) {
    return "No connectivity data.";
  }
  const parts = [];
  if (Number.isFinite(stats.uptime)) {
    parts.push(`${stats.uptime.toFixed(2)}% online`);
  }
  parts.push(`${stats.ok} ok`);
  if (stats.down) {
    parts.push(`${stats.down} fail`);
  }
  if (stats.missing) {
    parts.push(`${stats.missing} missing`);
  }
  return parts.join(" · ");
}

function getConnectivityIntervalMs(node, history) {
  let intervalMs = estimateIntervalFromHistory(history);
  if (!Number.isFinite(intervalMs) || intervalMs <= 0) {
    const configured = Number(node?.node?.connectivity_interval_seconds);
    if (Number.isFinite(configured) && configured > 0) {
      intervalMs = configured * 1000;
    }
  }
  if (!Number.isFinite(intervalMs) || intervalMs <= 0) {
    intervalMs = 60_000;
  }
  return intervalMs;
}

function formatConnectivityLatency(sample) {
  if (!sample) {
    return "";
  }
  if (sample.ok && Number.isFinite(sample.latency_ms)) {
    return `${sample.latency_ms} ms`;
  }
  if (!sample.ok && sample.error) {
    return sample.error;
  }
  if (Number.isFinite(sample.latency_ms)) {
    return `${sample.latency_ms} ms`;
  }
  if (sample.checked_at) {
    return formatTimestamp(new Date(sample.checked_at));
  }
  return "";
}

function buildServiceData(node, rangeStart, rangeEnd) {
  const targetMap = new Map((node.targets || []).map((t) => [t.id, t]));
  const metricsMap = new Map((node.services || []).map((svc) => [svc.id, svc]));
  const latestMap = new Map(
    (node.status?.checks || []).map((check) => [check.id, check]),
  );
  const compactTimelineMap = buildCompactTimelineMap(
    node.service_timelines || node.serviceTimelines,
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
    ...compactTimelineMap.keys(),
  ]);

  const services = [];
  ids.forEach((id) => {
    const target = targetMap.get(id);
    const metric = metricsMap.get(id);
    const latestCheck = latestMap.get(id);
    const compact = compactTimelineMap.get(id);
    let timeline = compact?.segments || null;
    let history = null;
    const rawHistory = (historyMap.get(id) || []).sort(
      (a, b) => new Date(a.timestamp) - new Date(b.timestamp),
    );
    let name =
      compact?.name ||
      target?.name ||
      metric?.name ||
      latestCheck?.name ||
      (rawHistory.length ? rawHistory[rawHistory.length - 1].name : null) ||
      id;
    const serviceUrl = normalizeServiceUrl(target?.url);
    if (!timeline || !timeline.length) {
      const intervalMs = getIntervalMs(node, rawHistory);
      history = fillMissingHistory(
        rawHistory,
        intervalMs,
        rangeStart,
        rangeEnd,
        id,
        name,
      );
      timeline = buildTimelineSegments(
        history,
        rangeStart,
        rangeEnd,
        HISTORY_POINTS,
      );
    }
    services.push({
      id,
      name,
      url: serviceUrl,
      metric,
      latestCheck,
      history,
      timeline,
    });
  });

  services.sort((a, b) => a.name.localeCompare(b.name, "en-US"));
  return services;
}

function normalizeServiceUrl(url) {
  if (typeof url !== "string") {
    return null;
  }
  const trimmed = url.trim();
  if (!trimmed) {
    return null;
  }
  const hasProtocol = /^[a-zA-Z][a-zA-Z\d+\-.]*:/.test(trimmed);
  const protocolRelative = trimmed.startsWith("//");
  const looksRelative =
    trimmed.startsWith("/") ||
    trimmed.startsWith("./") ||
    trimmed.startsWith("../");
  let candidate = trimmed;
  let base = undefined;
  if (hasProtocol) {
    candidate = trimmed;
  } else if (protocolRelative) {
    candidate = `https:${trimmed}`;
  } else if (looksRelative) {
    base = window.location.origin;
  } else {
    candidate = `https://${trimmed}`;
  }
  try {
    const parsed = base ? new URL(candidate, base) : new URL(candidate);
    if (parsed.protocol !== "http:" && parsed.protocol !== "https:") {
      return null;
    }
    return parsed.href;
  } catch (_err) {
    return null;
  }
}

function buildCompactTimelineMap(raw) {
  const map = new Map();
  if (!Array.isArray(raw)) {
    return map;
  }
  raw.forEach((item) => {
    if (!item?.service_id) {
      return;
    }
    const segments = Array.isArray(item.timeline)
      ? item.timeline.map((point) => compactPointToSegment(point))
      : [];
    map.set(item.service_id, {
      name: item.service_name || item.serviceId || item.service_id,
      segments,
    });
  });
  return map;
}

function mapTimelinePoints(raw) {
  if (!Array.isArray(raw)) {
    return [];
  }
  return raw.map((point) => compactPointToSegment(point));
}

function compactPointToSegment(point) {
  if (!point || typeof point !== "object") {
    return { className: "state-missing", tooltip: "No data" };
  }
  const className =
    point.className ||
    (typeof point.status === "string" && point.status.startsWith("state-")
      ? point.status
      : `state-${String(point.status || "missing").toLowerCase()}`);
  const tooltip = point.tooltip || formatCompactTimelineTooltip(point);
  return { className, tooltip };
}

function formatCompactTimelineTooltip(point) {
  const label = point.label || "Status";
  const start = point.start ? new Date(point.start) : null;
  const end = point.end ? new Date(point.end) : null;
  const base =
    start && end
      ? formatBucketTooltip(start.getTime(), end.getTime(), label)
      : label;
  const details = Array.isArray(point.details)
    ? point.details.slice(0, MAX_TIMELINE_DETAILS).map((detail) => {
        const ts = detail.timestamp ? new Date(detail.timestamp) : null;
        const tsLabel = ts ? formatTimestamp(ts) : "Unknown time";
        const state = detail.state || "no state";
        const error = detail.error ? ` - ${detail.error}` : "";
        return `${tsLabel}: ${state}${error}`;
      })
    : [];
  return details.length ? `${base}\n${details.join("\n")}` : base;
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
  return `${fmt.format(start)} - ${fmt.format(end)}`;
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
initOverviewChannel();

function setActiveRange(range) {
  if (range) {
    currentRange = range;
  }
  rangeButtons.forEach((button) => {
    button.classList.toggle("active", button.dataset.range === currentRange);
  });
}

function setActiveView(view) {
  const nextView = view === "overview" ? "overview" : "dashboard";
  const changed = currentViewMode !== nextView;
  currentViewMode = nextView;
  viewButtons.forEach((button) => {
    button.classList.toggle("active", button.dataset.view === currentViewMode);
  });
  dashboardSections.forEach((section) => {
    if (!section) {
      return;
    }
    section.hidden = currentViewMode !== "dashboard";
  });
  if (overviewPanel) {
    overviewPanel.hidden = currentViewMode !== "overview";
  }
  if (document.body) {
    document.body.dataset.view = currentViewMode;
  }
  if (connectionBanner) {
    if (currentViewMode !== "overview") {
      connectionBanner.hidden = true;
    } else if (!overviewSocket || overviewSocket.readyState !== WebSocket.OPEN) {
      showConnectionBanner("Live overview updates paused. Retrying connection...");
    }
  }
  if (changed) {
    logDebug("View switched", { view: currentViewMode });
  }
}

function initOverviewChannel() {
  if (!overviewGrid || !overviewPanel) {
    return;
  }
  startOverviewFallback();
  fetchOverviewSnapshot().catch((err) => {
    console.warn("Initial overview fetch failed", err);
  });
  connectOverviewWS();
}

function startOverviewFallback() {
  if (overviewFallbackTimer) {
    clearInterval(overviewFallbackTimer);
  }
  overviewFallbackTimer = window.setInterval(() => {
    if (overviewSocket && overviewSocket.readyState === WebSocket.OPEN) {
      return;
    }
    fetchOverviewSnapshot().catch(() => {});
  }, OVERVIEW_REFRESH_INTERVAL);
}

function connectOverviewWS() {
  if (typeof WebSocket === "undefined") {
    showConnectionBanner("Live overview requires WebSocket support.");
    return;
  }
  if (overviewSocket && (overviewSocket.readyState === WebSocket.OPEN || overviewSocket.readyState === WebSocket.CONNECTING)) {
    return;
  }
  const protocol = window.location.protocol === "https:" ? "wss" : "ws";
  const url = `${protocol}://${window.location.host}/ws/overview?limit=${OVERVIEW_LIMIT}`;
  const socket = new WebSocket(url);
  overviewSocket = socket;

  socket.addEventListener("open", () => {
    if (overviewReconnectTimer) {
      clearTimeout(overviewReconnectTimer);
      overviewReconnectTimer = null;
    }
    hideConnectionBanner();
    logDebug("Overview websocket connected", { limit: OVERVIEW_LIMIT });
  });

  socket.addEventListener("message", (event) => {
    try {
      const payload = JSON.parse(event.data);
      renderOverview(payload, "ws");
    } catch (err) {
      console.error("Overview payload parsing failed", err);
    }
  });

  let disconnectHandled = false;
  const handleDisconnect = () => {
    if (disconnectHandled) {
      return;
    }
    disconnectHandled = true;
    if (overviewSocket === socket) {
      overviewSocket = null;
    }
    showConnectionBanner("Live overview disconnected. Reconnecting...");
    if (overviewReconnectTimer) {
      clearTimeout(overviewReconnectTimer);
    }
    overviewReconnectTimer = window.setTimeout(() => {
      overviewReconnectTimer = null;
      connectOverviewWS();
    }, 4000);
  };

  socket.addEventListener("close", handleDisconnect);
  socket.addEventListener("error", () => {
    handleDisconnect();
    socket.close();
  });
}

function fetchOverviewSnapshot() {
  if (!overviewGrid) {
    return Promise.resolve();
  }
  const url = `/api/overview?limit=${OVERVIEW_LIMIT}`;
  return fetchJSON(url)
    .then((snapshot) => {
      renderOverview(snapshot, "http");
      hideConnectionBanner();
      return snapshot;
    })
    .catch((err) => {
      showConnectionBanner("Unable to refresh overview. Retrying...");
      throw err;
    });
}

function renderOverview(snapshot, source) {
  if (!overviewGrid) {
    return;
  }
  if (!snapshot || !Array.isArray(snapshot.items) || !snapshot.items.length) {
    overviewGrid.innerHTML = `<div class="overview-empty">No overview data available yet.</div>`;
    if (overviewMeta) {
    overviewMeta.textContent = "Waiting for live data...";
    }
    return;
  }

  updateOverviewMeta(snapshot);

  const fragment = document.createDocumentFragment();
  snapshot.items.forEach((item) => {
    fragment.appendChild(buildOverviewCard(item));
  });

  if (typeof overviewGrid.replaceChildren === "function") {
    overviewGrid.replaceChildren(fragment);
  } else {
    overviewGrid.innerHTML = "";
    overviewGrid.appendChild(fragment);
  }
  logDebug("Overview snapshot rendered", {
    source,
    generated_at: snapshot.generated_at,
    items: snapshot.items.length,
  });
}

function buildOverviewCard(item) {
  const card = document.createElement("div");
  card.className = "overview-card";
  card.dataset.kind = item?.kind || "service";
  if (item?.node_name) {
    card.dataset.nodeName = item.node_name;
  }

  const title = document.createElement("div");
  title.className = "overview-title";

  const name = document.createElement("span");
  name.className = "overview-name";
  name.textContent = item?.name || item?.id || "Unknown";

  const kind = document.createElement("span");
  kind.className = "overview-kind";
  const nodeLabel = typeof item?.node_name === "string" ? item.node_name.trim() : "";
  if (item?.kind === "connectivity") {
    title.classList.add("stacked");
    kind.classList.add("secondary");
    kind.textContent = nodeLabel ? `(${nodeLabel})` : "(server)";
    name.textContent = "Connectivity";
  } else {
    kind.textContent = "Service";
  }

  title.append(name, kind);

  const bars = document.createElement("div");
  bars.className = "overview-bars";
  const buckets = Array.isArray(item?.buckets) && item.buckets.length
    ? item.buckets.slice(-OVERVIEW_BUCKET_COUNT)
    : Array.from({ length: OVERVIEW_BUCKET_COUNT }, () => null);

  buckets.forEach((bucket) => {
    bars.appendChild(buildOverviewBar(bucket));
  });

  card.append(title, bars);
  return card;
}

function buildOverviewBar(bucket) {
  const bar = document.createElement("span");
  bar.className = `overview-bar ${mapOverviewState(bucket?.state)}`;
  const tooltip = formatOverviewTooltip(bucket);
  if (tooltip) {
    bar.title = tooltip;
  }
  return bar;
}

function mapOverviewState(state) {
  switch (state) {
    case "issue":
      return "state-issue";
    case "ok":
      return "state-ok";
    default:
      return "state-unknown";
  }
}

function formatOverviewTooltip(bucket) {
  if (!bucket) {
    return "";
  }
  const start = bucket?.start ? new Date(bucket.start) : null;
  const end = bucket?.end ? new Date(bucket.end) : null;
  const rangeLabel =
    start && end ? `${formatTimeOnly(start)} - ${formatTimeOnly(end)}` : "Last 10 min";
  const detail = bucket?.detail ? `\n${bucket.detail}` : "";
  return `${bucketStateLabel(bucket?.state)} (${rangeLabel})${detail}`;
}

function bucketStateLabel(state) {
  switch (state) {
    case "ok":
      return "All good";
    case "issue":
      return "Issue detected";
    default:
      return "No data";
  }
}

function updateOverviewMeta(snapshot) {
  if (!overviewMeta) {
    return;
  }
  if (!snapshot) {
    overviewMeta.textContent = "Live overview unavailable";
    return;
  }
  const start = snapshot.range_start ? new Date(snapshot.range_start) : null;
  const end = snapshot.range_end ? new Date(snapshot.range_end) : null;
  const updated = snapshot.generated_at ? new Date(snapshot.generated_at) : null;
  const windowLabel =
    start && end ? `${formatTimeOnly(start)} - ${formatTimeOnly(end)}` : "Last 30 minutes";
  const updateLabel = updated ? `updated ${formatTimeOnly(updated)}` : "waiting for data";
  overviewMeta.textContent = `${windowLabel} | ${updateLabel}`;
}

function formatTimeOnly(date) {
  if (!(date instanceof Date) || Number.isNaN(date.getTime())) {
    return "";
  }
  return date.toLocaleTimeString("en-US", {
    hour: "2-digit",
    minute: "2-digit",
  });
}

function showConnectionBanner(message) {
  if (!connectionBanner) {
    return;
  }
  if (currentViewMode !== "overview") {
    connectionBanner.hidden = true;
    return;
  }
  connectionBanner.textContent = message;
  connectionBanner.hidden = false;
}

function hideConnectionBanner() {
  if (!connectionBanner) {
    return;
  }
  connectionBanner.hidden = true;
}

