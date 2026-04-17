const state = {
  config: null,
  workflows: [],
  runs: [],
  runtimeProblems: [],
  pendingWorkflows: new Set(),
  poller: null,
  loadingRuns: false,
};

document.addEventListener("DOMContentLoaded", () => {
  void initializeDashboard();
});

async function initializeDashboard() {
  try {
    state.config = await fetchJSON("/api/config");
    renderConfigMeta();
    renderBanner();

    await loadWorkflows();
    if (state.config.remoteEnabled) {
      await loadRuns();
      startPolling();
    } else {
      renderRuns();
      renderChart();
    }
  } catch (error) {
    state.runtimeProblems = [error.message];
    renderBanner();
    renderWorkflows();
    renderRuns();
    renderChart();
  }
}

async function loadWorkflows() {
  try {
    state.workflows = await fetchJSON("/api/workflows");
  } catch (error) {
    state.runtimeProblems = [error.message];
  }
  renderBanner();
  renderWorkflows();
}

async function loadRuns() {
  if (!state.config?.remoteEnabled || state.loadingRuns) {
    return;
  }
  state.loadingRuns = true;
  try {
    state.runs = await fetchJSON("/api/runs?limit=20");
    state.runtimeProblems = [];
  } catch (error) {
    state.runtimeProblems = [error.message];
  } finally {
    state.loadingRuns = false;
    renderBanner();
    renderRuns();
    renderChart();
  }
}

function startPolling() {
  if (state.poller) {
    window.clearInterval(state.poller);
  }
  const intervalMs = (state.config?.pollIntervalSeconds ?? 5) * 1000;
  state.poller = window.setInterval(() => {
    void loadRuns();
  }, intervalMs);
}

function renderBanner() {
  const banner = document.getElementById("statusBanner");
  const configProblems = state.config?.problems ?? [];
  const problems = [...configProblems, ...state.runtimeProblems];

  if (!state.config) {
    banner.className = "status-banner status-loading";
    banner.innerHTML = "<strong>Loading dashboard configuration…</strong>";
    return;
  }

  if (problems.length === 0) {
    banner.className = "status-banner status-ready";
    banner.innerHTML = `<strong>Remote playground ready.</strong>${escapeHTML(state.config.argoServer)} / ${escapeHTML(state.config.namespace)} is available for dashboard launches.`;
    return;
  }

  banner.className = "status-banner status-degraded";
  banner.innerHTML = `<strong>Dashboard is running in degraded mode.</strong>${problems.map((problem) => escapeHTML(problem)).join("<br>")}`;
}

function renderConfigMeta() {
  const container = document.getElementById("configMeta");
  if (!state.config) {
    container.innerHTML = "";
    return;
  }

  const pills = [
    `server ${state.config.argoServer || "n/a"}`,
    `namespace ${state.config.namespace || "n/a"}`,
    `service account ${state.config.serviceAccount || "n/a"}`,
    `image ${state.config.image || "n/a"}`,
  ];

  container.innerHTML = pills
    .map((text) => `<span class="meta-pill">${escapeHTML(text)}</span>`)
    .join("");
}

function renderWorkflows() {
  const grid = document.getElementById("workflowGrid");
  if (state.workflows.length === 0) {
    grid.innerHTML = '<div class="empty-state">No launchable repo workflows were found under <code>workflows/</code>.</div>';
    return;
  }

  const remoteEnabled = Boolean(state.config?.remoteEnabled);
  grid.innerHTML = state.workflows
    .map((workflow) => {
      const pending = state.pendingWorkflows.has(workflow.path);
      const label = workflow.name.replaceAll("_", " ");
      return `
        <article class="workflow-card">
          <h3>${escapeHTML(label)}</h3>
          <div class="workflow-meta">
            <div>${escapeHTML(workflow.path)}</div>
            <div>${workflow.stepCount} step${workflow.stepCount === 1 ? "" : "s"}</div>
          </div>
          <button type="button" data-workflow="${escapeHTML(workflow.path)}" ${remoteEnabled && !pending ? "" : "disabled"}>
            ${pending ? "Submitting…" : "Launch in Kaizen"}
          </button>
        </article>
      `;
    })
    .join("");

  for (const button of grid.querySelectorAll("button[data-workflow]")) {
    button.addEventListener("click", () => {
      void launchWorkflow(button.dataset.workflow);
    });
  }
}

