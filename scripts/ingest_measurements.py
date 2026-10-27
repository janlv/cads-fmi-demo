#!/usr/bin/env python3
"""Validate measurement batches and load them into the STOR-HY database.

A batch is a directory holding ``metadata.yaml`` plus one ``time,value`` CSV per
channel (RFC3339 timestamps). Every batch is validated as a whole before
anything is written (D3.5 ARCH-DATA-004): missing metadata keys, empty units,
missing files, a wrong CSV header, non-monotonic time, non-numeric values or a
cadence that disagrees with ``sampling_interval_s`` by more than 1 % reject the
whole batch with a logged reason.

Accepted batches land in a per-demonstrator PostgreSQL/TimescaleDB schema named
after the site id (ARCH-DATA-001/007)::

    <site>.channels(channel, unit, source, sampling_interval_s, batch, ingested_at)
    <site>.measurements(time, channel, value)   -- hypertable when TimescaleDB is present

Re-ingesting a batch is idempotent: channels are upserted and measurement rows
that already exist are skipped.

With ``--notify URL`` (default ``$CADS_DASHBOARD_URL``), every site that got new
rows triggers one ``POST {URL}/api/sites/{site}/runs`` on the dashboard service
after loading, so assessments follow new data without a click. Notification
failures are warnings only and never change the exit code.

Exit codes: 0 all batches loaded (or validated with --dry-run), 2 at least one
batch rejected, 1 usage or connection error.
"""

from __future__ import annotations

import argparse
import csv
import math
import os
import re
import json
import sys
import urllib.error
import urllib.request
from datetime import datetime, timezone
from pathlib import Path

EXIT_OK = 0
EXIT_ERROR = 1
EXIT_REJECTED = 2

REQUIRED_BATCH_KEYS = ("site", "batch", "source", "channels")
REQUIRED_CHANNEL_KEYS = ("channel", "unit", "sampling_interval_s", "file")
IDENTIFIER_RE = re.compile(r"^[a-z][a-z0-9_]{0,62}$")
CADENCE_TOLERANCE = 0.01
NOTIFY_TIMEOUT_S = 10.0


class BatchRejected(Exception):
    """Raised when a batch fails validation; the message is the reason."""


def log(level: str, message: str) -> None:
    stream = sys.stderr if level in ("warn", "error") else sys.stdout
    print(f"[{level}] {message}", file=stream, flush=True)


def parse_args(argv=None) -> argparse.Namespace:
    parser = argparse.ArgumentParser(
        description="Validate measurement batches and load them into the per-site schema of the STOR-HY database.",
        epilog="Example: scripts/ingest_measurements.py --batch data/storhy/cheylas/measurements/batch-2026-05-20",
    )
    parser.add_argument(
        "--db-url",
        default=os.environ.get("CADS_DB_URL"),
        help="PostgreSQL URL, e.g. postgres://cads:cads@127.0.0.1:5432/storhy (default: $CADS_DB_URL).",
    )
    parser.add_argument(
        "--batch",
        action="append",
        required=True,
        type=Path,
        metavar="DIR",
        help="Batch directory containing metadata.yaml (repeatable).",
    )
    parser.add_argument("--site", help="Override the site id (database schema) from metadata.yaml.")
    parser.add_argument("--dry-run", action="store_true", help="Validate only; do not connect to the database.")
    parser.add_argument(
        "--notify",
        metavar="URL",
        default=os.environ.get("CADS_DASHBOARD_URL") or None,
        help="Dashboard service base URL; after loading, POST {URL}/api/sites/{site}/runs once per site that "
        "got new rows (default: $CADS_DASHBOARD_URL; unset = no notification; never on --dry-run).",
    )
    return parser.parse_args(argv)


def load_yaml(path: Path):
    try:
        import yaml
    except ImportError as exc:  # pragma: no cover - environment problem
        raise SystemExit("PyYAML is required: pip install pyyaml") from exc
    with open(path) as fh:
        return yaml.safe_load(fh)


def parse_time(text: str) -> datetime:
    value = text.strip()
    if value.endswith("Z"):
        value = value[:-1] + "+00:00"
    parsed = datetime.fromisoformat(value)
    if parsed.tzinfo is None:
        raise ValueError("timestamp has no UTC offset")
    return parsed.astimezone(timezone.utc)


