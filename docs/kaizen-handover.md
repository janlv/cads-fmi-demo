# CADS on the Kaizen OVH cloud: handover to Kaizen

This note describes what Kaizen needs to provide so that the CADS demo runtime
(`cads-fmi-demo`) can run against the STOR-HY database on the Kaizen OVH cloud.
It is written for Kaizen's platform engineers.

The split of responsibilities is:

- **Kaizen** runs the platform: OVH cloud, Kubernetes and Argo, S3 buckets,
  the PostgreSQL/TimescaleDB database, network access, secrets and
  authentication.
- **NORCE** owns the scientific part: the FMI models (FMUs), the workflow
  definitions, the runner, and the dashboard.

`cads-fmi-demo` is a demonstrator, not the final system. The models are
deterministic placeholders, not validated engineering models. The
architecture, however, is close to what we intend to build, so the
interfaces described here are the ones we want to keep. Section 6 lists the
places where Kaizen may prefer a different solution; most of them do not
require changes on the NORCE side.

## 1. How the local demo with a database works

Everything below runs on one laptop.

```text
 data/storhy/<site>/measurements/batch-*/     (metadata.yaml + CSV per channel)
        │
        │  scripts/ingest_measurements.py     (validate batch, insert rows)
        ▼
 TimescaleDB container  ─ database "storhy", one schema per site
   <site>.channels, <site>.measurements      (measured data)
   <site>.runs, <site>.run_outputs, <site>.run_series   (model results)
        ▲    │
        │    │ reads the input window, writes results
        │    ▼
 cads-workflow-runner (in a container)       (runs FMUs per workflow YAML)
        ▲
        │ starts runs ("Run assessment", or the ingest notification)
        │
 cads-workflow-service + browser dashboard   (reads history from the database)
```

The steps are:

1. `scripts/commands/run_db.sh` starts a `timescale/timescaledb` (PostgreSQL 16)
   container and ingests the bundled measurement batches. Each batch is a
   directory with a `metadata.yaml` (site, batch id, source, and per channel
   the unit, sampling interval and CSV file) and one `time,value` CSV per
   channel with RFC 3339 timestamps. A batch that fails validation is
   rejected as a whole.
2. The service runs with `CADS_DB_URL` set and `--execution local`, so it
   starts the runner itself instead of submitting to Argo.
3. A workflow declares database inputs per FMU variable
   (`input_series.db`: site and channel map) and a `data_window`. The runner
   uses these to read only the data that arrived since the previous
   successful run, and seeds the model state from that run's outputs
   (`carry_over`). If there is no new data, the run ends as `skipped` and
   writes nothing.
4. After every run (failed runs included) the runner writes the run record,
   scalar outputs and time series to `<site>.runs`, `run_outputs` and
   `run_series`.
5. The dashboard's Plots view reads the measurements and stored run results
   from the database and draws them.
6. `run_db.sh --next --notify` replays the next day of synthetic data. After
   loading, it calls the dashboard (`POST /api/sites/<site>/runs`), which runs
   that site's assessment workflows. This gives a continuous loop:
   ingest a day, assess it, plot the trend.

The demo currently has database-backed workflows for the Le Cheylas site
(`cheylas`): `monitoring/sediment_erosion_events.yaml` and
`maintenance/runner_rul_lockstep.yaml`. The Alqueva workflow still uses
bundled files.

## 2. What already runs on the Kaizen Playground

Without a database, the hosted path already works today:

- The runner image is published to GHCR (`ghcr.io/janlv/cads-fmi-demo:playground`)
  and the dashboard submits workflows to Argo in the `playground` namespace
  (`argoworkflows.cads.kzslab.dev`). It lists runs and reads results from the
  pod logs.
- The S3 secret `storhy-argo-artifacts-s3-credentials` (keys
  `access_key_id`, `secret_access_key`, `region`, `bucket_name`, `endpoint`)
  is projected into every workflow pod, so workflows can read inputs from S3.
- The generated Argo manifests **already** reference an optional secret
  `storhy-cads-db` (key `url`) and expose it as `CADS_DB_URL` in the runner
  pod. They also set `CADS_RUN_ID` to the Argo workflow name. While that
  secret is missing, runs fall back to bundled data and write nothing.

So the main thing missing on the platform side is a database plus that
secret. Sections 3 and 4 give the details.