async function launchWorkflow(workflowPath) {
  if (!workflowPath || !state.config?.remoteEnabled || state.pendingWorkflows.has(workflowPath)) {
    return;
  }

  state.pendingWorkflows.add(workflowPath);
  state.runtimeProblems = [];
  renderBanner();
  renderWorkflows();

  try {
    const submitted = await fetchJSON("/api/runs", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ workflow: workflowPath }),
    });

    mergeRun(submitted);
    renderRuns();
    renderChart();
    await refreshRun(submitted.name);
    await loadRuns();
  } catch (error) {
    state.runtimeProblems = [error.message];
    renderBanner();
  } finally {
    state.pendingWorkflows.delete(workflowPath);
    renderWorkflows();
  }
}

async function refreshRun(name) {
  if (!name) {
    return;
  }
  try {
    const run = await fetchJSON(`/api/runs/${encodeURIComponent(name)}`);
    mergeRun(run);
  } catch (_error) {
    // Submission already succeeded; the follow-up lookup can race with remote indexing.
  }
}

function mergeRun(run) {
  if (!run?.name) {
    return;
  }
  const others = state.runs.filter((item) => item.name !== run.name);
  others.push(run);
  others.sort((left, right) => new Date(right.createdAt || 0) - new Date(left.createdAt || 0));
  state.runs = others.slice(0, 20);
}

function renderRuns() {
  const list = document.getElementById("runsList");
  if (state.runs.length === 0) {
    list.innerHTML = '<div class="empty-state">No matching remote runs are visible yet.</div>';
    return;
  }

  list.innerHTML = state.runs
    .map((run) => {
      const phaseClass = classifyPhase(run.phase);
      return `
        <article class="run-card">
          <div class="run-header">
            <h3>${escapeHTML(run.name)}</h3>
            <span class="phase-pill ${phaseClass}">${escapeHTML(run.phase || "Unknown")}</span>
          </div>
          <div class="run-meta">
            <div>${escapeHTML(run.workflowPath || "unknown workflow")}</div>
            <div>duration ${escapeHTML(formatDuration(run.durationSeconds))} | progress ${escapeHTML(run.progress || "n/a")}</div>
            <div>created ${escapeHTML(formatTimestamp(run.createdAt))}</div>
            <div>started ${escapeHTML(formatTimestamp(run.startedAt))}</div>
            <div>image ${escapeHTML(run.image || "n/a")} | account ${escapeHTML(run.serviceAccount || "n/a")}</div>
            ${run.message ? `<div>${escapeHTML(run.message)}</div>` : ""}
          </div>
        </article>
      `;
    })
    .join("");
}

