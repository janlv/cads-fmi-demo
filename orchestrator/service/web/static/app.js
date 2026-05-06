const state = {
  config: null,
  workflows: [],
  runs: [],
  selectedWorkflowPath: "",
  selectedRunName: "",
  runsRailCollapsed: false,
  simulinkResult: null,
  simulinkResultsCache: new Map(),
  aeStatsResult: null,
  aeStatsResultsCache: new Map(),
  genericResult: null,
  genericResultsCache: new Map(),
  traceCharts: new Map(),
  hiddenTraceSeries: new Set(),
  runtimeProblems: [],
  pendingWorkflows: new Set(),
  poller: null,
  loadingRuns: false,
};

const SIMULINK_WORKFLOW_PATH = "workflows/calculate_aecis.yaml";
const AE_STATS_WORKFLOW_PATH = "workflows/ae_event_statistics.yaml";
const CIVECTOR_LABELS = ["Mean", "RMS", "Peak-to-Peak", "Skewness", "Kurtosis"];
const SIMULINK_RESULT_RETRY_MS = 15_000;
const AECIS_TREND_WINDOW_SECONDS = 2.5;
const SELECTED_WORKFLOW_STORAGE_KEY = "cads:selectedWorkflowPath";
const RUNS_RAIL_COLLAPSED_STORAGE_KEY = "cads:runsRailCollapsed";

document.addEventListener("DOMContentLoaded", () => {
  window.addEventListener("resize", resizeECharts);
  window.addEventListener("load", () => initializeECharts(document));
  void initializeDashboard();
});

async function initializeDashboard() {
  try {
    state.runsRailCollapsed = readPersistedRunsRailCollapsed();
    state.config = await fetchJSON("/api/config");
    renderConfigMeta();
    renderBanner();

    await loadWorkflows();
    if (state.config.remoteEnabled) {
      await loadRuns();
      startPolling();
    } else {
      renderRuns();
      renderWorkflowOutput();
    }
  } catch (error) {
    state.runtimeProblems = [error.message];
    renderBanner();
    renderWorkflows();
    renderRuns();
    renderWorkflowOutput();
  }
}

