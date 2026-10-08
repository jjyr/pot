#!/usr/bin/env python3
"""Loopback-only synthetic UI fixture. Never use an active product directory."""

import argparse
import copy
import http.server
import json
from pathlib import Path
from urllib.parse import parse_qs, urlsplit

parser = argparse.ArgumentParser(description=__doc__)
parser.add_argument("--root", type=Path, required=True, help="Disposable native App root")
parser.add_argument("--port", type=int, default=17903)
parser.add_argument("--mobile-assets", type=Path)
parser.add_argument(
    "--pages", action="store_true", help="Include synthetic Tasks/Sandbox/Usage data"
)
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


def task(identifier, title, phase, icon="openai"):
    return dict(
        id=identifier,
        title=title,
        phase=phase,
        sandbox_id="default",
        pending=2 if phase == "running" else 0,
        created_at_ms=1,
        updated_at_ms=1,
        resource="",
        executor_icon=icon,
    )


page_tasks = [
    task("task-one", "Shared page verification 中文", "running"),
    task(
        "task-two",
        "A long task title verifying truncation and tooltips across native and mobile screens 中文长标题",
        "queued",
        "anthropic",
    ),
    task("task-attention", "Review failed task", "failed"),
    task("task-archived", "Completed verification", "complete"),
]
page_inventory = dict(
    selected="default",
    items=[
        dict(
            id="default",
            name="Linux default",
            state=dict(phase="stopped", generation=1),
            running_tasks=0,
            active_leases=0,
            config=dict(cpus=2, memory_gib=4, disk_gib=20),
        ),
        dict(
            id="other",
            name="Second environment 中文",
            state=dict(phase="stopped", generation=1),
            running_tasks=0,
            active_leases=0,
            config=dict(cpus=2, memory_gib=4, disk_gib=20),
        ),
    ],
)
page_usage = dict(
    total=dict(input=12000, output=3500, cache=6000, requests=12, unknown=1),
    lifetime=dict(input=12000, output=3500, cache=6000, requests=12, unknown=1),
    routing_total=dict(input=2200, output=800, cache=1000, requests=3, unknown=0),
    agent_models=[
        dict(
            agent="pot",
            model="Synthetic Model",
            input=12000,
            output=3500,
            cache=6000,
            requests=12,
            unknown=1,
        )
    ],
    agent_model_next_offset=None,
    tools=[],
    timezone="Asia/Shanghai",
    scope="all",
    quota=dict(
        active=True,
        used=15500,
        reserved=3000,
        reset_label="2026-10-08 00:00",
        policy=dict(period="day", limit=100000, timezone="Asia/Shanghai", auto_resume=True),
    ),
    buckets=[
        dict(
            label=f"10/{day:02}",
            range=f"2026-10-{day:02}",
            input=2000,
            output=500,
            cache=1000,
            requests=2,
            unknown=0,
        )
        for day in range(1, 8)
    ],
)


fixture_writes = []


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
        if path == "/__fixture/writes" and args.pages:
            self.data(fixture_writes)
        elif path == "/access/status":
            # UI-only simulation: does not qualify real Passkey/authentication.
            self.data(dict(authenticated=True, enrollment_open=False, push_available=False))
        elif path == "/api/v1/snapshot":
            self.data(snapshot)
        elif path == "/api/v1/tasks/query":
            query = parse_qs(urlsplit(self.path).query)
            scope = query.get("scope", ["active"])[0]
            text = query.get("q", [""])[0].lower()
            offset = int(query.get("offset", [0])[0])
            items = (
                [
                    item
                    for item in page_tasks
                    if (
                        scope == "active"
                        and item["phase"] in ["running", "queued", "waiting"]
                        or scope == "attention"
                        and item["phase"] in ["failed", "interrupted"]
                        or scope == "archived"
                        and item["phase"] in ["complete", "cancelled"]
                    )
                    and text in (item["title"] + item["id"]).lower()
                ]
                if args.pages
                else []
            )
            self.data(
                dict(
                    items=items[offset : offset + 1],
                    total=len(items),
                    offset=offset,
                    next_offset=offset + 1 if offset + 1 < len(items) else None,
                    counts=dict(
                        active=2 if args.pages else 0,
                        attention=1 if args.pages else 0,
                        archived=1 if args.pages else 0,
                    ),
                    selected=None,
                )
            )
        elif path == "/api/v1/sandboxes" and args.pages:
            self.data(page_inventory)
        elif path == "/api/v1/usage/summary" and args.pages:
            self.data(page_usage)
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
        fixture_writes.append(dict(path=self.path, body=value))
        if self.path == "/api/v1/usage/policy" and args.pages:
            page_usage["quota"]["policy"] = value
            self.data(dict(accepted=True))
        elif self.path == "/api/v1/messages":
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