function renderChart() {
  const svg = document.getElementById("timelineChart");
  const caption = document.getElementById("chartCaption");
  const width = 760;
  const height = 300;
  const margin = { top: 18, right: 22, bottom: 44, left: 58 };
  const chartWidth = width - margin.left - margin.right;
  const chartHeight = height - margin.top - margin.bottom;
  const runs = state.runs
    .filter((run) => run.createdAt || run.startedAt)
    .map((run) => ({
      ...run,
      timestamp: new Date(run.startedAt || run.createdAt).getTime(),
      duration: Number(run.durationSeconds || 0),
    }))
    .filter((run) => Number.isFinite(run.timestamp));

  if (runs.length === 0) {
    svg.innerHTML = `
      <rect x="0" y="0" width="${width}" height="${height}" rx="18" fill="rgba(255,255,255,0.32)"></rect>
      <text x="${width / 2}" y="${height / 2}" text-anchor="middle" class="chart-label">Waiting for recent workflow runs…</text>
    `;
    caption.textContent = "No remote run timestamps available yet.";
    return;
  }

  const minX = Math.min(...runs.map((run) => run.timestamp));
  const maxX = Math.max(...runs.map((run) => run.timestamp));
  const maxY = Math.max(...runs.map((run) => run.duration), 1);
  const domainX = minX === maxX ? maxX + 60_000 : maxX;

  const yTicks = 4;
  const xTicks = 3;
  const parts = [
    `<rect x="0" y="0" width="${width}" height="${height}" rx="18" fill="rgba(255,255,255,0.32)"></rect>`,
  ];

  for (let index = 0; index <= yTicks; index += 1) {
    const ratio = index / yTicks;
    const y = margin.top + chartHeight - ratio * chartHeight;
    const value = maxY * ratio;
    parts.push(`<line x1="${margin.left}" y1="${y}" x2="${width - margin.right}" y2="${y}" class="chart-grid"></line>`);
    parts.push(`<text x="${margin.left - 10}" y="${y + 4}" text-anchor="end" class="chart-label">${escapeHTML(formatDuration(value))}</text>`);
  }

  for (let index = 0; index <= xTicks; index += 1) {
    const ratio = index / xTicks;
    const x = margin.left + ratio * chartWidth;
    const value = minX + ratio * (domainX - minX);
    parts.push(`<line x1="${x}" y1="${margin.top}" x2="${x}" y2="${margin.top + chartHeight}" class="chart-grid"></line>`);
    parts.push(`<text x="${x}" y="${height - 14}" text-anchor="middle" class="chart-label">${escapeHTML(formatShortClock(value))}</text>`);
  }

  parts.push(`<line x1="${margin.left}" y1="${margin.top + chartHeight}" x2="${width - margin.right}" y2="${margin.top + chartHeight}" class="chart-axis"></line>`);
  parts.push(`<line x1="${margin.left}" y1="${margin.top}" x2="${margin.left}" y2="${margin.top + chartHeight}" class="chart-axis"></line>`);

  for (const run of runs) {
    const xRatio = domainX === minX ? 0.5 : (run.timestamp - minX) / (domainX - minX);
    const yRatio = maxY === 0 ? 0 : run.duration / maxY;
    const x = margin.left + xRatio * chartWidth;
    const y = margin.top + chartHeight - yRatio * chartHeight;
    const color = phaseColor(run.phase);
    parts.push(`
      <circle cx="${x}" cy="${y}" r="7" fill="${color}" class="chart-dot">
        <title>${escapeHTML(`${run.name} • ${run.phase || "Unknown"} • ${formatDuration(run.duration)}`)}</title>
      </circle>
    `);
  }

  svg.innerHTML = parts.join("");
  caption.textContent = `Showing ${runs.length} recent run${runs.length === 1 ? "" : "s"} from ${formatShortClock(minX)} to ${formatShortClock(domainX)}.`;
}

function classifyPhase(phase) {
  switch ((phase || "").toLowerCase()) {
    case "running":
      return "phase-running";
    case "succeeded":
      return "phase-succeeded";
    case "failed":
      return "phase-failed";
    case "error":
      return "phase-error";
    default:
      return "phase-other";
  }
}

function phaseColor(phase) {
  switch ((phase || "").toLowerCase()) {
    case "running":
      return "#bf5f2f";
    case "succeeded":
      return "#2f7652";
    case "failed":
    case "error":
      return "#a53f2b";
    default:
      return "#6b6f77";
  }
}

function formatDuration(value) {
  const seconds = Number(value || 0);
  if (seconds >= 60) {
    const minutes = Math.floor(seconds / 60);
    const remainder = Math.round(seconds % 60);
    return `${minutes}m ${remainder}s`;
  }
  return `${seconds.toFixed(seconds >= 10 ? 0 : 1)}s`;
}

function formatTimestamp(raw) {
  if (!raw) {
    return "n/a";
  }
  const date = new Date(raw);
  if (Number.isNaN(date.getTime())) {
    return "n/a";
  }
  return date.toLocaleString([], {
    year: "numeric",
    month: "short",
    day: "2-digit",
    hour: "2-digit",
    minute: "2-digit",
    second: "2-digit",
  });
}

function formatShortClock(value) {
  const date = new Date(value);
  return date.toLocaleTimeString([], {
    hour: "2-digit",
    minute: "2-digit",
  });
}

async function fetchJSON(url, options = {}) {
  const response = await fetch(url, options);
  const text = await response.text();
  const data = text ? safeJSON(text) : null;

  if (!response.ok) {
    const message = data?.error || text || `${response.status} ${response.statusText}`;
    throw new Error(message);
  }

  return data;
}

function safeJSON(text) {
  try {
    return JSON.parse(text);
  } catch (_error) {
    return null;
  }
}

function escapeHTML(value) {
  return String(value)
    .replaceAll("&", "&amp;")
    .replaceAll("<", "&lt;")
    .replaceAll(">", "&gt;")
    .replaceAll('"', "&quot;")
    .replaceAll("'", "&#39;");
}
