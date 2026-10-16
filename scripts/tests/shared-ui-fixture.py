#!/usr/bin/env python3
"""Loopback-only synthetic UI fixture. Never use an active product directory."""

import argparse
import copy
import http.server
import json
import re
import time
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
            "api_version": 3,
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
    api_version=3,
    boot_id="ui-fixture",
    cursor=0,
    agent=dict(model="fixture", phase="ready", detail=None),
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
        assistant_id="main",
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
            name="Second execution_target 中文",
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
fixture_schedule = dict(
    id="fixture-schedule",
    spec=dict(
        title="Daily check",
        goal="Check the fixture",
        machine_id="fixture",
        kind="daily",
        timezone="Asia/Shanghai",
        time="09:00",
        notify="changes",
        misfire="coalesce",
        enabled=True,
    ),
    next_at_ms=1800000000000,
    next_label="Tomorrow",
    deleted=False,
    last_error="",
    baseline=None,
    created_at_ms=1,
)
machine_labels = ["desktop", "task-runner", "macos"]
profiles = [dict(id="fixture-openai", name="Fixture connection", model="fixture-model", revision=1)]
connections = [
    dict(
        profile_id="fixture-openai",
        endpoint="https://example.invalid/v1",
        protocol="openai_chat",
        has_key=True,
        model_settings=dict(catalog_provider="openai"),
    )
]
active_config = dict(source_profile_id="fixture-openai", model="fixture-model")
providers = [
    dict(
        id="openai",
        name="OpenAI",
        common=True,
        endpoint="https://example.invalid/v1",
        protocol="openai_chat",
        models=[dict(id="fixture-model", name="Fixture model", context=32768, output=8192)],
    )
]
if args.pages:
    providers.extend(
        dict(
            id=f"fixture-{index}",
            name=f"Fixture provider {index:02}",
            common=True,
            endpoint="https://example.invalid/v1",
            protocol="openai_chat",
            models=[],
        )
        for index in range(1, 16)
    )


