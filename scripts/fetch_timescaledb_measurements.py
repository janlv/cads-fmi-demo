#!/usr/bin/env python3
"""
Pulls the most recent measurement rows from a TimescaleDB/PostgreSQL instance
and renders them as a CSV that the Producer FMU already understands.
"""

from __future__ import annotations

import argparse
import csv
import os
import sys
from pathlib import Path
from typing import Iterable, Sequence, Tuple

import psycopg
from psycopg import sql


def _env_default(name: str, fallback: str) -> str:
    value = os.environ.get(name)
    return value if value else fallback


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(
        description="Download measurement points from TimescaleDB "
        "and store them under data/ so FMUs can consume them."
    )
    parser.add_argument(
        "--output",
        default=_env_default("TIMESCALE_OUTPUT", "data/measurements.csv"),
        help="Target CSV path (default: %(default)s or $TIMESCALE_OUTPUT).",
    )
    parser.add_argument(
        "--table",
        default=_env_default("TIMESCALE_TABLE", "public.measurements"),
        help="Schema-qualified table name (default: %(default)s or $TIMESCALE_TABLE).",
    )
    parser.add_argument(
        "--time-column",
        default=_env_default("TIMESCALE_TIME_COLUMN", "time"),
        help="Timestamp column name (default: %(default)s or $TIMESCALE_TIME_COLUMN).",
    )
    parser.add_argument(
        "--value-column",
        default=_env_default("TIMESCALE_VALUE_COLUMN", "value"),
        help="Value column name (default: %(default)s or $TIMESCALE_VALUE_COLUMN).",
    )
    parser.add_argument(
        "--limit",
        type=int,
        default=int(_env_default("TIMESCALE_LIMIT", "1000")),
        help="Number of latest rows to pull (default: %(default)s or $TIMESCALE_LIMIT).",
    )
    parser.add_argument(
        "--conninfo",
        default=os.environ.get("TIMESCALE_CONN"),
        help="psql-style conninfo string. When omitted, the host/user/password/"
        "database flags (or corresponding environment variables) are used.",
    )
    parser.add_argument(
        "--host",
        default=os.environ.get("TIMESCALE_HOST"),
        help="Database host when --conninfo is not provided.",
    )
    parser.add_argument(
        "--port",
        type=int,
        default=int(_env_default("TIMESCALE_PORT", "5432")),
        help="Database port when --conninfo is not provided.",
    )
    parser.add_argument(
        "--database",
        default=os.environ.get("TIMESCALE_DB"),
        help="Database name when --conninfo is not provided.",
    )
    parser.add_argument(
        "--user",
        default=os.environ.get("TIMESCALE_USER"),
        help="Database user when --conninfo is not provided.",
    )
    parser.add_argument(
        "--password",
        default=os.environ.get("TIMESCALE_PASSWORD"),
        help="Database password when --conninfo is not provided.",
    )
    parser.add_argument(
        "--sslmode",
        default=os.environ.get("TIMESCALE_SSLMODE", "require"),
        help="SSL mode passed to libpq (default: %(default)s or $TIMESCALE_SSLMODE).",
    )
    return parser.parse_args()


def main() -> int:
    args = parse_args()
    if args.limit <= 0:
        print("[timescale] --limit must be positive", file=sys.stderr)
        return 2

    conninfo = args.conninfo or build_conninfo(args)
    if not conninfo:
        print(
            "[timescale] Missing --conninfo or host/user/password/database arguments.",
            file=sys.stderr,
        )
        return 2

    table_sql = build_table_identifier(args.table)
    time_identifier = sql.Identifier(args.time_column)
    value_identifier = sql.Identifier(args.value_column)

    print("[timescale] Connecting to database…", file=sys.stderr)
    try:
        with psycopg.connect(conninfo, autocommit=True) as conn:
            rows = fetch_rows(
                conn,
                table_sql,
                time_identifier,
                value_identifier,
                args.limit,
            )
    except Exception as exc:  # pragma: no cover - logged by caller
        print(f"[timescale] Query failed: {exc}", file=sys.stderr)
        return 1

    if not rows:
        print("[timescale] No rows returned; nothing to write.", file=sys.stderr)
        return 0

    write_csv(args.output, rows)
    print(
        f"[timescale] Wrote {len(rows)} rows to {args.output}",
        file=sys.stderr,
    )
    return 0


def build_conninfo(args: argparse.Namespace) -> str | None:
    required = ("host", "database", "user", "password")
    missing = [field for field in required if not getattr(args, field)]
    if missing:
        print(
            "[timescale] Missing connection arguments: "
            + ", ".join(missing),
            file=sys.stderr,
        )
        return None

    parts = [
        f"host={args.host}",
        f"port={args.port}",
        f"dbname={args.database}",
        f"user={args.user}",
        f"password={args.password}",
    ]
    if args.sslmode:
        parts.append(f"sslmode={args.sslmode}")
    return " ".join(parts)


def build_table_identifier(identifier: str) -> sql.Composed:
    parts = [part.strip() for part in identifier.split(".") if part.strip()]
    if not parts:
        raise ValueError("table identifier cannot be empty")
    identifiers = [sql.Identifier(part) for part in parts]
    if len(identifiers) == 1:
        return identifiers[0]
    return sql.SQL(".").join(identifiers)


def fetch_rows(
    conn: psycopg.Connection,
    table_sql: sql.Composed,
    time_column: sql.Identifier,
    value_column: sql.Identifier,
    limit: int,
) -> Sequence[Tuple[object, object]]:
    stmt = (
        sql.SQL(
            "SELECT {time_col}, {value_col} "
            "FROM {table} "
            "ORDER BY {time_col} DESC "
            "LIMIT %s"
        )
        .format(
            time_col=time_column,
            value_col=value_column,
            table=table_sql,
        )
    )
    with conn.cursor() as cur:
        cur.execute(stmt, (limit,))
        rows = cur.fetchall()
    # Query sorted newest-first; reverse so CSV is chronological.
    rows.reverse()
    return rows


def write_csv(path: str, rows: Iterable[Tuple[object, object]]) -> None:
    target = Path(path)
    target.parent.mkdir(parents=True, exist_ok=True)
    with target.open("w", newline="") as handle:
        writer = csv.writer(handle)
        writer.writerow(["timestamp", "value"])
        for timestamp, value in rows:
            writer.writerow([serialize_value(timestamp), serialize_value(value)])


def serialize_value(value: object) -> str:
    if hasattr(value, "isoformat"):
        return value.isoformat()  # datetime/date from psycopg is nice already
    return str(value)


if __name__ == "__main__":
    sys.exit(main())