async function loadWorkflows() {
  try {
    state.workflows = await fetchJSON("/api/workflows");
  } catch (error) {
    state.runtimeProblems = [error.message];
  }
  ensureSelectedWorkflow();
  renderBanner();
  renderWorkflows();
  renderWorkflowOutput();
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
    await loadSelectedWorkflowResult();
    renderBanner();
    renderRuns();
    renderWorkflowOutput();
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

function ensureSelectedWorkflow() {
  if (state.workflows.length === 0) {
    state.selectedWorkflowPath = "";
    return;
  }

  if (state.workflows.some((workflow) => workflow.path === state.selectedWorkflowPath)) {
    return;
  }

  const savedPath = readPersistedWorkflowPath();
  if (savedPath && state.workflows.some((workflow) => workflow.path === savedPath)) {
    state.selectedWorkflowPath = savedPath;
    return;
  }

  const preferredPath =
    state.workflows.find((workflow) => workflow.path === AE_STATS_WORKFLOW_PATH)?.path ||
    state.workflows.find((workflow) => workflow.path === SIMULINK_WORKFLOW_PATH)?.path ||
    state.workflows[0]?.path ||
    "";
  state.selectedWorkflowPath = preferredPath;
}

function selectedWorkflow() {
  return state.workflows.find((workflow) => workflow.path === state.selectedWorkflowPath) || null;
}

function workflowLabel(workflow) {
  return String(workflow?.name || workflow?.path || "workflow").replaceAll("_", " ");
}

function readPersistedWorkflowPath() {
  try {
    return window.localStorage?.getItem(SELECTED_WORKFLOW_STORAGE_KEY) || "";
  } catch (_error) {
    return "";
  }
}

function persistSelectedWorkflowPath(workflowPath) {
  try {
    window.localStorage?.setItem(SELECTED_WORKFLOW_STORAGE_KEY, workflowPath);
  } catch (_error) {
    // Local storage can be unavailable in private or embedded browser contexts.
  }
}

function readPersistedRunsRailCollapsed() {
  try {
    return window.localStorage?.getItem(RUNS_RAIL_COLLAPSED_STORAGE_KEY) === "true";
  } catch (_error) {
    return false;
  }
}

function persistRunsRailCollapsed(collapsed) {
  try {
    window.localStorage?.setItem(RUNS_RAIL_COLLAPSED_STORAGE_KEY, collapsed ? "true" : "false");
  } catch (_error) {
    // Local storage can be unavailable in private or embedded browser contexts.
  }
}

function setRunsRailCollapsed(collapsed) {
  state.runsRailCollapsed = Boolean(collapsed);
  persistRunsRailCollapsed(state.runsRailCollapsed);
  renderRuns();
}

function selectWorkflow(workflowPath, options = {}) {
  if (!workflowPath || !state.workflows.some((workflow) => workflow.path === workflowPath)) {
    return;
  }

  const changed = state.selectedWorkflowPath !== workflowPath;
  state.selectedWorkflowPath = workflowPath;
  if (changed) {
    state.selectedRunName = "";
  }
  persistSelectedWorkflowPath(workflowPath);
  renderWorkflows();
  renderRuns();
  renderWorkflowOutput();

  if (options.loadResult !== false && state.config?.remoteEnabled) {
    void loadSelectedWorkflowResult().then(() => {
      renderRuns();
      renderWorkflowOutput();
    });
  }
}

function selectedWorkflowRuns() {
  if (!state.selectedWorkflowPath) {
    return [];
  }
  return state.runs.filter((run) => run.workflowPath === state.selectedWorkflowPath);
}

function successfulSelectedWorkflowRuns() {
  return selectedWorkflowRuns().filter((run) => String(run.phase || "").toLowerCase() === "succeeded");
}

function renderWorkflows() {
  const grid = document.getElementById("workflowGrid");
  const launchButton = document.getElementById("launchSelectedWorkflow");
  const selected = selectedWorkflow();
  const remoteEnabled = Boolean(state.config?.remoteEnabled);
  const selectedPending = Boolean(selected && state.pendingWorkflows.has(selected.path));

  if (launchButton) {
    launchButton.disabled = !remoteEnabled || !selected || selectedPending;
    launchButton.textContent = selectedPending ? "Submitting…" : "Launch selected workflow";
    launchButton.onclick = () => {
      if (state.selectedWorkflowPath) {
        void launchWorkflow(state.selectedWorkflowPath);
      }
    };
  }

  if (state.workflows.length === 0) {
    grid.innerHTML = '<div class="empty-state">No launchable repo workflows were found under <code>workflows/</code>.</div>';
    return;
  }

  grid.innerHTML = state.workflows
    .map((workflow) => {
      const pending = state.pendingWorkflows.has(workflow.path);
      const isSelected = workflow.path === state.selectedWorkflowPath;
      const label = workflowLabel(workflow);
      return `
        <button
          class="workflow-tab${isSelected ? " selected" : ""}${pending ? " pending" : ""}"
          type="button"
          role="tab"
          aria-selected="${isSelected ? "true" : "false"}"
          title="${escapeHTML(workflow.path)}"
          data-select-workflow="${escapeHTML(workflow.path)}"
        >
          <span class="workflow-tab-title">${escapeHTML(label)}</span>
          <span class="workflow-tab-meta">${workflow.stepCount} step${workflow.stepCount === 1 ? "" : "s"}${pending ? " | submitting" : ""}</span>
        </button>
      `;
    })
    .join("");

  for (const button of grid.querySelectorAll("[data-select-workflow]")) {
    button.addEventListener("click", () => {
      selectWorkflow(button.dataset.selectWorkflow);
    });
  }
}

async function launchWorkflow(workflowPath) {
  if (!workflowPath || !state.config?.remoteEnabled || state.pendingWorkflows.has(workflowPath)) {
    return;
  }

  selectWorkflow(workflowPath, { loadResult: false });
  state.pendingWorkflows.add(workflowPath);
  state.runtimeProblems = [];
  renderBanner();
  renderWorkflows();
  renderWorkflowOutput();

  try {
    const submitted = await fetchJSON("/api/runs", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ workflow: workflowPath }),
    });

    mergeRun(submitted);
    renderRuns();
    renderWorkflowOutput();
    await refreshRun(submitted.name);
    await loadRuns();
  } catch (error) {
    state.runtimeProblems = [error.message];
    renderBanner();
  } finally {
    state.pendingWorkflows.delete(workflowPath);
    renderWorkflows();
    renderWorkflowOutput();
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

async function loadSimulinkResults() {
  const candidates = successfulSimulinkRuns();
  if (candidates.length === 0) {
    state.simulinkResult = {
      state: "empty",
      message: "No successful calculate_aecis run has been observed yet.",
    };
    return;
  }

  const latestCandidate = candidates[0];
  const previousReady = state.simulinkResult?.state === "ready" ? state.simulinkResult : null;
  const skipped = [];

  for (const run of candidates) {
    const cached = state.simulinkResultsCache.get(run.name);
    if (cached?.state === "ready") {
      state.simulinkResult = buildSimulinkReadyState(run, cached.payload, latestCandidate.name, skipped);
      return;
    }
    if (cached?.state === "error" && Date.now() - (cached.checkedAt || 0) < SIMULINK_RESULT_RETRY_MS) {
      skipped.push({ runName: run.name, message: cached.message });
      continue;
    }

    if (!previousReady) {
      state.simulinkResult = {
        state: "loading",
        runName: run.name,
      };
    }

    try {
      const payload = await fetchJSON(`/api/runs/${encodeURIComponent(run.name)}/results`);
      state.simulinkResultsCache.set(run.name, {
        state: "ready",
        payload,
        checkedAt: Date.now(),
      });
      state.simulinkResult = buildSimulinkReadyState(run, payload, latestCandidate.name, skipped);
      return;
    } catch (error) {
      const message = error.message;
      state.simulinkResultsCache.set(run.name, {
        state: "error",
        message,
        checkedAt: Date.now(),
      });
      skipped.push({ runName: run.name, message });
    }
  }

  if (previousReady) {
    const payloadRunName = previousReady.payload?.runName || previousReady.runName;
    const stillVisible = candidates.some((run) => run.name === payloadRunName);
    if (stillVisible) {
      const latestFailure = skipped[0] || null;
      state.simulinkResult = {
        ...previousReady,
        skippedRuns: skipped,
        fallbackFrom: latestFailure ? latestFailure.runName : previousReady.fallbackFrom,
      };
      return;
    }
  }

  const latestFailure = skipped[0];
  state.simulinkResult = {
    state: "error",
    runName: latestFailure?.runName || latestCandidate.name,
    message: latestFailure?.message || "No structured Simulink result payload could be loaded from recent workflow logs.",
    skippedRuns: skipped,
  };
}

function successfulSimulinkRuns() {
  return state.runs.filter(
    (run) =>
      run.workflowPath === SIMULINK_WORKFLOW_PATH &&
      String(run.phase || "").toLowerCase() === "succeeded",
  );
}

function buildSimulinkReadyState(run, payload, latestRunName, skippedRuns) {
  return {
    state: "ready",
    runName: run.name,
    payload,
    fallbackFrom: latestRunName !== run.name ? latestRunName : "",
    skippedRuns,
  };
}

async function loadAeStatsResults() {
  const candidates = successfulAeStatsRuns();
  if (candidates.length === 0) {
    state.aeStatsResult = {
      state: "empty",
      message: "No successful AE event statistics run has been observed yet.",
    };
    return;
  }

  const latestCandidate = candidates[0];
  const previousReady = state.aeStatsResult?.state === "ready" ? state.aeStatsResult : null;
  const skipped = [];

  for (const run of candidates) {
    const cached = state.aeStatsResultsCache.get(run.name);
    if (cached?.state === "ready") {
      state.aeStatsResult = buildAeStatsReadyState(run, cached.payload, latestCandidate.name, skipped);
      return;
    }
    if (cached?.state === "error" && Date.now() - (cached.checkedAt || 0) < SIMULINK_RESULT_RETRY_MS) {
      skipped.push({ runName: run.name, message: cached.message });
      continue;
    }

    if (!previousReady) {
      state.aeStatsResult = {
        state: "loading",
        runName: run.name,
      };
    }

    try {
      const payload = await fetchJSON(`/api/runs/${encodeURIComponent(run.name)}/results`);
      state.aeStatsResultsCache.set(run.name, {
        state: "ready",
        payload,
        checkedAt: Date.now(),
      });
      state.aeStatsResult = buildAeStatsReadyState(run, payload, latestCandidate.name, skipped);
      return;
    } catch (error) {
      const message = error.message;
      state.aeStatsResultsCache.set(run.name, {
        state: "error",
        message,
        checkedAt: Date.now(),
      });
      skipped.push({ runName: run.name, message });
    }
  }

  if (previousReady) {
    const payloadRunName = previousReady.payload?.runName || previousReady.runName;
    const stillVisible = candidates.some((run) => run.name === payloadRunName);
    if (stillVisible) {
      const latestFailure = skipped[0] || null;
      state.aeStatsResult = {
        ...previousReady,
        skippedRuns: skipped,
        fallbackFrom: latestFailure ? latestFailure.runName : previousReady.fallbackFrom,
      };
      return;
    }
  }

  const latestFailure = skipped[0];
  state.aeStatsResult = {
    state: "error",
    runName: latestFailure?.runName || latestCandidate.name,
    message: latestFailure?.message || "No structured AE event statistics payload could be loaded from recent workflow logs.",
    skippedRuns: skipped,
  };
}

function successfulAeStatsRuns() {
  return state.runs.filter(
    (run) =>
      run.workflowPath === AE_STATS_WORKFLOW_PATH &&
      String(run.phase || "").toLowerCase() === "succeeded",
  );
}

function buildAeStatsReadyState(run, payload, latestRunName, skippedRuns) {
  return {
    state: "ready",
    runName: run.name,
    payload,
    fallbackFrom: latestRunName !== run.name ? latestRunName : "",
    skippedRuns,
  };
}

async function loadSelectedWorkflowResult() {
  const workflow = selectedWorkflow();
  if (!workflow) {
    state.genericResult = {
      state: "empty",
      message: "Choose a workflow to inspect its latest output.",
    };
    return;
  }

  if (workflow.path === SIMULINK_WORKFLOW_PATH) {
    await loadSimulinkResults();
    return;
  }

  if (workflow.path === AE_STATS_WORKFLOW_PATH) {
    await loadAeStatsResults();
    return;
  }

  await loadGenericWorkflowResult(workflow);
}

async function loadGenericWorkflowResult(workflow) {
  const candidates = successfulSelectedWorkflowRuns();
  if (candidates.length === 0) {
    state.genericResult = {
      state: "empty",
      workflowPath: workflow.path,
      message: `No successful ${workflowLabel(workflow)} run has been observed yet.`,
    };
    return;
  }

  const latestCandidate = candidates[0];
  const previousReady =
    state.genericResult?.state === "ready" && state.genericResult.workflowPath === workflow.path
      ? state.genericResult
      : null;
  const skipped = [];

  for (const run of candidates) {
    const cached = state.genericResultsCache.get(run.name);
    if (cached?.state === "ready") {
      state.genericResult = buildGenericReadyState(workflow, run, cached.payload, latestCandidate.name, skipped);
      return;
    }
    if (cached?.state === "error" && Date.now() - (cached.checkedAt || 0) < SIMULINK_RESULT_RETRY_MS) {
      skipped.push({ runName: run.name, message: cached.message });
      continue;
    }

    if (!previousReady) {
      state.genericResult = {
        state: "loading",
        workflowPath: workflow.path,
        runName: run.name,
      };
    }

    try {
      const payload = await fetchJSON(`/api/runs/${encodeURIComponent(run.name)}/results`);
      state.genericResultsCache.set(run.name, {
        state: "ready",
        payload,
        checkedAt: Date.now(),
      });
      state.genericResult = buildGenericReadyState(workflow, run, payload, latestCandidate.name, skipped);
      return;
    } catch (error) {
      const message = error.message;
      state.genericResultsCache.set(run.name, {
        state: "error",
        message,
        checkedAt: Date.now(),
      });
      skipped.push({ runName: run.name, message });
    }
  }

  if (previousReady) {
    const payloadRunName = previousReady.payload?.runName || previousReady.runName;
    const stillVisible = candidates.some((run) => run.name === payloadRunName);
    if (stillVisible) {
      const latestFailure = skipped[0] || null;
      state.genericResult = {
        ...previousReady,
        skippedRuns: skipped,
        fallbackFrom: latestFailure ? latestFailure.runName : previousReady.fallbackFrom,
      };
      return;
    }
  }

  const latestFailure = skipped[0];
  state.genericResult = {
    state: "error",
    workflowPath: workflow.path,
    runName: latestFailure?.runName || latestCandidate.name,
    message: latestFailure?.message || "No structured result payload could be loaded from recent workflow logs.",
    skippedRuns: skipped,
  };
}

function buildGenericReadyState(workflow, run, payload, latestRunName, skippedRuns) {
  return {
    state: "ready",
    workflowPath: workflow.path,
    runName: run.name,
    payload,
    fallbackFrom: latestRunName !== run.name ? latestRunName : "",
    skippedRuns,
  };
}

function resolveReadySimulinkView(simulink) {
  const stepEntries = Object.entries(simulink?.payload?.stepResults || {});
  if (stepEntries.length === 0) {
    return null;
  }

  const [stepName, stepResult] = preferredSimulinkStep(stepEntries);
  const trace = extractSimulinkTrace(stepResult);
  return {
    stepName,
    stepResult,
    trace,
    resultType: classifySimulinkPayload(simulink.payload),
  };
}

function buildSimulinkFallbackMarkup(simulink) {
  if (!simulink?.fallbackFrom || simulink.fallbackFrom === simulink.runName) {
    return "";
  }
  return `
    <div class="result-note">
      Latest successful run <code>${escapeHTML(simulink.fallbackFrom)}</code> had no structured result payload.
      Showing the most recent parseable result from <code>${escapeHTML(simulink.payload.runName || simulink.runName)}</code>.
    </div>
  `;
}

function renderWorkflowOutput() {
  const container = document.getElementById("workflowOutput");
  if (!container) {
    return;
  }

  const workflow = selectedWorkflow();
  renderOutputHeader(workflow);
  disposeEChartsIn(container);
  state.traceCharts.clear();

  container.className = "workflow-output";
  if (!workflow) {
    container.innerHTML = '<div class="empty-state">Choose a workflow to inspect its output.</div>';
    return;
  }

  if (workflow.path === SIMULINK_WORKFLOW_PATH) {
    container.classList.add("aecis-focus");
    renderAecisFocus(container);
    initializeECharts(container);
    return;
  }

  if (workflow.path === AE_STATS_WORKFLOW_PATH) {
    container.classList.add("ae-event-stats");
    renderAeEventStats(container);
    initializeECharts(container);
    return;
  }

  container.classList.add("generic-workflow-output");
  renderGenericWorkflowResult(container, workflow);
}

function renderOutputHeader(workflow) {
  const kicker = document.getElementById("outputKicker");
  const title = document.getElementById("outputTitle");
  const copy = document.getElementById("outputCopy");
  if (!kicker || !title || !copy) {
    return;
  }

  if (!workflow) {
    kicker.textContent = "Output";
    title.textContent = "Latest Result";
    copy.textContent = "The output window follows the selected workflow.";
    return;
  }

  if (workflow.path === SIMULINK_WORKFLOW_PATH) {
    kicker.textContent = "AECIS";
    title.textContent = "Trend Plot";
    copy.textContent = "Rolling mean and RMS from the latest parsed calculate_aecis signal trace.";
    return;
  }

  if (workflow.path === AE_STATS_WORKFLOW_PATH) {
    kicker.textContent = "AE Event Statistics";
    title.textContent = "CH2 / CH6 Comparison";
    copy.textContent = "Edge-computed acoustic-emission event features from the emailed CSV tables.";
    return;
  }

  kicker.textContent = "Output";
  title.textContent = workflowLabel(workflow);
  copy.textContent = workflow.path;
}

function renderGenericWorkflowResult(container, workflow) {
  const result = state.genericResult;

  if (!result || result.workflowPath !== workflow.path || result.state === "loading") {
    container.innerHTML = '<div class="empty-state">Waiting for the latest successful workflow result…</div>';
    return;
  }

  if (result.state === "empty") {
    container.innerHTML = `<div class="empty-state">${escapeHTML(result.message)}</div>`;
    return;
  }

  if (result.state === "error") {
    container.innerHTML = `<div class="empty-state">Unable to load results for <code>${escapeHTML(result.runName)}</code>.<br>${escapeHTML(result.message)}</div>`;
    return;
  }

  const payload = result.payload || {};
  const stepEntries = Object.entries(payload.stepResults || {});
  const stepSummary = stepEntries
    .map(([stepName, stepResult]) => {
      const valueCount = stepResult && typeof stepResult === "object" ? Object.keys(stepResult).length : 1;
      return `
        <div class="metric-chip">
          <span class="metric-label">${escapeHTML(stepName)}</span>
          <span class="metric-value">${valueCount} field${valueCount === 1 ? "" : "s"}</span>
        </div>
      `;
    })
    .join("");

  container.innerHTML = `
    <article class="result-card">
      <div class="result-head">
        <h3>${escapeHTML(payload.runName || result.runName)}</h3>
        <span class="result-kind-pill result-kind-trace">Structured Result</span>
      </div>
      <div class="result-meta">
        <div>${escapeHTML(payload.workflowPath || workflow.path)}</div>
        <div>${stepEntries.length} step result${stepEntries.length === 1 ? "" : "s"}</div>
      </div>
      ${buildGenericFallbackMarkup(result)}
      ${stepSummary ? `<div class="metric-grid">${stepSummary}</div>` : ""}
      <pre class="result-json">${escapeHTML(JSON.stringify(payload, null, 2))}</pre>
    </article>
  `;
}

function buildGenericFallbackMarkup(result) {
  if (!result?.fallbackFrom || result.fallbackFrom === result.runName) {
    return "";
  }
  return `
    <div class="result-note">
      Latest successful run <code>${escapeHTML(result.fallbackFrom)}</code> had no structured result payload.
      Showing the most recent parseable result from <code>${escapeHTML(result.payload?.runName || result.runName)}</code>.
    </div>
  `;
}

function renderSimulinkResults() {
  const container = document.getElementById("simulinkResults");
  const simulink = state.simulinkResult;

  if (!container) {
    return;
  }

  if (!simulink || simulink.state === "loading") {
    container.innerHTML = '<div class="empty-state">Waiting for the latest successful Simulink workflow result…</div>';
    return;
  }

  if (simulink.state === "empty") {
    container.innerHTML = `<div class="empty-state">${escapeHTML(simulink.message)}</div>`;
    return;
  }

  if (simulink.state === "error") {
    container.innerHTML = `<div class="empty-state">Unable to load Simulink results for <code>${escapeHTML(simulink.runName)}</code>.<br>${escapeHTML(simulink.message)}</div>`;
    return;
  }

  const stepEntries = Object.entries(simulink.payload?.stepResults || {});
  if (stepEntries.length === 0) {
    container.innerHTML = '<div class="empty-state">The workflow succeeded, but no structured result payload was found in its logs.</div>';
    return;
  }

  const activeView = resolveReadySimulinkView(simulink);
  if (!activeView) {
    container.innerHTML = '<div class="empty-state">The workflow succeeded, but no structured result payload was found in its logs.</div>';
    return;
  }
  const { stepName, stepResult, trace, resultType } = activeView;
  const derivedTrend = buildDerivedAecisTrend(trace);
  const summaryMetrics = resolveSummaryMetrics(stepResult, trace, derivedTrend);
  const metricCards = summaryMetrics
    .map((metric) => `
      <div class="metric-chip">
        <span class="metric-label">${escapeHTML(metric.label)}</span>
        <span class="metric-value">${escapeHTML(formatMetric(metric.value))}</span>
      </div>
    `)
    .join("");
  const scalarMetric =
    metricCards === "" && stepResult.CIvector !== undefined && !Array.isArray(stepResult.CIvector)
      ? `
        <div class="metric-grid single-metric">
          <div class="metric-chip">
            <span class="metric-label">CIvector</span>
            <span class="metric-value">${escapeHTML(formatMetric(stepResult.CIvector))}</span>
          </div>
        </div>
      `
      : "";
  const fallbackMarkup = buildSimulinkFallbackMarkup(simulink);
  const traceSummary = trace
    ? `<div class="result-note">Trend plots are derived from the traced <code>rawsig</code> signal in the AECIS Trend Plot panel above using a ${escapeHTML(formatMetric(AECIS_TREND_WINDOW_SECONDS))} s trailing window.</div>`
    : "";

  container.innerHTML = `
    <article class="result-card">
      <div class="result-head">
        <h3>${escapeHTML(simulink.payload.runName)}</h3>
        <span class="result-kind-pill result-kind-${escapeHTML(resultType.kind)}">${escapeHTML(resultType.label)}</span>
      </div>
      <div class="result-meta">
        <div>${escapeHTML(simulink.payload.workflowPath || SIMULINK_WORKFLOW_PATH)}</div>
        <div>step ${escapeHTML(stepName)}</div>
        ${stepResult.time !== undefined ? `<div>reported stop time ${escapeHTML(formatMetric(stepResult.time))}</div>` : ""}
      </div>
      ${fallbackMarkup}
      ${metricCards ? `<div class="metric-grid">${metricCards}</div>` : ""}
      ${scalarMetric}
      ${traceSummary}
      <pre class="result-json">${escapeHTML(JSON.stringify(stepResult, null, 2))}</pre>
    </article>
  `;
}

function renderAecisFocus(targetContainer = null) {
  const container = targetContainer || document.getElementById("aecisFocus");
  const simulink = state.simulinkResult;

  if (!container) {
    return;
  }

  if (!simulink || simulink.state === "loading") {
    container.innerHTML = '<div class="empty-state">Waiting for the latest successful AECIS trace…</div>';
    return;
  }

  if (simulink.state === "empty") {
    container.innerHTML = `<div class="empty-state">${escapeHTML(simulink.message)}</div>`;
    return;
  }

  if (simulink.state === "error") {
    container.innerHTML = `<div class="empty-state">Unable to load AECIS trace for <code>${escapeHTML(simulink.runName)}</code>.<br>${escapeHTML(simulink.message)}</div>`;
    return;
  }

  const activeView = resolveReadySimulinkView(simulink);
  if (!activeView) {
    container.innerHTML = '<div class="empty-state">The latest AECIS result has no structured trace payload.</div>';
    return;
  }

  const { stepName, stepResult, trace, resultType } = activeView;
  if (!trace) {
    container.innerHTML = `
      <div class="aecis-focus-meta">
        <h3 class="aecis-focus-title">${escapeHTML(simulink.payload.runName || simulink.runName)}</h3>
        <span class="result-kind-pill result-kind-${escapeHTML(resultType.kind)}">${escapeHTML(resultType.label)}</span>
        <span>step ${escapeHTML(stepName)}</span>
      </div>
      ${buildSimulinkFallbackMarkup(simulink)}
      <div class="empty-state">This AECIS result does not include sampled trace data.</div>
    `;
    return;
  }

  const derivedTrend = buildDerivedAecisTrend(trace);
  const rawSignalSeries = buildScalarTraceSeries(trace, ["rawsig"]);
  const cards = [];
  if (derivedTrend.series.length > 0) {
    cards.push(
      renderTraceCard(
        "Mean / RMS Trend",
        `Rolling mean and RMS derived from the traced raw signal using a ${formatMetric(AECIS_TREND_WINDOW_SECONDS)} s trailing window.`,
        derivedTrend.times,
        derivedTrend.series,
      ),
    );
  }
  if (rawSignalSeries.length > 0) {
    cards.push(
      renderTraceCard(
        "Input Signal",
        "Sampled rawsig values applied to the FMU from the CSV input series.",
        trace.times,
        rawSignalSeries,
      ),
    );
  }

  container.innerHTML = `
    <div class="aecis-focus-meta">
      <h3 class="aecis-focus-title">${escapeHTML(simulink.payload.runName || simulink.runName)}</h3>
      <span class="result-kind-pill result-kind-${escapeHTML(resultType.kind)}">${escapeHTML(resultType.label)}</span>
      <span>${escapeHTML(simulink.payload.workflowPath || SIMULINK_WORKFLOW_PATH)}</span>
      <span>step ${escapeHTML(stepName)}</span>
      ${stepResult.time !== undefined ? `<span>reported stop time ${escapeHTML(formatMetric(stepResult.time))}</span>` : ""}
    </div>
    ${buildSimulinkFallbackMarkup(simulink)}
    ${cards.length > 0 ? `<div class="trace-stack">${cards.join("")}</div>` : '<div class="empty-state">No trace series were available in the latest AECIS result.</div>'}
  `;
}

function renderAeEventStats(targetContainer = null) {
  const container = targetContainer || document.getElementById("aeEventStats");
  const aeStats = state.aeStatsResult;

  if (!container) {
    return;
  }

  if (!aeStats || aeStats.state === "loading") {
    container.innerHTML = '<div class="empty-state">Waiting for the latest successful AE event statistics workflow result…</div>';
    return;
  }

  if (aeStats.state === "empty") {
    container.innerHTML = `<div class="empty-state">${escapeHTML(aeStats.message)}</div>`;
    return;
  }

  if (aeStats.state === "error") {
    container.innerHTML = `<div class="empty-state">Unable to load AE statistics for <code>${escapeHTML(aeStats.runName)}</code>.<br>${escapeHTML(aeStats.message)}</div>`;
    return;
  }

  const stepResults = aeStats.payload?.stepResults || {};
  const channels = [
    buildAeChannel("CH2", "ae_ch2", stepResults.ae_ch2, paletteColor(0)),
    buildAeChannel("CH6", "ae_ch6", stepResults.ae_ch6, paletteColor(1)),
  ].filter(Boolean);

  if (channels.length === 0) {
    container.innerHTML = '<div class="empty-state">The latest AE statistics result does not include CH2 or CH6 step output.</div>';
    return;
  }

  container.innerHTML = `
    <div class="aecis-focus-meta">
      <h3 class="aecis-focus-title">${escapeHTML(aeStats.payload.runName || aeStats.runName)}</h3>
      <span class="result-kind-pill result-kind-ae">AE Event Stats</span>
      <span>${escapeHTML(aeStats.payload.workflowPath || AE_STATS_WORKFLOW_PATH)}</span>
      <span>${channels.length} channel${channels.length === 1 ? "" : "s"}</span>
    </div>
    ${buildAeStatsFallbackMarkup(aeStats)}
    <div class="result-note">These plots use edge-computed AE event features from the emailed CSV tables. They are not raw waveform AECIS outputs.</div>
    <div class="ae-channel-grid">
      ${channels.map((channel) => renderAeChannelCard(channel)).join("")}
    </div>
    <div class="trace-stack">
      ${renderAeComparisonTraceCard(
        "Rolling Event Rate",
        "Events per second in a 300 s trailing window.",
        channels,
        [{ signal: "rolling_event_rate_hz", suffix: "event rate" }],
      )}
      ${renderAeComparisonTraceCard(
        "Rolling p95 Feature Values",
        "Windowed p95 values for RMS, amplitude, and ASL by AE channel.",
        channels,
        [
          { signal: "rolling_rms_p95", suffix: "RMS p95" },
          { signal: "rolling_amplitude_p95", suffix: "amplitude p95" },
          { signal: "rolling_asl_p95", suffix: "ASL p95" },
        ],
      )}
      ${renderAeComparisonTraceCard(
        "Cumulative Energy",
        "Cumulative event energy over elapsed measurement time.",
        channels,
        [{ signal: "cumulative_energy", suffix: "energy" }],
      )}
    </div>
  `;
}

function buildAeStatsFallbackMarkup(aeStats) {
  if (!aeStats?.fallbackFrom || aeStats.fallbackFrom === aeStats.runName) {
    return "";
  }
  return `
    <div class="result-note">
      Latest successful AE run <code>${escapeHTML(aeStats.fallbackFrom)}</code> had no structured result payload.
      Showing the most recent parseable result from <code>${escapeHTML(aeStats.payload.runName || aeStats.runName)}</code>.
    </div>
  `;
}

function buildAeChannel(label, stepName, stepResult, color) {
  if (!stepResult || typeof stepResult !== "object") {
    return null;
  }
  return {
    label,
    stepName,
    color,
    result: stepResult,
    trace: extractSimulinkTrace(stepResult),
  };
}

function renderAeChannelCard(channel) {
  const result = channel.result;
  const metrics = [
    { label: "Events", value: result.event_count },
    { label: "Duration", value: `${formatMetric(Number(result.duration_seconds || 0) / 3600)} h` },
    { label: "Rate", value: `${formatMetric(result.event_rate_hz)} Hz` },
    { label: "Invalid Rows", value: result.invalid_rows },
    { label: "RMS p95", value: result.rms_p95 },
    { label: "Amplitude Max", value: result.amplitude_max },
  ];

  return `
    <article class="ae-channel-card">
      <div class="ae-channel-head">
        <h3>${escapeHTML(channel.label)}</h3>
        <span>step ${escapeHTML(channel.stepName)}</span>
      </div>
      <div class="metric-grid ae-metric-grid">
        ${metrics
          .map((metric) => `
            <div class="metric-chip">
              <span class="metric-label">${escapeHTML(metric.label)}</span>
              <span class="metric-value">${escapeHTML(formatMetricOrText(metric.value))}</span>
            </div>
          `)
          .join("")}
      </div>
      <div class="ae-range-stack">
        ${renderAeRangeRow("Amplitude", result.amplitude_p50, result.amplitude_p95, result.amplitude_max)}
        ${renderAeRangeRow("RMS", result.rms_p50, result.rms_p95, result.rms_max)}
        ${renderAeRangeRow("ASL", result.asl_p50, result.asl_p95, result.asl_max)}
      </div>
      <div class="result-meta ae-channel-meta">
        <div>frequency centroid p50 ${escapeHTML(formatMetric(result.frequency_centroid_p50))} kHz</div>
        <div>peak frequency p50 ${escapeHTML(formatMetric(result.peak_frequency_p50))} kHz</div>
        <div>average frequency p50 ${escapeHTML(formatMetric(result.average_frequency_p50))} kHz</div>
      </div>
    </article>
  `;
}

function renderAeRangeRow(label, p50, p95, max) {
  const maxValue = Math.max(Number(max) || 0, Number(p95) || 0, Number(p50) || 0, 1e-9);
  const p50Width = Math.max(0, Math.min(100, (Number(p50) / maxValue) * 100));
  const p95Width = Math.max(0, Math.min(100, (Number(p95) / maxValue) * 100));
  return `
    <div class="ae-range-row">
      <div class="ae-range-label">
        <span>${escapeHTML(label)}</span>
        <span>p50 ${escapeHTML(formatMetric(p50))} | p95 ${escapeHTML(formatMetric(p95))} | max ${escapeHTML(formatMetric(max))}</span>
      </div>
      <div class="ae-range-track" aria-hidden="true">
        <span class="ae-range-p95" style="width:${p95Width}%"></span>
        <span class="ae-range-p50" style="width:${p50Width}%"></span>
      </div>
    </div>
  `;
}

function renderAeComparisonTraceCard(title, description, channels, signalSpecs) {
  const series = channels.flatMap((channel, channelIndex) =>
    signalSpecs
      .map((spec, specIndex) => buildAeTraceSeries(channel, spec, channelIndex, specIndex))
      .filter(Boolean),
  );
  if (series.length === 0) {
    return "";
  }
  return renderMultiTraceCard(title, description, series);
}

function buildAeTraceSeries(channel, spec, channelIndex, specIndex) {
  const values = channel.trace?.signals?.[spec.signal];
  if (!Array.isArray(channel.trace?.times) || !Array.isArray(values)) {
    return null;
  }
  const samples = values
    .slice(0, channel.trace.times.length)
    .map((value) => coerceTraceNumber(value));
  if (samples.length === 0 || samples.every((value) => !Number.isFinite(value))) {
    return null;
  }
  return {
    name: `${channel.label} ${spec.suffix}`,
    color: paletteColor(channelIndex + specIndex * 2),
    times: channel.trace.times,
    values: samples,
  };
}

function renderMultiTraceCard(title, description, series) {
  const chartId = traceChartId(title);
  registerTraceChart(chartId, {
    kind: "multi",
    title,
    yAxisLabel: resolveChartYAxisLabel(title),
    series,
  });
  return `
    <section class="trace-card">
      <div class="trace-head">
        <div>
          <h4>${escapeHTML(title)}</h4>
          <p>${escapeHTML(description)}</p>
        </div>
      </div>
      <div class="trace-chart-shell">
        ${renderTraceChartShell(chartId, buildMultiTraceChartSVG(series, resolveChartYAxisLabel(title), chartId))}
      </div>
    </section>
  `;
}

function traceChartId(title) {
  return `chart-${traceSlug(title, "trace")}`;
}

function traceSeriesId(name, index) {
  return `series-${index}-${traceSlug(name, "trace-series")}`;
}

function traceSeriesKey(chartId, seriesId) {
  return `${chartId}::${seriesId}`;
}

function isTraceSeriesHidden(chartId, seriesId) {
  return state.hiddenTraceSeries.has(traceSeriesKey(chartId, seriesId));
}

function traceSlug(value, fallback) {
  const slug = String(value || "")
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, "-")
    .replace(/^-+|-+$/g, "");
  return slug || fallback;
}