assistant_names = (
    [
        "Pot",
        "Research",
        "Code",
        "旅行计划",
        "Design",
        "Writing",
        "Reading",
        "Analytics",
        "Long assistant name for layout",
        "Assistant 10",
        "Assistant 11",
        "Assistant 12",
    ]
    if args.pages
    else ["Pot"]
)
assistant_definitions = [
    dict(
        id="main" if i == 0 else f"assistant-{i + 1}",
        name=name,
        revision=1,
        preview="Continue our conversation · 继续对话",
    )
    for i, name in enumerate(assistant_names)
]
assistant_configurations = {}
assistant_rules = {}
default_provider_id = "fixture-openai"


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
        owner_match = re.match(r"/api/v1/assistants/([^/]+)/", path)
        owner = owner_match[1] if owner_match else "main"
        if owner_match:
            path = "/api/v1/" + path[owner_match.end() :]
        if path in ("/api/v1/events", "/api/v1/changes", "/api/v1/machines/watch") or path.endswith(
            "/events"
        ):
            self.send_response(200)
            self.send_header("Content-Type", "text/event-stream")
            self.end_headers()
            try:
                for _ in range(60):
                    self.wfile.write(b": fixture heartbeat\n\n")
                    self.wfile.flush()
                    time.sleep(5)
            except (BrokenPipeError, ConnectionResetError):
                pass
        elif path == "/api/v1/runtime":
            self.data(
                dict(
                    runtime_id="fixture",
                    capabilities=["conversation", "tasks", "runtime", "accounting", "schedules"],
                )
            )
        elif path == "/api/v1/assistants":
            self.data(dict(assistants=assistant_definitions))
        elif path == "/api/v1/agent":
            configuration = assistant_configurations.get(owner)
            config = (
                dict(source_profile_id=configuration["provider_id"], model=configuration["model"])
                if configuration
                else active_config
            )
            self.data(
                dict(
                    profiles=profiles,
                    thread=dict(config=config),
                    selection=configuration,
                    default_provider_id=default_provider_id,
                )
            )
        elif path == "/api/v1/providers":
            self.data(
                dict(
                    connections=connections,
                    directory=providers,
                    default_provider_id=default_provider_id,
                )
            )
        elif path == "/api/v1/agent/routing":
            self.data(dict(revision=0, profile_limits={}, provider_limits={}))
        elif path == "/api/v1/usage/providers":
            self.data(dict(providers=[]))
        elif path == "/api/v1/instructions":
            self.data(
                assistant_rules.get(owner, dict(text="Fixture assistant rules", revision="fixture"))
            )
        elif path == "/api/v1/permissions":
            self.data(dict(requests=[], grants=[]))
        elif path == "/api/v1/completions":
            self.data(
                dict(
                    items=[
                        dict(token="/machine:fixture", label="Control", detail="Machine"),
                        dict(token="/tag:desktop", label="desktop", detail="Machine label"),
                        dict(token="/tag:task-runner", label="task-runner", detail="Machine label"),
                        dict(
                            token="/skill:skill-creator",
                            label="skill-creator",
                            detail="Create Skills",
                        ),
                    ]
                )
            )
        elif path == "/api/v1/machines":
            self.data(
                dict(
                    machines=[
                        dict(
                            runtime_id="fixture",
                            name="Control",
                            local=True,
                            connection="online",
                            tags=machine_labels,
                            execution_targets=[
                                dict(
                                    execution_target=dict(
                                        runtime_id="fixture", target=dict(kind="host")
                                    ),
                                    name="Host",
                                    state=dict(phase="running"),
                                )
                            ],
                        )
                    ]
                )
            )
        elif path == "/api/v1/schedules":
            selected = parse_qs(urlsplit(self.path).query).get("selected", [""])[0]
            self.data(
                dict(
                    items=[fixture_schedule],
                    total=1,
                    offset=0,
                    next_offset=None,
                    selected=fixture_schedule if selected == fixture_schedule["id"] else None,
                    runs=[],
                    runs_next_offset=None,
                )
            )
        elif path == "/__fixture/writes" and args.pages:
            self.data(fixture_writes)
        elif path == "/access/status":
            # UI-only simulation: does not qualify real Passkey/authentication.
            self.data(dict(authenticated=True, enrollment_open=False, push_available=False))
        elif path == "/api/v1/snapshot":
            current = copy.deepcopy(snapshot)
            current["conversation_id"] = owner
            for item in current["messages"]:
                item["conversation_id"] = owner
            self.data(current)
        elif path in ("/api/v1/tasks/query", "/api/v1/assistant-tasks"):
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
        global default_provider_id
        length = int(self.headers.get("Content-Length", 0))
        if length > 65536:
            self.send_error(413)
            return
        path = urlsplit(self.path).path
        owner_match = re.match(r"/api/v1/assistants/([^/]+)/", path)
        owner = owner_match[1] if owner_match else "main"
        if owner_match:
            path = "/api/v1/" + path[owner_match.end() :]
        value = json.loads(self.rfile.read(length))
        fixture_writes.append(dict(path=self.path, body=value))
        if self.path == "/access/token":
            self.data(dict(accepted=True))
        elif path == "/__fixture/chat" and args.pages:
            if value.get("boot_id") != "ui-fixture" or value.get("conversation_id") != "main":
                self.send_error(400)
                return
            snapshot.clear()
            snapshot.update(value)
            self.data(dict(accepted=True))
        elif path == "/api/v1/assistant-mail":
            self.data([])
        elif path == "/api/v1/providers/check":
            self.data(
                dict(models=["fixture-model", "fixture-model-2"], message="Fixture check passed")
            )
        elif path == "/api/v1/providers/default":
            default_provider_id = value["provider_id"]
            self.data(dict(saved=True))
        elif path == "/api/v1/assistants":
            definition = dict(
                id=f"assistant-{len(assistant_definitions) + 1}", name=value["name"], revision=1
            )
            assistant_definitions.append(definition)
            assistant_configurations[definition["id"]] = value["configuration"]
            assistant_rules[definition["id"]] = dict(text=value.get("text", ""), revision="created")
            self.data(definition)
        elif path == "/api/v1/definition":
            definition = next(a for a in assistant_definitions if a["id"] == owner)
            definition.update(name=value["name"], revision=definition["revision"] + 1)
            assistant_configurations[owner] = value["configuration"]
            rules = dict(text=value["text"], revision=str(definition["revision"]))
            assistant_rules[owner] = rules
            self.data(dict(definition=definition, rules=rules))
        elif path == "/api/v1/providers":
            profile = next(
                (profile for profile in profiles if profile["id"] == value["profile_id"]), None
            )
            if profile is None:
                profile = dict(id=value["profile_id"], revision=0)
                profiles.append(profile)
            profile.update(
                name=value["name"], model=value["model"], revision=profile["revision"] + 1
            )
            connection = next((c for c in connections if c["profile_id"] == profile["id"]), None)
            if connection is None:
                connection = dict(profile_id=profile["id"])
                connections.append(connection)
            connection.update(
                endpoint=value["endpoint"],
                protocol=value["protocol"],
                has_key=True,
                model_settings=dict(catalog_provider=value.get("catalog_provider")),
                models=value.get("models", []),
            )
            self.data(dict(saved=True))
        elif path == "/api/v1/agent":
            active_config.update(
                source_profile_id=value["profile"]["id"], model=value["profile"]["model"]
            )
            self.data(dict(accepted=True))
        elif path == "/api/v1/schedules" and value.get("action") == "update":
            fixture_schedule["spec"].update(value["spec"])
            self.data(dict(accepted=True))
        elif self.path == "/api/v1/usage/policy" and args.pages:
            page_usage["quota"]["policy"] = value
            self.data(dict(accepted=True))
        elif path == "/api/v1/machine-connections" and value.get("action") == "set_tags":
            machine_labels[:] = value["tags"]
            self.data(dict(tags=machine_labels))
        elif path == "/api/v1/messages":
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
