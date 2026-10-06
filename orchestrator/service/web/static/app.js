const state = {
  config: null,
  workflows: [],
  runs: [],
  selectedWorkflowPath: "",
  selectedWorkflowModelIndex: null,
  // Model-card entry key: "<stepIndex>" for a step, "<stepIndex>.<member>" for a co-sim member.
  selectedModelKey: "",
  selectedRunName: "",
  selectedDemonstratorId: "portfolio",
  runsRailCollapsed: false,
  navTab: "matrix",
  modelMatrix: null,
  modelMatrixError: "",
  modelFocus: "",
  candidateFocus: "",
  consortiumFamilyCount: 0,
  simulinkResult: null,
  simulinkResultsCache: new Map(),
  aeStatsResult: null,
  aeStatsResultsCache: new Map(),
  genericResult: null,
  genericResultsCache: new Map(),
  runResultsInflight: new Set(),
  traceCharts: new Map(),
  hiddenTraceSeries: new Set(),
  runtimeProblems: [],
  pendingWorkflows: new Set(),
  poller: null,
  loadingRuns: false,
};

const SIMULINK_WORKFLOW_PATH = "workflows/tests/calculate_aecis.yaml";
const AE_STATS_WORKFLOW_PATH = "workflows/tests/ae_event_statistics.yaml";
const PYTHON_CHAIN_WORKFLOW_PATH = "workflows/tests/python_chain.yaml";
const CHEYLAS_RUNNER_RUL_PATH = "workflows/demonstrators/cheylas/maintenance/runner_rul_lockstep.yaml";
const CHEYLAS_SEDIMENT_EROSION_PATH = "workflows/demonstrators/cheylas/monitoring/sediment_erosion_events.yaml";
const ALQUEVA_BATTERY_EMS_PATH = "workflows/demonstrators/alqueva/hybrid/battery_ems_cosim.yaml";
// The demo scope: two demonstrators, three workflows (other sites have none).
const CHEYLAS_WORKFLOW_PATHS = [CHEYLAS_RUNNER_RUL_PATH, CHEYLAS_SEDIMENT_EROSION_PATH];
const ALQUEVA_WORKFLOW_PATHS = [ALQUEVA_BATTERY_EMS_PATH];
const CIVECTOR_LABELS = ["Mean", "RMS", "Peak-to-Peak", "Skewness", "Kurtosis"];
const SIMULINK_RESULT_RETRY_MS = 15_000;
const AECIS_TREND_WINDOW_SECONDS = 2.5;
const RUN_INFO_STEP = "_run";
const FINISHED_RUN_PHASES = ["succeeded", "failed", "error"];
const COSIM_MODEL_LABELS = { ems: "EMS", iot: "IoT", rul: "RUL" };
const SELECTED_WORKFLOW_STORAGE_KEY = "cads:selectedWorkflowPath";
const SELECTED_DEMONSTRATOR_STORAGE_KEY = "cads:selectedDemonstratorId";
const RUNS_RAIL_COLLAPSED_STORAGE_KEY = "cads:runsRailCollapsed";
// Navigation tabs above the runs rail and results (first is the default).
const NAV_TABS = ["matrix", "details", "map"];
const NAV_TAB_STORAGE_KEY = "cads:navTab";
const MODEL_MATRIX_URL = "/static/cads-model-matrix.json";
const SEQUENTIAL_COUPLING_LABEL = "Sequential one-way hand-over (final values between steps)";
// Fallback summary for a STOR-HY workflow without its own dashboard config.
const STORHY_DEFAULT_SUMMARY = ["rul_days", "damage_index", "net_benefit_eur", "benefit_cost_ratio", "status_code", "recommendation_code"];
// Tail step shared by the W1 and W3 workflows: the FMI 3 degradation-cost model (M14).
const DEGRADATION_COST_STEP = "degradation_cost";
const DEGRADATION_COST_SUMMARY = [
  { key: "net_benefit_eur", step: DEGRADATION_COST_STEP },
  { key: "benefit_cost_ratio", step: DEGRADATION_COST_STEP },
];
const DEGRADATION_COST_VALUES = {
  title: "Degradation Cost Versus Revenue",
  description: "Final values from the degradation-cost model.",
  values: [
    { key: "gross_revenue_eur", step: DEGRADATION_COST_STEP },
    { key: "degradation_cost_eur", step: DEGRADATION_COST_STEP },
    { key: "net_benefit_eur", step: DEGRADATION_COST_STEP },
  ],
};
const DEGRADATION_COST_DECISION = {
  status: { key: "status_code", step: DEGRADATION_COST_STEP },
  recommendation: { key: "recommendation_code", step: DEGRADATION_COST_STEP },
};
const STORHY_DASHBOARD_CONFIG = {
  [CHEYLAS_RUNNER_RUL_PATH]: {
    summary: [
      "rul.rul_days",
      "rul.damage_index",
      "iot.condition_indicator",
      ...DEGRADATION_COST_SUMMARY,
    ],
    ...DEGRADATION_COST_DECISION,
    charts: [
      {
        title: "Lock-step Inputs",
        description: "Jacobi lock-step: runner load and the IoT condition indicator are exchanged at the same communication point.",
        step: "runner_health",
        signals: ["runner.load_pu", "iot.condition_indicator"],
      },
      {
        title: "Stress And Vibration",
        description: "Runner stress amplitude and the vibration level observed by the IoT model.",
        step: "runner_health",
        signals: ["runner.stress_amplitude_mpa", "iot.vibration_rms_mm_s"],
      },
      {
        title: "Damage",
        description: "Accumulated damage index computed by the RUL model.",
        step: "runner_health",
        signals: ["rul.damage_index"],
      },
      {
        title: "Runner Remaining Life",
        description: "Remaining useful life from the coupled RUL model.",
        step: "runner_health",
        signals: ["rul.rul_days"],
      },
      {
        title: "Energy",
        description: "Energy produced by the runner over the simulated window.",
        step: "runner_health",
        signals: ["runner.energy_mwh"],
      },
    ],
    valueBlocks: [DEGRADATION_COST_VALUES],
  },
  [CHEYLAS_SEDIMENT_EROSION_PATH]: {
    summary: [
      "sediment.cleaning_count",
      "sediment.cumulative_cleaning_cost_eur",
      "rul.damage_index",
      "rul.rul_days",
      "events.high_exposure.count",
    ],
    status: { key: "rul.status_code" },
    charts: [
      {
        title: "Exposure Versus Threshold",
        description: "Sediment exposure against the event threshold; the event flag is 1 while the cleaning trigger is raised.",
        step: "sediment_erosion",
        signals: ["sediment.sediment_exposure", "events.high_exposure.active"],
        eventThreshold: { step: "sediment_erosion", event: "high_exposure" },
      },
      {
        title: "Sediment Concentration",
        description: "Suspended sediment concentration from the sediment model.",
        step: "sediment_erosion",
        signals: ["sediment.sediment_concentration_g_l"],
      },
      {
        title: "Event-driven Cleanings",
        description: "Cleanings triggered by the high-exposure event.",
        step: "sediment_erosion",
        signals: ["sediment.cleaning_count", "events.high_exposure.active"],
      },
      {
        title: "Erosion Damage",
        description: "Accumulated damage index from the coupled RUL model; sediment exposure is its condition input.",
        step: "sediment_erosion",
        signals: ["rul.damage_index"],
      },
      {
        title: "Remaining Useful Life",
        description: "Remaining useful life estimated by the RUL model as erosion damage accumulates.",
        step: "sediment_erosion",
        signals: ["rul.rul_days"],
      },
    ],
    valueBlocks: [],
  },
  [ALQUEVA_BATTERY_EMS_PATH]: {
    summary: [
      "battery.soh_percent",
      "battery.rul_days",
      "battery.cycle_count",
      "ems.revenue_eur",
      ...DEGRADATION_COST_SUMMARY,
    ],
    ...DEGRADATION_COST_DECISION,
    charts: [
      {
        title: "Battery SoC And SoH",
        description: "State of charge and state of health exchanged by the battery FMU at every communication point.",
        step: "battery_ems",
        signals: ["battery.soc_percent", "battery.soh_percent"],
      },
      {
        title: "EMS Set-point Versus Battery Power",
        description: "Gauss-Seidel ping-pong: the EMS set-point drives the battery, whose SoC feeds back into the EMS.",
        step: "battery_ems",
        signals: ["ems.power_setpoint_mw", "battery.power_actual_mw"],
      },
      {
        title: "Day-ahead Price",
        description: "Price seen by the EMS when it chooses to charge or discharge.",
        step: "battery_ems",
        signals: ["ems.price_eur_mwh"],
      },
      {
        title: "Cumulative EMS Revenue",
        description: "Revenue accumulated by the EMS over the simulated day.",
        step: "battery_ems",
        signals: ["ems.revenue_eur"],
      },
      {
        title: "Battery Remaining Life",
        description: "Battery RUL estimate as cycling accumulates.",
        step: "battery_ems",
        signals: ["battery.rul_days"],
      },
    ],
    valueBlocks: [DEGRADATION_COST_VALUES],
  },
};
// Full site data; applyDemoScope() trims it to matrix.demo_scope.sites at load,
// so re-enabling a site is a change to the matrix JSON only.
const DEMONSTRATORS = [
  {
    id: "portfolio",
    label: "Both demo sites",
    shortLabel: "All sites",
    location: "Cheylas (France) and Alqueva (Portugal)",
    operator: "STOR-HY consortium",
    country: "Europe",
    focus: "Cheylas and Alqueva: the two CADS demonstrator sites in the demo.",
    capacity: "Two CADS demonstrator sites in the demo",
    workflowPaths: [],
    facts: [
      "Le Cheylas: 500 MW pumped storage with sediment-laden fluid and frequent cycling",
      "Alqueva: 520 MW hybrid pumped storage with battery storage and floating PV",
      "One FMI 3.0 placeholder FMU per model family; lock-step, event-driven and ping-pong co-simulation",
    ],
  },
  {
    id: "vsmc",
    label: "VSMC dams",
    shortLabel: "VSMC",
    location: "Ain River, Bourgogne-Franche-Comte, France",
    operator: "EDF",
    country: "France",
    mapX: 76.95,
    mapY: 44.37,
    mapLabelX: 78.85,
    mapLabelY: 41.15,
    mapSubtitle: "Vouglans - Saut Mortier - Coiselet",
    focus: "Cascade optimisation and variable-speed tandem pumping.",
    capacity: "362 MW generation + 72 MW storage",
    workflowPaths: [],
    facts: [
      "Three reservoirs in cascade",
      "Three Francis turbines and one pump turbine",
      "Unconventional low-head tandem pumping scheme",
    ],
  },
  {
    id: "cheylas",
    label: "Le Cheylas power station",
    shortLabel: "Le Cheylas",
    location: "Isere Valley, Auvergne-Rhone-Alpes, France",
    operator: "EDF",
    country: "France",
    mapX: 78.14,
    mapY: 49.98,
    mapLabelX: 79.45,
    mapLabelY: 52.62,
    mapSubtitle: "Pumped-storage power station",
    focus: "Wear assessment and sensor-driven monitoring under high pump-turbine cycling.",
    capacity: "500 MW generation and storage",
    workflowPaths: CHEYLAS_WORKFLOW_PATHS,
    facts: [
      "Two reservoirs and two pump turbines",
      "Sediment-laden fluid and frequent cycling",
      "Runner RUL lock-step and sediment erosion workflows in the demo",
    ],
  },
  {
    id: "la-rance",
    label: "La Rance tidal power station",
    shortLabel: "La Rance",
    location: "La Rance river estuary, Brittany, France",
    operator: "EDF",
    country: "France",
    mapX: 49.17,
    mapY: 30.22,
    mapLabelX: 47.1,
    mapLabelY: 27.2,
    mapLabelAlign: "right",
    mapSubtitle: "Tidal power station",
    focus: "Saltwater operation, corrosion, anti-fouling, and low tidal-head cycling.",
    capacity: "240 MW generation",
    workflowPaths: [],
    facts: [
      "Large-scale tidal power station",
      "24 bulb turbines",
      "Harsh saltwater environment with 4-6 starts/stops per day",
    ],
  },
  {
    id: "alqueva",
    label: "Alqueva hydroelectric power station",
    shortLabel: "Alqueva",
    location: "Alqueva and Moura, Alentejo, Portugal",
    operator: "EDP",
    country: "Portugal",
    mapX: 29.41,
    mapY: 90.08,
    mapLabelX: 31.15,
    mapLabelY: 87.35,
    mapSubtitle: "Hybrid PSP / BESS / FPV",
    focus: "Operational management for a hybrid pumped-storage, battery, and floating PV plant.",
    capacity: "520 MW generation and storage",
    workflowPaths: ALQUEVA_WORKFLOW_PATHS,
    facts: [
      "Largest dam and artificial lake in Western Europe",
      "Four pump turbines",
      "Triple hybrid PSP with battery storage and floating photovoltaic generation",
    ],
  },
  {
    id: "vilarinho",
    label: "Vilarinho das Furnas dam",
    shortLabel: "Vilarinho",
    location: "Homem River, North Region, Portugal",
    operator: "EDP",
    country: "Portugal",
    mapX: 26.88,
    mapY: 70.7,
    mapLabelX: 24.65,
    mapLabelY: 68.0,
    mapLabelAlign: "right",
    mapSubtitle: "Dam and hydropower plant",
    focus: "Main inlet valve control, hydraulic short-circuit operation, and multistage pumping.",
    capacity: "146 MW generation + 70 MW storage",
    workflowPaths: [],
    facts: [
      "Two reservoirs",
      "One multistage pump and one Francis turbine",
      "Main inlet valve and hydraulic short-circuit operation",
    ],
  },
];

document.addEventListener("DOMContentLoaded", () => {
  window.addEventListener("resize", resizeECharts);
  window.addEventListener("load", () => initializeECharts(document));
  void initializeDashboard();
});

async function initializeDashboard() {
  try {
    state.selectedDemonstratorId = readPersistedDemonstratorId();
    state.runsRailCollapsed = readPersistedRunsRailCollapsed();
    state.navTab = readPersistedNavTab();
    bindNavTabs();
    renderNavChrome();
    const [config] = await Promise.all([fetchJSON("/api/config"), loadModelMatrix()]);
    state.config = config;
    applyDemoScope();
    renderConfigMeta();
    renderBanner();
    renderDemonstrators();

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
    renderDemonstrators();
    renderWorkflows();
    renderRuns();
    renderWorkflowOutput();
  }
}

// matrix.demo_scope.sites is the authoritative site list for the whole UI: the
// demonstrator list, matrix columns and rows are trimmed to it (portfolio stays).
function applyDemoScope() {
  const scope = Array.isArray(state.modelMatrix?.demo_scope?.sites) ? state.modelMatrix.demo_scope.sites : [];
  if (scope.length === 0) {
    return;
  }
  for (let index = DEMONSTRATORS.length - 1; index >= 0; index -= 1) {
    if (DEMONSTRATORS[index].id !== "portfolio" && !scope.includes(DEMONSTRATORS[index].id)) {
      DEMONSTRATORS.splice(index, 1);
    }
  }
  const matrix = state.modelMatrix;
  state.consortiumFamilyCount = matrix.models.length;
  matrix.sites = (matrix.sites || []).filter((site) => scope.includes(site.id));
  matrix.models = matrix.models.filter((model) =>
    scope.some((siteId) => model.sites && model.sites[siteId]));
  ensureSelectedDemonstrator();
}

function scopedSiteIds() {
  return DEMONSTRATORS.filter((demo) => demo.id !== "portfolio").map((demo) => demo.id);
}

// The model-by-demonstrator matrix is optional: when it fails to load the
// dashboard still works, the matrix pane just explains what is missing.
async function loadModelMatrix() {
  try {
    const matrix = await fetchJSON(MODEL_MATRIX_URL);
    state.modelMatrix = matrix && Array.isArray(matrix.models) ? matrix : null;
    state.modelMatrixError = state.modelMatrix ? "" : "Model matrix file has no models.";
  } catch (error) {
    state.modelMatrix = null;
    state.modelMatrixError = error.message;
  }
}

async function loadWorkflows() {
  try {
    state.workflows = await fetchJSON("/api/workflows");
  } catch (error) {
    state.runtimeProblems = [error.message];
  }
  ensureSelectedDemonstrator();
  ensureSelectedWorkflow();
  renderBanner();
  renderDemonstrators();
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
    banner.innerHTML = "<strong>Loading dashboard…</strong>";
    return;
  }

  if (problems.length === 0) {
    banner.className = "status-banner status-ready";
    const version = state.config.version ? ` Dashboard ${escapeHTML(state.config.version)}.` : "";
    banner.innerHTML = `<strong>Ready.</strong>Select a demonstrator below to inspect its workflows and recent outputs.${version}`;
    return;
  }

  banner.className = "status-banner status-degraded";
  banner.innerHTML = `<strong>Dashboard needs attention.</strong>${problems.map((problem) => escapeHTML(problem)).join("<br>")}`;
}

