"""Tests for the dashboard notification in scripts/ingest_measurements.py (stdlib only)."""

import contextlib
import io
import json
import socket
import threading
import unittest
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

from _loader import load_script

ingest = load_script("scripts/ingest_measurements.py", "ingest_measurements")


class _Recorder:
    def __init__(self, status=200, body=None):
        self.status = status
        self.body = body if body is not None else {"site": "x", "runs": [{"name": "a"}, {"name": "b"}]}
        self.requests = []


def _serve(recorder):
    class Handler(BaseHTTPRequestHandler):
        def do_POST(self):  # noqa: N802 - http.server API
            length = int(self.headers.get("Content-Length") or 0)
            body = self.rfile.read(length) if length else b""
            recorder.requests.append((self.command, self.path, body))
            payload = json.dumps(recorder.body).encode()
            self.send_response(recorder.status)
            self.send_header("Content-Type", "application/json")
            self.send_header("Content-Length", str(len(payload)))
            self.end_headers()
            self.wfile.write(payload)

        def log_message(self, *args):
            pass

    server = ThreadingHTTPServer(("127.0.0.1", 0), Handler)
    thread = threading.Thread(target=server.serve_forever, daemon=True)
    thread.start()
    return server, f"http://127.0.0.1:{server.server_address[1]}"


def _unused_port():
    with socket.socket() as sock:
        sock.bind(("127.0.0.1", 0))
        return sock.getsockname()[1]


def _quiet(func, *args, **kwargs):
    out, err = io.StringIO(), io.StringIO()
    with contextlib.redirect_stdout(out), contextlib.redirect_stderr(err):
        result = func(*args, **kwargs)
    return result, out.getvalue(), err.getvalue()


class NotifySitesTests(unittest.TestCase):
    def setUp(self):
        self.recorder = _Recorder()
        self.server, self.url = _serve(self.recorder)

    def tearDown(self):
        self.server.shutdown()
        self.server.server_close()

    def test_notifies_once_per_site_with_new_rows(self):
        count, out, err = _quiet(ingest.notify_sites, self.url + "/", {"cheylas": 576, "alqueva": 12, "idle": 0})
        self.assertEqual(count, 2)
        self.assertEqual(
            sorted(path for _, path, _ in self.recorder.requests),
            ["/api/sites/alqueva/runs", "/api/sites/cheylas/runs"],
        )
        for method, _, body in self.recorder.requests:
            self.assertEqual(method, "POST")
            self.assertEqual(body, b"")
        self.assertIn(f"[ok] notified {self.url} for site cheylas: 2 run(s) started, 0 up to date", out)
        self.assertEqual(err, "")

    def test_reports_up_to_date_entries(self):
        self.recorder.body = {"runs": [], "upToDate": [{"workflow": "a"}, {"workflow": "b"}]}
        _, out, _ = _quiet(ingest.notify_sites, self.url, {"cheylas": 1})
        self.assertIn("0 run(s) started, 2 up to date", out)

    def test_nothing_for_zero_rows_or_no_url(self):
        count, _, _ = _quiet(ingest.notify_sites, self.url, {"cheylas": 0})
        self.assertEqual(count, 0)
        count, _, _ = _quiet(ingest.notify_sites, "", {"cheylas": 10})
        self.assertEqual(count, 0)
        count, _, _ = _quiet(ingest.notify_sites, None, {"cheylas": 10})
        self.assertEqual(count, 0)
        self.assertEqual(self.recorder.requests, [])

    def test_http_error_only_warns(self):
        self.recorder.status = 500
        count, out, err = _quiet(ingest.notify_sites, self.url, {"cheylas": 5})
        self.assertEqual(count, 0)
        self.assertEqual(len(self.recorder.requests), 1)
        self.assertIn("[warn]", err)
        self.assertIn("HTTP 500", err)
        self.assertNotIn("[ok]", out)

    def test_non_json_body_is_tolerated(self):
        self.recorder.body = "not an object"
        count, out, _ = _quiet(ingest.notify_sites, self.url, {"cheylas": 5})
        self.assertEqual(count, 1)
        self.assertIn("0 run(s) started, 0 up to date", out)


class NotifyFailureTests(unittest.TestCase):
    def test_connection_refused_only_warns(self):
        url = f"http://127.0.0.1:{_unused_port()}"
        count, _, err = _quiet(ingest.notify_sites, url, {"cheylas": 5}, timeout=2.0)
        self.assertEqual(count, 0)
        self.assertIn("[warn] notify", err)


class MainNotifyTests(unittest.TestCase):
    def setUp(self):
        self.recorder = _Recorder()
        self.server, self.url = _serve(self.recorder)

    def tearDown(self):
        self.server.shutdown()
        self.server.server_close()

    def test_no_notification_on_dry_run_or_rejected_batch(self):
        code, _, err = _quiet(
            ingest.main, ["--dry-run", "--notify", self.url, "--batch", "/nonexistent/batch-2026-05-21"]
        )
        self.assertEqual(code, ingest.EXIT_REJECTED)
        self.assertIn("rejected", err)
        self.assertEqual(self.recorder.requests, [])

    def test_notify_defaults_to_env_and_empty_disables(self):
        import os
        from unittest import mock

        with mock.patch.dict(os.environ, {"CADS_DASHBOARD_URL": "http://example.invalid:8080"}):
            self.assertEqual(ingest.parse_args(["--batch", "x"]).notify, "http://example.invalid:8080")
            self.assertFalse(ingest.parse_args(["--batch", "x", "--notify", ""]).notify)
        with mock.patch.dict(os.environ, {}, clear=True):
            self.assertIsNone(ingest.parse_args(["--batch", "x"]).notify)


if __name__ == "__main__":
    unittest.main()