def validate_channel_file(path: Path, channel: str, interval_s: float):
    rows = []
    with open(path, newline="") as fh:
        reader = csv.reader(fh)
        header = next(reader, None)
        if header is None or [h.strip() for h in header] != ["time", "value"]:
            raise BatchRejected(f"{path.name}: header must be 'time,value', got {header!r}")
        previous = None
        for line_no, record in enumerate(reader, start=2):
            if not record or all(not cell.strip() for cell in record):
                continue
            if len(record) != 2:
                raise BatchRejected(f"{path.name}:{line_no}: expected 2 columns, got {len(record)}")
            try:
                stamp = parse_time(record[0])
            except ValueError as exc:
                raise BatchRejected(f"{path.name}:{line_no}: invalid RFC3339 time {record[0]!r} ({exc})") from None
            try:
                value = float(record[1])
            except ValueError:
                raise BatchRejected(f"{path.name}:{line_no}: non-numeric value {record[1]!r}") from None
            if not math.isfinite(value):
                raise BatchRejected(f"{path.name}:{line_no}: non-finite value {record[1]!r}")
            if previous is not None:
                gap = (stamp - previous).total_seconds()
                if gap <= 0:
                    raise BatchRejected(f"{path.name}:{line_no}: time is not strictly increasing")
                if abs(gap - interval_s) > CADENCE_TOLERANCE * interval_s:
                    raise BatchRejected(
                        f"{path.name}:{line_no}: cadence {gap:g} s differs from sampling_interval_s "
                        f"{interval_s:g} s by more than {CADENCE_TOLERANCE:.0%}"
                    )
            previous = stamp
            rows.append((stamp, channel, value))
    if not rows:
        raise BatchRejected(f"{path.name}: no data rows")
    return rows


def validate_batch(batch_dir: Path, site_override: str | None):
    """Return a validated batch dict or raise BatchRejected."""
    meta_path = batch_dir / "metadata.yaml"
    if not batch_dir.is_dir():
        raise BatchRejected(f"{batch_dir} is not a directory")
    if not meta_path.is_file():
        raise BatchRejected(f"missing {meta_path.name}")
    try:
        meta = load_yaml(meta_path)
    except Exception as exc:  # yaml.YAMLError and friends
        raise BatchRejected(f"cannot parse {meta_path.name}: {exc}") from None
    if not isinstance(meta, dict):
        raise BatchRejected(f"{meta_path.name} must be a mapping")
    for key in REQUIRED_BATCH_KEYS:
        if meta.get(key) in (None, "", []):
            raise BatchRejected(f"metadata is missing required key '{key}'")
    site = str(site_override or meta["site"]).strip()
    if not IDENTIFIER_RE.match(site):
        raise BatchRejected(f"site id {site!r} is not a valid schema name (lowercase letters, digits, _)")
    channels = meta["channels"]
    if not isinstance(channels, list):
        raise BatchRejected("metadata 'channels' must be a list")

    seen = set()
    validated = []
    for index, entry in enumerate(channels):
        if not isinstance(entry, dict):
            raise BatchRejected(f"channels[{index}] must be a mapping")
        for key in REQUIRED_CHANNEL_KEYS:
            if key not in entry or entry[key] is None or str(entry[key]).strip() == "":
                label = entry.get("channel", f"channels[{index}]")
                raise BatchRejected(f"channel {label}: missing or empty '{key}'")
        name = str(entry["channel"]).strip()
        if not IDENTIFIER_RE.match(name):
            raise BatchRejected(f"channel name {name!r} must be lowercase letters, digits and _")
        if name in seen:
            raise BatchRejected(f"channel {name} is listed twice")
        seen.add(name)
        try:
            interval = float(entry["sampling_interval_s"])
        except (TypeError, ValueError):
            raise BatchRejected(f"channel {name}: sampling_interval_s must be a number") from None
        if not math.isfinite(interval) or interval <= 0:
            raise BatchRejected(f"channel {name}: sampling_interval_s must be positive")
        file_path = batch_dir / str(entry["file"])
        if file_path.resolve().parent != batch_dir.resolve():
            raise BatchRejected(f"channel {name}: file must live inside the batch directory")
        if not file_path.is_file():
            raise BatchRejected(f"channel {name}: file {entry['file']} does not exist")
        rows = validate_channel_file(file_path, name, interval)
        validated.append(
            {"channel": name, "unit": str(entry["unit"]).strip(), "interval": interval, "rows": rows}
        )

    return {
        "dir": batch_dir,
        "site": site,
        "batch": str(meta["batch"]).strip(),
        "source": str(meta["source"]).strip(),
        "channels": validated,
    }


def ensure_schema(conn, site: str) -> None:
    from psycopg import sql

    schema = sql.Identifier(site)
    with conn.cursor() as cur:
        cur.execute(sql.SQL("create schema if not exists {}").format(schema))
        cur.execute(
            sql.SQL(
                "create table if not exists {}.channels ("
                " channel text primary key,"
                " unit text not null,"
                " source text not null,"
                " sampling_interval_s double precision not null,"
                " batch text not null,"
                " ingested_at timestamptz not null default now())"
            ).format(schema)
        )
        cur.execute(
            sql.SQL(
                "create table if not exists {}.measurements ("
                " time timestamptz not null,"
                " channel text not null references {}.channels(channel),"
                " value double precision not null,"
                " primary key (time, channel))"
            ).format(schema, schema)
        )
        cur.execute("select 1 from pg_extension where extname = 'timescaledb'")
        if cur.fetchone():
            cur.execute(
                "select create_hypertable(%s, 'time', if_not_exists => true)",
                (f"{site}.measurements",),
            )