function resolveChartYAxisLabel(title) {
  const normalized = String(title || "").toLowerCase();
  if (normalized.includes("event rate")) {
    return "Event rate (events/s)";
  }
  if (normalized.includes("energy")) {
    return "Cumulative energy";
  }
  if (normalized.includes("p95")) {
    return "Feature value";
  }
  if (normalized.includes("mean") || normalized.includes("rms") || normalized.includes("signal")) {
    return "Signal value";
  }
  return "Value";
}

function resolveSummaryMetrics(stepResult, trace, derivedTrend) {
  const metrics = [];
  const latestTrend = latestDerivedTrend(derivedTrend);
  if (latestTrend) {
    metrics.push(
      { label: "Mean", value: latestTrend.mean },
      { label: "RMS", value: latestTrend.rms },
    );
    return metrics;
  }

  const ciVector = resolveCIVector(stepResult, trace);
  if (ciVector.length >= 2) {
    metrics.push(
      { label: CIVECTOR_LABELS[0], value: ciVector[0] },
      { label: CIVECTOR_LABELS[1], value: ciVector[1] },
    );
  }
  return metrics;
}

function classifySimulinkPayload(payload) {
  const stepEntries = Object.entries(payload?.stepResults || {});
  for (const [, stepResult] of stepEntries) {
    if (extractSimulinkTrace(stepResult)) {
      return {
        kind: "trace",
        label: "Trace Result",
      };
    }
  }
  if (stepEntries.length > 0) {
    return {
      kind: "legacy",
      label: "Legacy Result",
    };
  }
  return {
    kind: "unknown",
    label: "Unknown Result",
  };
}

