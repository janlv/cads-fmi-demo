# Proposed figures for D3.4, section 6

Open the [four-page PDF set](section-6-figure-set.pdf) or the [visual overview](preview.png).

Four standalone figures, saved separately from the deliverable. They use aligned
boxes, a common type scale and a consistent palette. Each figure explicitly
identifies whether it describes the target architecture, the current prototype,
or a proposal awaiting agreement with Kaizen.

| Figure | Suggested placement | Purpose |
| --- | --- | --- |
| [01 — Target platform](01-target-platform.svg) | §6.1; proposed replacement for Figure 2 | Separates data ingestion, database, data access and computation within the target authorization boundary. |
| [02 — Modules and workflows](02-modules-and-workflows.svg) | §6.2; companion to or replacement for the model-library part of Figure 3 | Shows four module roles using one execution contract, without implying a fixed processing pipeline. |
| [03 — Prototype execution stack](03-prototype-execution-stack.svg) | §6.3; new implementation figure | Connects browser, Go service, Argo CLI/API/controller, Kubernetes, Go runner, cgo/C++ bridge, FMIL and FMUs; includes the result-return path. |
| [04 — Proposed data access](04-proposed-data-access.svg) | §6.3; new integration figure | Explains option 2 using a batch-oriented sequence while leaving connector packaging and deployment open. |

Every figure is available as:

- **SVG:** editable vector master, with selectable text and individual shapes.
- **PNG:** 3200 pixels wide, white background, for straightforward insertion.
- **PDF:** standalone export for review and layout workflows.

The original Figure 3 also covers the partner access matrix. Figure 02 does not
replace that matrix: keep detailed partner/site permissions in the access-control
section or a dedicated table. Figure 01 shows the cross-cutting authorization
responsibility without claiming it is implemented in the prototype.

## Suggested captions

1. **Target CADS platform architecture.** Authorized data access separates the
   database from workflow computation. The dedicated data-access component is a
   proposed integration boundary whose implementation is to be agreed with Kaizen.
2. **CADS module roles and common execution contract.** Declarative workflows
   select and connect FMUs representing physical modeling, condition monitoring
   and maintenance, optimization, and decision support. Categories do not impose
   an execution order.
3. **Execution stack of the current CADS prototype.** The local dashboard service
   submits and queries hosted workflows through Argo. Kubernetes runs the bundled
   workflow image, where the Go runner executes FMUs through the cgo/C++ bridge
   and FMIL. Hosted results are recovered from JSON in pod logs.
4. **Proposed shared data-access sequence.** The runner obtains a scoped input
   dataset through a dedicated adapter, executes the model FMUs, and returns
   outputs and provenance for persistence. The sequence expresses responsibilities,
   not a requirement for one global connector process or a background FMU.

## Interpretation notes

- Figures 01, 02 and 04 are architectural proposals/target views, not deployment
  acceptance evidence. Figure 03 reflects source inspection of this checkout.
- In Figure 03, Argo's controller-to-pod arrow abbreviates pod creation through
  the Kubernetes API, scheduling and startup; the controller does not invoke FMIL.
- Blue arrows in Figure 03 describe calls/control, with two heads where responses
  are included. Teal shows the result-return path. In the sequence diagram,
  arrowheads give message direction; time progresses downward.
- A current workflow completes and destroys each FMU instance before starting
  the next. Parallel or cyclic numerical coupling is not implied.
- CSV, S3 and synthetic inputs in the prototype are not an integrated DB path.
  Authentication to Argo is not the full target user/site access-control model.
- Figure 04 illustrates the successful data flow. Retry, idempotency, authorization,
  schema compatibility and connection budgets still need an agreed contract.
- The FMI version is intentionally omitted from shared model boxes: the target
  document requests FMI 3, while bundled Python examples use FMI 2 and the bridge
  contains both FMI 2 and FMI 3 paths.

Source analysis: [section 6 review](../../d3-4-section-6-review.md).

## Regeneration

From the repository root, generate SVG files using Python's standard library:

```bash
python3 docs/figures/section-6/generate_figures.py
```

On macOS, export PNG and PDF using the supplied AppKit helper:

```bash
swift -module-cache-path /tmp/cads-swift-cache \
  docs/figures/section-6/export_figures.swift docs/figures/section-6
```

The SVG masters can also be opened in an SVG editor or browser on other systems.
