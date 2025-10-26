const latestTableBody = document.querySelector("#latest-table tbody");
const latestTimestamp = document.querySelector("#latest-timestamp");
const uptimeTableBody = document.querySelector("#uptime-table tbody");
const uptimeGenerated = document.querySelector("#uptime-generated");

async function refresh() {
  try {
    await Promise.all([loadLatest(), loadUptime()]);
  } catch (err) {
    console.error("Refresh failed", err);
  }
}

async function loadLatest() {
  const res = await fetch("/api/status", { cache: "no-store" });
  if (!res.ok) {
    throw new Error(`status ${res.status}`);
  }
  const data = await res.json();

  latestTimestamp.textContent = data.timestamp
    ? `Ostatni pomiar: ${new Date(data.timestamp).toLocaleString()}`
    : "Brak danych.";

  latestTableBody.innerHTML = "";
  (data.checks || []).forEach((check) => {
    const row = document.createElement("tr");
    row.innerHTML = `
      <td>${check.name}</td>
      <td>${check.state ?? "-"}</td>
      <td class="${check.ok ? "status-ok" : "status-fail"}">${check.ok ? "ACTIVE" : "INACTIVE"}</td>
      <td>${check.error ?? ""}</td>
    `;
    latestTableBody.appendChild(row);
  });
}

async function loadUptime() {
  const res = await fetch("/api/uptime", { cache: "no-store" });
  if (!res.ok) {
    throw new Error(`uptime ${res.status}`);
  }
  const data = await res.json();

  uptimeTableBody.innerHTML = "";
  let generated = "";
  data.forEach((item) => {
    const row = document.createElement("tr");
    row.innerHTML = `
      <td>${item.name}</td>
      <td>${item.uptime_percent.toFixed(2)}%</td>
      <td>${item.total_checks}</td>
      <td>${item.passing}</td>
      <td>${item.failing}</td>
      <td>${item.last_state ?? "-"}</td>
    `;
    uptimeTableBody.appendChild(row);
    generated = item.generated_at_utc;
  });
  uptimeGenerated.textContent = generated
    ? `Dane wygenerowano: ${new Date(generated).toLocaleString()}`
    : "";
}

refresh();
setInterval(refresh, 30_000);
