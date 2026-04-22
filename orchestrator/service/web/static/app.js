const state = {
  config: null,
  workflows: [],
  runs: [],
  simulinkResult: null,
  simulinkResultsCache: new Map(),
  runtimeProblems: [],
  pendingWorkflows: new Set(),
  poller: null,
  loadingRuns: false,
};

const SIMULINK_WORKFLOW_PATH = "workflows/calculate_aecis.yaml";
const CIVECTOR_LABELS = ["Mean", "RMS", "Peak-to-Peak", "Skewness", "Kurtosis"];
const SIMULINK_RESULT_RETRY_MS = 15_000;
const AECIS_TREND_WINDOW_SECONDS = 2.5;

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
      renderSimulinkResults();
      renderRuns();
      renderAecisFocus();
    }
  } catch (error) {
    state.runtimeProblems = [error.message];
    renderBanner();
    renderWorkflows();
    renderSimulinkResults();
    renderRuns();
    renderAecisFocus();
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
    await loadSimulinkResults();
    renderBanner();
    renderSimulinkResults();
    renderRuns();
    renderAecisFocus();
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
    renderAecisFocus();
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

function renderAecisFocus() {
  const container = document.getElementById("aecisFocus");
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
  return `
    <section class="trace-card">
      <div class="trace-head">
        <div>
          <h4>${escapeHTML(title)}</h4>
          <p>${escapeHTML(description)}</p>
        </div>
        <div class="trace-legend">
          ${series
            .map(
              (item) => `
                <span class="trace-legend-item">
                  <span class="trace-swatch" style="background:${item.color}"></span>
                  ${escapeHTML(item.name)}
                </span>
              `,
            )
            .join("")}
        </div>
      </div>
      <div class="trace-chart-shell">
        ${buildTraceChartSVG(times, series)}
      </div>
    </section>
  `;
}

function buildTraceChartSVG(times, series) {
  const width = 520;
  const height = 210;
  const margin = { top: 18, right: 18, bottom: 30, left: 44 };
  const chartWidth = width - margin.left - margin.right;
  const chartHeight = height - margin.top - margin.bottom;
  const xValues = times.filter((value) => Number.isFinite(value));
  const flatValues = series.flatMap((item) => item.values.filter((value) => Number.isFinite(value)));

  if (xValues.length === 0 || flatValues.length === 0) {
    return `<svg viewBox="0 0 ${width} ${height}" preserveAspectRatio="none"><text x="${width / 2}" y="${height / 2}" text-anchor="middle" class="chart-label">Trace data is unavailable.</text></svg>`;
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
  const parts = [
    `<svg viewBox="0 0 ${width} ${height}" preserveAspectRatio="none" role="img" aria-label="Simulink trace chart">`,
    `<rect x="0" y="0" width="${width}" height="${height}" rx="18" fill="rgba(255,255,255,0.36)"></rect>`,
  ];

  for (let index = 0; index <= 3; index += 1) {
    const ratio = index / 3;
    const x = margin.left + ratio * chartWidth;
    parts.push(`<line x1="${x}" y1="${margin.top}" x2="${x}" y2="${margin.top + chartHeight}" class="chart-grid"></line>`);
    parts.push(`<text x="${x}" y="${height - 10}" text-anchor="middle" class="chart-label">${escapeHTML(formatMetric(minX + ratio * xSpan))}</text>`);
  }

  for (let index = 0; index <= 3; index += 1) {
    const ratio = index / 3;
    const value = minY + ratio * ySpan;
    const y = margin.top + chartHeight - ((value - domainMinY) / domainYSpan) * chartHeight;
    parts.push(`<line x1="${margin.left}" y1="${y}" x2="${width - margin.right}" y2="${y}" class="chart-grid"></line>`);
    parts.push(`<text x="${margin.left - 8}" y="${y + 4}" text-anchor="end" class="chart-label">${escapeHTML(formatMetric(value))}</text>`);
  }

  parts.push(`<line x1="${margin.left}" y1="${margin.top + chartHeight}" x2="${width - margin.right}" y2="${margin.top + chartHeight}" class="chart-axis"></line>`);
  parts.push(`<line x1="${margin.left}" y1="${margin.top}" x2="${margin.left}" y2="${margin.top + chartHeight}" class="chart-axis"></line>`);

  for (const item of series) {
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
  }

  parts.push("</svg>");
  return parts.join("");
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
  if (state.runs.length === 0) {
    list.innerHTML = '<div class="empty-state">No matching remote runs are visible yet.</div>';
    return;
  }

  list.innerHTML = state.runs
    .map((run) => {
      const phaseClass = classifyPhase(run.phase);
      const resultType = classifySimulinkRun(run);
      const resultPill = resultType
        ? `<span class="result-kind-pill result-kind-${escapeHTML(resultType.kind)}">${escapeHTML(resultType.label)}</span>`
        : "";
      return `
        <article class="run-card">
          <div class="run-header">
            <div class="run-title-block">
              <h3>${escapeHTML(run.name)}</h3>
              ${resultPill}
            </div>
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
