#!/usr/bin/env python3
"""Loopback remote Space for native UI qualification; no model calls or real credentials."""

import argparse
import http.server
import json
import os
from pathlib import Path
from urllib.parse import parse_qs, urlsplit
import uuid

parser = argparse.ArgumentParser(description=__doc__)
parser.add_argument("--root", type=Path, required=True)
parser.add_argument("--port", type=int, default=17947)
args = parser.parse_args()
# Reserve an ephemeral port when requested, before publishing the client config.
import socket

listener = socket.socket()
listener.bind(("127.0.0.1", args.port))
args.port = listener.getsockname()[1]
listener.close()
args.root.mkdir(parents=True, exist_ok=True)
config = args.root / "control-client.json"
if config.exists():
    raise SystemExit("Use a new disposable App root")
control = "00000000-0000-4000-8000-000000000047"
config.write_text(
    json.dumps(
        dict(
            url=f"http://127.0.0.1:{args.port}", token="ui-fixture", ca_pem=None, runtime_id=control
        )
    )
)
config.chmod(0o600)
key = args.root / "test-key"
key.write_bytes(os.urandom(32))
key.chmod(0o600)
assistants = [
    dict(id="main", name="Pot", revision=1),
    dict(id="research", name="研究助手", revision=1),
]
tasks = [
    dict(
        id="00000000-0000-4000-8000-000000000048",
        assistant_id="research",
        title="云端研究任务 · 中文长标题测试",
        phase="running",
        pending=0,
        created_at_ms=1,
        updated_at_ms=2,
        resource="",
        sandbox_id="",
        executor_icon="openai",
        environment_ref=dict(runtime_id=control, target=dict(kind="host")),
    )
]
schedule = dict(
    id="00000000-0000-4000-8000-000000000049",
    spec=dict(
        title="每日云端检查",
        goal="Inspect the project",
        timezone="Asia/Shanghai",
        kind="daily",
        at="",
        every_minutes=0,
        time="09:00",
        weekdays=[],
        misfire="coalesce",
        notify="changes",
        enabled=True,
        sandbox_id="",
    ),
    next_at_ms=9999999999999,
    next_label="Tomorrow 09:00",
    deleted=False,
    last_error="",
    baseline=None,
    created_at_ms=1,
)


