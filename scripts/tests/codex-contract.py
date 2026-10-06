#!/usr/bin/env python3
"""Compare Codex transports against loopback-only mock model/MCP servers.

Uses an empty native home, no credentials, no paid calls and no version gate.
This host-side probe is not guest/login/permission/background-process acceptance.
"""

import argparse
import asyncio
import contextlib
import json
import os
from pathlib import Path
import signal
import tempfile


class Acp:
    def __init__(self, process):
        self.process = process
        self.pending = {}
        self.updates = []
        self.sequence = 0
        self.changed = asyncio.Event()
        self.reader = asyncio.create_task(self.read())

    async def read(self):
        try:
            while line := await self.process.stdout.readline():
                value = json.loads(line)
                if "id" in value and "method" not in value:
                    future = self.pending.pop(value["id"], None)
                    if future is not None and not future.done():
                        future.set_result(value)
                elif "id" in value:
                    self.updates.append(value)
                    params = value.get("params", {})
                    if (
                        value["method"] == "session/request_permission"
                        and params.get("_meta", {}).get("is_mcp_tool_approval") is True
                        and params.get("toolCall", {}).get("toolCallId") in ["call_a", "call_b"]
                    ):
                        # Only the deterministic mock's harmless identity probe.
                        self.send(
                            {
                                "jsonrpc": "2.0",
                                "id": value["id"],
                                "result": {
                                    "outcome": {"outcome": "selected", "optionId": "allow_once"}
                                },
                            }
                        )
                    elif (
                        value["method"] == "mcpServer/elicitation/request"
                        and params.get("serverName") == "pot"
                        and params.get("_meta", {}).get("codex_approval_kind") == "mcp_tool_call"
                        and params.get("_meta", {}).get("tool_params")
                        in [{"lane": "a"}, {"lane": "b"}]
                    ):
                        self.send(
                            {"id": value["id"], "result": {"action": "accept", "content": {}}}
                        )
                    else:
                        self.send(
                            {
                                "jsonrpc": "2.0",
                                "id": value["id"],
                                "error": {
                                    "code": -32601,
                                    "message": "Client capability not offered",
                                },
                            }
                        )
                else:
                    self.updates.append(value)
                self.changed.set()
        finally:
            for future in self.pending.values():
                if not future.done():
                    future.set_exception(RuntimeError("ACP ended without a response"))
            self.pending.clear()

    def send(self, value):
        self.process.stdin.write((json.dumps(value) + "\n").encode())

    async def request(self, method, params):
        self.sequence += 1
        key = self.sequence
        future = asyncio.get_running_loop().create_future()
        self.pending[key] = future
        self.send({"jsonrpc": "2.0", "id": key, "method": method, "params": params})
        try:
            reply = await asyncio.wait_for(future, 30)
        finally:
            self.pending.pop(key, None)
        if "error" in reply:
            raise RuntimeError(f"{method}: {reply['error']}")
        return reply["result"]

    async def wait_update(self, method, thread, turn):
        async def wait():
            while True:
                self.changed.clear()
                for update in self.updates:
                    params = update.get("params", {})
                    if (
                        update.get("method") == method
                        and params.get("threadId") == thread
                        and params.get("turn", {}).get("id") == turn
                    ):
                        return params
                await self.changed.wait()

        return await asyncio.wait_for(wait(), 30)

    async def close(self):
        self.process.stdin.close()
        with contextlib.suppress(asyncio.TimeoutError):
            await asyncio.wait_for(self.process.wait(), 3)
        with contextlib.suppress(ProcessLookupError):
            os.killpg(self.process.pid, signal.SIGKILL)
        await self.process.wait()
        await self.reader