function renderConfigMeta() {
  const container = document.getElementById("configMeta");
  if (!container) {
    return;
  }
  container.innerHTML = "";
}

function ensureSelectedDemonstrator() {
  if (!DEMONSTRATORS.some((demo) => demo.id === state.selectedDemonstratorId)) {
    state.selectedDemonstratorId = "portfolio";
  }
}

function ensureSelectedWorkflow() {
  const candidates = visibleWorkflows();
  const demo = selectedDemonstrator();
  if (candidates.length === 0) {
    state.selectedWorkflowPath = "";
    state.selectedWorkflowModelIndex = null;
    return;
  }

  if (candidates.some((workflow) => workflow.path === state.selectedWorkflowPath)) {
    return;
  }

  const savedPath = readPersistedWorkflowPath();
  if (savedPath && candidates.some((workflow) => workflow.path === savedPath)) {
    state.selectedWorkflowPath = savedPath;
    state.selectedWorkflowModelIndex = null;
    return;
  }

  const preferredPath =
    candidates.find((workflow) => demo.id !== "portfolio" && workflowSiteId(workflow) === demo.id)?.path ||
    candidates.find((workflow) => workflow.path === PYTHON_CHAIN_WORKFLOW_PATH)?.path ||
    candidates[0]?.path ||
    "";
  state.selectedWorkflowPath = preferredPath;
  state.selectedWorkflowModelIndex = null;
}

function selectedWorkflow() {
  return state.workflows.find((workflow) => workflow.path === state.selectedWorkflowPath) || null;
}

function workflowByPath(workflowPath) {
  return state.workflows.find((workflow) => workflow.path === workflowPath) || null;
}

function selectedDemonstrator() {
  return DEMONSTRATORS.find((demo) => demo.id === state.selectedDemonstratorId) || DEMONSTRATORS[0];
}

function workflowSiteId(workflow) {
  return String(workflow?.metadata?.siteId || "").trim();
}

function workflowCategory(workflow) {
  return String(workflow?.metadata?.category || "").trim();
}

function workflowResultFamily(workflow) {
  return String(workflow?.metadata?.resultFamily || "").trim();
}

function workflowsForDemonstrator(demo) {
  if (!demo || demo.id === "portfolio") {
    // Shared workflows (no site or "portfolio") plus those of the scoped sites.
    const sites = new Set(scopedSiteIds());
    return state.workflows.filter((workflow) => {
      const siteId = workflowSiteId(workflow);
      return !siteId || siteId === "portfolio" || sites.has(siteId);
    });
  }

  const allowed = new Set(demo.workflowPaths || []);
  return state.workflows.filter((workflow) => allowed.has(workflow.path) || workflowSiteId(workflow) === demo.id);
}

function visibleWorkflows() {
  return workflowsForDemonstrator(selectedDemonstrator());
}

function workflowLabel(workflow) {
  return String(workflow?.metadata?.displayName || workflow?.name || workflow?.path || "workflow").replaceAll("_", " ");
}

function workflowDescription(workflow) {
  if (!workflow) {
    return "";
  }
  if (workflow.metadata?.description) {
    return workflow.metadata.description;
  }
  if (workflow.path === AE_STATS_WORKFLOW_PATH) {
    return "Compares edge-computed acoustic-emission event statistics for CH2 and CH6 from the emailed CSV tables.";
  }
  if (workflow.path === SIMULINK_WORKFLOW_PATH) {
    return "Runs the AECIS FMU and displays rolling mean, RMS, and input-signal traces from the latest result.";
  }
  if (workflow.path === PYTHON_CHAIN_WORKFLOW_PATH) {
    return "Runs the bundled Producer and Consumer Python FMUs as a simple chained workflow smoke test.";
  }
  return workflow.path || "";
}

function workflowMaxRuntimeLabel(workflow) {
  const seconds = Number(workflow?.metadata?.limits?.maxRuntimeSeconds || state.config?.maxRuntimeSeconds || 0);
  return seconds > 0 ? `max runtime ${formatDuration(seconds)}` : "";
}

function workflowModels(workflow) {
  return Array.isArray(workflow?.models)
    ? workflow.models.filter((model) => model && (model.name || model.label || model.fmu))
    : [];
}

function renderWorkflowModelOverview(workflow) {
  const models = workflowModels(workflow);
  if (models.length === 0) {
    return "";
  }

  const selectedIndex = Number.isInteger(state.selectedWorkflowModelIndex) &&
    state.selectedWorkflowModelIndex >= 0 &&
    state.selectedWorkflowModelIndex < models.length
      ? state.selectedWorkflowModelIndex
      : null;

  return `
    <div class="workflow-model-overview">
      <div class="workflow-model-section-title">Coupling</div>
      <div class="workflow-model-chain" aria-label="Workflow model sequence">
        ${models.map((model, index) => `
          ${isCosimModel(model) ? renderCosimGroup(model, index, selectedIndex === index) : `
          <button
            class="workflow-model-node${selectedIndex === index ? " selected" : ""}"
            type="button"
            aria-pressed="${selectedIndex === index ? "true" : "false"}"
            aria-label="Show ${escapeHTML(workflowModelLabel(model))} details"
            data-select-workflow-model="${index}"
          >
            <span class="workflow-model-index">${index + 1}</span>
            <span>${escapeHTML(workflowModelLabel(model))}</span>
          </button>
          `}
          ${index < models.length - 1 ? '<span class="workflow-model-arrow" aria-hidden="true">&rarr;</span>' : ""}
        `).join("")}
      </div>
    </div>
  `;
}

function toggleWorkflowModel(index) {
  if (!Number.isInteger(index) || index < 0 || index >= workflowModels(selectedWorkflow()).length) {
    return;
  }

  state.selectedWorkflowModelIndex = state.selectedWorkflowModelIndex === index ? null : index;
  state.selectedModelKey = state.selectedWorkflowModelIndex === null ? "" : String(index);
  renderWorkflows();
}

function isCosimModel(model) {
  return model?.kind === "cosim" && Boolean(model.cosim);
}

function renderWorkflowModelProblems(model) {
  const problems = Array.isArray(model?.problems) ? model.problems.filter(Boolean) : [];
  if (problems.length === 0) {
    return "";
  }
  return `
    <ul class="workflow-model-problems">
      ${problems.map((problem) => `<li>${escapeHTML(problem)}</li>`).join("")}
    </ul>
  `;
}

function cosimSchemeLabel(scheme) {
  switch (String(scheme || "").toLowerCase()) {
    case "gauss_seidel":
      return "Gauss-Seidel (ping-pong)";
    case "jacobi":
      return "Jacobi (lock-step)";
    default:
      return scheme ? formatWorkflowCategory(scheme) : "Co-simulation";
  }
}

function cosimVarModel(ref) {
  return String(ref || "").split(".")[0];
}

// Collapses directed FMU connections into model-level edges; a pair connected
// in both directions becomes one bidirectional edge (rendered "a <-> b").
function cosimEdges(cosim) {
  const connections = Array.isArray(cosim?.connections) ? cosim.connections : [];
  const directed = new Map();
  for (const connection of connections) {
    const from = connection?.fromModel || cosimVarModel(connection?.from);
    const to = connection?.toModel || cosimVarModel(connection?.to);
    if (!from || !to) {
      continue;
    }
    const key = `${from}\u0000${to}`;
    if (!directed.has(key)) {
      directed.set(key, { from, to, connections: [] });
    }
    directed.get(key).connections.push(connection);
  }

  const edges = [];
  const consumed = new Set();
  for (const [key, edge] of directed) {
    if (consumed.has(key)) {
      continue;
    }
    consumed.add(key);
    const reverseKey = `${edge.to}\u0000${edge.from}`;
    const reverse = edge.from !== edge.to ? directed.get(reverseKey) : null;
    if (reverse) {
      consumed.add(reverseKey);
    }
    edges.push({
      from: edge.from,
      to: edge.to,
      bidirectional: Boolean(reverse),
      count: edge.connections.length + (reverse ? reverse.connections.length : 0),
    });
  }
  return edges;
}

function cosimPatterns(cosim) {
  if (Array.isArray(cosim?.patterns) && cosim.patterns.length > 0) {
    return cosim.patterns.map((pattern) => formatWorkflowCategory(pattern)).filter(Boolean);
  }
  const edges = cosimEdges(cosim);
  const scheme = String(cosim?.scheme || "").toLowerCase();
  const patterns = [];
  if (scheme === "gauss_seidel" && edges.some((edge) => edge.bidirectional)) {
    patterns.push("sequential ping-pong");
  }
  if (scheme === "jacobi") {
    patterns.push("parallel lock-step");
  }
  if (edges.some((edge) => !edge.bidirectional)) {
    patterns.push("one-way");
  }
  if (Array.isArray(cosim?.events) && cosim.events.length > 0) {
    patterns.push("event-driven");
  }
  return patterns;
}

function cosimEventText(event) {
  const name = event?.name || "event";
  const when = event?.when ? ` when ${event.when}` : "";
  const target = event?.set ? ` sets ${event.set}` : "";
  const mode = event?.mode ? ` (${event.mode})` : "";
  return `event: ${name}${when}${target}${mode}`;
}

function renderCosimGroup(model, index, isSelected) {
  const cosim = model.cosim || {};
  const members = Array.isArray(cosim.models) ? cosim.models : [];
  const edges = cosimEdges(cosim);
  const events = Array.isArray(cosim.events) ? cosim.events : [];
  const patterns = cosimPatterns(cosim);
  const label = workflowModelLabel(model);
  return `
    <div class="workflow-cosim-group${isSelected ? " selected" : ""}" role="group" aria-label="${escapeHTML(label)} co-simulation">
      <div class="workflow-cosim-head">
        <button
          class="workflow-model-node${isSelected ? " selected" : ""}"
          type="button"
          aria-pressed="${isSelected ? "true" : "false"}"
          aria-label="Show ${escapeHTML(label)} details"
          data-select-workflow-model="${index}"
        >
          <span class="workflow-model-index">${index + 1}</span>
          <span>${escapeHTML(label)}</span>
        </button>
        <span class="cosim-scheme-badge ${couplingPatternClass(cosimSchemeBadge(cosim.scheme))}">${escapeHTML(cosimSchemeLabel(cosim.scheme))}</span>
      </div>
      ${members.length > 0 ? `
        <div class="cosim-member-row">
          ${members.map((member) => {
            const key = `${index}.${member.name}`;
            const memberSelected = isSelected && state.selectedModelKey === key;
            return `<button type="button" class="cosim-model-pill${memberSelected ? " selected" : ""}" data-model-entry="${escapeHTML(key)}" aria-pressed="${memberSelected ? "true" : "false"}">${escapeHTML(member.label || member.name || "model")}</button>`;
          }).join("")}
        </div>
      ` : ""}
      ${edges.length > 0 ? `
        <ul class="cosim-edge-list" aria-label="Coupling">
          ${edges.map((edge) => `<li><code>${escapeHTML(`${edge.from} ${edge.bidirectional ? "<->" : "->"} ${edge.to}`)}</code></li>`).join("")}
        </ul>
      ` : ""}
      ${events.map((event) => `<div class="cosim-event-line">${escapeHTML(cosimEventText(event))}</div>`).join("")}
      ${patterns.length > 0 ? `<div class="cosim-pattern-line">${escapeHTML(patterns.join(" | "))}</div>` : ""}
      ${Array.isArray(model.problems) && model.problems.length > 0 ? '<div class="cosim-problem-flag">Invalid co-simulation spec, see details</div>' : ""}
    </div>
  `;
}

function cosimMemberInputs(member) {
  const labels = [];
  for (const input of Array.isArray(member?.inputs) ? member.inputs : []) {
    if (typeof input === "string") {
      labels.push(input);
    } else if (input && (input.name || input.source)) {
      labels.push(input.source && input.name ? `${input.source} -> ${input.name}` : input.name || input.source);
    }
  }
  if (member?.inputSeries) {
    labels.push(member.inputSeries);
  }
  return labels;
}

function cosimMemberParameters(member) {
  const parameters = member?.parameters;
  if (Array.isArray(parameters)) {
    return parameters;
  }
  if (parameters && typeof parameters === "object") {
    return Object.entries(parameters).map(([key, value]) => `${key}=${value}`);
  }
  return [];
}

function renderCosimStepCard(model, index) {
  const cosim = model.cosim || {};
  const members = Array.isArray(cosim.models) ? cosim.models : [];
  const connections = Array.isArray(cosim.connections) ? cosim.connections : [];
  const events = Array.isArray(cosim.events) ? cosim.events : [];
  const startInputs = workflowStartInputLabels(model, index);
  const timing = [
    cosimSchemeLabel(cosim.scheme),
    Number.isFinite(Number(cosim.stopTime)) && cosim.stopTime !== undefined
      ? `${formatSimDuration(cosim.startTime || 0)} to ${formatSimDuration(cosim.stopTime)}`
      : "",
    Number(cosim.communicationStep) > 0 ? `H = ${formatSimDuration(cosim.communicationStep)}` : "",
  ].filter(Boolean).join(" | ");
  return `
    <article class="workflow-model-card workflow-cosim-card">
      <h4>
        <span>${index + 1}. ${escapeHTML(workflowModelLabel(model))}</span>
        <code>${escapeHTML(timing)}</code>
      </h4>
      ${members.length > 0 ? `
        <div class="workflow-model-row">
          <span>Models</span>
          <div class="cosim-table-wrap">
            <table class="cosim-model-table">
              <thead><tr><th>Model</th><th>FMU</th><th>Parameters</th><th>Inputs</th></tr></thead>
              <tbody>
                ${members.map((member) => `
                  <tr>
                    <td><strong>${escapeHTML(member.label || member.name || "model")}</strong></td>
                    <td><code>${escapeHTML(String(member.fmu || "n/a").split("/").pop())}</code></td>
                    <td>${renderWorkflowChips(cosimMemberParameters(member), 4)}</td>
                    <td>${renderWorkflowChips(cosimMemberInputs(member), 4)}</td>
                  </tr>
                `).join("")}
              </tbody>
            </table>
          </div>
        </div>
      ` : ""}
      <div class="workflow-model-row">
        <span>Connections</span>
        ${connections.length > 0 ? `
          <ul class="cosim-connection-list">
            ${connections.map((connection) => `<li><code>${escapeHTML(`${connection.from || ""} -> ${connection.to || ""}`)}</code></li>`).join("")}
          </ul>
        ` : '<span class="workflow-model-muted">none (independent models)</span>'}
      </div>
      ${events.length > 0 ? `
        <div class="workflow-model-row">
          <span>Events</span>
          <ul class="cosim-connection-list">
            ${events.map((event) => `<li><code>${escapeHTML(cosimEventText(event))}</code></li>`).join("")}
          </ul>
        </div>
      ` : ""}
      ${startInputs.length > 0 ? `
        <div class="workflow-model-row">
          <span>Step inputs</span>
          <div class="workflow-model-chip-row">${renderWorkflowChips(startInputs, 6)}</div>
        </div>
      ` : ""}
      <div class="workflow-model-row">
        <span>Outputs</span>
        <div class="workflow-model-chip-row">${renderWorkflowChips(model.outputs, 8)}</div>
      </div>
      ${renderWorkflowModelProblems(model)}
    </article>
  `;
}


function workflowStartInputLabels(model, index) {
  const inputs = Array.isArray(model.inputs) ? model.inputs : [];
  if (inputs.length > 0) {
    return inputs
      .map((input) => {
        if (input?.sourceStep && input?.sourceOutput && input?.name) {
          return `${input.sourceStep}.${input.sourceOutput} -> ${input.name}`;
        }
        return input?.name || input?.source;
      })
      .filter(Boolean);
  }

  const labels = [];
  if (model.inputSeries) {
    labels.push(model.inputSeries);
  }
  if (Array.isArray(model.parameters) && model.parameters.length > 0) {
    labels.push(...model.parameters);
  }
  if (labels.length === 0 && index === 0) {
    labels.push("workflow start");
  }
  return labels;
}

function renderWorkflowChips(values, limit) {
  const items = Array.isArray(values) ? values.filter(Boolean) : [];
  if (items.length === 0) {
    return '<span class="workflow-model-muted">n/a</span>';
  }
  const shown = items.slice(0, limit);
  const hiddenCount = items.length - shown.length;
  return `
    ${shown.map((item) => `<code class="workflow-model-chip">${escapeHTML(item)}</code>`).join("")}
    ${hiddenCount > 0 ? `<span class="workflow-model-more">+${hiddenCount} more</span>` : ""}
  `;
}

function workflowModelLabel(model) {
  return String(model?.label || model?.name || model?.fmu || "model").replaceAll("_", " ");
}