function classifySimulinkRun(run) {
  if (run?.workflowPath !== SIMULINK_WORKFLOW_PATH) {
    return null;
  }
  if (String(run.phase || "").toLowerCase() !== "succeeded") {
    return null;
  }
  const cached = state.simulinkResultsCache.get(run.name);
  if (cached?.state === "ready") {
    return classifySimulinkPayload(cached.payload);
  }
  if (cached?.state === "error") {
    return {
      kind: "missing",
      label: "No Result Payload",
    };
  }
  return {
    kind: "pending",
    label: "Unchecked Result",
  };
}

function classifyAeStatsRun(run) {
  if (run?.workflowPath !== AE_STATS_WORKFLOW_PATH) {
    return null;
  }
  if (String(run.phase || "").toLowerCase() !== "succeeded") {
    return null;
  }
  const cached = state.aeStatsResultsCache.get(run.name);
  if (cached?.state === "ready") {
    return {
      kind: "ae",
      label: "AE Event Stats",
    };
  }
  if (cached?.state === "error") {
    return {
      kind: "missing",
      label: "No Result Payload",
    };
  }
  return {
    kind: "pending",
    label: "Unchecked Result",
  };
}

function preferredSimulinkStep(stepEntries) {
  for (const entry of stepEntries) {
    if (extractSimulinkTrace(entry[1])) {
      return entry;
    }
  }
  for (const entry of stepEntries) {
    if (Array.isArray(entry[1]?.CIvector)) {
      return entry;
    }
  }
  return stepEntries[0];
}