class Mock:
    def __init__(self, runtime_tools=False):
        self.runtime_tools = runtime_tools
        self.summary_calls = 0
        self.requests = []
        self.fault_status = None
        self.calls = []
        self.errors = []
        self.model_started = asyncio.Event()
        self.model_stopped = asyncio.Event()

    async def handle(self, reader, writer):
        try:
            head = await asyncio.wait_for(reader.readuntil(b"\r\n\r\n"), 10)
            lines = head.decode().split("\r\n")
            verb, path, _ = lines[0].split()
            headers = dict(line.lower().split(":", 1) for line in lines[1:] if ":" in line)
            size = int(headers.get("content-length", "0"))
            assert size <= 4 * 1024 * 1024, "request too large"
            body = json.loads(await reader.readexactly(size)) if size else {}
            status, extra, response = "200 OK", "Content-Type: application/json\r\n", b"{}"
            if verb != "POST":
                status = "405 Method Not Allowed"
            elif path.startswith("/mcp/"):
                lane = path.rsplit("/", 1)[1]
                assert headers.get("authorization", "").strip() == f"bearer probe-{lane}", (
                    f"MCP lane authorization mismatch: {lane}"
                )
                method = body["method"]
                if "id" not in body:
                    status = "202 Accepted"
                else:
                    if method == "initialize":
                        result = {
                            "protocolVersion": "2025-03-26",
                            "capabilities": {"tools": {}},
                            "serverInfo": {"name": "pot-probe", "version": "1"},
                        }
                    elif method == "tools/list":
                        result = {
                            "tools": [
                                {
                                    "name": "pot_probe",
                                    "description": "Verify lane identity",
                                    "inputSchema": {
                                        "type": "object",
                                        "properties": {"lane": {"type": "string"}},
                                        "required": ["lane"],
                                    },
                                }
                            ]
                        }
                    elif method == "tools/call":
                        assert body["params"]["name"] == "pot_probe", "unexpected MCP tool"
                        assert body["params"]["arguments"]["lane"].lower() == lane, (
                            "MCP lane argument mismatch"
                        )
                        self.calls.append(lane)
                        result = {"content": [{"type": "text", "text": f"verified {lane}"}]}
                    elif method == "ping":
                        result = {}
                    else:
                        raise AssertionError(f"Unexpected MCP method: {method}")
                    response = json.dumps(
                        {"jsonrpc": "2.0", "id": body["id"], "result": result}
                    ).encode()
            else:
                assert path == "/v1/responses", path
                self.requests.append(body)
                if self.fault_status is not None:
                    fault = self.fault_status
                    response = json.dumps(
                        {
                            "error": {
                                "message": "Fixture authentication expired",
                                "type": "authentication_error",
                                "code": "invalid_api_key",
                            }
                        }
                    ).encode()
                    writer.write(
                        (
                            f"HTTP/1.1 {fault} Unauthorized\r\nContent-Type: application/json\r\nContent-Length: {len(response)}\r\nConnection: close\r\n\r\n"
                        ).encode()
                        + response
                    )
                    await writer.drain()
                    return
                if "CANCEL_WAIT" in json.dumps(body["input"]):
                    writer.write(
                        b'HTTP/1.1 200 OK\r\nContent-Type: text/event-stream\r\nConnection: close\r\n\r\ndata: {"type":"response.created","response":{"id":"cancel","status":"in_progress"}}\n\n'
                    )
                    await writer.drain()
                    self.model_started.set()
                    await asyncio.wait_for(reader.read(), 15)
                    self.model_stopped.set()
                    return
                auxiliary = body["model"] != "pot-mock"
                tools = []
                for tool in body.get("tools", []):
                    if tool.get("type") == "namespace":
                        tools.extend(
                            tool["name"] + "__" + function["name"]
                            for function in tool.get("tools", [])
                        )
                    else:
                        tools.append(tool.get("name", ""))
                if self.runtime_tools:
                    assert not any("unmanaged" in name for name in tools), (
                        "Inherited MCP server bypassed managed policy"
                    )
                current_user = next(
                    (item for item in reversed(body["input"]) if item.get("role") == "user"), {}
                )
                is_summary = "Pot task completion notification." in json.dumps(current_user)
                if self.runtime_tools and is_summary:
                    assert "Summarize background task results." in json.dumps(body["input"]), (
                        "Runtime role instructions missing"
                    )
                    self.summary_calls += 1
                    assert not any(
                        name.rsplit("__", 1)[-1]
                        in ["exec_command", "shell", "shell_command", "apply_patch"]
                        for name in tools
                    ), tools
                assert not any(
                    name.rsplit("__", 1)[-1] in ["spawn_agent", "send_input", "wait_agent"]
                    for name in tools
                )
                text = json.dumps(body["input"])
                lane = "a" if "LANE_A" in text else "b"
                probe_name = "pot_list_tasks" if self.runtime_tools else "pot_probe"
                probe = next((name for name in tools if probe_name in name), None)
                inputs = body["input"]
                if self.runtime_tools:
                    last_user = max(
                        (i for i, item in enumerate(inputs) if item.get("role") == "user"),
                        default=0,
                    )
                    inputs = inputs[last_user:]
                has_output = any(item.get("type") == "function_call_output" for item in inputs)
                background = (
                    self.runtime_tools
                    and hasattr(self, "background_pid")
                    and "BACKGROUND_TEST" in json.dumps(inputs)
                )
                scheduled = (
                    self.runtime_tools
                    and "This is one independent scheduled run." in json.dumps(inputs)
                )
                preference_save = self.runtime_tools and "PREF_SAVE" in json.dumps(current_user)
                preference_read = self.runtime_tools and "PREF_READ" in json.dumps(current_user)
                preference_route = self.runtime_tools and "PREF_ROUTE" in json.dumps(current_user)
                preference_outputs = sum(
                    item.get("type") == "function_call_output" for item in inputs
                )
                preference_steps = (
                    [
                        ("pot_get_execution_preferences", {}),
                        (
                            "pot_update_execution_preferences",
                            {"text": "Use Codex test for coding.", "expected_revision": 0},
                        ),
                        ("pot_get_execution_preferences", {}),
                    ]
                    if preference_save
                    else [("pot_get_execution_preferences", {})]
                )
                if preference_route:
                    preference_steps = [
                        (
                            "pot_create_task",
                            {
                                "text": "Preference routed worker",
                                "profile": "codex-test",
                                "preference_revision": 1,
                                "selection_reason": "Your coding preference",
                            },
                        )
                    ]
                if (
                    preference_save or preference_read or preference_route
                ) and preference_outputs < len(preference_steps):
                    name, arguments = preference_steps[preference_outputs]
                    command = next(
                        name_in_tools
                        for name_in_tools in tools
                        if name_in_tools.rsplit("__", 1)[-1] == name
                    )
                    item = {
                        "type": "function_call",
                        "id": f"fc_preference_{preference_outputs}",
                        "call_id": f"call_preference_{preference_outputs}",
                        "name": command.rsplit("__", 1)[-1],
                        "arguments": json.dumps(arguments),
                    }
                    if "__" in command:
                        item["namespace"] = command.rsplit("__", 1)[0]
                elif scheduled and not has_output:
                    command = next(
                        name for name in tools if name.rsplit("__", 1)[-1] == "exec_command"
                    )
                    report = json.dumps(
                        {
                            "summary": "mock observation",
                            "fingerprint": "stable-v1",
                            "evidence": ["loopback fixture"],
                            "artifacts": [],
                        }
                    )
                    item = {
                        "type": "function_call",
                        "id": "fc_schedule",
                        "call_id": "call_schedule",
                        "name": command.rsplit("__", 1)[-1],
                        "arguments": json.dumps(
                            {
                                "cmd": "printf '%s' '" + report + "' > .pot-schedule-result.json",
                                "max_output_tokens": 32,
                            }
                        ),
                    }
                    if "__" in command:
                        item["namespace"] = command.rsplit("__", 1)[0]
                elif background and not has_output:
                    command = next(
                        name for name in tools if name.rsplit("__", 1)[-1] == "exec_command"
                    )
                    item = {
                        "type": "function_call",
                        "id": "fc_background",
                        "call_id": "call_background",
                        "name": command.rsplit("__", 1)[-1],
                        "arguments": json.dumps(
                            {
                                "cmd": f"echo $$ > '{self.background_pid}'; exec sleep 60",
                                "yield_time_ms": 1000,
                                "max_output_tokens": 32,
                            }
                        ),
                    }
                    if "__" in command:
                        item["namespace"] = command.rsplit("__", 1)[0]
                elif probe and not has_output and not auxiliary:
                    item = {
                        "type": "function_call",
                        "id": f"fc_{lane}",
                        "call_id": f"call_{lane}",
                        "name": probe.rsplit("__", 1)[-1],
                        "namespace": probe.rsplit("__", 1)[0],
                        "arguments": json.dumps({} if self.runtime_tools else {"lane": lane}),
                    }
                else:
                    item = {
                        "type": "message",
                        "id": f"msg_{lane}",
                        "role": "assistant",
                        "status": "completed",
                        "content": [
                            {
                                "type": "output_text",
                                "text": json.dumps({"title": "Pot mock title"})
                                if auxiliary
                                else f"POT_CODEX_{lane.upper()}_OK",
                                "annotations": [],
                            }
                        ],
                    }
                completed = {
                    "id": f"resp_{len(self.requests)}",
                    "object": "response",
                    "status": "completed",
                    "output": [item],
                    "usage": {
                        "input_tokens": 20,
                        "output_tokens": 5,
                        "total_tokens": 25,
                        "input_tokens_details": {"cached_tokens": 0},
                    },
                }
                events = [
                    {
                        "type": "response.created",
                        "response": {"id": completed["id"], "status": "in_progress", "output": []},
                    },
                    {"type": "response.output_item.added", "output_index": 0, "item": item},
                ]
                if item["type"] == "message":
                    events.append(
                        {
                            "type": "response.output_text.delta",
                            "item_id": item["id"],
                            "output_index": 0,
                            "content_index": 0,
                            "delta": item["content"][0]["text"],
                        }
                    )
                events.extend(
                    [
                        {"type": "response.output_item.done", "output_index": 0, "item": item},
                        {"type": "response.completed", "response": completed},
                    ]
                )
                response = "".join(
                    f"event: {e['type']}\ndata: {json.dumps(e)}\n\n" for e in events
                ).encode()
                extra = "Content-Type: text/event-stream\r\n"
            writer.write(
                (
                    f"HTTP/1.1 {status}\r\n{extra}Content-Length: {len(response)}\r\n"
                    "Connection: close\r\n\r\n"
                ).encode()
                + response
            )
            await writer.drain()
        except asyncio.IncompleteReadError as error:
            if error.partial:
                self.errors.append("Truncated HTTP request")
        except Exception as error:
            self.errors.append(repr(error))
        finally:
            writer.close()
            await writer.wait_closed()


