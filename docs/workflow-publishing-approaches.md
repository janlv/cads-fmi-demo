# Workflow Publishing Approaches

This note compares ways to package and publish CADS workflows, models, and
runtime code for the Kaizen Playground.

The key distinction is:

- **Model/runtime**: executable code, FMUs, FMIL, dependencies, runner binaries.
- **Workflow**: orchestration YAML that defines which models run, in what order,
  and how inputs/outputs connect.

## 1. Bundled Image

Package runtime, models, and workflow YAML files into one GHCR image.

```text
GHCR image = runner + dependencies + FMUs/models + workflows
```

This is the current implementation.

Pros:

- Simple to understand and operate.
- One immutable artifact can describe a complete demo state.
- Argo only needs an image and a command.
- Good for early demos and integration testing.
- Fewer Playground-side objects to manage.

Cons:

- Any workflow-only change requires rebuilding and pushing a full image.
- A developer who wants to replace one workflow can accidentally replace all
  workflows in the image.
- Workflow authors need GHCR write access.
- Access control is coarse: image push permission can change workflows, models,
  runner code, dependencies, and packaged files.
- Slower workflow iteration because image build/push/pull is in the loop.

## 2. Runtime Image + Playground Workflow Store

Package runtime and models in GHCR, but publish workflow YAML separately to the
Playground, for example as ConfigMaps or another Kubernetes-side object.

```text
GHCR image = runner + dependencies + FMUs/models
Playground = workflow YAML definitions
```

Each workflow run can still be its own independent pod. The pod uses the common
runtime image and mounts or loads the selected workflow definition.

Pros:

- Workflow-only changes do not require GHCR publishing.
- A developer can update only the workflow they worked on.
- Better separation of responsibilities:
  - model/runtime maintainers publish images
  - workflow authors publish workflow definitions
  - dashboard users launch approved workflows
- Better access control and audit trails.
- Faster workflow iteration.
- Runtime image caching improves because the large image changes less often.

Cons:

- More implementation work.
- More Playground-side objects to manage.
- Reproducibility must record both runtime image tag and workflow version/hash.
- Requires validation so workflow definitions cannot mount unsafe secrets, use
  unapproved images, or bypass resource policies.

## 3. One Image Per Workflow

Build one image per workflow.

```text
ghcr.io/.../workflow-a:tag = runtime + models + workflow-a
ghcr.io/.../workflow-b:tag = runtime + models + workflow-b
```

Pros:

- Each workflow can be updated and rolled back independently.
- Still uses a simple image-based deployment model.
- Clear mapping from workflow to image.
- Shared Docker layers may reduce pull overhead if images share the same base.

Cons:

- Workflow-only changes still require GHCR publishing.
- Many images and tags to manage.
- Workflow authors still need registry access unless CI handles publishing.
- Runtime/model duplication can become operationally noisy.

## 4. Runtime Image + Workflow URL

Use a stable runtime image and pass each pod a workflow URL.

```text
cads-workflow-runner --workflow-url https://.../workflows/foo.yaml
```

Pros:

- Very flexible.
- No Kubernetes ConfigMap size limits.
- Workflows can live in object storage, Git, or an internal API.
- Workflow updates can be independent of runtime images.

Cons:

- Pods need network access to fetch workflow definitions.
- Requires authentication, integrity checks, and version pinning.
- Reproducibility depends on recording URL plus exact version/hash.
- More security-sensitive because execution config is fetched at runtime.

## 5. GitOps Workflow Repository

Keep workflows in a separate Git repository. CI/CD validates and publishes
approved workflows to the Playground.

Pros:

- Strong review workflow through pull requests.
- Clear audit trail.
- Workflow authors do not need direct cluster or GHCR access.
- CI can validate workflow syntax, allowed models, resource limits, and naming.
- Good production governance model.

Cons:

- Requires CI/CD or GitOps infrastructure.
- Slower than direct publish unless the pipeline is automated well.
- Adds another repository and release process.

## 6. Argo WorkflowTemplates

Publish each approved workflow as an Argo `WorkflowTemplate` or
`ClusterWorkflowTemplate`.

```text
Playground has approved templates.
Dashboard launches a selected template.
Each run becomes its own workflow/pod.
```

Pros:

- Native Argo concept.
- Good fit for "approved workflows users can launch."
- Each workflow definition is independently updateable.
- Kubernetes RBAC can control who updates which templates.
- Dashboard can list/launch approved templates.
- Compatible with one independent pod per workflow run.

Cons:

- Requires adapting dashboard and submission logic.
- Needs naming, versioning, and ownership conventions.
- RBAC and template permissions need careful setup.
- Runtime image and workflow template versions must both be recorded per run.

## 7. Hybrid: Approved Workflows + Experimental Images

Use a governed workflow publishing path for approved workflows, but allow
developers to publish temporary runtime images for experiments.

Pros:

- Keeps the normal user path stable.
- Supports fast experiments with changed runtime/model code.
- Avoids polluting the approved Playground image or workflow set.

Cons:

- Needs cleanup of temporary images and workflow objects.
- Requires clear naming conventions.
- More moving parts for developers.

## Recommendation

For the current demo, the bundled image approach is acceptable because it is
simple and already works.

For partner-facing or production-like use, the best next step is:

```text
Runtime/model image in GHCR
Approved workflows as Argo WorkflowTemplates in the Playground
```

That gives the right ownership model:

- STOR-HY partner has a new model: publish a new runtime/model image.
- Workflow developer changes orchestration: publish one workflow template.
- Dashboard user: connect to Playground and launch approved workflows.

For reproducibility, every run should record:

```text
runtime image tag or digest
workflow name
workflow version or hash
publisher/user
timestamp
```