function extractSimulinkTrace(stepResult) {
  const trace = stepResult?.trace;
  if (!Array.isArray(trace?.time) || !trace?.signals || typeof trace.signals !== "object") {
    return null;
  }
  const times = trace.time.map((value) => Number(value));
  if (times.length === 0 || times.some((value) => !Number.isFinite(value))) {
    return null;
  }
  return {
    times,
    signals: trace.signals,
  };
}

function resolveCIVector(stepResult, trace) {
  if (Array.isArray(stepResult?.CIvector)) {
    return stepResult.CIvector;
  }
  const ciSamples = trace?.signals?.CIvector;
  if (!Array.isArray(ciSamples) || ciSamples.length === 0) {
    return [];
  }
  const lastSample = ciSamples[ciSamples.length - 1];
  return Array.isArray(lastSample) ? lastSample : [];
}

function buildDerivedAecisTrend(trace) {
  const rawSignal = trace?.signals?.rawsig;
  if (!Array.isArray(trace?.times) || !Array.isArray(rawSignal) || trace.times.length === 0) {
    return {
      times: [],
      series: [],
    };
  }

  const samples = rawSignal
    .slice(0, trace.times.length)
    .map((value) => coerceTraceNumber(value));
  const trendTimes = [];
  const meanValues = [];
  const rmsValues = [];
  let startIndex = 0;

  for (let index = 0; index < trace.times.length; index += 1) {
    const currentTime = trace.times[index];
    while (startIndex < index && trace.times[startIndex] < currentTime - AECIS_TREND_WINDOW_SECONDS) {
      startIndex += 1;
    }
    const window = samples
      .slice(startIndex, index + 1)
      .filter((value) => Number.isFinite(value));
    if (window.length < 2) {
      continue;
    }
    const mean = window.reduce((sum, value) => sum + value, 0) / window.length;
    const rms = Math.sqrt(window.reduce((sum, value) => sum + value * value, 0) / window.length);
    trendTimes.push(currentTime);
    meanValues.push(mean);
    rmsValues.push(rms);
  }

  const series = [];
  if (meanValues.some((value) => Number.isFinite(value))) {
    series.push({
      name: "Mean",
      color: paletteColor(0),
      values: meanValues,
    });
  }
  if (rmsValues.some((value) => Number.isFinite(value))) {
    series.push({
      name: "RMS",
      color: paletteColor(1),
      values: rmsValues,
    });
  }

  return {
    times: trendTimes,
    series,
  };
}

function latestDerivedTrend(derivedTrend) {
  if (!derivedTrend?.series?.length || !derivedTrend.times?.length) {
    return null;
  }
  const meanSeries = derivedTrend.series.find((series) => series.name === "Mean");
  const rmsSeries = derivedTrend.series.find((series) => series.name === "RMS");
  const mean = meanSeries?.values?.[meanSeries.values.length - 1];
  const rms = rmsSeries?.values?.[rmsSeries.values.length - 1];
  if (!Number.isFinite(mean) && !Number.isFinite(rms)) {
    return null;
  }
  return {
    mean,
    rms,
  };
}

function buildScalarTraceSeries(trace, signalNames) {
  return signalNames
    .map((name, index) => {
      const values = trace.signals?.[name];
      if (!Array.isArray(values)) {
        return null;
      }
      const samples = values
        .slice(0, trace.times.length)
        .map((value) => coerceTraceNumber(value));
      if (samples.length === 0 || samples.every((value) => !Number.isFinite(value))) {
        return null;
      }
      return {
        name,
        color: paletteColor(index),
        values: samples,
      };
    })
    .filter(Boolean);
}

function renderTraceCard(title, description, times, series) {
  const chartId = traceChartId(title);
  registerTraceChart(chartId, {
    kind: "shared",
    title,
    times,
    yAxisLabel: resolveChartYAxisLabel(title),
    series,
  });
  return `
    <section class="trace-card">
      <div class="trace-head">
        <div>
          <h4>${escapeHTML(title)}</h4>
          <p>${escapeHTML(description)}</p>
        </div>
      </div>
      <div class="trace-chart-shell">
        ${renderTraceChartShell(chartId, buildTraceChartSVG(times, series, resolveChartYAxisLabel(title), chartId))}
      </div>
    </section>
  `;
}

function registerTraceChart(chartId, chart) {
  state.traceCharts.set(chartId, chart);
}

function renderTraceChartShell(chartId, fallbackSVG) {
  return `
    <div class="echarts-chart" data-trace-chart="${escapeHTML(chartId)}" aria-hidden="true"></div>
    <div class="svg-chart-fallback">${fallbackSVG}</div>
  `;
}

function initializeECharts(root = document) {
  if (!window.echarts || typeof window.echarts.init !== "function") {
    return;
  }

  for (const element of root.querySelectorAll(".echarts-chart[data-trace-chart]")) {
    const chartId = element.dataset.traceChart || "";
    const chart = state.traceCharts.get(chartId);
    if (!chart) {
      continue;
    }

    const option = buildEChartsOption(chartId, chart);
    if (option.series.length === 0) {
      continue;
    }

    const shell = element.closest(".trace-chart-shell");
    shell?.classList.add("echarts-ready");
    element.setAttribute("aria-hidden", "false");
    const instance =
      window.echarts.getInstanceByDom(element) ||
      window.echarts.init(element, null, { renderer: "canvas" });

    instance.off("legendselectchanged");
    instance.setOption(option, true);
    instance.on("legendselectchanged", (event) => {
      syncEChartsHiddenSeries(chartId, chart, event.selected || {});
      window.setTimeout(() => instance.resize(), 0);
    });
  }
}