async def run(args):
    mock = Mock()
    server = await asyncio.start_server(mock.handle, "127.0.0.1", 0)
    port = server.sockets[0].getsockname()[1]
    try:
        with tempfile.TemporaryDirectory(prefix="pot-codex-contract-") as temporary:
            root = Path(temporary)
            native_home = root / "native"
            native_home.mkdir()
            (native_home / "config.toml").write_text(
                'model = "pot-mock"\nmodel_provider = "pot_mock"\n'
                '[model_providers.pot_mock]\nname = "Pot loopback mock"\n'
                f'base_url = "http://127.0.0.1:{port}/v1"\n'
                'wire_api = "responses"\nrequires_openai_auth = false\n[features]\nmulti_agent = false\n'
            )
            config = {
                "model": "pot-mock",
                "features": {"multi_agent": False},
                "approval_policy": "never",
                "sandbox_mode": "read-only",
            }
            # Deliberately construct child environment; no user auth/config/proxy variables.
            environment = {
                "PATH": os.environ["PATH"],
                "HOME": str(root),
                "CODEX_HOME": str(native_home),
                "CODEX_CONFIG": json.dumps(config),
                "MODEL_PROVIDER": "pot_mock",
                "CODEX_PATH": str(Path(args.codex).resolve()),
                "INITIAL_AGENT_MODE": "read-only",
            }
            command = (
                [str(Path(args.acp).resolve())]
                if args.acp
                else [str(Path(args.codex).resolve()), "app-server"]
            )
            process = await asyncio.create_subprocess_exec(
                *command,
                env=environment,
                cwd=root,
                stdin=asyncio.subprocess.PIPE,
                stdout=asyncio.subprocess.PIPE,
                stderr=asyncio.subprocess.DEVNULL,
                start_new_session=True,
                limit=4 * 1024 * 1024,
            )
            acp = Acp(process)
            try:
                hello = await acp.request(
                    "initialize",
                    {
                        "protocolVersion": 1,
                        "clientCapabilities": {},
                        "capabilities": {"experimentalApi": True},
                        "clientInfo": {"name": "pot-contract-probe", "version": "1"},
                    },
                )
                if args.acp:
                    assert hello["protocolVersion"] == 1
                    assert hello["agentCapabilities"]["mcpCapabilities"]["http"]
                else:
                    acp.send({"jsonrpc": "2.0", "method": "initialized", "params": {}})
                sessions = []
                for lane in ["a", "b"]:
                    workspace = root / lane
                    workspace.mkdir()
                    params = {
                        "cwd": str(workspace),
                        "mcpServers": [
                            {
                                "type": "http",
                                "name": "pot",
                                "url": f"http://127.0.0.1:{port}/mcp/{lane}",
                                "headers": [
                                    {"name": "Authorization", "value": f"Bearer probe-{lane}"}
                                ],
                            }
                        ],
                    }
                    if args.acp:
                        session = await acp.request("session/new", params)
                        sessions.append((session["sessionId"], params))
                    else:
                        params = {
                            "cwd": str(workspace),
                            "model": "pot-mock",
                            "modelProvider": "pot_mock",
                            "approvalPolicy": "on-request",
                            "sandbox": "read-only",
                            "config": {
                                "features.multi_agent": False,
                                "mcp_servers": {
                                    "pot": {
                                        "url": f"http://127.0.0.1:{port}/mcp/{lane}",
                                        "http_headers": {"Authorization": f"Bearer probe-{lane}"},
                                    }
                                },
                            },
                        }
                        session = await acp.request("thread/start", params)
                        assert session["model"] == "pot-mock"
                        sessions.append((session["thread"]["id"], params))
                assert sessions[0][0] != sessions[1][0]

                async def prompt(index):
                    if not args.acp:
                        thread = sessions[index][0]
                        response = await acp.request(
                            "turn/start",
                            {
                                "threadId": thread,
                                "input": [
                                    {
                                        "type": "text",
                                        "text": f"LANE_{'AB'[index]}: call pot_probe then report the marker.",
                                    }
                                ],
                            },
                        )
                        completed = await acp.wait_update(
                            "turn/completed", thread, response["turn"]["id"]
                        )
                        assert completed["turn"]["status"] == "completed", completed
                        usage = [
                            u["params"].get("tokenUsage")
                            for u in acp.updates
                            if u.get("method") == "thread/tokenUsage/updated"
                            and u["params"].get("threadId") == thread
                        ]
                        return {"stopReason": "end_turn", "usage": usage[-1] if usage else None}
                    return await acp.request(
                        "session/prompt",
                        {
                            "sessionId": sessions[index][0],
                            "prompt": [
                                {
                                    "type": "text",
                                    "text": f"LANE_{'AB'[index]}: call pot_probe then report the marker.",
                                }
                            ],
                        },
                    )

                results = await asyncio.gather(prompt(0), prompt(1))
                assert all(result["stopReason"] == "end_turn" for result in results)
                assert not mock.errors, mock.errors
                assert sorted(mock.calls) == ["a", "b"], {
                    "calls": mock.calls,
                    "tools": [
                        [t.get("name", t.get("type")) for t in r.get("tools", [])]
                        for r in mock.requests
                    ],
                    "permissions": [u for u in acp.updates if "id" in u],
                    "tool_outputs": [
                        i
                        for r in mock.requests
                        for i in r["input"]
                        if i.get("type") == "function_call_output"
                    ],
                }
                for index, marker in enumerate(["POT_CODEX_A_OK", "POT_CODEX_B_OK"]):
                    updates = [
                        u
                        for u in acp.updates
                        if u.get("params", {}).get("sessionId" if args.acp else "threadId")
                        == sessions[index][0]
                    ]
                    assert marker in json.dumps(updates)
                    assert ("POT_CODEX_B_OK" if index == 0 else "POT_CODEX_A_OK") not in json.dumps(
                        updates
                    )
                if args.acp:
                    await acp.request(
                        "session/load", {"sessionId": sessions[0][0], **sessions[0][1]}
                    )
                else:
                    resumed_session = await acp.request(
                        "thread/resume", {"threadId": sessions[0][0], **sessions[0][1]}
                    )
                    assert resumed_session["thread"]["id"] == sessions[0][0]
                    assert resumed_session["model"] == "pot-mock"
                resumed = await prompt(0)
                assert resumed["stopReason"] == "end_turn"
                if not args.acp:
                    assert results[0]["usage"]["total"]["totalTokens"] == 50
                    assert resumed["usage"]["total"]["totalTokens"] == 75
                assert not mock.errors, mock.errors
                # Interrupt an in-flight HTTP model stream; this does not test shell children.
                if args.acp:
                    pending = asyncio.create_task(
                        acp.request(
                            "session/prompt",
                            {
                                "sessionId": sessions[0][0],
                                "prompt": [{"type": "text", "text": "CANCEL_WAIT"}],
                            },
                        )
                    )
                    await asyncio.wait_for(mock.model_started.wait(), 10)
                    acp.send(
                        {
                            "jsonrpc": "2.0",
                            "method": "session/cancel",
                            "params": {"sessionId": sessions[0][0]},
                        }
                    )
                    assert (await pending)["stopReason"] == "cancelled"
                else:
                    turn = await acp.request(
                        "turn/start",
                        {
                            "threadId": sessions[0][0],
                            "input": [{"type": "text", "text": "CANCEL_WAIT"}],
                        },
                    )
                    await asyncio.wait_for(mock.model_started.wait(), 10)
                    await acp.request(
                        "turn/interrupt", {"threadId": sessions[0][0], "turnId": turn["turn"]["id"]}
                    )
                    stopped = await acp.wait_update(
                        "turn/completed", sessions[0][0], turn["turn"]["id"]
                    )
                    assert stopped["turn"]["status"] == "interrupted"
                if not args.acp:
                    for session_id, *_ in sessions:
                        terminals = await acp.request(
                            "thread/backgroundTerminals/list",
                            {"threadId": session_id, "limit": 100},
                        )
                        assert terminals["data"] == [], terminals
                await asyncio.wait_for(mock.model_stopped.wait(), 10)
                assert not mock.errors, mock.errors
                print(
                    json.dumps(
                        {
                            "agent": hello.get("agentInfo", hello.get("userAgent")),
                            "transport": "acp" if args.acp else "app_server",
                            "auxiliary_models": sorted(
                                {r["model"] for r in mock.requests if r["model"] != "pot-mock"}
                            ),
                            "model_requests": len(mock.requests),
                            "mcp_lanes": sorted(mock.calls),
                            "concurrent_sessions": True,
                            "native_resume": True,
                            "cancelled_model_stream_closed": True,
                            "background_children_tested": False,
                            "usage_first": results[0].get("usage"),
                            "usage_resumed": resumed.get("usage"),
                            "paid_calls": 0,
                            "guest_acceptance": False,
                        },
                        ensure_ascii=False,
                    )
                )
            finally:
                await acp.close()
    finally:
        server.close()
        await server.wait_closed()


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--acp", help="Optional ACP executable; omission tests native App Server")
    parser.add_argument("--codex", required=True, help="Installed original Codex executable")
    asyncio.run(run(parser.parse_args()))