def load_batch(conn, batch) -> dict:
    from psycopg import sql

    site = batch["site"]
    schema = sql.Identifier(site)
    summary = {}
    with conn.transaction():
        ensure_schema(conn, site)
        with conn.cursor() as cur:
            for channel in batch["channels"]:
                cur.execute(
                    sql.SQL(
                        "insert into {}.channels (channel, unit, source, sampling_interval_s, batch)"
                        " values (%s, %s, %s, %s, %s)"
                        " on conflict (channel) do update set unit = excluded.unit, source = excluded.source,"
                        " sampling_interval_s = excluded.sampling_interval_s, batch = excluded.batch,"
                        " ingested_at = now()"
                    ).format(schema),
                    (channel["channel"], channel["unit"], batch["source"], channel["interval"], batch["batch"]),
                )
                inserted = 0
                insert = sql.SQL(
                    "insert into {}.measurements (time, channel, value) values (%s, %s, %s)"
                    " on conflict do nothing"
                ).format(schema)
                for row in channel["rows"]:
                    cur.execute(insert, row)
                    inserted += cur.rowcount if cur.rowcount and cur.rowcount > 0 else 0
                summary[channel["channel"]] = inserted
    return summary


def _count(value) -> int:
    if isinstance(value, list):
        return len(value)
    if isinstance(value, bool):
        return int(value)
    if isinstance(value, int):
        return value
    return 0


def notify_sites(url, new_rows_by_site, timeout=NOTIFY_TIMEOUT_S) -> int:
    """POST ``{url}/api/sites/{site}/runs`` for every site with new rows.

    Returns the number of sites notified successfully. Failures are logged as
    warnings and never raised.
    """
    if not url:
        return 0
    base = url.rstrip("/")
    notified = 0
    for site in sorted(new_rows_by_site):
        if new_rows_by_site[site] <= 0:
            continue
        endpoint = f"{base}/api/sites/{site}/runs"
        request = urllib.request.Request(endpoint, data=b"", method="POST")
        try:
            with urllib.request.urlopen(request, timeout=timeout) as response:
                body = response.read()
        except urllib.error.HTTPError as exc:
            log("warn", f"notify {endpoint} failed: HTTP {exc.code} {exc.reason}")
            continue
        except (urllib.error.URLError, OSError, ValueError) as exc:
            reason = getattr(exc, "reason", exc)
            log("warn", f"notify {endpoint} failed: {reason}")
            continue
        runs = up_to_date = 0
        try:
            payload = json.loads(body.decode("utf-8")) if body else {}
        except (UnicodeDecodeError, ValueError):
            payload = {}
        if isinstance(payload, dict):
            runs = _count(payload.get("runs"))
            up_to_date = _count(payload.get("upToDate"))
        notified += 1
        log("ok", f"notified {base} for site {site}: {runs} run(s) started, {up_to_date} up to date")
    return notified


def describe(batch, inserted=None) -> None:
    log("info", f"batch {batch['batch']} -> schema {batch['site']} ({batch['dir']})")
    for channel in batch["channels"]:
        rows = channel["rows"]
        first = rows[0][0].strftime("%Y-%m-%dT%H:%M:%SZ")
        last = rows[-1][0].strftime("%Y-%m-%dT%H:%M:%SZ")
        extra = ""
        if inserted is not None:
            new = inserted.get(channel["channel"], 0)
            extra = f", {new} new, {len(rows) - new} already present"
        log(
            "info",
            f"  {channel['channel']} [{channel['unit']}] every {channel['interval']:g} s: "
            f"{len(rows)} rows {first} .. {last}{extra}",
        )


def main(argv=None) -> int:
    args = parse_args(argv)
    if not args.dry_run and not args.db_url:
        log("error", "no database configured: pass --db-url or set CADS_DB_URL (or use --dry-run)")
        return EXIT_ERROR

    accepted = []
    rejected = 0
    for batch_dir in args.batch:
        try:
            accepted.append(validate_batch(batch_dir, args.site))
        except BatchRejected as exc:
            rejected += 1
            log("error", f"batch {batch_dir} rejected: {exc}")

    if args.dry_run:
        for batch in accepted:
            describe(batch)
            log("ok", f"batch {batch['batch']} is valid (dry run, nothing written)")
        return EXIT_REJECTED if rejected else EXIT_OK

    new_rows_by_site = {}
    if accepted:
        try:
            import psycopg
        except ImportError:
            log("error", "psycopg is required: pip install 'psycopg[binary]'")
            return EXIT_ERROR
        try:
            with psycopg.connect(args.db_url) as conn:
                for batch in accepted:
                    inserted = load_batch(conn, batch)
                    describe(batch, inserted)
                    log("ok", f"batch {batch['batch']} loaded into {batch['site']}.measurements")
                    site = batch["site"]
                    new_rows_by_site[site] = new_rows_by_site.get(site, 0) + sum(inserted.values())
        except psycopg.Error as exc:
            log("error", f"database error: {exc}")
            notify_sites(args.notify, new_rows_by_site)
            return EXIT_ERROR

    notify_sites(args.notify, new_rows_by_site)

    if rejected:
        log("error", f"{rejected} batch(es) rejected, {len(accepted)} loaded")
        return EXIT_REJECTED
    return EXIT_OK


if __name__ == "__main__":
    sys.exit(main())