function buildEChartsOption(chartId, chart) {
  const { scale } = resolveChartScale(chart);
  const theme = chartTheme();
  const series = buildEChartsSeries(chart, scale);
  const selected = {};

  for (const [index, item] of chart.series.entries()) {
    const seriesId = traceSeriesId(item.name, index);
    selected[item.name] = !isTraceSeriesHidden(chartId, seriesId);
  }

  return {
    animation: false,
    backgroundColor: "transparent",
    color: chart.series.map((item) => item.color),
    textStyle: {
      color: theme.muted,
      fontFamily: theme.fontFamily,
      fontSize: 12,
      fontWeight: 500,
    },
    grid: {
      left: 64,
      right: 24,
      top: 54,
      bottom: 54,
      containLabel: true,
    },
    legend: {
      type: "scroll",
      top: 8,
      right: 10,
      left: "auto",
      itemWidth: 9,
      itemHeight: 9,
      icon: "circle",
      selected,
      inactiveColor: "rgba(92,104,103,0.34)",
      pageIconColor: theme.accent,
      pageIconInactiveColor: "rgba(92,104,103,0.3)",
      pageTextStyle: {
        color: theme.muted,
        fontFamily: theme.fontFamily,
      },
      textStyle: {
        color: theme.muted,
        fontFamily: theme.fontFamily,
        fontSize: 12,
        fontWeight: 600,
      },
    },
    tooltip: {
      trigger: "axis",
      confine: true,
      appendToBody: true,
      className: "echarts-tooltip",
      backgroundColor: "rgba(253, 250, 243, 0.98)",
      borderColor: "rgba(23, 33, 38, 0.12)",
      borderWidth: 1,
      padding: [10, 12],
      textStyle: {
        color: theme.ink,
        fontFamily: theme.fontFamily,
        fontSize: 12,
        fontWeight: 500,
      },
      axisPointer: {
        type: "line",
        lineStyle: {
          color: "rgba(15, 124, 120, 0.34)",
          width: 1,
        },
      },
      formatter: (params) => renderEChartsTooltip(params, chart, scale),
    },
    xAxis: {
      type: "value",
      name: scale.axis,
      nameLocation: "middle",
      nameGap: 34,
      scale: true,
      axisLine: {
        lineStyle: {
          color: "rgba(23, 33, 38, 0.24)",
        },
      },
      axisLabel: {
        color: theme.muted,
        fontFamily: theme.fontFamily,
        fontSize: 12,
        fontWeight: 600,
        hideOverlap: true,
        formatter: (value) => formatChartValueTick(value),
      },
      axisTick: {
        show: false,
      },
      nameTextStyle: {
        color: theme.muted,
        fontFamily: theme.fontFamily,
        fontSize: 12,
        fontWeight: 700,
      },
      splitLine: {
        lineStyle: {
          color: "rgba(23, 33, 38, 0.09)",
          type: "dashed",
        },
      },
    },
    yAxis: {
      type: "value",
      name: chart.yAxisLabel,
      nameLocation: "end",
      nameGap: 12,
      scale: true,
      axisLine: {
        show: true,
        lineStyle: {
          color: "rgba(23, 33, 38, 0.24)",
        },
      },
      axisLabel: {
        color: theme.muted,
        fontFamily: theme.fontFamily,
        fontSize: 12,
        fontWeight: 600,
        hideOverlap: true,
        formatter: (value) => formatChartValueTick(value),
      },
      axisTick: {
        show: false,
      },
      nameTextStyle: {
        align: "left",
        color: theme.muted,
        fontFamily: theme.fontFamily,
        fontSize: 12,
        fontWeight: 700,
      },
      splitLine: {
        lineStyle: {
          color: "rgba(23, 33, 38, 0.09)",
          type: "dashed",
        },
      },
    },
    series,
  };
}

function buildEChartsSeries(chart, scale) {
  return chart.series
    .map((item, index) => {
      const rawTimes = chart.kind === "shared" ? chart.times : item.times;
      const points = rawTimes
        .slice(0, item.values.length)
        .map((time, pointIndex) => {
          const x = Number(time) / scale.divisor;
          const y = coerceTraceNumber(item.values[pointIndex]);
          return Number.isFinite(x) && Number.isFinite(y) ? [x, y] : null;
        })
        .filter(Boolean);

      if (points.length === 0) {
        return null;
      }

      return {
        name: item.name,
        type: "line",
        data: points,
        showSymbol: false,
        symbol: "circle",
        symbolSize: 5,
        sampling: "lttb",
        clip: true,
        lineStyle: {
          width: 2.5,
          color: item.color,
        },
        itemStyle: {
          color: item.color,
        },
        emphasis: {
          focus: "series",
          lineStyle: {
            width: 3,
          },
        },
      };
    })
    .filter(Boolean);
}

function renderEChartsTooltip(params, chart, scale) {
  const items = Array.isArray(params) ? params : [params];
  const visibleItems = items.filter((item) => Array.isArray(item.value) && Number.isFinite(Number(item.value[1])));
  if (visibleItems.length === 0) {
    return "";
  }

  const time = visibleItems[0].value[0];
  const rows = visibleItems
    .map((item) => {
      const value = Number(item.value[1]);
      return `
        <div class="echarts-tooltip-row">
          <span class="echarts-tooltip-marker" style="background:${item.color}"></span>
          <span class="echarts-tooltip-name">${escapeHTML(item.seriesName)}</span>
          <strong>${escapeHTML(formatChartValueTick(value))}</strong>
        </div>
      `;
    })
    .join("");

  return `
    <div class="echarts-tooltip-card">
      <div class="echarts-tooltip-title">${escapeHTML(scale.axis)}: ${escapeHTML(formatChartValueTick(time))}</div>
      <div class="echarts-tooltip-subtitle">${escapeHTML(chart.yAxisLabel)}</div>
      ${rows}
    </div>
  `;
}

function syncEChartsHiddenSeries(chartId, chart, selected) {
  for (const [index, item] of chart.series.entries()) {
    const seriesId = traceSeriesId(item.name, index);
    const key = traceSeriesKey(chartId, seriesId);
    if (selected[item.name] === false) {
      state.hiddenTraceSeries.add(key);
    } else {
      state.hiddenTraceSeries.delete(key);
    }
  }
}

function resolveChartScale(chart) {
  const xValues = chart.kind === "shared"
    ? chart.times.filter((value) => Number.isFinite(value))
    : chart.series.flatMap((item) => item.times.filter((value) => Number.isFinite(value)));
  const minX = xValues.length > 0 ? Math.min(...xValues) : 0;
  const maxX = xValues.length > 0 ? Math.max(...xValues) : 1;
  const scale = chartTimeScale(maxX === minX ? 1 : maxX - minX);

  return { scale };
}

function chartTheme() {
  const styles = window.getComputedStyle(document.body);
  return {
    fontFamily: styles.fontFamily || 'Inter, Aptos, "Segoe UI", sans-serif',
    muted: styles.getPropertyValue("--muted").trim() || "#5c6867",
    ink: styles.getPropertyValue("--ink").trim() || "#172126",
    accent: styles.getPropertyValue("--accent").trim() || "#0f7c78",
  };
}

function disposeEChartsIn(root) {
  if (!window.echarts || typeof window.echarts.getInstanceByDom !== "function") {
    return;
  }
  for (const element of root.querySelectorAll(".echarts-chart")) {
    const instance = window.echarts.getInstanceByDom(element);
    if (instance) {
      instance.dispose();
    }
  }
}

function resizeECharts() {
  if (!window.echarts || typeof window.echarts.getInstanceByDom !== "function") {
    return;
  }
  for (const element of document.querySelectorAll(".echarts-chart")) {
    const instance = window.echarts.getInstanceByDom(element);
    if (instance) {
      instance.resize();
    }
  }
}