## 3. What Kaizen needs to provide

| # | Item | Details |
| --- | --- | --- |
| 1 | PostgreSQL database | Version 14 or newer (the history query uses `date_bin`). The `timescaledb` extension is recommended but optional: the code turns `measurements` into a hypertable only when the extension exists, and uses only `create_hypertable`, which the open-source (Apache) edition includes. One database (for example `storhy`) with one schema per site. |
| 2 | Connection pooler | Transaction mode works; statement mode does not. See section 4. |
| 3 | Roles | A runtime role, an ingest role and a read-only dashboard role per site, or shared roles at first. Grants are in section 5. |
| 4 | Site schemas | Either let the roles create them (simplest), or create them in advance (section 5). |
| 5 | Kubernetes secret | `storhy-cads-db` in the `playground` namespace, key `url`, holding a libpq URL for the runtime role, for example `postgresql://cads_runtime:…@<pool-host>:<port>/storhy?sslmode=require&default_query_exec_mode=exec`. No manifest changes are needed. |
| 6 | Network access | From Argo workflow pods to the database (OVH IP allowlist or private network). Also from wherever the dashboard runs; see section 6.4. |
| 7 | Data ingestion | Measured data has to reach `<site>.channels` and `<site>.measurements`. Kaizen's pipeline can write them directly, or run our ingest script. See section 6.2. |
| 8 | Scheduling | Something has to start assessment runs when new data arrives. See section 6.3. |

TLS: `sslmode=require` works with the current image. `sslmode=verify-full`
also needs the OVH CA certificate inside the image (we can add it) and
`sslrootcert=<path>` in the URL.

## 4. Connection pooling: transaction mode

PgBouncer, which OVH's managed PostgreSQL uses for connection pools, has
three modes: session, transaction and statement. **Transaction mode is fine
for this code, with one URL option.**

What we checked in the runtime:

- No session state is used: no `SET`, no `search_path`, no `LISTEN/NOTIFY`,
  no temporary tables. All tables are schema-qualified (`cheylas.runs`).
- The only lock is transaction-scoped (`pg_advisory_xact_lock`, used while
  creating a site schema).
- Multi-statement writes, including `COPY` of results, run inside a single
  transaction. That works in transaction mode. **Statement mode would break
  it**, because statement mode forbids multi-statement transactions.

The one catch is **prepared statements**:

- **Runner and dashboard (Go, pgx v5).** By default pgx caches named prepared
  statements on each server connection. Behind PgBouncer older than 1.21, or
  with `max_prepared_statements = 0`, this fails with errors like
  `prepared statement "stmtcache_…" does not exist`. Add
  `default_query_exec_mode=exec` to the URL. This is a client-side option and
  needs no code change. Our integration tests run with it.
- **Ingest script (Python, psycopg 3).** psycopg prepares a statement after it
  has run 5 times, and the ingest inserts row by row. Behind a
  transaction-mode pooler it must connect with `prepare_threshold=None`.
  NORCE will add this option if the script is used against the pooler.
  Ingestion can also connect to the database directly, bypassing the
  pooler.

Each runner pod opens at most 4 connections and only during its run. The
dashboard service also opens at most 4.

## 5. Database layout and grants

One schema per site (`cheylas`, `alqueva`, …). The full DDL is
`SchemaStatements` in
[`orchestrator/service/internal/cadsdb/pg.go`](../orchestrator/service/internal/cadsdb/pg.go).
For one site it is:

```sql
CREATE SCHEMA IF NOT EXISTS cheylas;

CREATE TABLE IF NOT EXISTS cheylas.channels (
  channel text PRIMARY KEY,
  unit text NOT NULL,
  source text NOT NULL,
  sampling_interval_s double precision NOT NULL,
  batch text NOT NULL,
  ingested_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE IF NOT EXISTS cheylas.measurements (
  time timestamptz NOT NULL,
  channel text NOT NULL REFERENCES cheylas.channels(channel),
  value double precision NOT NULL,
  PRIMARY KEY (time, channel)
);
CREATE TABLE IF NOT EXISTS cheylas.runs (
  run_id text PRIMARY KEY,
  workflow_path text NOT NULL,
  workflow_sha256 text,
  runner_version text,
  status text NOT NULL,
  started_at timestamptz,
  finished_at timestamptz,
  wall_seconds double precision,
  simulated_seconds double precision,
  provenance jsonb NOT NULL
);
CREATE TABLE IF NOT EXISTS cheylas.run_outputs (
  run_id text REFERENCES cheylas.runs(run_id) ON DELETE CASCADE,
  step text NOT NULL,
  name text NOT NULL,
  value double precision,
  value_text text,
  PRIMARY KEY (run_id, step, name)
);
CREATE TABLE IF NOT EXISTS cheylas.run_series (
  run_id text REFERENCES cheylas.runs(run_id) ON DELETE CASCADE,
  step text NOT NULL,
  signal text NOT NULL,
  t double precision NOT NULL,
  value double precision,
  PRIMARY KEY (run_id, step, signal, t)
);

-- With TimescaleDB:
SELECT create_hypertable('cheylas.measurements', 'time', if_not_exists => true);
```

**Who creates the schema.** The runner checks that the five tables exist
before it writes. If any is missing, it runs the DDL above, which requires
`CREATE` rights on the database. If all tables already exist, it runs no DDL.
Kaizen therefore has two options:

- **Kaizen creates the schemas** (recommended for production), for example
  through its own migrations. The runtime role then needs only the grants
  below. This is covered by an integration test
  (`TestPostgresLeastPrivilegeWriter`).
- **The runtime role owns the database or schemas** and creates them on first
  use (simplest for a test environment).

**Grants per site**, with schemas created in advance (example for `cheylas`):

```sql
-- Runtime (runner pods): read inputs and previous runs, write results.
-- A rerun under the same run id first deletes the old record, hence DELETE.
GRANT USAGE ON SCHEMA cheylas TO cads_runtime;
GRANT SELECT ON cheylas.channels, cheylas.measurements TO cads_runtime;
GRANT SELECT, INSERT, DELETE ON cheylas.runs, cheylas.run_outputs, cheylas.run_series TO cads_runtime;

-- Ingestion: upsert channel metadata, append measurements.
GRANT USAGE ON SCHEMA cheylas TO cads_ingest;
GRANT SELECT, INSERT, UPDATE ON cheylas.channels TO cads_ingest;
GRANT SELECT, INSERT ON cheylas.measurements TO cads_ingest;

-- Dashboard: read only.
GRANT USAGE ON SCHEMA cheylas TO cads_dashboard;
GRANT SELECT ON ALL TABLES IN SCHEMA cheylas TO cads_dashboard;
```

Per-site schemas are meant to support per-demonstrator authorisation later,
for example a role that can only see one site.

**Data contract for ingestion.** Whatever loads the data must keep these
rules:

- One row in `<site>.channels` per channel, written before its measurements.
  The `channel` name is what the workflow YAML refers to (for example
  `turbidity_g_l`).
- `<site>.measurements` holds UTC timestamps and one value per
  (time, channel). Duplicate rows are ignored (`ON CONFLICT DO NOTHING`).
- Data should arrive in time order per channel. The runner assesses the
  window from the end of the previous successful run up to the newest
  sample. Late data that falls before an already-assessed window is stored
  but never assessed.

## 6. Alternatives Kaizen may prefer

The NORCE side depends only on the interfaces above: the per-site tables, a
`CADS_DB_URL` in the runner pod, Argo for scheduling, and the result JSON on
the runner's stdout. Most platform choices can change without touching the
runtime.

### 6.1 Database

| Option | Effect on NORCE side |
| --- | --- |
| OVH managed PostgreSQL with TimescaleDB (assumed) | None. |
| Plain PostgreSQL without TimescaleDB | None. `measurements` stays a normal table; fine for demo volumes. |
| One database per site instead of one schema per site | The schema name is still the site id. Each site needs its own URL, which needs a small change (the runner currently takes one URL). |
| Kaizen-managed migrations instead of runtime DDL | Supported (section 5). Kaizen must apply NORCE's schema changes when we add tables or columns; we will announce these. |

### 6.2 Ingestion (bucket to database)