function formatWorkflowCategory(category) {
  return String(category || "")
    .replaceAll("_", " ")
    .replaceAll("-", " ")
    .trim();
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

function readPersistedDemonstratorId() {
  try {
    const saved = window.localStorage?.getItem(SELECTED_DEMONSTRATOR_STORAGE_KEY) || "portfolio";
    return DEMONSTRATORS.some((demo) => demo.id === saved) ? saved : "portfolio";
  } catch (_error) {
    return "portfolio";
  }
}

function persistSelectedDemonstratorId(demonstratorId) {
  try {
    window.localStorage?.setItem(SELECTED_DEMONSTRATOR_STORAGE_KEY, demonstratorId);
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

function readPersistedNavTab() {
  try {
    const saved = window.localStorage?.getItem(NAV_TAB_STORAGE_KEY) || "";
    return NAV_TABS.includes(saved) ? saved : NAV_TABS[0];
  } catch (_error) {
    return NAV_TABS[0];
  }
}

function persistNavTab(tab) {
  try {
    window.localStorage?.setItem(NAV_TAB_STORAGE_KEY, tab);
  } catch (_error) {
    // Local storage can be unavailable in private or embedded browser contexts.
  }
}

function setNavTab(tab, options = {}) {
  if (!NAV_TABS.includes(tab)) {
    return;
  }
  state.navTab = tab;
  persistNavTab(tab);
  renderNavChrome();
  if (options.focus) {
    document.querySelector(`#navTabs [data-nav-tab="${tab}"]`)?.focus();
  }
}

// Nav tabs live in #navTabs and use data-nav-tab hooks; the workflow picker
// tabs in #workflowGrid keep their own [role='tab'] buttons.
function bindNavTabs() {
  const tablist = document.getElementById("navTabs");
  if (!tablist) {
    return;
  }
  for (const button of tablist.querySelectorAll("[data-nav-tab]")) {
    button.addEventListener("click", () => setNavTab(button.dataset.navTab));
  }
  tablist.addEventListener("keydown", (event) => {
    const index = NAV_TABS.indexOf(state.navTab);
    let next = -1;
    if (event.key === "ArrowRight") {
      next = (index + 1) % NAV_TABS.length;
    } else if (event.key === "ArrowLeft") {
      next = (index - 1 + NAV_TABS.length) % NAV_TABS.length;
    } else if (event.key === "Home") {
      next = 0;
    } else if (event.key === "End") {
      next = NAV_TABS.length - 1;
    }
    if (next >= 0) {
      event.preventDefault();
      setNavTab(NAV_TABS[next], { focus: true });
    }
  });
}

function setRunsRailCollapsed(collapsed) {
  state.runsRailCollapsed = Boolean(collapsed);
  persistRunsRailCollapsed(state.runsRailCollapsed);
  renderRuns();
}

function selectDemonstrator(demonstratorId, options = {}) {
  if (!DEMONSTRATORS.some((demo) => demo.id === demonstratorId)) {
    return;
  }

  const changed = state.selectedDemonstratorId !== demonstratorId;
  state.selectedDemonstratorId = demonstratorId;
  if (changed) {
    state.selectedWorkflowModelIndex = null;
  }
  persistSelectedDemonstratorId(demonstratorId);
  ensureSelectedWorkflow();
  renderDemonstrators();
  renderWorkflows();
  renderRuns();
  renderWorkflowOutput();

  if (options.loadResult !== false && state.config?.remoteEnabled && state.selectedWorkflowPath) {
    void loadSelectedWorkflowResult().then(() => {
      renderRuns();
      renderWorkflowOutput();
    });
  }
}

function selectWorkflow(workflowPath, options = {}) {
  if (!workflowPath || !visibleWorkflows().some((workflow) => workflow.path === workflowPath)) {
    return;
  }

  const changed = state.selectedWorkflowPath !== workflowPath;
  state.selectedWorkflowPath = workflowPath;
  if (changed) {
    state.selectedRunName = "";
    state.selectedWorkflowModelIndex = null;
  }
  persistSelectedWorkflowPath(workflowPath);
  renderDemonstrators();
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

function isFinishedRunPhase(phase) {
  return FINISHED_RUN_PHASES.includes(String(phase || "").toLowerCase());
}

// Succeeded, failed, and errored runs all publish (possibly partial) results
// plus the _run provenance block, so the output panel follows the latest one.
function finishedSelectedWorkflowRuns() {
  return selectedWorkflowRuns().filter((run) => isFinishedRunPhase(run.phase));
}

function renderDemonstrators() {
  const map = document.getElementById("demonstratorMap");
  if (!map) {
    return;
  }

  const selected = selectedDemonstrator();
  renderModelMatrix();
  renderNavChrome();
  const demonstratorsWithLocations = DEMONSTRATORS.filter((demo) => Number.isFinite(demo.mapX) && Number.isFinite(demo.mapY));
  map.innerHTML = `
    <div class="demo-map-canvas" role="img" aria-label="Clickable map of STOR-HY demonstrator locations">
      <div class="demo-map-layer">
        <img class="demo-map-image" src="/static/storhy-demonstrators-map.png" alt="">
        ${demonstratorsWithLocations.map((demo) => renderDemoMapLabel(demo, selected.id === demo.id)).join("")}
        ${demonstratorsWithLocations.map((demo) => renderDemoMarker(demo, selected.id === demo.id)).join("")}
      </div>
    </div>
  `;

  for (const button of map.querySelectorAll("[data-demo-id]")) {
    button.addEventListener("click", () => {
      selectDemonstrator(button.dataset.demoId || "portfolio");
    });
  }
}

function renderNavChrome() {
  for (const tab of NAV_TABS) {
    const active = state.navTab === tab;
    const button = document.querySelector(`#navTabs [data-nav-tab="${tab}"]`);
    if (button) {
      button.classList.toggle("selected", active);
      button.setAttribute("aria-selected", active ? "true" : "false");
      button.tabIndex = active ? 0 : -1;
    }
    const panel = document.querySelector(`[data-nav-panel="${tab}"]`);
    if (panel) {
      panel.hidden = !active;
    }
  }
  const demo = selectedDemonstrator();
  const chip = document.getElementById("navSiteChip");
  if (chip) {
    chip.textContent = demo.id === "portfolio" ? "All sites" : demo.shortLabel || demo.label;
    chip.classList.toggle("all-sites", demo.id === "portfolio");
  }
  const allSites = document.getElementById("matrixAllSites");
  if (allSites) {
    allSites.hidden = demo.id === "portfolio";
    allSites.onclick = () => selectDemonstrator("portfolio");
  }
}

function renderDemoMapLabel(demo, isSelected) {
  const position = demonstratorMapLabelPosition(demo);
  const alignClass = demo.mapLabelAlign === "right" ? " align-right" : "";
  const subtitle = demo.mapSubtitle ? `<span class="demo-map-label-subtitle">${escapeHTML(demo.mapSubtitle)}</span>` : "";
  return `
    <button
      class="demo-map-label${alignClass}${isSelected ? " selected" : ""}"
      type="button"
      style="--x:${position.x}%; --y:${position.y}%"
      data-demo-id="${escapeHTML(demo.id)}"
      aria-label="Show ${escapeHTML(demo.label)} workflows"
    >
      <span class="demo-map-label-name">${escapeHTML(demo.shortLabel || demo.label)}</span>
      ${subtitle}
    </button>
  `;
}

function renderDemoMarker(demo, isSelected) {
  const position = demonstratorMapPosition(demo);
  return `
    <button
      class="demo-marker${isSelected ? " selected" : ""}"
      type="button"
      style="--x:${position.x}%; --y:${position.y}%"
      data-demo-id="${escapeHTML(demo.id)}"
      aria-label="Show ${escapeHTML(demo.label)} workflows"
    ></button>
  `;
}

function demonstratorMapPosition(demo) {
  return {
    x: clampNumber(demo.mapX, 0, 100),
    y: clampNumber(demo.mapY, 0, 100),
  };
}

function demonstratorMapLabelPosition(demo) {
  return {
    x: clampNumber(Number.isFinite(demo.mapLabelX) ? demo.mapLabelX : demo.mapX, 0, 100),
    y: clampNumber(Number.isFinite(demo.mapLabelY) ? demo.mapLabelY : demo.mapY, 0, 100),
  };
}

// ---------------------------------------------------------------------------
// Model-by-demonstrator matrix (Task 3.3). Static data from
// cads-model-matrix.json; the matrix is the primary site navigation and maps
// repo demo workflows onto the consortium's candidate workflows (W1..W7).
// ---------------------------------------------------------------------------

function matrixModels() {
  return Array.isArray(state.modelMatrix?.models) ? state.modelMatrix.models : [];
}

function matrixSites() {
  return Array.isArray(state.modelMatrix?.sites) ? state.modelMatrix.sites : [];
}

function matrixCandidateWorkflows() {
  return Array.isArray(state.modelMatrix?.workflows) ? state.modelMatrix.workflows : [];
}

function matrixModelById(modelId) {
  return matrixModels().find((model) => model.id === modelId) || null;
}

function matrixCandidateById(candidateId) {
  return matrixCandidateWorkflows().find((candidate) => candidate.id === candidateId) || null;
}

function matrixCategoryLabel(categoryId) {
  const categories = Array.isArray(state.modelMatrix?.categories) ? state.modelMatrix.categories : [];
  return categories.find((category) => category.id === categoryId)?.label || formatWorkflowCategory(categoryId);
}

function matrixCategoryClass(categoryId) {
  const slug = String(categoryId || "").toLowerCase().replace(/[^a-z0-9-]/g, "");
  return slug ? `cat-${slug}` : "cat-unknown";
}

// Draft mapping of a repo workflow onto a candidate workflow and model families.
function workflowMatrixMapping(workflow) {
  const mapping = workflow ? state.modelMatrix?.demo_workflows?.[workflow.path] : null;
  if (!mapping) {
    return null;
  }
  return {
    candidate: String(mapping.candidate || ""),
    models: Array.isArray(mapping.models) ? mapping.models.map(String) : [],
  };
}

function workflowMatrixLabel(workflow) {
  const mapping = workflowMatrixMapping(workflow);
  if (!mapping) {
    return "";
  }
  const models = mapping.models.join(", ");
  return [mapping.candidate, models].filter(Boolean).join(" · ");
}

function hasMatrixFocus() {
  return Boolean(state.modelFocus || state.candidateFocus);
}

function workflowMatchesMatrixFocus(workflow) {
  const mapping = workflowMatrixMapping(workflow);
  if (!mapping) {
    return false;
  }
  if (state.modelFocus && !mapping.models.includes(state.modelFocus)) {
    return false;
  }
  if (state.candidateFocus && mapping.candidate !== state.candidateFocus) {
    return false;
  }
  return true;
}

function setModelFocus(modelId) {
  state.modelFocus = modelId && matrixModelById(modelId) ? modelId : "";
  renderDemonstrators();
  renderWorkflows();
  revealFirstFocusedWorkflowTab();
}

function setCandidateFocus(candidateId) {
  state.candidateFocus = candidateId && matrixCandidateById(candidateId) ? candidateId : "";
  renderDemonstrators();
  renderWorkflows();
  revealFirstFocusedWorkflowTab();
}

// The workflow list scrolls inside its card; bring the first highlighted tab into view.
function revealFirstFocusedWorkflowTab() {
  const grid = document.getElementById("workflowGrid");
  const tab = grid?.querySelector(".workflow-tab.focus-match");
  if (!grid || !tab) {
    return;
  }
  const offset = tab.getBoundingClientRect().top - grid.getBoundingClientRect().top;
  grid.scrollTop = Math.max(0, grid.scrollTop + offset - 8);
}

function selectMatrixCell(siteId, modelId) {
  const sameCell = state.modelFocus === modelId && state.selectedDemonstratorId === siteId;
  state.modelFocus = sameCell ? "" : modelId;
  if (state.selectedDemonstratorId === siteId) {
    renderDemonstrators();
    renderWorkflows();
  } else {
    selectDemonstrator(siteId);
  }
  revealFirstFocusedWorkflowTab();
}

function renderModelMatrix() {
  const container = document.getElementById("modelMatrix");
  if (!container) {
    return;
  }
  if (!state.modelMatrix) {
    container.innerHTML = `
      <div class="empty-state">Model matrix unavailable${state.modelMatrixError ? `: ${escapeHTML(state.modelMatrixError)}` : ""}.</div>
    `;
    return;
  }

  const selectedId = selectedDemonstrator().id;
  const sites = matrixSites();
  const models = matrixModels();
  const legend = state.modelMatrix.legend || {};
  const selectedMapping = workflowMatrixMapping(selectedWorkflow());
  const usedModels = new Set(selectedMapping?.models || []);
  const categories = Array.isArray(state.modelMatrix.categories) ? state.modelMatrix.categories : [];

  container.innerHTML = `
    ${renderDemoScopeNote()}
    <div class="model-matrix-legend" aria-label="Matrix legend">
      <span><b class="mm-mark identified" aria-hidden="true">&#9679;</b> ${escapeHTML(legend.identified || "identified")}</span>
      <span><b class="mm-mark candidate" aria-hidden="true">&#9675;</b> ${escapeHTML(legend.candidate || "candidate or to be decided")}</span>
      <span class="mm-legend-sep" aria-hidden="true"></span>
      ${categories.map((category) => `
        <span class="mm-cat-legend ${matrixCategoryClass(category.id)}"><i aria-hidden="true"></i>${escapeHTML(category.label || category.id)}</span>
      `).join("")}
      ${selectedMapping ? '<span class="mm-used-legend"><i aria-hidden="true"></i>used by selected workflow</span>' : ""}
    </div>
    <div class="mm-layout">
    <div class="model-matrix-scroll">
      <table class="model-matrix">
        <thead>
          <tr>
            <th scope="col" class="mm-model-col">Model family</th>
            ${sites.map((site) => renderMatrixSiteHeader(site, site.id === selectedId)).join("")}
            <th scope="col" class="mm-w-col">Workflows</th>
          </tr>
        </thead>
        <tbody>
          ${models.map((model) => renderMatrixModelRow(model, sites, selectedId, usedModels.has(model.id))).join("")}
        </tbody>
      </table>
    </div>
    ${renderCandidateWorkflowStrip()}
    </div>
    ${renderCouplingLegend()}
    ${state.modelMatrix.source ? `<p class="model-matrix-source">Source: ${escapeHTML(state.modelMatrix.source)}</p>` : ""}
  `;

  for (const button of container.querySelectorAll("[data-matrix-site]")) {
    button.addEventListener("click", () => {
      const siteId = button.dataset.matrixSite || "portfolio";
      const modelId = button.dataset.matrixModel || "";
      if (modelId) {
        selectMatrixCell(siteId, modelId);
      } else {
        selectDemonstrator(siteId);
      }
    });
  }
  for (const button of container.querySelectorAll("[data-candidate-focus]")) {
    button.addEventListener("click", () => {
      const candidateId = button.dataset.candidateFocus || "";
      setCandidateFocus(state.candidateFocus === candidateId ? "" : candidateId);
    });
  }
}

function renderDemoScopeNote() {
  if (!Array.isArray(state.modelMatrix?.demo_scope?.sites)) {
    return "";
  }
  const names = matrixSites().map((site) => site.label || site.id);
  const total = state.consortiumFamilyCount || matrixModels().length;
  return `<p class="demo-scope-note">${escapeHTML(`Demo scope: ${names.join(" and ")}, one FMU per model family. The full consortium matrix lists ${total} families across six demonstrators.`)}</p>`;
}

function renderMatrixSiteHeader(site, isSelected) {
  const demo = DEMONSTRATORS.find((entry) => entry.id === site.id);
  const count = demo ? workflowsForDemonstrator(demo).length : 0;
  return `
    <th scope="col" class="mm-site-col${isSelected ? " selected" : ""}">
      <button
        class="mm-site-button${isSelected ? " selected" : ""}"
        type="button"
        data-matrix-site="${escapeHTML(site.id)}"
        aria-pressed="${isSelected ? "true" : "false"}"
        title="Show ${escapeHTML(demo?.label || site.label)} workflows"
      >
        <span>${escapeHTML(site.label || site.id)}</span>
        <small>${count} workflow${count === 1 ? "" : "s"}</small>
      </button>
    </th>
  `;
}

function renderMatrixModelRow(model, sites, selectedId, usedBySelectedWorkflow) {
  const siteMarks = model.sites && typeof model.sites === "object" ? model.sites : {};
  const hasSites = sites.some((site) => siteMarks[site.id]);
  const demoFmus = Array.isArray(model.demo_fmus) ? model.demo_fmus.filter(Boolean) : [];
  const owners = Array.isArray(model.owners) ? model.owners.filter(Boolean) : [];
  const workflows = Array.isArray(model.workflows) ? model.workflows.filter(Boolean) : [];
  const isFocused = state.modelFocus === model.id;
  const rowClasses = [
    "mm-row",
    matrixCategoryClass(model.category),
    usedBySelectedWorkflow ? "used" : "",
    isFocused ? "focused" : "",
    hasSites ? "" : "no-site",
  ].filter(Boolean).join(" ");
  return `
    <tr class="${rowClasses}">
      <th scope="row" class="mm-model-cell">
        <div class="mm-model">
          <span class="mm-badge" title="${escapeHTML(matrixCategoryLabel(model.category))}">${escapeHTML(model.id)}</span>
          <span class="mm-model-name" title="${escapeHTML(model.name || model.id)}">${escapeHTML(model.name || model.id)}</span>
          ${owners.length > 0 ? `<span class="mm-owners">${escapeHTML(owners.join(", "))}</span>` : ""}
          ${hasSites ? "" : '<span class="mm-owners">no site yet</span>'}
          ${demoFmus.length > 0 ? `
            <span class="mm-fmu-pill" title="${escapeHTML(`Demo FMUs in this repo: ${demoFmus.join(", ")}`)}">${demoFmus.length} FMU${demoFmus.length === 1 ? "" : "s"}</span>
          ` : ""}
        </div>
        ${isFocused && demoFmus.length > 0 ? `
          <div class="mm-demo-row">
            ${demoFmus.map((fmu) => `<code class="mm-demo-chip">demo: ${escapeHTML(fmu)}</code>`).join("")}
          </div>
        ` : ""}
      </th>
      ${sites.map((site) => renderMatrixCell(model, site, siteMarks[site.id], site.id === selectedId)).join("")}
      <td class="mm-w-cell">
        ${workflows.map((candidateId) => `
          <button
            class="mm-w-chip${state.candidateFocus === candidateId ? " selected" : ""}"
            type="button"
            data-candidate-focus="${escapeHTML(candidateId)}"
            title="${escapeHTML(matrixCandidateById(candidateId)?.title || candidateId)}"
          >${escapeHTML(candidateId)}</button>
        `).join("")}
      </td>
    </tr>
  `;
}

function renderMatrixCell(model, site, status, isSelectedSite) {
  const mark = status === "identified" ? "&#9679;" : status === "candidate" ? "&#9675;" : "";
  const statusText = status === "identified" ? "identified" : status === "candidate" ? "candidate or to be decided" : "not planned";
  const isFocused = isSelectedSite && state.modelFocus === model.id;
  return `
    <td class="mm-cell${isSelectedSite ? " selected" : ""}${status ? ` ${escapeHTML(status)}` : ""}">
      <button
        class="mm-cell-button${isFocused ? " focused" : ""}"
        type="button"
        data-matrix-site="${escapeHTML(site.id)}"
        data-matrix-model="${escapeHTML(model.id)}"
        aria-pressed="${isFocused ? "true" : "false"}"
        aria-label="${escapeHTML(`${model.id} ${model.name || ""} at ${site.label || site.id}: ${statusText}`)}"
        title="${escapeHTML(`${model.id} at ${site.label || site.id}: ${statusText}`)}"
      >${mark}</button>
    </td>
  `;
}

// Model ids left of the first arrow in a candidate chain ("M1 M6 M7 → M8" gives
// M1, M6, M7). A chain with none there (W7) falls back to every model family
// that lists the candidate in its workflows.
function candidateInputModels(candidate) {
  const left = String(candidate?.chain || "").split("→")[0];
  const ids = left.match(/M\d+/g) || [];
  if (ids.length > 0) {
    return [...new Set(ids)];
  }
  return matrixModels()
    .filter((model) => Array.isArray(model.workflows) && model.workflows.includes(candidate?.id))
    .map((model) => model.id);
}

// Pure relevance rule for the candidate strip. A candidate is relevant at a site
// when (a) one of its input models has an identified or candidate mark there,
// or (b) a repo workflow of that site maps to it. "identified" tells whether any
// (a) mark is identified; "demoCount" counts the (b) workflows.
function candidateRelevantAtSite(candidate, siteId) {
  const demoCount = state.workflows.filter((workflow) =>
    workflowSiteId(workflow) === siteId && workflowMatrixMapping(workflow)?.candidate === candidate?.id).length;
  const marks = candidateInputModels(candidate)
    .map((modelId) => matrixModelById(modelId)?.sites?.[siteId])
    .filter(Boolean);
  return {
    relevant: marks.length > 0 || demoCount > 0,
    identified: marks.includes("identified"),
    demoCount,
  };
}

function renderCandidateWorkflowStrip() {
  const candidates = matrixCandidateWorkflows();
  if (candidates.length === 0) {
    return "";
  }
  const demo = selectedDemonstrator();
  const isPortfolio = demo.id === "portfolio";
  const visible = visibleWorkflows();
  const shown = candidates
    .map((candidate) => ({
      candidate,
      relevance: isPortfolio ? null : candidateRelevantAtSite(candidate, demo.id),
      // All sites: relevant at any scoped site.
      anywhere: isPortfolio && scopedSiteIds().some((siteId) => candidateRelevantAtSite(candidate, siteId).relevant),
    }))
    .filter((item) => (isPortfolio ? item.anywhere : item.relevance.relevant));
  const siteName = demo.shortLabel || demo.label;
  return `
    <div class="mm-candidates">
      <div class="mm-candidates-title">
        Candidate workflows
        <span>${escapeHTML(isPortfolio
          ? `showing ${shown.length} of ${candidates.length}: those relevant at a demo site; click one to highlight its demo workflows`
          : `showing ${shown.length} of ${candidates.length}: those whose chain models are identified or candidate at ${siteName}, or that have demo workflows here`)}</span>
      </div>
      <div class="mm-candidate-row">
        ${shown.map(({ candidate, relevance }) => {
          const mapped = visible.filter((workflow) => workflowMatrixMapping(workflow)?.candidate === candidate.id);
          const count = mapped.length;
          const badges = couplingBadgesFor(mapped);
          const isSelected = state.candidateFocus === candidate.id;
          const candidateOnly = relevance && !relevance.identified && relevance.demoCount === 0;
          return `
            <button
              class="mm-candidate${isSelected ? " selected" : ""}${count === 0 ? " empty" : ""}"
              type="button"
              data-candidate-focus="${escapeHTML(candidate.id)}"
              aria-pressed="${isSelected ? "true" : "false"}"
            >
              <span class="mm-candidate-top">
                <strong>${escapeHTML(candidate.id)}</strong>
                <span>${count} demo workflow${count === 1 ? "" : "s"}</span>
              </span>
              <span class="mm-candidate-title">${escapeHTML(candidate.title || "")}</span>
              ${candidate.chain ? `<code>${escapeHTML(candidate.chain)}</code>` : ""}
              ${badges.length > 0 || candidateOnly ? `
                <span class="coupling-badge-row">
                  ${renderCouplingBadges(badges)}
                  ${candidateOnly ? `<em class="mm-candidate-marker" title="${escapeHTML(`Only candidate model marks at ${siteName} and no demo workflow yet`)}">candidate</em>` : ""}
                </span>
              ` : ""}
            </button>
          `;
        }).join("")}
      </div>
    </div>
  `;
}

const COUPLING_BADGE_ORDER = ["ping-pong", "lock-step", "event-driven", "one-way", "single step"];

// Combined coupling badges of a set of workflows, in legend order.
function couplingBadgesFor(workflows) {
  const badges = new Set();
  for (const workflow of workflows) {
    workflowCouplingInfo(workflow).badges.forEach((badge) => badges.add(badge));
  }
  return COUPLING_BADGE_ORDER.filter((badge) => badges.has(badge));
}

function renderCouplingLegend() {
  const items = [
    ["ping-pong", "Gauss-Seidel: sequential exchange each communication step"],
    ["lock-step", "Jacobi: parallel exchange each communication step"],
    ["event-driven", "condition-triggered discrete input"],
    ["one-way", "final values handed to the next step"],
  ];
  return `
    <div class="coupling-legend" aria-label="FMU coupling legend">
      ${items.map(([badge, text]) => `<span>${renderCouplingBadges([badge])} ${escapeHTML(text)}</span>`).join("")}
    </div>
  `;
}

function renderMatrixFocusChips() {
  const chips = [];
  if (state.modelFocus) {
    const model = matrixModelById(state.modelFocus);
    chips.push(`
      <span class="matrix-focus-chip">
        Model focus: <strong>${escapeHTML(state.modelFocus)}</strong> ${escapeHTML(model?.name || "")}
        <button type="button" data-clear-focus="model" aria-label="Clear model focus">&times;</button>
      </span>
    `);
  }
  if (state.candidateFocus) {
    const candidate = matrixCandidateById(state.candidateFocus);
    chips.push(`
      <span class="matrix-focus-chip">
        Candidate: <strong>${escapeHTML(state.candidateFocus)}</strong> ${escapeHTML(candidate?.title || "")}
        <button type="button" data-clear-focus="candidate" aria-label="Clear candidate workflow focus">&times;</button>
      </span>
    `);
  }
  return chips.join("");
}

// ---------------------------------------------------------------------------
// FMU communication pattern per workflow, derived from the catalog cosim block.
// ---------------------------------------------------------------------------

function rawCosimPatterns(cosim) {
  if (Array.isArray(cosim?.patterns) && cosim.patterns.length > 0) {
    return cosim.patterns.map((pattern) => String(pattern || "").toLowerCase()).filter(Boolean);
  }
  const scheme = String(cosim?.scheme || "").toLowerCase();
  const edges = cosimEdges(cosim);
  const patterns = [];
  if (scheme === "gauss_seidel") {
    patterns.push("sequential");
  } else if (scheme === "jacobi") {
    patterns.push("parallel");
  }
  if (edges.some((edge) => edge.bidirectional)) {
    patterns.push("bidirectional");
  }
  if (edges.some((edge) => !edge.bidirectional)) {
    patterns.push("one-way");
  }
  if (Array.isArray(cosim?.events) && cosim.events.length > 0) {
    patterns.push("event-driven");
  }
  return patterns;
}

function cosimCouplingSummary(cosim) {
  const scheme = String(cosim?.scheme || "").toLowerCase();
  const patterns = rawCosimPatterns(cosim);
  if (Array.isArray(cosim?.events) && cosim.events.length > 0 && !patterns.includes("event-driven")) {
    patterns.push("event-driven");
  }
  let schemeText = "Co-simulation";
  let schemeBadge = "co-sim";
  if (scheme === "gauss_seidel") {
    schemeText = "Gauss-Seidel ping-pong";
    schemeBadge = "ping-pong";
  } else if (scheme === "jacobi") {
    schemeText = "Jacobi lock-step";
    schemeBadge = "lock-step";
  } else if (scheme) {
    schemeText = `${formatWorkflowCategory(scheme)} co-simulation`;
  }
  // "sequential"/"parallel" restate the scheme, so only the coupling shape is listed.
  const extras = patterns.filter((pattern) => pattern !== "sequential" && pattern !== "parallel");
  const badges = [schemeBadge];
  if (extras.includes("event-driven")) {
    badges.push("event-driven");
  }
  if (extras.includes("one-way")) {
    badges.push("one-way");
  }
  return {
    text: [schemeText, ...extras].join(", "),
    badges,
  };
}

// Returns { label, badges[] } describing how the workflow's FMUs exchange data.
function workflowCouplingInfo(workflow) {
  const models = workflowModels(workflow);
  if (models.length === 0) {
    return { label: "", badges: [] };
  }
  const cosimIndexes = models.map((model, index) => (isCosimModel(model) ? index : -1)).filter((index) => index >= 0);
  if (cosimIndexes.length === 0) {
    if (models.length === 1) {
      return { label: "Single FMU step (no coupling)", badges: ["single step"] };
    }
    return { label: SEQUENTIAL_COUPLING_LABEL, badges: ["one-way"] };
  }

  const parts = [];
  const badges = [];
  const addBadge = (badge) => {
    if (!badges.includes(badge)) {
      badges.push(badge);
    }
  };
  const firstCosim = cosimIndexes[0];
  const lastCosim = cosimIndexes[cosimIndexes.length - 1];
  if (firstCosim > 0) {
    const head = models.slice(0, firstCosim).map(workflowModelLabel);
    parts.push(`one-way hand-over from ${head.join(", ")}`);
    addBadge("one-way");
  }
  for (const index of cosimIndexes) {
    const summary = cosimCouplingSummary(models[index].cosim);
    parts.push(summary.text);
    summary.badges.forEach(addBadge);
  }
  if (lastCosim < models.length - 1) {
    const tail = models.slice(lastCosim + 1).map(workflowModelLabel);
    parts.push(`one-way hand-over to ${tail.join(", ")}`);
    addBadge("one-way");
  }
  return { label: parts.join(" + "), badges };
}

// One CSS class per coupling pattern so each badge has the same colour everywhere.
function couplingPatternClass(badge) {
  switch (String(badge || "").toLowerCase()) {
    case "ping-pong":
      return "pattern-pingpong";
    case "lock-step":
      return "pattern-lockstep";
    case "event-driven":
      return "pattern-eventdriven";
    case "one-way":
      return "pattern-oneway";
    default:
      return "pattern-other";
  }
}

function cosimSchemeBadge(scheme) {
  switch (String(scheme || "").toLowerCase()) {
    case "gauss_seidel":
      return "ping-pong";
    case "jacobi":
      return "lock-step";
    default:
      return "co-sim";
  }
}

function renderCouplingBadges(badges) {
  return badges
    .map((badge) => `<span class="coupling-badge ${couplingPatternClass(badge)}">${escapeHTML(badge)}</span>`)
    .join("");
}

function bindLaunchButton(selected) {
  const launchButton = document.getElementById("launchSelectedWorkflow");
  if (!launchButton) {
    return;
  }
  const remoteEnabled = Boolean(state.config?.remoteEnabled);
  const selectedPending = Boolean(selected && state.pendingWorkflows.has(selected.path));
  launchButton.disabled = !remoteEnabled || !selected || selectedPending;
  launchButton.textContent = selectedPending ? "Submitting…" : "Launch selected workflow";
  launchButton.title = remoteEnabled ? "" : "Remote launching is disabled in the current dashboard configuration.";
  launchButton.onclick = () => {
    if (state.selectedWorkflowPath) {
      void launchWorkflow(state.selectedWorkflowPath);
    }
  };
}

// ---------------------------------------------------------------------------
// Details tab: breadcrumb plus Demonstrator / Workflow / Model cards.
// ---------------------------------------------------------------------------

function fmuBaseName(fmuPath) {
  return String(fmuPath || "").split("/").pop().replace(/\.fmu$/i, "");
}

function matrixFamilyForFmu(fmuPath) {
  const base = fmuBaseName(fmuPath);
  if (!base) {
    return null;
  }
  return matrixModels().find((model) => Array.isArray(model.demo_fmus) && model.demo_fmus.includes(base)) || null;
}

function matrixSiteEntry(siteId) {
  return matrixSites().find((site) => site.id === siteId) || null;
}

// Selectable model entries of a workflow: each plain step, and for a co-sim
// step the step itself followed by each of its member FMUs.
function workflowModelEntries(workflow) {
  const entries = [];
  workflowModels(workflow).forEach((model, index) => {
    if (isCosimModel(model)) {
      entries.push({ key: String(index), stepIndex: index, kind: "cosim", label: workflowModelLabel(model), stepName: model.name, model });
      const members = Array.isArray(model.cosim.models) ? model.cosim.models : [];
      for (const member of members) {
        entries.push({
          key: `${index}.${member.name}`,
          stepIndex: index,
          kind: "member",
          label: member.label || member.name || "model",
          stepName: model.name,
          memberName: member.name,
          fmu: member.fmu,
          inputs: member.inputs,
          outputs: member.outputs,
          parameters: member.parameters,
          inputSeries: member.inputSeries,
        });
      }
      return;
    }
    entries.push({
      key: String(index),
      stepIndex: index,
      kind: "fmu",
      label: workflowModelLabel(model),
      stepName: model.name,
      fmu: model.fmu,
      inputs: model.inputs,
      outputs: model.outputs,
      parameters: model.parameters,
      inputSeries: model.inputSeries,
      model,
    });
  });
  return entries;
}

// The entry is only valid while its step is the one highlighted in the chain,
// so selecting another chain node or workflow resets the Model card.
function selectedModelEntry(workflow) {
  const index = state.selectedWorkflowModelIndex;
  if (!Number.isInteger(index)) {
    return null;
  }
  const entries = workflowModelEntries(workflow);
  return entries.find((entry) => entry.key === state.selectedModelKey && entry.stepIndex === index) ||
    entries.find((entry) => entry.key === String(index)) ||
    null;
}

function selectModelEntry(key) {
  const entry = workflowModelEntries(selectedWorkflow()).find((item) => item.key === key);
  if (!entry) {
    return;
  }
  const same = state.selectedModelKey === key && state.selectedWorkflowModelIndex === entry.stepIndex;
  state.selectedModelKey = same ? "" : key;
  state.selectedWorkflowModelIndex = same ? null : entry.stepIndex;
  renderWorkflows();
}

function entryFamily(entry) {
  return entry && entry.kind !== "cosim" ? matrixFamilyForFmu(entry.fmu) : null;
}

// Latest finished result payload already loaded for the workflow, if any.
function latestLoadedPayload(workflow) {
  if (!workflow) {
    return null;
  }
  if (state.genericResult?.state === "ready" && state.genericResult.workflowPath === workflow.path) {
    return state.genericResult.payload || null;
  }
  for (const run of state.runs) {
    if (run.workflowPath === workflow.path && isFinishedRunPhase(run.phase)) {
      const cached = cachedRunResults(run.name);
      if (cached?.state === "ready") {
        return cached.payload || null;
      }
    }
  }
  return null;
}

function runtimeFmuDescriptor(workflow, entry) {
  const info = runInfo(latestLoadedPayload(workflow));
  if (!info || !entry?.fmu) {
    return null;
  }
  const base = fmuBaseName(entry.fmu);
  const steps = Array.isArray(info.steps) ? info.steps : [];
  const ordered = [...steps.filter((step) => step?.name === entry.stepName), ...steps.filter((step) => step?.name !== entry.stepName)];
  for (const step of ordered) {
    const fmus = Array.isArray(step?.fmus) ? step.fmus : [];
    const match = fmus.find((fmu) => entry.memberName && fmu?.model === entry.memberName && fmuBaseName(fmu?.path) === base) ||
      fmus.find((fmu) => fmuBaseName(fmu?.path) === base);
    if (match) {
      return match;
    }
  }
  return null;
}

function latestRunFor(paths) {
  const allowed = new Set(paths);
  return state.runs.find((run) => allowed.has(run.workflowPath)) || null;
}

function renderRunStatusLine(run) {
  if (!state.config?.remoteEnabled) {
    return '<span class="workflow-model-muted">run history needs Argo access</span>';
  }
  if (!run) {
    return '<span class="workflow-model-muted">no runs yet</span>';
  }
  return `
    <span class="details-run-phase ${escapeHTML(classifyPhase(run.phase))}">${escapeHTML(run.phase || "Unknown")}</span>
    <span>${escapeHTML(formatTimestampCompact(run.createdAt))}</span>
  `;
}

function renderDetailsDynamic() {
  renderDetailsBreadcrumb();
  renderDetailsDemoCard();
  renderDetailsWorkflowSummary();
  renderDetailsModelCard();
}

function renderDetailsBreadcrumb() {
  const container = document.getElementById("detailsBreadcrumb");
  if (!container) {
    return;
  }
  const demo = selectedDemonstrator();
  const workflow = selectedWorkflow();
  const entry = workflow ? selectedModelEntry(workflow) : null;
  const crumb = (target, text, emptyText) => text
    ? `<button type="button" class="crumb" data-crumb="${target}">${escapeHTML(text)}</button>`
    : `<button type="button" class="crumb empty" data-crumb="${target}">${escapeHTML(emptyText)}</button>`;
  container.innerHTML = `
    ${crumb("detailsDemoCard", demo.id === "portfolio" ? "All sites" : demo.shortLabel || demo.label, "All sites")}
    <span class="crumb-sep" aria-hidden="true">&rsaquo;</span>
    ${crumb("detailsWorkflowCard", workflow ? workflowLabel(workflow) : "", "choose a workflow")}
    <span class="crumb-sep" aria-hidden="true">&rsaquo;</span>
    ${crumb("detailsModelCard", entry ? entry.label : "", "choose a model")}
  `;
  for (const button of container.querySelectorAll("[data-crumb]")) {
    button.addEventListener("click", () => focusDetailsCard(button.dataset.crumb));
  }
}

function focusDetailsCard(id) {
  const card = document.getElementById(id);
  if (!card) {
    return;
  }
  card.scrollIntoView({ block: "nearest", behavior: "smooth" });
  card.focus({ preventScroll: true });
  card.classList.remove("flash");
  void card.offsetWidth;
  card.classList.add("flash");
}

function renderDetailsDemoCard() {
  const card = document.getElementById("detailsDemoCard");
  if (!card) {
    return;
  }
  const demo = selectedDemonstrator();
  const isPortfolio = demo.id === "portfolio";
  const workflows = workflowsForDemonstrator(demo);
  const site = isPortfolio ? null : matrixSiteEntry(demo.id);
  const partners = isPortfolio
    ? [...new Set(matrixSites().flatMap((entry) => (Array.isArray(entry.partners) ? entry.partners : [])))]
    : Array.isArray(site?.partners) ? site.partners : [];
  const families = matrixModels()
    .map((model) => {
      const marks = model.sites && typeof model.sites === "object" ? model.sites : {};
      const status = isPortfolio
        ? (Object.values(marks).includes("identified") ? "identified" : Object.values(marks).length > 0 ? "candidate" : "")
        : marks[demo.id] || "";
      return { model, status };
    })
    .filter((item) => item.status);
  const latestRun = latestRunFor(workflows.map((workflow) => workflow.path));
  card.innerHTML = `
    <header class="details-card-head">
      <p class="panel-kicker">Demonstrator</p>
      <h3>${escapeHTML(demo.label)}</h3>
    </header>
    <p class="details-sub">${escapeHTML([demo.operator, demo.location].filter(Boolean).join(" | "))}</p>
    <p class="details-text">${escapeHTML(demo.focus || "")}</p>
    <dl class="details-facts">
      <div><dt>Capacity</dt><dd>${escapeHTML(demo.capacity || "n/a")}</dd></div>
      <div><dt>Mapped workflows</dt><dd>${escapeHTML(isPortfolio
        ? `${workflows.length} workflow${workflows.length === 1 ? "" : "s"} across ${scopedSiteIds().length} demo sites`
        : String(workflows.length))}</dd></div>
      ${isPortfolio ? `<div><dt>Model families</dt><dd>${matrixModels().length}</dd></div>` : ""}
    </dl>
    ${(demo.facts || []).length > 0 ? `<ul class="demo-facts">${demo.facts.map((fact) => `<li>${escapeHTML(fact)}</li>`).join("")}</ul>` : ""}
    <div class="details-block">
      <span class="details-label">Model families ${isPortfolio ? "across sites" : "at this site"}</span>
      <div class="details-family-row">
        ${families.length > 0 ? families.map(({ model, status }) => `
          <button
            type="button"
            class="family-badge ${matrixCategoryClass(model.category)} ${status === "identified" ? "identified" : "candidate"}${state.modelFocus === model.id ? " focused" : ""}"
            data-family-focus="${escapeHTML(model.id)}"
            title="${escapeHTML(`${model.id} ${model.name || ""}: ${status === "identified" ? "identified" : "candidate or to be decided"}`)}"
          >${escapeHTML(model.id)}</button>
        `).join("") : '<span class="workflow-model-muted">none in the matrix</span>'}
      </div>
    </div>
    <div class="details-block">
      <span class="details-label">Partners with access</span>
      <div class="details-chip-row">
        ${partners.length > 0 ? partners.map((partner) => `<span class="partner-chip">${escapeHTML(partner)}</span>`).join("") : '<span class="workflow-model-muted">not listed</span>'}
      </div>
      ${state.modelMatrix?.partners_source ? `<span class="details-caption">${escapeHTML(state.modelMatrix.partners_source)}</span>` : ""}
    </div>
    <div class="details-block details-inline">
      <span class="details-label">Latest run ${isPortfolio ? "anywhere" : "at this site"}</span>
      ${renderRunStatusLine(latestRun)}
    </div>
  `;
  for (const button of card.querySelectorAll("[data-family-focus]")) {
    button.addEventListener("click", () => focusFamily(button.dataset.familyFocus));
  }
}

// Sets the model focus and, when the selected workflow uses that family,
// selects the matching model in the Model card.
function focusFamily(familyId) {
  const same = state.modelFocus === familyId;
  state.modelFocus = same ? "" : familyId;
  if (!same) {
    const entry = workflowModelEntries(selectedWorkflow()).find((item) => entryFamily(item)?.id === familyId);
    if (entry) {
      state.selectedModelKey = entry.key;
      state.selectedWorkflowModelIndex = entry.stepIndex;
    }
  }
  renderDemonstrators();
  renderWorkflows();
  revealFirstFocusedWorkflowTab();
}

function renderDetailsWorkflowSummary() {
  const container = document.getElementById("detailsWorkflowSummary");
  if (!container) {
    return;
  }
  const workflow = selectedWorkflow();
  if (!workflow) {
    container.innerHTML = '<div class="details-empty">Choose a workflow from the list.</div>';
    return;
  }
  const mapping = workflowMatrixMapping(workflow);
  const candidate = mapping ? matrixCandidateById(mapping.candidate) : null;
  const coupling = workflowCouplingInfo(workflow);
  const payload = latestLoadedPayload(workflow);
  const syntheticCase = payload?.stepResults?._synthetic_case;
  const caseName = syntheticCase?.name || workflow.metadata?.syntheticCase || "";
  const runtime = workflowMaxRuntimeLabel(workflow).replace(/^max runtime /, "");
  container.innerHTML = `
    <h4 class="details-workflow-title">${escapeHTML(workflowLabel(workflow))}</h4>
    ${workflowDescription(workflow) ? `<p class="details-text">${escapeHTML(workflowDescription(workflow))}</p>` : ""}
    <dl class="details-kv">
      <div>
        <dt>Candidate</dt>
        <dd>${mapping ? `
          <span class="matrix-map-chip">${escapeHTML(workflowMatrixLabel(workflow))}</span>
          ${candidate ? `<span>${escapeHTML(candidate.title || "")}</span>${candidate.chain ? ` <code>${escapeHTML(candidate.chain)}</code>` : ""}` : ""}
          <span class="workflow-model-muted">draft mapping</span>
        ` : '<span class="workflow-model-muted">not mapped</span>'}</dd>
      </div>
      <div>
        <dt>FMU coupling</dt>
        <dd>${escapeHTML(coupling.label || "n/a")} <span class="coupling-badge-row">${renderCouplingBadges(coupling.badges)}</span></dd>
      </div>
      <div><dt>Steps</dt><dd>${workflow.stepCount}</dd></div>
      <div><dt>Max runtime</dt><dd>${escapeHTML(runtime || "n/a")}</dd></div>
      <div><dt>Synthetic case</dt><dd>${caseName ? escapeHTML(caseName) : '<span class="workflow-model-muted">shown after the first finished run</span>'}</dd></div>
      <div><dt>Latest run</dt><dd>${renderRunStatusLine(latestRunFor([workflow.path]))}</dd></div>
    </dl>
  `;
}

function renderDetailsModelCard() {
  const card = document.getElementById("detailsModelCard");
  if (!card) {
    return;
  }
  const workflow = selectedWorkflow();
  const entries = workflowModelEntries(workflow);
  const selected = workflow ? selectedModelEntry(workflow) : null;
  card.innerHTML = `
    <header class="details-card-head">
      <p class="panel-kicker">Model</p>
      <h3>${escapeHTML(selected ? selected.label : "Models in this workflow")}</h3>
    </header>
    ${!workflow ? '<div class="details-empty">Choose a workflow to see its models.</div>' : `
      <div class="details-chain">${renderWorkflowModelOverview(workflow)}</div>
      <div class="details-model-list" role="list">
        ${entries.map((entry) => {
          const family = entryFamily(entry);
          const isSelected = selected?.key === entry.key;
          return `
            <button
              type="button"
              role="listitem"
              class="details-model-item${entry.kind === "member" ? " member" : ""}${isSelected ? " selected" : ""}"
              data-model-entry="${escapeHTML(entry.key)}"
              aria-pressed="${isSelected ? "true" : "false"}"
            >
              <span class="details-model-index">${entry.kind === "member" ? "&middot;" : entry.stepIndex + 1}</span>
              <span class="details-model-name">${escapeHTML(entry.label)}</span>
              ${entry.kind === "cosim" ? `<span class="cosim-scheme-badge ${couplingPatternClass(cosimSchemeBadge(entry.model.cosim.scheme))}">${escapeHTML(cosimSchemeLabel(entry.model.cosim.scheme))}</span>` : ""}
              ${family ? `<span class="mm-badge ${matrixCategoryClass(family.category)}">${escapeHTML(family.id)}</span>` : ""}
            </button>
          `;
        }).join("")}
      </div>
      ${selected ? renderModelEntryDetails(workflow, selected) : '<div class="details-empty">Select a model to see its model family, the demo FMU standing in for it, and its inputs and outputs.</div>'}
    `}
  `;
  for (const button of card.querySelectorAll("[data-model-entry]")) {
    button.addEventListener("click", () => selectModelEntry(button.dataset.modelEntry));
  }
  for (const button of card.querySelectorAll("[data-select-workflow-model]")) {
    button.addEventListener("click", () => toggleWorkflowModel(Number(button.dataset.selectWorkflowModel)));
  }
}

function renderModelEntryDetails(workflow, entry) {
  if (entry.kind === "cosim") {
    return `<div class="details-model-detail">${renderCosimStepCard(entry.model, entry.stepIndex)}</div>`;
  }
  const family = entryFamily(entry);
  const descriptor = runtimeFmuDescriptor(workflow, entry);
  const marks = family?.sites && typeof family.sites === "object" ? family.sites : {};
  const siteLabel = (siteId) => matrixSiteEntry(siteId)?.label || siteId;
  const identified = Object.keys(marks).filter((siteId) => marks[siteId] === "identified").map(siteLabel);
  const candidates = Object.keys(marks).filter((siteId) => marks[siteId] === "candidate").map(siteLabel);
  const inputs = (Array.isArray(entry.inputs) ? entry.inputs : [])
    .map((input) => {
      if (typeof input === "string") {
        return input;
      }
      if (!input) {
        return "";
      }
      const source = input.sourceStep && input.sourceOutput ? `${input.sourceStep}.${input.sourceOutput}` : input.source;
      return source && input.name ? `${source} -> ${input.name}` : input.name || source;
    })
    .filter(Boolean);
  if (entry.inputSeries) {
    inputs.push(entry.inputSeries);
  }
  const parameters = Array.isArray(entry.parameters)
    ? entry.parameters
    : entry.parameters && typeof entry.parameters === "object"
      ? Object.entries(entry.parameters).map(([key, value]) => `${key}=${value}`)
      : [];
  return `
    <div class="details-model-detail">
      <div class="details-block">
        <span class="details-label">Model family</span>
        ${family ? `
          <div class="details-family">
            <span class="mm-badge ${matrixCategoryClass(family.category)}">${escapeHTML(family.id)}</span>
            <div>
              <strong>${escapeHTML(family.name || family.id)}</strong>
              <span class="details-caption">${escapeHTML([
                (family.owners || []).join(", "),
                matrixCategoryLabel(family.category),
              ].filter(Boolean).join(" | "))}</span>
              <span class="details-caption">${escapeHTML([
                identified.length > 0 ? `identified: ${identified.join(", ")}` : "",
                candidates.length > 0 ? `candidate: ${candidates.join(", ")}` : "",
                (family.workflows || []).length > 0 ? `workflows: ${family.workflows.join(", ")}` : "",
              ].filter(Boolean).join(" | ") || "no site assigned yet")}</span>
            </div>
          </div>
        ` : '<span class="workflow-model-muted">not mapped to a model family</span>'}
      </div>
      <div class="details-block">
        <span class="details-label">Demo FMU standing in</span>
        <code class="details-fmu-name">${escapeHTML(String(entry.fmu || "n/a").split("/").pop())}</code>
        ${descriptor ? `
          <span class="details-caption">${escapeHTML([
            descriptor.fmi_version ? `FMI ${descriptor.fmi_version}` : "",
            descriptor.declared_step !== undefined && descriptor.declared_step !== null ? `declared step ${formatMetric(descriptor.declared_step)} s` : "",
            descriptor.model_version ? `version ${descriptor.model_version}` : "",
            descriptor.generation_tool || "",
            descriptor.sha256 ? `sha ${String(descriptor.sha256).slice(0, 12)}` : "",
          ].filter(Boolean).join(" | "))}</span>
        ` : '<span class="details-caption">FMI version, step size and checksum: run the workflow to see</span>'}
      </div>
      <div class="details-block">
        <span class="details-label">Inputs</span>
        <div class="workflow-model-chip-row">${renderWorkflowChips(inputs, 8)}</div>
      </div>
      <div class="details-block">
        <span class="details-label">Outputs</span>
        <div class="workflow-model-chip-row">${renderWorkflowChips(entry.outputs, 10)}</div>
      </div>
      <div class="details-block">
        <span class="details-label">Parameters</span>
        <div class="workflow-model-chip-row">${renderWorkflowChips(parameters, 8)}</div>
      </div>
      <p class="details-note">Deterministic placeholder, not validated physics.</p>
    </div>
  `;
}

function renderWorkflows() {
  const grid = document.getElementById("workflowGrid");
  const context = document.getElementById("workflowContext");
  const workflows = visibleWorkflows();
  const demo = selectedDemonstrator();

  bindLaunchButton(selectedWorkflow());
  renderDetailsDynamic();

  if (state.workflows.length === 0) {
    grid.innerHTML = '<div class="empty-state">No launchable repo workflows were found under <code>workflows/</code>.</div>';
    if (context) {
      context.innerHTML = "";
    }
    return;
  }

  if (context) {
    const focusMatches = hasMatrixFocus() ? workflows.filter(workflowMatchesMatrixFocus).length : 0;
    context.innerHTML = hasMatrixFocus() ? `
      ${renderMatrixFocusChips()}
      <span class="matrix-focus-count">${focusMatches} of ${workflows.length} highlighted</span>
    ` : "";

    for (const button of context.querySelectorAll("[data-clear-focus]")) {
      button.addEventListener("click", () => {
        if (button.dataset.clearFocus === "model") {
          setModelFocus("");
        } else {
          setCandidateFocus("");
        }
      });
    }
  }

  if (workflows.length === 0) {
    grid.innerHTML = `
      <div class="empty-state">
        No repo workflow is mapped to <strong>${escapeHTML(demo.label)}</strong> yet.
        <button type="button" class="inline-action" data-select-demo-all>Show all workflows</button>
      </div>
    `;
    const allButton = grid.querySelector("[data-select-demo-all]");
    allButton?.addEventListener("click", () => selectDemonstrator("portfolio"));
    return;
  }

  grid.innerHTML = workflows
    .map((workflow) => {
      const pending = state.pendingWorkflows.has(workflow.path);
      const isSelected = workflow.path === state.selectedWorkflowPath;
      const label = workflowLabel(workflow);
      const category = formatWorkflowCategory(workflowCategory(workflow));
      const metaPrefix = category ? `${category} | ` : "";
      const coupling = workflowCouplingInfo(workflow);
      const matrixLabel = workflowMatrixLabel(workflow);
      const focusClass = hasMatrixFocus() ? (workflowMatchesMatrixFocus(workflow) ? " focus-match" : " focus-dim") : "";
      return `
        <button
          class="workflow-tab${isSelected ? " selected" : ""}${pending ? " pending" : ""}${focusClass}"
          type="button"
          role="tab"
          aria-selected="${isSelected ? "true" : "false"}"
          title="${escapeHTML(workflow.path)}"
          data-select-workflow="${escapeHTML(workflow.path)}"
        >
          <span class="workflow-tab-title">${escapeHTML(label)}</span>
          <span class="workflow-tab-meta">${escapeHTML(metaPrefix)}${workflow.stepCount} step${workflow.stepCount === 1 ? "" : "s"}${pending ? " | submitting" : ""}</span>
          ${coupling.badges.length > 0 || matrixLabel ? `
            <span class="workflow-tab-badges" title="${escapeHTML(coupling.label)}">
              ${renderCouplingBadges(coupling.badges)}
              ${matrixLabel ? `<span class="matrix-map-chip">${escapeHTML(matrixLabel)}</span>` : ""}
            </span>
          ` : ""}
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
  const candidates = finishedSelectedWorkflowRuns();
  if (candidates.length === 0) {
    state.genericResult = {
      state: "empty",
      workflowPath: workflow.path,
      message: `No finished ${workflowLabel(workflow)} run has been observed yet.`,
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
  // Run status and FMU provenance in the Details cards follow loaded runs and results.
  renderDetailsDynamic();
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

  if (workflowResultFamily(workflow) === "storhy_mock") {
    container.classList.add("storhy-mock-output");
    renderStorhyMockResult(container, workflow);
    initializeECharts(container);
    return;
  }

  container.classList.add("generic-workflow-output");
  renderGenericWorkflowResult(container, workflow);
}

// Coupling badges plus, once a result is loaded, what each step actually did.
function renderOutputCoupling(workflow) {
  const container = document.getElementById("outputCoupling");
  if (!container) {
    return;
  }
  if (!workflow) {
    container.innerHTML = "";
    return;
  }
  const coupling = workflowCouplingInfo(workflow);
  const info = runInfo(latestLoadedPayload(workflow));
  const steps = Array.isArray(info?.steps) ? info.steps : [];
  const stepText = steps.map((step) => {
    const name = step?.name || "step";
    if (step?.kind === "cosim") {
      const scheme = String(step.scheme || "").toLowerCase();
      const label = scheme === "gauss_seidel" ? "Gauss-Seidel ping-pong" : scheme === "jacobi" ? "Jacobi lock-step" : cosimSchemeLabel(step.scheme);
      const points = step.communication_points ? `, ${step.communication_points} communication points` : "";
      const events = Array.isArray(step.events) && step.events.length > 0 ? `, ${step.events.length} event edge${step.events.length === 1 ? "" : "s"}` : "";
      return `${name}: ${label}${points}${events}`;
    }
    const calls = (Array.isArray(step?.fmus) ? step.fmus : []).reduce((sum, fmu) => sum + (Number(fmu?.do_step_calls) || 0), 0);
    const mode = steps.length > 1 ? "one-way" : "single step";
    return `${name}: ${mode}${calls > 0 ? `, ${calls} steps` : ""}`;
  });
  container.innerHTML = `
    ${coupling.badges.length > 0 ? `<span class="coupling-badge-row" title="${escapeHTML(coupling.label)}">${renderCouplingBadges(coupling.badges)}</span>` : ""}
    ${stepText.length > 0 ? `<span class="output-step-line">${escapeHTML(stepText.join(" · "))}</span>` : ""}
  `;
}

function renderOutputHeader(workflow) {
  renderOutputCoupling(workflow);
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

  if (workflowResultFamily(workflow) === "storhy_mock") {
    kicker.textContent = workflowCategory(workflow) ? formatWorkflowCategory(workflowCategory(workflow)) : "STOR-HY Replica";
    title.textContent = workflowLabel(workflow);
    copy.textContent = workflowDescription(workflow) || "Python FMU replica workflow for the selected STOR-HY demonstrator.";
    return;
  }

  kicker.textContent = "Output";
  title.textContent = workflowLabel(workflow);
  copy.textContent = workflowDescription(workflow) || workflow.path;
}

function renderStorhyMockResult(container, workflow) {
  const result = state.genericResult;

  if (!result || result.workflowPath !== workflow.path || result.state === "loading") {
    container.innerHTML = '<div class="empty-state">Waiting for the latest finished STOR-HY replica workflow result…</div>';
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
  const run = runSummaryByName(payload.runName || result.runName);
  const syntheticCase = payload.stepResults?._synthetic_case || null;
  const modelStepEntries = modelStepEntriesOf(payload);
  const alertBanner = renderRunAlertBanner(payload, run);
  const resultHead = `
    <div class="result-head">
      <h3>${escapeHTML(payload.runName || result.runName)}</h3>
      <div class="result-head-actions">
        ${renderResultExportButtons(payload)}
        <span class="result-kind-pill result-kind-storhy">STOR-HY Mock</span>
      </div>
    </div>
  `;
  if (modelStepEntries.length === 0) {
    container.innerHTML = `
      <article class="result-card storhy-summary-card">
        ${alertBanner}
        ${resultHead}
        <div class="result-meta">
          <div>${escapeHTML(payload.workflowPath || workflow.path)}</div>
          ${renderRunTimingStrip(payload, run)}
        </div>
        ${renderStorhyStepChain(payload, modelStepEntries)}
        <div class="empty-state">The latest run did not publish structured step results.</div>
      </article>
    `;
    bindResultExportButtons(container);
    return;
  }

  const dashboardConfig = storhyDashboardConfig(workflow);
  const [summaryStepName, summaryStep] = preferredStorhySummaryStep(modelStepEntries);
  const metricCards = buildStorhyMetricCards(modelStepEntries, dashboardConfig.summary || STORHY_DEFAULT_SUMMARY).join("");
  const syntheticCaseMarkup = renderStorhySyntheticCase(syntheticCase);
  const valueBlocks = renderStorhyValueBlocks(modelStepEntries, dashboardConfig.valueBlocks || []);
  const configuredTraceCards = renderStorhyConfiguredTraceCards(modelStepEntries, dashboardConfig.charts || [], workflow);
  const fallbackTraceCard = configuredTraceCards
    ? ""
    : renderStorhyFallbackTraceCard(modelStepEntries);
  const traceCards = configuredTraceCards || fallbackTraceCard;

  container.innerHTML = `
    <article class="result-card storhy-summary-card">
      ${alertBanner}
      ${resultHead}
      <div class="result-meta">
        <div>${escapeHTML(payload.workflowPath || workflow.path)}</div>
        <div>${modelStepEntries.length} model step${modelStepEntries.length === 1 ? "" : "s"}</div>
        <div>summary step ${escapeHTML(summaryStepName)}</div>
        ${renderRunTimingStrip(payload, run)}
      </div>
      ${buildGenericFallbackMarkup(result)}
      ${renderStorhyStepChain(payload, modelStepEntries)}
      ${syntheticCaseMarkup}
      ${metricCards ? `<div class="metric-grid storhy-metric-grid">${metricCards}</div>` : ""}
      ${valueBlocks ? `<div class="storhy-visual-grid">${valueBlocks}</div>` : ""}
      ${renderStorhyDecisionGrid(modelStepEntries, summaryStep, dashboardConfig)}
    </article>
    ${traceCards ? `<div class="trace-stack">${traceCards}</div>` : ""}
  `;
  bindResultExportButtons(container);
}

function modelStepEntriesOf(payload) {
  return Object.entries(payload?.stepResults || {}).filter(
    ([stepName, stepResult]) => !stepName.startsWith("_") && stepResult && typeof stepResult === "object",
  );
}

// Chain of workflow steps; uses _run.steps when present so failed and skipped
// steps (which publish no results) still appear and the failed one is highlighted.
function renderStorhyStepChain(payload, modelStepEntries) {
  const info = runInfo(payload);
  const failedStep = info?.failed_step || payload?.failedStep || "";
  const steps = Array.isArray(info?.steps) && info.steps.length > 0
    ? info.steps.map((step) => ({ name: step?.name || "", status: String(step?.status || "").toLowerCase(), kind: step?.kind || "" }))
    : modelStepEntries.map(([stepName]) => ({ name: stepName, status: "", kind: "" }));
  if (steps.length === 0) {
    return "";
  }
  return `
    <div class="storhy-model-chain" aria-label="Replica model chain">
      ${steps.map((step) => {
        const status = step.name && step.name === failedStep ? "failed" : step.status;
        const statusClass = status ? ` chain-step-${traceSlug(status, "unknown")}` : "";
        const title = [step.kind === "cosim" ? "co-simulation step" : "", status].filter(Boolean).join(", ");
        return `<span class="chain-step${statusClass}"${title ? ` title="${escapeHTML(title)}"` : ""}>${escapeHTML(formatWorkflowCategory(step.name))}${step.kind === "cosim" ? ' <em class="chain-step-kind">co-sim</em>' : ""}${status && status !== "succeeded" ? ` <em class="chain-step-kind">${escapeHTML(status)}</em>` : ""}</span>`;
      }).join("")}
    </div>
  `;
}

function renderStorhyDecisionGrid(modelStepEntries, summaryStep, dashboardConfig) {
  // config.status / config.recommendation are {key, step?} specs; without them
  // the summary step's own status_code / recommendation_code are used.
  const statusValue = dashboardConfig.status
    ? findStorhyMetricValue(modelStepEntries, dashboardConfig.status.key, dashboardConfig.status.step || "")?.value
    : summaryStep?.status_code;
  const recommendationValue = dashboardConfig.recommendation
    ? findStorhyMetricValue(modelStepEntries, dashboardConfig.recommendation.key, dashboardConfig.recommendation.step || "")?.value
    : summaryStep?.recommendation_code !== undefined
      ? summaryStep.recommendation_code
      : findStorhyMetricValue(modelStepEntries, "recommendation_code")?.value;
  const cards = [];
  if (statusValue !== undefined && statusValue !== null) {
    const status = storhyStatus(statusValue);
    cards.push(`
      <div class="storhy-decision-card">
        <span class="metric-label">Status</span>
        <strong>${escapeHTML(status.label)}</strong>
        <p>${escapeHTML(status.description)}</p>
      </div>
    `);
  }
  if (recommendationValue !== undefined && recommendationValue !== null) {
    const recommendation = storhyRecommendation(recommendationValue);
    cards.push(`
      <div class="storhy-decision-card">
        <span class="metric-label">Recommendation</span>
        <strong>${escapeHTML(recommendation.label)}</strong>
        <p>${escapeHTML(recommendation.description)}</p>
      </div>
    `);
  }
  return cards.length > 0 ? `<div class="storhy-decision-grid">${cards.join("")}</div>` : "";
}

function storhyDashboardConfig(workflow) {
  return STORHY_DASHBOARD_CONFIG[workflow?.path] || {
    summary: STORHY_DEFAULT_SUMMARY,
    charts: [],
    valueBlocks: [],
  };
}

function preferredStorhySummaryStep(stepEntries) {
  const preferredNames = [DEGRADATION_COST_STEP];
  for (const name of preferredNames) {
    const entry = stepEntries.find(([stepName]) => stepName === name);
    if (entry) {
      return entry;
    }
  }
  return stepEntries[stepEntries.length - 1];
}

function buildStorhyMetricCards(stepEntries, metricSpecs) {
  return metricSpecs
    .map((metricSpec) => {
      const spec = normalizeStorhyMetricSpec(metricSpec);
      const resolved = findStorhyMetricValue(stepEntries, spec.key, spec.step);
      return resolved ? { ...spec, ...resolved } : null;
    })
    .filter(Boolean)
    .slice(0, 10)
    .map((metric) => `
      <div class="metric-chip">
        <span class="metric-label">${escapeHTML(metric.label)}</span>
        <span class="metric-value">${escapeHTML(formatStorhyMetric(metric.key, metric.value))}</span>
        <span class="metric-source">${escapeHTML(formatWorkflowCategory(metric.stepName))}</span>
      </div>
    `);
}

function normalizeStorhyMetricSpec(metricSpec) {
  if (typeof metricSpec === "string") {
    return {
      key: metricSpec,
      label: storhyMetricLabel(metricSpec),
      step: "",
    };
  }
  return {
    key: metricSpec.key,
    label: metricSpec.label || storhyMetricLabel(metricSpec.key),
    step: metricSpec.step || "",
  };
}

function storhyMetricLabel(key) {
  const text = String(key || "");
  const parts = text.split(".");
  if (parts.length === 3 && parts[0] === "events") {
    const eventName = formatWorkflowCategory(parts[1]);
    const eventLabel = eventName.charAt(0).toUpperCase() + eventName.slice(1);
    if (parts[2] === "count") {
      return `${eventLabel} events`;
    }
    if (parts[2] === "active") {
      return `${eventLabel} active`;
    }
    return `${eventLabel} ${formatWorkflowCategory(parts[2])}`;
  }
  if (parts.length >= 2) {
    const model = parts[0];
    const variable = parts.slice(1).join(".");
    const modelLabel = COSIM_MODEL_LABELS[model] || formatWorkflowCategory(model);
    return `${storhyBaseMetricLabel(variable)} (${modelLabel})`;
  }
  return storhyBaseMetricLabel(text);
}

function storhyBaseMetricLabel(key) {
  const labels = {
    benefit_cost_ratio: "Benefit-cost ratio",
    cumulative_cleaning_cost_eur: "Cumulative cleaning cost",
    degradation_cost_eur: "Degradation cost",
    energy_mwh: "Energy",
    gross_revenue_eur: "Gross revenue",
    head_m: "Head",
    hours_since_cleaning: "Hours since cleaning",
    net_benefit_eur: "Net benefit",
    cleaning_count: "Cleanings",
    condition_indicator: "Condition indicator",
    cycle_count: "Cycle count",
    downtime_h: "Downtime",
    load_pu: "Load",
    power_actual_mw: "Actual power",
    power_setpoint_mw: "Power set-point",
    price_eur_mwh: "Price",
    revenue_eur: "Revenue",
    sediment_concentration_g_l: "Sediment concentration",
    soh_percent: "State of health",
    start_stop_count: "Start-stop count",
    stress_amplitude_mpa: "Stress amplitude",
    vibration_rms_mm_s: "Vibration RMS",
    confidence: "Confidence",
    damage_index: "Damage index",
    power_mw: "Power",
    recommendation_code: "Recommendation",
    risk_index: "Risk index",
    rul_days: "RUL days",
    score: "Score",
    sediment_exposure: "Sediment exposure",
    soc_percent: "State of charge",
    status_code: "Status",
  };
  return labels[key] || formatWorkflowCategory(key);
}

function findStorhyMetricValue(stepEntries, key, preferredStep = "") {
  if (!key) {
    return null;
  }
  if (preferredStep) {
    const entry = stepEntries.find(([stepName]) => stepName === preferredStep);
    if (entry?.[1]?.[key] !== undefined) {
      return {
        stepName: entry[0],
        value: entry[1][key],
      };
    }
  }
  for (const [stepName, stepResult] of [...stepEntries].reverse()) {
    if (stepResult?.[key] !== undefined) {
      return {
        stepName,
        value: stepResult[key],
      };
    }
  }
  // Fall back to the final trace sample: cosim summary keys may be trace-only signals.
  const candidates = preferredStep
    ? stepEntries.filter(([stepName]) => stepName === preferredStep)
    : [...stepEntries].reverse();
  for (const [stepName, stepResult] of candidates) {
    const series = stepResult?.trace?.signals?.[key];
    if (Array.isArray(series) && series.length > 0) {
      return {
        stepName,
        value: series[series.length - 1],
      };
    }
  }
  return null;
}

function formatStorhyMetric(key, value) {
  const numeric = Number(value);
  if (!Number.isFinite(numeric)) {
    return String(value);
  }
  const name = String(key || "");
  if (name === "status_code" || name.endsWith(".status_code")) {
    return storhyStatus(numeric).label;
  }
  if (name === "recommendation_code" || name.endsWith(".recommendation_code")) {
    return storhyRecommendation(numeric).label;
  }
  if (name.endsWith("_percent")) {
    return `${formatMetric(numeric)}%`;
  }
  if (name.endsWith("_eur_mwh")) {
    return `${formatMetric(numeric)} EUR/MWh`;
  }
  if (name.endsWith("_eur")) {
    return `${formatMetric(numeric)} EUR`;
  }
  if (name.endsWith("_tonnes")) {
    return `${formatMetric(numeric)} t`;
  }
  if (name.endsWith("rul_days")) {
    return `${formatMetric(numeric)} days`;
  }
  if (name.endsWith("_ratio")) {
    return `${formatMetric(numeric)}×`;
  }
  const units = [
    ["_mm_s", "mm/s"],
    ["_mwh", "MWh"],
    ["_g_l", "g/L"],
    ["_mpa", "MPa"],
    ["_mw", "MW"],
    ["_pu", "p.u."],
    ["_h", "h"],
    ["_m", "m"],
  ];
  for (const [suffix, unit] of units) {
    if (name.endsWith(suffix)) {
      return `${formatMetric(numeric)} ${unit}`;
    }
  }
  return formatMetric(numeric);
}

// Codes published by the degradation-cost (and RUL) FMUs: 0 ok, 1 warning, 2 alarm.
function storhyStatus(code) {
  switch (Number(code)) {
    case 2:
      return {
        label: "Alarm",
        description: "Degradation or its cost is outside the acceptable demo range.",
      };
    case 1:
      return {
        label: "Warning",
        description: "Degradation is rising; worth watching before the next operating campaign.",
      };
    default:
      return {
        label: "OK",
        description: "The current operating envelope is inside the nominal demo range.",
      };
  }
}

// 0 continue, 1 reduce cycling, 2 schedule maintenance (degradation-cost model).
function storhyRecommendation(code) {
  switch (Number(code)) {
    case 1:
      return { label: "Reduce cycling to limit degradation cost", description: "Degradation cost is eating into revenue; fewer cycles or starts would improve the net benefit." };
    case 2:
      return { label: "Schedule maintenance / inspection", description: "Damage or cost has reached the level where an inspection should be planned." };
    default:
      return { label: "Continue current operating envelope", description: "Revenue covers the degradation cost; no intervention is recommended." };
  }
}

function preferredStorhyTrace(stepEntries) {
  for (const [stepName, stepResult] of [...stepEntries].reverse()) {
    const trace = extractSimulinkTrace(stepResult);
    if (trace) {
      return { stepName, trace };
    }
  }
  return null;
}

function buildStorhyTraceSeries(trace) {
  const preferredSignals = [
    "damage_index",
    "rul_days",
    "net_benefit_eur",
    "degradation_cost_eur",
    "power_mw",
  ];
  const availableSignals = preferredSignals.filter((name) => Array.isArray(trace?.signals?.[name]));
  return buildScalarTraceSeries(trace, availableSignals.slice(0, 5));
}

function renderStorhySyntheticCase(syntheticCase) {
  if (!syntheticCase || typeof syntheticCase !== "object") {
    return "";
  }
  const values = syntheticCase.values && typeof syntheticCase.values === "object" ? syntheticCase.values : {};
  const valueRows = Object.entries(values)
    .filter(([, value]) => value !== null && value !== undefined)
    .slice(0, 8)
    .map(([key, value]) => `
      <div class="storhy-case-value">
        <span>${escapeHTML(formatWorkflowCategory(key))}</span>
        <strong>${escapeHTML(formatMetricOrText(value))}</strong>
      </div>
    `)
    .join("");
  return `
    <section class="storhy-case-card">
      <div>
        <span class="metric-label">Synthetic Case</span>
        <h4>${escapeHTML(syntheticCase.name || "Synthetic operating case")}</h4>
        <p>${escapeHTML(syntheticCase.operating_mode || syntheticCase.data_basis || "Representative synthetic data included in the workflow image.")}</p>
      </div>
      <div class="storhy-case-meta">
        ${syntheticCase.site ? `<span>${escapeHTML(syntheticCase.site)}</span>` : ""}
        ${syntheticCase.period ? `<span>${escapeHTML(syntheticCase.period)}</span>` : ""}
        ${syntheticCase.source ? `<span>${escapeHTML(syntheticCase.source)}</span>` : ""}
      </div>
      ${valueRows ? `<div class="storhy-case-values">${valueRows}</div>` : ""}
    </section>
  `;
}

function renderStorhyFallbackTraceCard(stepEntries) {
  const trace = preferredStorhyTrace(stepEntries);
  const traceSeries = trace ? buildStorhyTraceSeries(trace.trace) : [];
  if (!trace || traceSeries.length === 0) {
    return "";
  }
  return renderTraceCard(
    "Replica Model Trace",
    `Selected output signals from step ${formatWorkflowCategory(trace.stepName)}.`,
    trace.trace.times,
    traceSeries,
  );
}

function renderStorhyConfiguredTraceCards(stepEntries, chartSpecs, workflow = null) {
  return chartSpecs
    .map((chartSpec) => renderStorhyConfiguredTraceCard(stepEntries, chartSpec, workflow))
    .filter(Boolean)
    .join("");
}

function renderStorhyConfiguredTraceCard(stepEntries, chartSpec, workflow = null) {
  const candidates = chartSpec.step
    ? stepEntries.filter(([stepName]) => stepName === chartSpec.step)
    : stepEntries;
  for (const [stepName, stepResult] of candidates) {
    const trace = extractSimulinkTrace(stepResult);
    if (!trace) {
      continue;
    }
    const series = buildStorhyConfiguredTraceSeries(trace, chartSpec.signals || []);
    if (series.length === 0) {
      continue;
    }
    const threshold = chartSpec.eventThreshold
      ? cosimEventThreshold(workflow, chartSpec.eventThreshold.step || stepName, chartSpec.eventThreshold.event)
      : null;
    if (threshold) {
      series.push({
        name: `Threshold ${threshold.op} ${formatMetric(threshold.value)}`,
        color: "#a53f2b",
        values: trace.times.map(() => threshold.value),
      });
    }
    return renderTraceCard(
      chartSpec.title || "Workflow Trace",
      chartSpec.description || `Signals from step ${formatWorkflowCategory(stepName)}.`,
      trace.times,
      series,
    );
  }
  return "";
}

function buildStorhyConfiguredTraceSeries(trace, signalSpecs) {
  return signalSpecs
    .map((signalSpec, index) => {
      const spec = normalizeStorhySignalSpec(signalSpec);
      const values = trace.signals?.[spec.key];
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
        name: spec.label,
        color: spec.color || paletteColor(index),
        values: samples,
      };
    })
    .filter(Boolean);
}

// Reads the numeric threshold of a cosim event ("model.var > 0.8") from the
// workflow catalog so the chart reference line follows the YAML.
function cosimEventThreshold(workflow, stepName, eventName) {
  const step = workflowModels(workflow).find((model) => isCosimModel(model) && model.name === stepName);
  const event = (step?.cosim?.events || []).find((item) => item?.name === eventName);
  const match = /^\s*([A-Za-z_][\w.]*)\s*(<=|>=|==|!=|<|>)\s*(-?[0-9.]+(?:[eE][-+]?\d+)?)\s*$/.exec(String(event?.when || ""));
  if (!match) {
    return null;
  }
  const value = Number(match[3]);
  return Number.isFinite(value) ? { signal: match[1], op: match[2], value } : null;
}

function normalizeStorhySignalSpec(signalSpec) {
  if (typeof signalSpec === "string") {
    return {
      key: signalSpec,
      label: storhyMetricLabel(signalSpec),
      color: "",
    };
  }
  return {
    key: signalSpec.key,
    label: signalSpec.label || storhyMetricLabel(signalSpec.key),
    color: signalSpec.color || "",
  };
}

function renderStorhyValueBlocks(stepEntries, blockSpecs) {
  return blockSpecs
    .map((blockSpec) => renderStorhyValueBlock(stepEntries, blockSpec))
    .filter(Boolean)
    .join("");
}

function renderStorhyValueBlock(stepEntries, blockSpec) {
  const rows = (blockSpec.values || [])
    .map((valueSpec) => {
      const spec = normalizeStorhyMetricSpec(valueSpec);
      const resolved = findStorhyMetricValue(stepEntries, spec.key, spec.step);
      return resolved ? { ...spec, ...resolved } : null;
    })
    .filter(Boolean);
  if (rows.length === 0) {
    return "";
  }

  const scaleMaxByType = rows.reduce((accumulator, row) => {
    const type = storhyValueScaleType(row.key);
    const current = accumulator[type] || storhyValueDefaultMax(type);
    accumulator[type] = Math.max(current, Math.abs(Number(row.value) || 0));
    return accumulator;
  }, {});
  return `
    <section class="storhy-value-block">
      <div class="storhy-value-head">
        <h4>${escapeHTML(blockSpec.title || "Indicators")}</h4>
        ${blockSpec.description ? `<p>${escapeHTML(blockSpec.description)}</p>` : ""}
      </div>
      <div class="storhy-value-rows">
        ${rows.map((row) => renderStorhyValueRow(row, scaleMaxByType[storhyValueScaleType(row.key)] || 1)).join("")}
      </div>
    </section>
  `;
}

function storhyValueScaleType(key) {
  if (key.endsWith("_eur")) {
    return "money";
  }
  if (key.endsWith("_tonnes")) {
    return "co2";
  }
  if (key.endsWith("_percent")) {
    return "percent";
  }
  if (key.endsWith("_index") || key.endsWith("_ratio") || key.endsWith("confidence") || key.endsWith("sediment_exposure") || key.endsWith("condition_indicator")) {
    return "ratio";
  }
  return "absolute";
}

function storhyValueDefaultMax(type) {
  if (type === "ratio") {
    return 1;
  }
  if (type === "percent") {
    return 10;
  }
  return 1;
}

function renderStorhyValueRow(row, maxAbs) {
  const numeric = Number(row.value);
  const width = Number.isFinite(numeric)
    ? clampNumber((Math.abs(numeric) / maxAbs) * 100, 3, 100)
    : 0;
  const signedClass = numeric < 0 ? " negative" : "";
  return `
    <div class="storhy-value-row">
      <div class="storhy-value-label">
        <span>${escapeHTML(row.label)}</span>
        <strong>${escapeHTML(formatStorhyMetric(row.key, row.value))}</strong>
      </div>
      <div class="storhy-value-track" aria-hidden="true">
        <span class="storhy-value-fill${signedClass}" style="width:${width}%"></span>
      </div>
      <span class="storhy-value-source">${escapeHTML(formatWorkflowCategory(row.stepName))}</span>
    </div>
  `;
}

function renderGenericWorkflowResult(container, workflow) {
  const result = state.genericResult;

  if (!result || result.workflowPath !== workflow.path || result.state === "loading") {
    container.innerHTML = '<div class="empty-state">Waiting for the latest finished workflow result…</div>';
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
  const run = runSummaryByName(payload.runName || result.runName);
  const stepEntries = Object.entries(payload.stepResults || {}).filter(([stepName]) => !stepName.startsWith("_"));
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
      ${renderRunAlertBanner(payload, run)}
      <div class="result-head">
        <h3>${escapeHTML(payload.runName || result.runName)}</h3>
        <div class="result-head-actions">
          ${renderResultExportButtons(payload)}
          <span class="result-kind-pill result-kind-trace">Structured Result</span>
        </div>
      </div>
      <div class="result-meta">
        <div>${escapeHTML(payload.workflowPath || workflow.path)}</div>
        <div>${stepEntries.length} step result${stepEntries.length === 1 ? "" : "s"}</div>
        ${renderRunTimingStrip(payload, run)}
      </div>
      ${buildGenericFallbackMarkup(result)}
      ${stepSummary ? `<div class="metric-grid">${stepSummary}</div>` : ""}
      <pre class="result-json">${escapeHTML(JSON.stringify(payload, null, 2))}</pre>
    </article>
  `;
  bindResultExportButtons(container);
}

function runSummaryByName(runName) {
  return state.runs.find((run) => run.name === runName) || null;
}

function runInfo(payload) {
  const info = payload?.stepResults?.[RUN_INFO_STEP];
  return info && typeof info === "object" && !Array.isArray(info) ? info : null;
}

// Combines the Argo phase, the runner's _run block, and the results endpoint
// fields into one outcome used by the alert banner and run pills.
function runOutcome(payload, run) {
  const info = runInfo(payload);
  const phase = String(payload?.phase || run?.phase || "").toLowerCase();
  let status = String(info?.status || payload?.status || "").toLowerCase();
  if (!status) {
    status = phase === "failed" || phase === "error" ? "failed" : phase;
  }
  if ((phase === "failed" || phase === "error") && status === "succeeded") {
    status = "failed";
  }
  const deadlineExceeded = Boolean(run?.deadlineExceeded);
  return {
    status,
    phase,
    failed: status === "failed" || status === "error" || status === "cancelled" || deadlineExceeded,
    cancelled: status === "cancelled",
    partial: Boolean(payload?.partial),
    deadlineExceeded,
    deadlineSeconds: Number(run?.deadlineSeconds || 0),
    failedStep: info?.failed_step || payload?.failedStep || "",
    error: info?.error || payload?.error || run?.message || "",
  };
}

function renderRunAlertBanner(payload, run) {
  const outcome = runOutcome(payload, run);
  if (!outcome.failed && !outcome.partial) {
    return "";
  }
  let title = "Run failed";
  if (outcome.deadlineExceeded) {
    title = outcome.deadlineSeconds > 0
      ? `Max execution time exceeded (${formatDuration(outcome.deadlineSeconds)})`
      : "Max execution time exceeded";
  } else if (outcome.cancelled) {
    title = "Run cancelled";
  } else if (!outcome.failed) {
    title = "Partial result";
  }
  const hasResults = modelStepEntriesOf(payload).length > 0;
  return `
    <div class="run-alert" role="alert">
      <strong>${escapeHTML(title)}</strong>
      ${outcome.failedStep ? `<span>Failed at step <code>${escapeHTML(outcome.failedStep)}</code>.</span>` : ""}
      ${outcome.error ? `<span class="run-alert-error">${escapeHTML(outcome.error)}</span>` : ""}
      <span>${hasResults
        ? "Showing results from the steps that completed before the run stopped."
        : "No step results were recovered from the run logs."}</span>
    </div>
  `;
}

function renderRunTimingStrip(payload, run) {
  const info = runInfo(payload);
  const items = [];
  if (info) {
    if (Number.isFinite(Number(info.wall_seconds)) && info.wall_seconds !== null) {
      items.push(["wall", formatWallSeconds(info.wall_seconds)]);
    }
    if (Number.isFinite(Number(info.simulated_seconds)) && info.simulated_seconds !== null) {
      items.push(["simulated", formatSimDuration(info.simulated_seconds)]);
    }
    if (Number.isFinite(Number(info.ratio)) && info.ratio !== null && Number(info.ratio) > 0) {
      items.push(["", formatRatio(info.ratio)]);
    }
    if (info.status) {
      items.push(["status", String(info.status)]);
    }
  } else if (run && Number(run.durationSeconds) > 0) {
    items.push(["pod duration", formatDuration(run.durationSeconds)]);
  }
  if (items.length === 0) {
    return "";
  }
  return `
    <div class="run-timing-strip" aria-label="Run timing">
      ${items.map(([label, value]) => `<span><strong>${escapeHTML(value)}</strong>${label ? ` ${escapeHTML(label)}` : ""}</span>`).join("")}
    </div>
  `;
}

function renderResultExportButtons(payload) {
  const hasTrace = modelStepEntriesOf(payload).some(([, stepResult]) => extractSimulinkTrace(stepResult));
  const hasOutputs = modelStepEntriesOf(payload).length > 0;
  if (!hasTrace && !hasOutputs) {
    return "";
  }
  return `
    <span class="result-export-buttons">
      ${hasTrace ? '<button type="button" class="result-export-button" data-export-csv="trace">Trace CSV</button>' : ""}
      ${hasOutputs ? '<button type="button" class="result-export-button" data-export-csv="outputs">Outputs CSV</button>' : ""}
    </span>
  `;
}

function bindResultExportButtons(container) {
  for (const button of container.querySelectorAll("[data-export-csv]")) {
    button.addEventListener("click", () => exportResultCsv(button.dataset.exportCsv));
  }
}

function exportResultCsv(kind) {
  const result = state.genericResult;
  if (result?.state !== "ready" || !result.payload) {
    return;
  }
  const payload = result.payload;
  const run = runSummaryByName(payload.runName || result.runName);
  const meta = {
    runName: payload.runName || result.runName || "",
    workflowPath: payload.workflowPath || result.workflowPath || "",
    workflowSha256: runInfo(payload)?.workflow?.sha256 || run?.workflowSha256 || "",
  };
  const stem = traceSlug(meta.runName, "run");
  if (kind === "trace") {
    downloadCsv(`${stem}-trace.csv`, buildTraceCsv(payload, meta));
  } else if (kind === "outputs") {
    downloadCsv(`${stem}-outputs.csv`, buildFinalOutputsCsv(payload, meta));
  }
}

function csvCell(value) {
  const text = value === null || value === undefined ? "" : String(value);
  return /[",\r\n]/.test(text) ? `"${text.replaceAll('"', '""')}"` : text;
}

function csvScalar(value) {
  if (value === null || value === undefined) {
    return "";
  }
  if (typeof value === "boolean") {
    return value ? "1" : "0";
  }
  if (typeof value === "object") {
    return JSON.stringify(value);
  }
  return String(value);
}

// Long format: one row per (step, sample time, signal).
function buildTraceCsv(payload, meta = {}) {
  const header = ["run_name", "workflow_path", "workflow_sha256", "step", "time_s", "signal", "value"];
  const lines = [header.join(",")];
  const prefix = [meta.runName || payload?.runName || "", meta.workflowPath || payload?.workflowPath || "", meta.workflowSha256 || ""];
  for (const [stepName, stepResult] of modelStepEntriesOf(payload)) {
    const trace = extractSimulinkTrace(stepResult);
    if (!trace) {
      continue;
    }
    const signals = Object.entries(trace.signals).filter(([, values]) => Array.isArray(values));
    trace.times.forEach((time, index) => {
      for (const [signal, values] of signals) {
        if (index >= values.length) {
          continue;
        }
        lines.push([...prefix, stepName, time, signal, csvScalar(values[index])].map(csvCell).join(","));
      }
    });
  }
  return `${lines.join("\n")}\n`;
}

function buildFinalOutputsCsv(payload, meta = {}) {
  const header = ["run_name", "workflow_path", "workflow_sha256", "step", "output", "value"];
  const lines = [header.join(",")];
  const prefix = [meta.runName || payload?.runName || "", meta.workflowPath || payload?.workflowPath || "", meta.workflowSha256 || ""];
  for (const [stepName, stepResult] of modelStepEntriesOf(payload)) {
    for (const [output, value] of Object.entries(stepResult)) {
      if (output === "trace") {
        continue;
      }
      lines.push([...prefix, stepName, output, csvScalar(value)].map(csvCell).join(","));
    }
  }
  return `${lines.join("\n")}\n`;
}

function downloadCsv(filename, text) {
  const blob = new Blob([text], { type: "text/csv;charset=utf-8" });
  const url = URL.createObjectURL(blob);
  const link = document.createElement("a");
  link.href = url;
  link.download = filename;
  link.style.display = "none";
  document.body.appendChild(link);
  link.click();
  link.remove();
  window.setTimeout(() => URL.revokeObjectURL(url), 1000);
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

function classifyStorhyMockRun(run) {
  const workflow = workflowByPath(run?.workflowPath || "");
  if (workflowResultFamily(workflow) !== "storhy_mock") {
    return null;
  }
  const phase = String(run.phase || "").toLowerCase();
  if (!isFinishedRunPhase(phase)) {
    return null;
  }
  const cached = state.genericResultsCache.get(run.name);
  if (cached?.state === "ready") {
    const outcome = runOutcome(cached.payload, run);
    if (outcome.failed || outcome.partial) {
      return modelStepEntriesOf(cached.payload).length > 0
        ? { kind: "partial", label: "Partial Result" }
        : { kind: "missing", label: "No Result Payload" };
    }
    return {
      kind: "storhy",
      label: "STOR-HY Mock",
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

function clampNumber(value, min, max) {
  return Math.min(max, Math.max(min, Number(value) || 0));
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
  const expandedRun = runs.find((run) => run.name === state.selectedRunName);
  if (expandedRun) {
    ensureRunResultsLoaded(expandedRun);
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
              ${renderRunProvenance(run)}
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

function cachedRunResults(runName) {
  for (const cache of [state.genericResultsCache, state.simulinkResultsCache, state.aeStatsResultsCache]) {
    const cached = cache.get(runName);
    if (cached?.state === "ready") {
      return cached;
    }
  }
  return state.genericResultsCache.get(runName) || null;
}

// Fetches /results for an expanded finished run so its card can show the _run
// provenance. Safe to call on every render: in-flight and cached runs are skipped.
function ensureRunResultsLoaded(run) {
  if (!run?.name || !isFinishedRunPhase(run.phase) || state.runResultsInflight.has(run.name)) {
    return;
  }
  const cached = cachedRunResults(run.name);
  if (cached?.state === "ready") {
    return;
  }
  if (cached?.state === "error" && Date.now() - (cached.checkedAt || 0) < SIMULINK_RESULT_RETRY_MS) {
    return;
  }
  state.runResultsInflight.add(run.name);
  void fetchJSON(`/api/runs/${encodeURIComponent(run.name)}/results`)
    .then((payload) => {
      state.genericResultsCache.set(run.name, { state: "ready", payload, checkedAt: Date.now() });
    })
    .catch((error) => {
      state.genericResultsCache.set(run.name, { state: "error", message: error.message, checkedAt: Date.now() });
    })
    .finally(() => {
      state.runResultsInflight.delete(run.name);
      renderRuns();
    });
}

function normalizeSha(value) {
  return String(value || "").trim().toLowerCase();
}

// Labels carry a 12-hex prefix while annotations and _run carry the full hash,
// so two hashes match when one is a prefix of the other.
function shaMatches(left, right) {
  const a = normalizeSha(left);
  const b = normalizeSha(right);
  if (!a || !b) {
    return true;
  }
  return a.startsWith(b) || b.startsWith(a);
}

function workflowShaCheck(run, info) {
  const submitted = normalizeSha(run?.workflowSha256);
  const executed = normalizeSha(info?.workflow?.sha256);
  const current = normalizeSha(workflowByPath(run?.workflowPath || "")?.sha256);
  const warnings = [];
  if (submitted && executed && !shaMatches(submitted, executed)) {
    warnings.push("The workflow the runner executed differs from the one the dashboard submitted.");
  }
  const reference = executed || submitted;
  if (reference && current && !shaMatches(reference, current)) {
    warnings.push("The workflow file in the catalog has changed since this run.");
  }
  return {
    entries: [["Submitted", submitted], ["Executed", executed], ["Current", current]].filter(([, value]) => value),
    warnings,
  };
}

function uniqueRunFmus(info) {
  const seen = new Map();
  for (const step of Array.isArray(info?.steps) ? info.steps : []) {
    for (const fmu of Array.isArray(step?.fmus) ? step.fmus : []) {
      const key = fmu?.sha256 || fmu?.path || fmu?.model;
      if (!key) {
        continue;
      }
      if (!seen.has(key)) {
        seen.set(key, { ...fmu, usedBy: [] });
      }
      seen.get(key).usedBy.push(fmu.model ? `${step.name}.${fmu.model}` : step.name);
    }
  }
  return [...seen.values()];
}

function formatRunResources(resources) {
  const part = (label, values) => {
    const items = [values?.cpu ? `cpu ${values.cpu}` : "", values?.memory ? `mem ${values.memory}` : ""].filter(Boolean);
    return items.length > 0 ? `${label} ${items.join(", ")}` : "";
  };
  return [part("requests", resources?.requests), part("limits", resources?.limits)].filter(Boolean).join(" | ");
}

function renderRunProvenance(run) {
  const lines = [];
  if (Number(run.deadlineSeconds) > 0) {
    lines.push(`
      <div class="run-detail-line">
        <span>Max execution time</span>
        <strong>${escapeHTML(formatDuration(run.deadlineSeconds))}${run.deadlineExceeded ? " (exceeded)" : ""}</strong>
      </div>
    `);
  }
  const resources = formatRunResources(run.resources);
  if (resources) {
    lines.push(`<div class="run-detail-line"><span>Resources</span><code>${escapeHTML(resources)}</code></div>`);
  }
  if (run.submittedFrom || run.dashboardVersion) {
    lines.push(`
      <div class="run-detail-line">
        <span>Submitted from</span>
        <code>${escapeHTML([run.submittedFrom, run.dashboardVersion ? `dashboard ${run.dashboardVersion}` : ""].filter(Boolean).join(" | "))}</code>
      </div>
    `);
  }

  let body = "";
  if (!isFinishedRunPhase(run.phase)) {
    body = '<div class="run-provenance-note">Run provenance is published when the run finishes.</div>';
  } else {
    const cached = cachedRunResults(run.name);
    if (state.runResultsInflight.has(run.name) || !cached) {
      body = '<div class="run-provenance-note">Loading run provenance…</div>';
    } else if (cached.state === "error") {
      body = `<div class="run-provenance-note">Provenance unavailable: ${escapeHTML(cached.message || "no result payload")}</div>`;
    } else {
      body = renderRunInfoDetails(run, cached.payload);
    }
  }

  return `
    <div class="run-provenance" aria-label="Run provenance">
      <div class="run-provenance-title">Provenance</div>
      ${lines.join("")}
      ${body}
    </div>
  `;
}

function renderRunInfoDetails(run, payload) {
  const info = runInfo(payload);
  const outcome = runOutcome(payload, run);
  const shaCheck = workflowShaCheck(run, info);
  const shaMarkup = `
    ${shaCheck.entries.length > 0 ? `
      <div class="run-detail-line">
        <span>Workflow sha256</span>
        <div class="run-sha-list">
          ${shaCheck.entries.map(([label, value]) => `<code title="${escapeHTML(value)}">${escapeHTML(label)} ${escapeHTML(value.slice(0, 12))}</code>`).join("")}
        </div>
      </div>
    ` : ""}
    ${shaCheck.warnings.map((warning) => `<div class="run-sha-warning" role="status">${escapeHTML(warning)}</div>`).join("")}
  `;
  if (!info) {
    return `
      ${shaMarkup}
      <div class="run-provenance-note">${payload?.partial
        ? escapeHTML(outcome.error || "The run stopped before the runner could publish provenance.")
        : "This run predates run provenance (no _run block in its results)."}</div>
    `;
  }

  const steps = Array.isArray(info.steps) ? info.steps : [];
  const fmus = uniqueRunFmus(info);
  return `
    <div class="run-detail-line">
      <span>Outcome</span>
      <strong class="run-outcome run-outcome-${escapeHTML(traceSlug(outcome.status, "unknown"))}">${escapeHTML(outcome.status || "unknown")}</strong>
    </div>
    ${outcome.failedStep ? `<div class="run-detail-line"><span>Failed step</span><code>${escapeHTML(outcome.failedStep)}</code></div>` : ""}
    ${info.error ? `<div class="run-message">${escapeHTML(info.error)}</div>` : ""}
    <div class="run-detail-line">
      <span>Timing</span>
      <strong>${escapeHTML(`${formatWallSeconds(info.wall_seconds)} wall | ${formatSimDuration(info.simulated_seconds)} simulated | ${formatRatio(info.ratio)}`)}</strong>
    </div>
    ${info.started_at || info.finished_at ? `
      <div class="run-detail-line">
        <span>Runner window</span>
        <strong>${escapeHTML(`${formatTimestamp(info.started_at)} to ${formatTimestamp(info.finished_at)}`)}</strong>
      </div>
    ` : ""}
    ${shaMarkup}
    ${steps.length > 0 ? `
      <div class="run-detail-line">
        <span>Steps</span>
        <ul class="run-step-list">
          ${steps.map((step) => `
            <li class="run-step-${escapeHTML(traceSlug(step?.status, "unknown"))}">
              <span class="run-step-name">
                <code>${escapeHTML(step?.name || "step")}</code>
                ${renderCouplingBadges([step?.kind === "cosim" ? cosimSchemeBadge(step.scheme) : steps.length > 1 ? "one-way" : "single step"])}
                ${step?.kind === "cosim" && Array.isArray(step?.events) && step.events.length > 0 ? renderCouplingBadges(["event-driven"]) : ""}
              </span>
              <em>${escapeHTML([
                step?.kind === "cosim" ? cosimSchemeLabel(step.scheme) : step?.kind,
                step?.status,
                step?.wall_seconds !== undefined ? formatWallSeconds(step.wall_seconds) : "",
                step?.ratio ? formatRatio(step.ratio) : "",
                step?.communication_points ? `${step.communication_points} comm. points` : "",
                Array.isArray(step?.events) && step.events.length > 0 ? `${step.events.length} event edge${step.events.length === 1 ? "" : "s"}` : "",
              ].filter(Boolean).join(" | "))}</em>
            </li>
          `).join("")}
        </ul>
      </div>
    ` : ""}
    ${fmus.length > 0 ? `
      <div class="run-detail-line">
        <span>FMUs</span>
        <ul class="run-fmu-list">
          ${fmus.map((fmu) => `
            <li>
              <strong>${escapeHTML([fmu.model_name || String(fmu.path || fmu.model || "fmu").split("/").pop(), fmu.model_version].filter(Boolean).join(" "))}</strong>
              <em>${escapeHTML([
                fmu.fmi_version ? `FMI ${fmu.fmi_version}` : "",
                fmu.generation_tool,
                fmu.declared_step !== undefined && fmu.declared_step !== null ? `declared step ${formatMetric(fmu.declared_step)}` : "",
                fmu.sha256 ? `sha ${String(fmu.sha256).slice(0, 12)}` : "",
              ].filter(Boolean).join(" | "))}</em>
              ${fmu.usedBy.length > 0 ? `<code>${escapeHTML(fmu.usedBy.join(", "))}</code>` : ""}
            </li>
          `).join("")}
        </ul>
      </div>
    ` : ""}
    <div class="run-detail-line">
      <span>Versions</span>
      <code>${escapeHTML([`runner ${info.runner_version || "unknown"}`, run.dashboardVersion ? `dashboard ${run.dashboardVersion}` : ""].filter(Boolean).join(" | "))}</code>
    </div>
  `;
}

function renderRunResultPill(run) {
  const resultType = classifySimulinkRun(run) || classifyAeStatsRun(run) || classifyStorhyMockRun(run);
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

function formatWallSeconds(value) {
  const seconds = Number(value);
  if (value === null || value === undefined || !Number.isFinite(seconds)) {
    return "n/a";
  }
  if (seconds < 1) {
    return `${Math.round(seconds * 1000)} ms`;
  }
  return formatDuration(seconds);
}

// Simulated time is in the FMU's own unit; the FMI 3 demo workflows use SI seconds.
function formatSimDuration(value) {
  const seconds = Number(value);
  if (value === null || value === undefined || !Number.isFinite(seconds)) {
    return "n/a";
  }
  const abs = Math.abs(seconds);
  if (abs >= 86400) {
    return `${trimFixed(seconds / 86400, 2)} d`;
  }
  if (abs >= 3600) {
    return `${trimFixed(seconds / 3600, 2)} h`;
  }
  if (abs >= 60) {
    return `${trimFixed(seconds / 60, 1)} min`;
  }
  return `${trimFixed(seconds, 2)} s`;
}

function formatRatio(value) {
  const ratio = Number(value);
  if (value === null || value === undefined || !Number.isFinite(ratio) || ratio <= 0) {
    return "n/a";
  }
  if (ratio >= 1000) {
    return `${Math.round(ratio).toLocaleString("en-US")}x real time`;
  }
  if (ratio >= 10) {
    return `${ratio.toFixed(0)}x real time`;
  }
  return `${trimFixed(ratio, 2)}x real time`;
}

function formatMetric(value) {
  const numeric = Number(value);
  if (!Number.isFinite(numeric)) {
    return String(value);
  }
  const magnitude = Math.abs(numeric);
  if (magnitude >= 1e9 || (magnitude > 0 && magnitude < 0.001)) {
    return numeric.toExponential(3);
  }
  if (magnitude >= 1000) {
    return numeric.toLocaleString("en-US", { maximumFractionDigits: magnitude >= 10000 ? 0 : 1 });
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
