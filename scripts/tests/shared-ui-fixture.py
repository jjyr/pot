#!/usr/bin/env python3
"""Loopback-only synthetic UI fixture. Never use an active product directory."""

import argparse
import copy
import http.server
import json
from pathlib import Path
from urllib.parse import urlsplit

parser = argparse.ArgumentParser(description=__doc__)
parser.add_argument("--root", type=Path, required=True, help="Disposable native App root")
parser.add_argument("--port", type=int, default=17903)
parser.add_argument("--mobile-assets", type=Path)
args = parser.parse_args()
product = args.root / "product"
product.mkdir(parents=True, exist_ok=True)
endpoint = product / "runtime.json"
if endpoint.exists():
    old = json.loads(endpoint.read_text())
    if old.get("boot_id") != "ui-fixture":
        raise SystemExit("Refusing to replace a non-fixture Runtime endpoint")
endpoint.write_text(
    json.dumps(
        {
            "api_version": 1,
            "port": args.port,
            "token": "isolated-ui-fixture",
            "boot_id": "ui-fixture",
        }
    )
)
endpoint.chmod(0o600)


def message(identifier, role, text, request=None):
    return dict(
        id=identifier,
        conversation_id="main",
        role=role,
        text=text,
        status="complete",
        created_at_ms=1,
        request_id=request,
    )


snapshot = dict(
    api_version=1,
    boot_id="ui-fixture",
    cursor=0,
    engine=dict(engine="pot-agent", phase="ready", detail=None, version="fixture", auth_methods=[]),
    conversation_id="main",
    turn=None,
    budget=None,
    messages=[
        message("user-fixture", "user", "Please check the shared UI.\n保留桌面样式。"),
        message("tool-fixture", "tool", 'read\n{"path":"notes.md"}\nFixture tool result.'),
        message(
            "reply-fixture",
            "assistant",
            "## Shared UI verification\n\nA **bold** reply with `inline code` and a [link](https://example.com).\n\n- Chat and tasks remain available.\n- 中文内容保持原样。\n\n```rust\nlet shared = true;\n```",
        ),
    ],
)


class Handler(http.server.SimpleHTTPRequestHandler):
    def __init__(self, *values, **kwargs):
        super().__init__(*values, directory=str(args.mobile_assets or product), **kwargs)

    def end_headers(self):
        self.send_header("Cross-Origin-Opener-Policy", "same-origin")
        self.send_header("Cross-Origin-Embedder-Policy", "require-corp")
        self.send_header("Cache-Control", "no-store")
        super().end_headers()

    def data(self, value):
        self.send_response(200)
        self.send_header("Content-Type", "application/json")
        self.end_headers()
        self.wfile.write(json.dumps(value).encode())

    def do_GET(self):
        path = urlsplit(self.path).path
        if path == "/access/status":
            # UI-only simulation: does not qualify real Passkey/authentication.
            self.data(dict(authenticated=True, enrollment_open=False, push_available=False))
        elif path == "/api/v1/snapshot":
            self.data(snapshot)
        elif path == "/api/v1/tasks/query":
            self.data(
                dict(
                    items=[],
                    total=0,
                    offset=0,
                    next_offset=None,
                    counts=dict(active=0, attention=0, archived=0),
                    selected=None,
                )
            )
        elif path.startswith("/api/v1/tasks/"):
            task = copy.deepcopy(snapshot)
            task["conversation_id"] = path.split("/")[-1]
            task["messages"] = []
            self.data(task)
        elif path.startswith("/api/v1/"):
            self.data({})
        elif args.mobile_assets:
            super().do_GET()
        else:
            self.send_error(404)

    def do_POST(self):
        length = int(self.headers.get("Content-Length", 0))
        if length > 65536:
            self.send_error(413)
            return
        value = json.loads(self.rfile.read(length))
        if self.path == "/api/v1/messages":
            duplicate = any(
                m.get("request_id") == value["request_id"] for m in snapshot["messages"]
            )
            if not duplicate:
                snapshot["messages"].append(
                    message(value["request_id"], "user", value["text"], value["request_id"])
                )
            self.data(dict(message_id=value["request_id"], duplicate=duplicate))
        else:
            self.send_error(405)

    def log_message(self, *_values):
        pass


http.server.ThreadingHTTPServer(("127.0.0.1", args.port), Handler).serve_forever()