| Option | Notes |
| --- | --- |
| **A. Kaizen's pipeline writes the tables directly** | Most natural if Kaizen already processes the plant data. Must follow the data contract in section 5. |
| B. Our `scripts/ingest_measurements.py` runs as a Kubernetes job or Argo workflow | Reads the batch format described in section 1. Needs the S3 batch layout agreed, plus the pooler fix from section 4. It currently creates the schema itself, so it needs `CREATE` rights or the same existence check as the runner. |
| C. Kaizen writes batches to S3, and NORCE ingests them | Keeps the database write in one place on our side, but adds a hop. |

### 6.3 What triggers assessment runs

Locally the ingest script notifies the dashboard, which starts the runs. In
the cloud the ingestion cannot reach a dashboard on someone's laptop, so this
needs a different trigger:

| Option | Notes |
| --- | --- |
| **A. Argo CronWorkflow per assessment workflow** (recommended to start) | Simplest. A run without new data ends as `skipped`, exits 0 and writes nothing, so polling every 15–60 minutes costs little. NORCE adds a CronWorkflow variant to the manifest generator. |
| B. Argo Events: S3 bucket notification or webhook from the ingestion | Runs start as soon as data lands. Needs Argo Events installed and an event source per site. |
| C. Ingestion calls the dashboard service API (`POST /api/sites/<site>/runs`) | Works as it does locally, but only if the dashboard runs in the cluster (6.4). |

Runs may overlap. Two runs of the same workflow started at the same time
can assess the same window. For the demo this is acceptable. If Kaizen
chooses B or C, Argo's `synchronization` (a mutex per workflow) would
prevent it.

### 6.4 Where the dashboard runs

The dashboard reads the database directly (Plots view, data status, stored
results) and calls the Argo server to list and submit runs.

| Option | Notes |
| --- | --- |
| A. On each user's laptop (current) | Each user needs Argo credentials (the Kaizen kubeconfig) and network access to the database with a read-only URL. Hard to scale beyond a few NORCE users. |
| **B. Hosted in the cluster behind Kaizen's authentication** (recommended) | One deployment of `cads-workflow-service --serve` from the same image, behind an OAuth/OIDC proxy or ingress authentication. It uses a service account with Argo submit/list rights in `playground` and the read-only database URL. The dashboard has no login of its own, so the proxy must provide it. |

### 6.5 Secrets, images and registries

- **Secrets.** A plain Kubernetes secret is enough. If Kaizen uses External
  Secrets or Vault, it only needs to produce a secret named `storhy-cads-db`
  with key `url`. A different name is a one-line change on the NORCE side
  (a constant in the manifest generator and in the dashboard).
- **Image registry.** The image is on GHCR today. Moving it to an OVH or
  Kaizen registry only means changing the image name in
  `config/playground.env` and providing an image pull secret if the registry
  is private.
- **S3.** Unchanged from today.

## 7. NORCE to-dos and verification

On the NORCE side:

1. Publish an image that contains the database code (`./run_publish.sh`).
   The current `:playground` image predates it.
2. Add `prepare_threshold=None` support to the ingest script if it is used
   (6.2 B).
3. Add a CronWorkflow variant to `scripts/generate_remote_workflow.sh` (6.3 A).
4. If the dashboard is hosted (6.4 B), provide a Deployment/Service manifest.

Once Kaizen has created the database and the `storhy-cads-db` secret, we
check the setup as follows:

1. Ingest one Le Cheylas batch (`data/storhy/cheylas/measurements/batch-2026-05-20/`).
2. Submit `workflows/demonstrators/cheylas/monitoring/sediment_erosion_events.yaml`
   to the Playground.
3. In the run's `_run` record, `data_window.resolved` is `true`, the inputs
   show `source: db`, and `results_sink.status` is `written`.
4. A row with the Argo workflow name as `run_id` appears in `cheylas.runs`.
5. A second submission without new data ends as `skipped`.

## 8. Open questions for Kaizen

1. Which PostgreSQL version and pooler (PgBouncer version,
   `max_prepared_statements`) does the OVH database use, and is TimescaleDB
   available?
2. Does Kaizen want to own the schema (migrations) and roles, or let the
   runtime role create site schemas?
3. Will Kaizen's pipeline write measurements to the tables directly (6.2 A),
   or deliver batches for NORCE to ingest?
4. Which trigger fits Kaizen's platform: CronWorkflow, Argo Events or an API
   call?
5. Can Kaizen host the dashboard behind its authentication?
6. How do the Argo pods reach the database (private network or allowlist),
   and should NORCE laptops reach it for development?