function buildTraceChartSVG(times, series, yAxisLabel = "Value", chartId = "") {
  const width = 640;
  const height = 240;
  const margin = { top: 24, right: 34, bottom: 48, left: 62 };
  const chartWidth = width - margin.left - margin.right;
  const chartHeight = height - margin.top - margin.bottom;
  const visibleSeries = visibleTraceSeries(series, chartId);
  const xValues = visibleSeries.length > 0 ? times.filter((value) => Number.isFinite(value)) : [];
  const flatValues = visibleSeries.flatMap((item) => item.values.filter((value) => Number.isFinite(value)));

  if (xValues.length === 0 || flatValues.length === 0) {
    return renderEmptyTraceSVG(width, height, visibleSeries.length === 0 ? "No visible series. Use the legend to restore a line." : "Trace data is unavailable.");
  }

  const minX = Math.min(...xValues);
  const maxX = Math.max(...xValues);
  const minY = Math.min(...flatValues);
  const maxY = Math.max(...flatValues);
  const xSpan = maxX === minX ? 1 : maxX - minX;
  const ySpan = maxY === minY ? Math.max(1, Math.abs(maxY) || 1) : maxY - minY;
  const xPad = maxX === minX ? 0.5 : Math.max(xSpan * 0.03, 0.1);
  const yPad = Math.max(ySpan * 0.08, Math.abs(maxY) * 0.02, 0.02);
  const domainMinX = minX - xPad;
  const domainMaxX = maxX + xPad;
  const domainMinY = minY - yPad;
  const domainMaxY = maxY + yPad;
  const domainXSpan = domainMaxX - domainMinX || 1;
  const domainYSpan = domainMaxY - domainMinY || 1;
  const xAxisLabel = chartTimeAxisLabel(xSpan);
  const parts = [
    `<svg viewBox="0 0 ${width} ${height}" preserveAspectRatio="none" role="img" aria-label="Simulink trace chart">`,
    `<rect x="0" y="0" width="${width}" height="${height}" rx="18" fill="rgba(255,255,255,0.36)"></rect>`,
    `<text x="${margin.left}" y="16" text-anchor="start" class="chart-axis-title">${escapeHTML(yAxisLabel)}</text>`,
  ];

  for (let index = 0; index <= 3; index += 1) {
    const ratio = index / 3;
    const x = margin.left + ratio * chartWidth;
    const anchor = chartTickAnchor(index, 3);
    parts.push(`<line x1="${x}" y1="${margin.top}" x2="${x}" y2="${margin.top + chartHeight}" class="chart-grid"></line>`);
    parts.push(`<text x="${x}" y="${height - 28}" text-anchor="${anchor}" class="chart-label">${escapeHTML(formatChartTimeTick(minX + ratio * xSpan, xSpan))}</text>`);
  }

  for (let index = 0; index <= 3; index += 1) {
    const ratio = index / 3;
    const value = minY + ratio * ySpan;
    const y = margin.top + chartHeight - ((value - domainMinY) / domainYSpan) * chartHeight;
    parts.push(`<line x1="${margin.left}" y1="${y}" x2="${width - margin.right}" y2="${y}" class="chart-grid"></line>`);
    parts.push(`<text x="${margin.left - 10}" y="${y + 4}" text-anchor="end" class="chart-label">${escapeHTML(formatChartValueTick(value))}</text>`);
  }

  parts.push(`<line x1="${margin.left}" y1="${margin.top + chartHeight}" x2="${width - margin.right}" y2="${margin.top + chartHeight}" class="chart-axis"></line>`);
  parts.push(`<line x1="${margin.left}" y1="${margin.top}" x2="${margin.left}" y2="${margin.top + chartHeight}" class="chart-axis"></line>`);
  parts.push(`<text x="${margin.left + chartWidth / 2}" y="${height - 8}" text-anchor="middle" class="chart-axis-title">${escapeHTML(xAxisLabel)}</text>`);

  for (const [seriesIndex, item] of series.entries()) {
    const points = item.values
      .map((value, index) => {
        const time = times[index];
        if (!Number.isFinite(time) || !Number.isFinite(value)) {
          return null;
        }
        const x = margin.left + ((time - domainMinX) / domainXSpan) * chartWidth;
        const y = margin.top + chartHeight - ((value - domainMinY) / domainYSpan) * chartHeight;
        return {
          x,
          y,
          value,
        };
      })
      .filter(Boolean);
    if (points.length < 2) {
      continue;
    }
    const seriesId = traceSeriesId(item.name, seriesIndex);
    const hidden = isTraceSeriesHidden(chartId, seriesId);
    parts.push(
      `<g class="trace-series${hidden ? " hidden" : ""}" data-trace-chart="${escapeHTML(chartId)}" data-trace-series="${escapeHTML(seriesId)}" aria-hidden="${hidden ? "true" : "false"}">`,
    );
    parts.push(
      `<polyline fill="none" stroke="${item.color}" stroke-width="2.25" stroke-linecap="round" stroke-linejoin="round" points="${points.map((point) => `${point.x},${point.y}`).join(" ")}"></polyline>`,
    );
    const markerIndexes = new Set([0, points.length - 1]);
    let peakIndex = 0;
    for (let index = 1; index < points.length; index += 1) {
      if (points[index].value > points[peakIndex].value) {
        peakIndex = index;
      }
    }
    markerIndexes.add(peakIndex);
    for (const index of markerIndexes) {
      const point = points[index];
      if (!point) {
        continue;
      }
      parts.push(
        `<circle cx="${point.x}" cy="${point.y}" r="3.2" fill="${item.color}" stroke="rgba(255,255,255,0.92)" stroke-width="1.1"></circle>`,
      );
    }
    parts.push("</g>");
  }

  parts.push("</svg>");
  return parts.join("");
}

function buildMultiTraceChartSVG(series, yAxisLabel = "Value", chartId = "") {
  const width = 640;
  const height = 240;
  const margin = { top: 24, right: 34, bottom: 48, left: 62 };
  const chartWidth = width - margin.left - margin.right;
  const chartHeight = height - margin.top - margin.bottom;
  const visibleSeries = visibleTraceSeries(series, chartId);
  const xValues = visibleSeries.flatMap((item) => item.times.filter((value) => Number.isFinite(value)));
  const flatValues = visibleSeries.flatMap((item) => item.values.filter((value) => Number.isFinite(value)));

  if (xValues.length === 0 || flatValues.length === 0) {
    return renderEmptyTraceSVG(width, height, visibleSeries.length === 0 ? "No visible series. Use the legend to restore a line." : "Trace data is unavailable.");
  }

  const minX = Math.min(...xValues);
  const maxX = Math.max(...xValues);
  const minY = Math.min(...flatValues);
  const maxY = Math.max(...flatValues);
  const xSpan = maxX === minX ? 1 : maxX - minX;
  const ySpan = maxY === minY ? Math.max(1, Math.abs(maxY) || 1) : maxY - minY;
  const xPad = maxX === minX ? 0.5 : Math.max(xSpan * 0.03, 0.1);
  const yPad = Math.max(ySpan * 0.08, Math.abs(maxY) * 0.02, 0.02);
  const domainMinX = minX - xPad;
  const domainMaxX = maxX + xPad;
  const domainMinY = minY - yPad;
  const domainMaxY = maxY + yPad;
  const domainXSpan = domainMaxX - domainMinX || 1;
  const domainYSpan = domainMaxY - domainMinY || 1;
  const xAxisLabel = chartTimeAxisLabel(xSpan);
  const parts = [
    `<svg viewBox="0 0 ${width} ${height}" preserveAspectRatio="none" role="img" aria-label="AE event statistics chart">`,
    `<rect x="0" y="0" width="${width}" height="${height}" rx="18" fill="rgba(255,255,255,0.36)"></rect>`,
    `<text x="${margin.left}" y="16" text-anchor="start" class="chart-axis-title">${escapeHTML(yAxisLabel)}</text>`,
  ];

  for (let index = 0; index <= 3; index += 1) {
    const ratio = index / 3;
    const x = margin.left + ratio * chartWidth;
    const anchor = chartTickAnchor(index, 3);
    parts.push(`<line x1="${x}" y1="${margin.top}" x2="${x}" y2="${margin.top + chartHeight}" class="chart-grid"></line>`);
    parts.push(`<text x="${x}" y="${height - 28}" text-anchor="${anchor}" class="chart-label">${escapeHTML(formatChartTimeTick(minX + ratio * xSpan, xSpan))}</text>`);
  }

  for (let index = 0; index <= 3; index += 1) {
    const ratio = index / 3;
    const value = minY + ratio * ySpan;
    const y = margin.top + chartHeight - ((value - domainMinY) / domainYSpan) * chartHeight;
    parts.push(`<line x1="${margin.left}" y1="${y}" x2="${width - margin.right}" y2="${y}" class="chart-grid"></line>`);
    parts.push(`<text x="${margin.left - 10}" y="${y + 4}" text-anchor="end" class="chart-label">${escapeHTML(formatChartValueTick(value))}</text>`);
  }

  parts.push(`<line x1="${margin.left}" y1="${margin.top + chartHeight}" x2="${width - margin.right}" y2="${margin.top + chartHeight}" class="chart-axis"></line>`);
  parts.push(`<line x1="${margin.left}" y1="${margin.top}" x2="${margin.left}" y2="${margin.top + chartHeight}" class="chart-axis"></line>`);
  parts.push(`<text x="${margin.left + chartWidth / 2}" y="${height - 8}" text-anchor="middle" class="chart-axis-title">${escapeHTML(xAxisLabel)}</text>`);

  for (const [seriesIndex, item] of series.entries()) {
    const points = item.values
      .map((value, index) => {
        const time = item.times[index];
        if (!Number.isFinite(time) || !Number.isFinite(value)) {
          return null;
        }
        const x = margin.left + ((time - domainMinX) / domainXSpan) * chartWidth;
        const y = margin.top + chartHeight - ((value - domainMinY) / domainYSpan) * chartHeight;
        return { x, y, value };
      })
      .filter(Boolean);
    if (points.length < 2) {
      continue;
    }
    const seriesId = traceSeriesId(item.name, seriesIndex);
    const hidden = isTraceSeriesHidden(chartId, seriesId);
    parts.push(
      `<g class="trace-series${hidden ? " hidden" : ""}" data-trace-chart="${escapeHTML(chartId)}" data-trace-series="${escapeHTML(seriesId)}" aria-hidden="${hidden ? "true" : "false"}">`,
    );
    parts.push(
      `<polyline fill="none" stroke="${item.color}" stroke-width="2.15" stroke-linecap="round" stroke-linejoin="round" points="${points.map((point) => `${point.x},${point.y}`).join(" ")}"></polyline>`,
    );
    for (const point of [points[0], points[points.length - 1]]) {
      parts.push(
        `<circle cx="${point.x}" cy="${point.y}" r="3" fill="${item.color}" stroke="rgba(255,255,255,0.92)" stroke-width="1.1"></circle>`,
      );
    }
    parts.push("</g>");
  }

  parts.push("</svg>");
  return parts.join("");
}

function visibleTraceSeries(series, chartId) {
  return series.filter((item, index) => !isTraceSeriesHidden(chartId, traceSeriesId(item.name, index)));
}