class Handler(http.server.BaseHTTPRequestHandler):
    def log_message(self, *unused):
        pass

    def data(self, value, status=200):
        encoded = json.dumps(value).encode()
        self.send_response(status)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(encoded)))
        self.end_headers()
        self.wfile.write(encoded)

    def route(self):
        parsed = urlsplit(self.path)
        path, owner = parsed.path, "main"
        if path.startswith("/api/v1/assistants/"):
            owner, suffix = path[len("/api/v1/assistants/") :].split("/", 1)
            path = "/api/v1/" + suffix
        return path, owner, parse_qs(parsed.query)

    def authorized(self):
        if self.headers.get("Authorization") != "Bearer ui-fixture":
            self.data(dict(message="Invalid fixture credential"), 401)
            return False
        return True

    def do_GET(self):
        if not self.authorized():
            return
        path, owner, query = self.route()
        if path == "/api/v1/runtime":
            self.data(
                dict(
                    runtime_id=(args.root / "replacement-id").read_text()
                    if (args.root / "replacement-id").exists()
                    else control,
                    capabilities=["conversation", "tasks", "accounting", "schedules"],
                )
            )
        elif path == "/api/v1/assistants":
            self.data(dict(assistants=assistants))
        elif path == "/api/v1/instructions":
            self.data(dict(text="Fixture assistant instructions", revision="1"))
        elif path == "/api/v1/permissions":
            self.data(
                dict(
                    assistant_id=owner,
                    requests=[],
                    grants=[],
                    policy=dict(rules=[], sandboxes=[], sandbox_limits={}),
                )
            )
        elif path == "/api/v1/machines":
            self.data(
                dict(
                    machines=[
                        dict(
                            runtime_id=control,
                            name="云端构建机",
                            local=True,
                            connection="online",
                            tags=[],
                            environments=[
                                dict(
                                    environment_ref=dict(
                                        runtime_id=control, target=dict(kind="host")
                                    ),
                                    name="Host",
                                    state=dict(phase="running"),
                                )
                            ],
                        )
                    ]
                )
            )
        elif path == "/api/v1/completions":
            self.data(dict(items=[]))
        elif path in ("/api/v1/assistant-tasks", "/api/v1/tasks/query"):
            if int(query.get("limit", ["20"])[0]) > 50:
                self.data(dict(message="Invalid page size"), 400)
                return
            items = tasks if query.get("scope", ["active"])[0] == "active" else []
            self.data(
                dict(
                    items=items,
                    total=len(items),
                    offset=0,
                    next_offset=None,
                    counts=dict(active=1, attention=0, archived=0),
                    selected=tasks[0] if query.get("selected", [""])[0] == tasks[0]["id"] else None,
                )
            )
        elif path == "/api/v1/schedules":
            items = [schedule] if owner == "research" else []
            self.data(
                dict(
                    items=items,
                    total=len(items),
                    offset=0,
                    next_offset=None,
                    selected=schedule if query.get("selected", [""])[0] == schedule["id"] else None,
                    runs=[],
                    runs_next_offset=None,
                )
            )
        elif path == "/api/v1/usage/summary":
            total = dict(input=1200, output=300, cache=200, unknown=0, requests=2)
            self.data(
                dict(
                    scope="",
                    total=total,
                    lifetime=total,
                    historical={},
                    routing_total={},
                    agent_models=[dict(agent="fixture", model="fixture", **total)],
                    buckets=[],
                    quota={},
                    waiting=[],
                    tools=[],
                    requests=[],
                    compactions=[],
                    compaction_total={},
                    compaction_count=0,
                    offset=0,
                    agent_model_next_offset=None,
                )
            )
        elif path == "/api/v1/snapshot" or path.startswith("/api/v1/tasks/"):
            conversation = owner if path.endswith("snapshot") else path.rsplit("/", 1)[-1]
            self.data(
                dict(
                    api_version=2,
                    boot_id="fixture",
                    cursor=1,
                    conversation_id=conversation,
                    agent=dict(model="fixture", phase="ready", detail=None),
                    turn=None,
                    budget=None,
                    messages=[
                        dict(
                            id="fixture-reply",
                            conversation_id=conversation,
                            role="assistant",
                            text="这是云端助手的对话。This is an isolated UI fixture.",
                            status="complete",
                            created_at_ms=1,
                            request_id=None,
                        )
                    ],
                )
            )
        else:
            self.data({})

    def do_POST(self):
        if not self.authorized():
            return
        value = json.loads(self.rfile.read(int(self.headers.get("Content-Length", 0))))
        with (args.root / "fixture-writes.jsonl").open("a") as out:
            out.write(json.dumps(dict(path=self.path, body=value)) + "\n")
        path, owner, _ = self.route()
        if path == "/api/v1/assistant-mail":
            operation = value["operation"]
            if operation == "directory":
                self.data(
                    dict(
                        control_id=control,
                        assistants=[
                            dict(
                                address=dict(control_id=control, assistant_id=a["id"]),
                                name=a["name"],
                            )
                            for a in assistants
                        ],
                    )
                )
            elif operation in ("pending", "history"):
                self.data([])
            else:
                self.data({})
        elif path == "/api/v1/assistants":
            definition = dict(id=str(uuid.uuid4()), name=value["name"], revision=1)
            assistants.append(definition)
            self.data(definition)
        elif path == "/api/v1/messages":
            self.data(dict(message="Fixture does not invoke models"), 400)
        elif path == "/api/v1/schedules":
            schedule["spec"] = value.get("spec", schedule["spec"])
            self.data(dict(id=schedule["id"]))
        else:
            self.data({})


print(f"Remote fixture: http://127.0.0.1:{args.port}; App root: {args.root}", flush=True)
http.server.ThreadingHTTPServer(("127.0.0.1", args.port), Handler).serve_forever()