function renderEmptyTraceSVG(width, height, message) {
  return `
    <svg viewBox="0 0 ${width} ${height}" preserveAspectRatio="none" role="img" aria-label="${escapeHTML(message)}">
      <rect x="0" y="0" width="${width}" height="${height}" rx="18" fill="rgba(255,255,255,0.36)"></rect>
      <text x="${width / 2}" y="${height / 2}" text-anchor="middle" class="chart-label">${escapeHTML(message)}</text>
    </svg>
  `;
}

function chartTickAnchor(index, lastIndex) {
  if (index === 0) {
    return "start";
  }
  if (index === lastIndex) {
    return "end";
  }
  return "middle";
}

function chartTimeScale(spanSeconds) {
  const span = Math.abs(Number(spanSeconds) || 0);
  if (span >= 3600) {
    return { divisor: 3600, suffix: "h", axis: "Elapsed time (hours)" };
  }
  if (span >= 60) {
    return { divisor: 60, suffix: "min", axis: "Elapsed time (minutes)" };
  }
  return { divisor: 1, suffix: "s", axis: "Elapsed time (seconds)" };
}

function chartTimeAxisLabel(spanSeconds) {
  return chartTimeScale(spanSeconds).axis;
}

function formatChartTimeTick(value, spanSeconds) {
  const scale = chartTimeScale(spanSeconds);
  const scaled = Number(value) / scale.divisor;
  return `${formatChartValueTick(scaled)} ${scale.suffix}`;
}

function formatChartValueTick(value) {
  const numeric = Number(value);
  if (!Number.isFinite(numeric)) {
    return String(value);
  }
  const abs = Math.abs(numeric);
  if (abs >= 1_000_000) {
    return `${trimFixed(numeric / 1_000_000, 1)}M`;
  }
  if (abs >= 10_000) {
    return `${trimFixed(numeric / 1_000, 1)}k`;
  }
  if (abs >= 100) {
    return trimFixed(numeric, 0);
  }
  if (abs >= 10) {
    return trimFixed(numeric, 1);
  }
  if (abs >= 1) {
    return trimFixed(numeric, 2);
  }
  if (abs >= 0.01) {
    return trimFixed(numeric, 3);
  }
  if (abs > 0) {
    return trimFixed(numeric, 4);
  }
  return "0";
}

function trimFixed(value, digits) {
  const fixed = Number(value).toFixed(digits);
  return fixed.includes(".") ? fixed.replace(/\.?0+$/, "") : fixed;
}

function paletteColor(index) {
  const colors = ["#0f7c78", "#bf5f2f", "#2f7652", "#7a5af8", "#90522d", "#355c7d"];
  return colors[index % colors.length];
}

function coerceTraceNumber(value) {
  if (value === null || value === undefined) {
    return NaN;
  }
  const numeric = Number(value);
  return Number.isFinite(numeric) ? numeric : NaN;
}

function renderRuns() {
  const list = document.getElementById("runsList");
  const rail = document.getElementById("runsRail");
  const workspace = document.getElementById("dashboardWorkspace");
  const toggle = document.getElementById("runsRailToggle");
  const restore = document.getElementById("runsRailRestore");
  const collapsedPanel = document.getElementById("runsRailCollapsed");
  const counts = document.getElementById("runsRailCounts");
  const workflow = selectedWorkflow();
  const runs = selectedWorkflowRuns();

  if (rail) {
    rail.classList.toggle("collapsed", state.runsRailCollapsed);
  }
  if (workspace) {
    workspace.classList.toggle("runs-collapsed", state.runsRailCollapsed);
  }
  if (toggle) {
    toggle.setAttribute("aria-expanded", state.runsRailCollapsed ? "false" : "true");
    toggle.onclick = () => setRunsRailCollapsed(true);
  }
  if (restore) {
    restore.onclick = () => setRunsRailCollapsed(false);
  }
  if (collapsedPanel) {
    collapsedPanel.setAttribute("aria-hidden", state.runsRailCollapsed ? "false" : "true");
  }
  if (counts) {
    counts.innerHTML = renderRunCounts(runs);
  }

  if (state.selectedRunName && !runs.some((run) => run.name === state.selectedRunName)) {
    state.selectedRunName = "";
  }

  if (state.runsRailCollapsed) {
    if (list) {
      list.innerHTML = "";
    }
    return;
  }

  if (!workflow) {
    list.innerHTML = '<div class="empty-state">Choose a workflow to inspect its run history.</div>';
    return;
  }

  if (runs.length === 0) {
    const remoteHint = state.config?.remoteEnabled
      ? `No visible remote runs for ${workflowLabel(workflow)} yet.`
      : "Remote launching is disabled in the current dashboard configuration.";
    list.innerHTML = `<div class="empty-state">${escapeHTML(remoteHint)}</div>`;
    return;
  }

  list.innerHTML = runs
    .map((run) => {
      const phaseClass = classifyPhase(run.phase);
      const expanded = run.name === state.selectedRunName;
      const resultPill = renderRunResultPill(run);
      return `
        <article class="run-card${expanded ? " expanded" : ""}">
          <button class="run-summary" type="button" data-run-name="${escapeHTML(run.name)}" aria-expanded="${expanded ? "true" : "false"}">
            <span class="run-status-dot ${phaseClass}" aria-hidden="true"></span>
            <span class="run-summary-main">
              <span class="run-name">${escapeHTML(run.name)}</span>
              <span class="run-subline">${escapeHTML(formatTimestampCompact(run.createdAt))} | ${escapeHTML(formatDuration(run.durationSeconds))} | ${escapeHTML(run.progress || "n/a")}</span>
            </span>
            <span class="run-expand-indicator" aria-hidden="true">${expanded ? "-" : "+"}</span>
          </button>
          ${expanded ? `
            <div class="run-details">
              <div class="run-detail-line">
                <span>Phase</span>
                <strong>${escapeHTML(run.phase || "Unknown")}</strong>
              </div>
              ${resultPill ? `<div class="run-detail-line"><span>Result</span>${resultPill}</div>` : ""}
              <div class="run-detail-line">
                <span>Workflow</span>
                <code>${escapeHTML(run.workflowPath || "unknown workflow")}</code>
              </div>
              <div class="run-detail-line">
                <span>Started</span>
                <strong>${escapeHTML(formatTimestamp(run.startedAt))}</strong>
              </div>
              <div class="run-detail-line">
                <span>Finished</span>
                <strong>${escapeHTML(formatTimestamp(run.finishedAt))}</strong>
              </div>
              <div class="run-detail-line">
                <span>Image</span>
                <code>${escapeHTML(run.image || "n/a")}</code>
              </div>
              <div class="run-detail-line">
                <span>Account</span>
                <code>${escapeHTML(run.serviceAccount || "n/a")}</code>
              </div>
              ${run.message ? `<div class="run-message">${escapeHTML(run.message)}</div>` : ""}
            </div>
          ` : ""}
        </article>
      `;
    })
    .join("");

  for (const button of list.querySelectorAll("[data-run-name]")) {
    button.addEventListener("click", () => {
      state.selectedRunName = state.selectedRunName === button.dataset.runName ? "" : button.dataset.runName;
      renderRuns();
    });
  }
}

function renderRunResultPill(run) {
  const resultType = classifySimulinkRun(run) || classifyAeStatsRun(run);
  return resultType
    ? `<span class="result-kind-pill result-kind-${escapeHTML(resultType.kind)}">${escapeHTML(resultType.label)}</span>`
    : "";
}

function renderRunCounts(runs) {
  const counts = runs.reduce(
    (accumulator, run) => {
      const phase = String(run.phase || "").toLowerCase();
      accumulator.total += 1;
      if (phase === "running") {
        accumulator.running += 1;
      } else if (phase === "succeeded") {
        accumulator.succeeded += 1;
      } else if (phase === "failed" || phase === "error") {
        accumulator.failed += 1;
      }
      return accumulator;
    },
    { total: 0, running: 0, succeeded: 0, failed: 0 },
  );
  const breakdown = `${counts.total} total, ${counts.running} running, ${counts.succeeded} succeeded, ${counts.failed} failed`;

  return `
    <span class="rail-count total" title="${escapeHTML(breakdown)}">
      <strong>${counts.total}</strong>
      <span>runs</span>
    </span>
  `;
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

function formatDuration(value) {
  const seconds = Number(value || 0);
  if (seconds >= 60) {
    const minutes = Math.floor(seconds / 60);
    const remainder = Math.round(seconds % 60);
    return `${minutes}m ${remainder}s`;
  }
  return `${seconds.toFixed(seconds >= 10 ? 0 : 1)}s`;
}

function formatMetric(value) {
  const numeric = Number(value);
  if (!Number.isFinite(numeric)) {
    return String(value);
  }
  if (Math.abs(numeric) >= 1000 || (Math.abs(numeric) > 0 && Math.abs(numeric) < 0.001)) {
    return numeric.toExponential(3);
  }
  return numeric.toFixed(4).replace(/\.?0+$/, "");
}

function formatMetricOrText(value) {
  const numeric = Number(value);
  return Number.isFinite(numeric) ? formatMetric(numeric) : String(value);
}

function formatTimestampCompact(raw) {
  if (!raw) {
    return "n/a";
  }
  const date = new Date(raw);
  if (Number.isNaN(date.getTime())) {
    return "n/a";
  }
  return date.toLocaleString([], {
    month: "short",
    day: "2-digit",
    hour: "2-digit",
    minute: "2-digit",
  });
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
