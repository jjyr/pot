#!/usr/bin/env python3
"""Original Claude CLI + Runtime contract with isolated state and loopback Anthropic API."""

import argparse
import asyncio
import json
import os
from pathlib import Path
import signal
import sqlite3
import sys
import tempfile
import urllib.request


async def run(args):
    requests = []
    fault_status = None
    errors = []
    model_started = asyncio.Event()
    model_stopped = asyncio.Event()
    summary_checks = 0
    pidfile = None

    async def handle(reader, writer):
        nonlocal summary_checks, fault_status
        try:
            head = await asyncio.wait_for(reader.readuntil(b"\r\n\r\n"), 10)
            lines = head.decode().split("\r\n")
            verb, path, _ = lines[0].split()
            headers = dict(line.lower().split(":", 1) for line in lines[1:] if ":" in line)
            body = json.loads(await reader.readexactly(int(headers.get("content-length", 0))))
            if verb != "POST" or not path.startswith("/v1/messages"):
                writer.write(
                    b"HTTP/1.1 404 Not Found\r\nContent-Length: 2\r\nConnection: close\r\n\r\n{}"
                )
                await writer.drain()
                return
            requests.append(body)
            if fault_status is not None:
                fault = fault_status
                response = json.dumps(
                    {
                        "type": "error",
                        "error": {
                            "type": "authentication_error"
                            if fault == 401
                            else "invalid_request_error",
                            "message": "Fixture authentication expired",
                        },
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
            assert len(requests) < 50, "Mock request loop"
            assert body["model"] == "claude-mock", "Unexpected auxiliary model"
            names = [tool["name"] for tool in body.get("tools", [])]
            assert not any(
                n in names
                for n in (
                    "Task",
                    "Agent",
                    "SendMessage",
                    "Workflow",
                    "CronCreate",
                    "ScheduleWakeup",
                )
            ), names
            current = next(m for m in reversed(body["messages"]) if m["role"] == "user")
            content = current["content"]
            if "CANCEL_WAIT" in json.dumps(current):
                writer.write(
                    b"HTTP/1.1 200 OK\r\nContent-Type: text/event-stream\r\nConnection: close\r\n\r\n"
                )
                await writer.drain()
                model_started.set()
                try:
                    await asyncio.wait_for(reader.read(), 20)
                except ConnectionResetError:
                    pass  # Linux CLI may reset instead of sending a clean TCP FIN.
                model_stopped.set()
                return
            summary = "Pot task completion notification." in json.dumps(current)
            if summary:
                assert not any(n in names for n in ("Bash", "Write", "Edit", "NotebookEdit")), names
                summary_checks += 1
            has_output = isinstance(content, list) and any(
                b.get("type") == "tool_result" for b in content
            )
            if has_output:
                assert not any(
                    b.get("is_error") for b in content if b.get("type") == "tool_result"
                ), content
            tool = next((n for n in names if n.endswith("pot_list_tasks")), None)
            block = {"type": "text", "text": "POT_CLAUDE_OK"}
            if not has_output and "BACKGROUND_TEST" in json.dumps(current):
                block = {
                    "type": "tool_use",
                    "id": f"tool_background_{len(requests)}",
                    "name": "Bash",
                    "input": {
                        "command": f"echo $$ > '{pidfile}'; exec sleep 60",
                        "run_in_background": True,
                        "description": "Verify isolated background process cleanup",
                    },
                }
            elif not has_output and ".pot-schedule-result.json" in json.dumps(current):
                report = json.dumps(
                    {
                        "summary": "Mock source observed",
                        "fingerprint": "stable-v1",
                        "evidence": ["local mock source"],
                        "artifacts": [],
                    }
                )
                block = {
                    "type": "tool_use",
                    "id": f"tool_schedule_{len(requests)}",
                    "name": "Bash",
                    "input": {
                        "command": "printf '%s' '" + report + "' > .pot-schedule-result.json",
                        "description": "Write deterministic schedule report",
                    },
                }
            elif tool and not has_output:
                block = {
                    "type": "tool_use",
                    "id": f"tool_pot_{len(requests)}",
                    "name": tool,
                    "input": {},
                }
            message = {
                "id": f"msg_{len(requests)}",
                "type": "message",
                "role": "assistant",
                "model": body["model"],
                "content": [],
                "stop_reason": None,
                "stop_sequence": None,
                "usage": {"input_tokens": 20, "output_tokens": 0},
            }
            events = [
                {"type": "message_start", "message": message},
                {
                    "type": "content_block_start",
                    "index": 0,
                    "content_block": {
                        **block,
                        **({"text": ""} if block["type"] == "text" else {"input": {}}),
                    },
                },
                {
                    "type": "content_block_delta",
                    "index": 0,
                    "delta": (
                        {"type": "text_delta", "text": block["text"]}
                        if block["type"] == "text"
                        else {
                            "type": "input_json_delta",
                            "partial_json": json.dumps(block["input"]),
                        }
                    ),
                },
                {"type": "content_block_stop", "index": 0},
                {
                    "type": "message_delta",
                    "delta": {
                        "stop_reason": "end_turn" if block["type"] == "text" else "tool_use",
                        "stop_sequence": None,
                    },
                    "usage": {"output_tokens": 5},
                },
                {"type": "message_stop"},
            ]
            writer.write(
                b"HTTP/1.1 200 OK\r\nContent-Type: text/event-stream\r\nConnection: close\r\n\r\n"
            )
            for event in events:
                writer.write(
                    ("event: " + event["type"] + "\ndata: " + json.dumps(event) + "\n\n").encode()
                )
            await writer.drain()
        except Exception as e:
            errors.append(str(e))
        finally:
            writer.close()

    server = await asyncio.start_server(handle, "127.0.0.1", 0)
    port = server.sockets[0].getsockname()[1]
    process = None
    with tempfile.TemporaryDirectory(prefix="pot-claude-runtime-") as temp:
        root = Path(temp)
        pidfile = root / "background.pid"
        data = root / "data"
        data.mkdir()
        native = root / "native"
        native.mkdir()
        bin_dir = root / "bin"
        bin_dir.mkdir()
        (bin_dir / "claude").symlink_to(Path(args.claude).resolve())
        env = {
            "PATH": str(bin_dir) + os.pathsep + os.environ["PATH"],
            "HOME": temp,
            "CLAUDE_CONFIG_DIR": str(native),
            "ANTHROPIC_API_KEY": "fixture-not-a-real-key",
            "ANTHROPIC_BASE_URL": f"http://127.0.0.1:{port}",
            "HTTPS_PROXY": f"http://127.0.0.1:{port}",
            "HTTP_PROXY": f"http://127.0.0.1:{port}",
            "NO_PROXY": "127.0.0.1",
            "POT_DATA_DIR": str(data),
            "POT_WORKSPACE": str(root / "workspace"),
            "POT_RUNTIME_BIND": "127.0.0.1:0",
        }

        async def start():
            nonlocal process
            (data / "runtime.json").unlink(missing_ok=True)
            process = await asyncio.create_subprocess_exec(
                str(Path(args.runtime).resolve()),
                env=env,
                stdout=asyncio.subprocess.DEVNULL,
                stderr=asyncio.subprocess.PIPE,
            )
            for _ in range(200):
                if (data / "runtime.json").exists():
                    return json.loads((data / "runtime.json").read_text())
                if process.returncode is not None:
                    raise AssertionError((await process.stderr.read()).decode())
                await asyncio.sleep(0.025)
            raise AssertionError("Runtime did not start")

        async def stop():
            process.terminate()
            await asyncio.wait_for(process.wait(), 15)

        endpoint = await start()

        async def api(path, body=None):
            def call():
                request = urllib.request.Request(
                    f"http://127.0.0.1:{endpoint['port']}/api/v1/{path}",
                    data=None if body is None else json.dumps(body).encode(),
                    headers={
                        "Authorization": f"Bearer {endpoint['token']}",
                        "Content-Type": "application/json",
                    },
                )
                with urllib.request.urlopen(request, timeout=10) as response:
                    return json.load(response)

            return await asyncio.to_thread(call)

        async def turn(text, expected="complete"):
            accepted = await api(
                "messages", {"conversation_id": "main", "request_id": text, "text": text}
            )
            for _ in range(4800 if expected == "failed" else 800):
                snapshot = await api("snapshot")
                user = next(m for m in snapshot["messages"] if m["id"] == accepted["message_id"])
                if user["status"] in ("complete", "failed", "cancelled"):
                    assert user["status"] == expected, snapshot
                    if expected == "complete":
                        assert any("POT_CLAUDE_OK" in m["text"] for m in snapshot["messages"]), (
                            snapshot
                        )
                    return snapshot
                await asyncio.sleep(0.05)
            raise AssertionError("Turn did not settle")

        try:
            wrapper = root / "fixture-wrapper"
            wrapper.write_text(
                "#!/bin/sh\n"
                '[ "$1" = "--wrapper-label" ] && [ "$2" = "a b" ] || exit 91\n'
                "shift 2\n"
                'exec claude "$@"\n'
            )
            wrapper.chmod(0o700)
            profile = {
                "id": "claude-test",
                "revision": 1,
                "name": "Claude test",
                "engine": "claude",
                "provider": "anthropic",
                "model": "claude-mock",
                "auth_ref": "cli:claude",
                "launch_spec": {
                    "transport": "claude_json",
                    "executable": str(wrapper),
                    "cwd_policy": "workspace",
                    "argv": [
                        {"kind": "literal", "value": "--wrapper-label"},
                        {"kind": "literal", "value": "a b"},
                        {"kind": "runtime_arguments"},
                    ],
                },
                "params": {"max_output_tokens": 256, "reasoning_effort": None},
                "tool_policy": {
                    "worker_orchestration": False,
                    "summary_read_only": True,
                    "unattended": "guest_vm",
                },
            }
            await api("agent", {"profile": profile, "expected_revision": 0, "request_id": "setup"})
            await turn("first")
            before = (await api("agent"))["bindings"][0]["native_ref"]
            await turn("second")
            await stop()
            endpoint = await start()
            await turn("restart")
            after = (await api("agent"))["bindings"][0]["native_ref"]
            assert before == after and before["started"], (before, after)
            with sqlite3.connect(data / "usage.sqlite") as db:
                rows = db.execute(
                    "SELECT input,output,state FROM requests WHERE scope='main'"
                ).fetchall()
                assert rows == [(40, 10, "native_reported")] * 3, rows
            # A provider authentication failure must retain the original native session.
            fault_status = 401
            failure_started = asyncio.get_running_loop().time()
            failed = await turn("authentication-expired", expected="failed")
            assert asyncio.get_running_loop().time() - failure_started < 20, (
                "Authentication retry did not settle promptly"
            )
            assert any("Claude authentication required" in m["text"] for m in failed["messages"]), (
                failed
            )
            fault_status = None
            assert failed["engine"]["phase"] == "ready" and failed["turn"] is None, failed
            assert not any(
                "Fixture authentication expired" in m["text"] for m in failed["messages"]
            ), failed
            with sqlite3.connect(data / "usage.sqlite") as db:
                assert (
                    db.execute(
                        "SELECT COUNT(*) FROM requests WHERE scope='main' AND state='native_incomplete'"
                    ).fetchone()[0]
                    == 1
                )
            failed_binding = (await api("agent"))["bindings"][0]
            assert failed_binding["native_ref"] == before and failed_binding["state"] == "active", (
                failed_binding
            )
            await turn("authentication-restored")
            assert (await api("agent"))["bindings"][0]["native_ref"] == before
            # A non-retryable provider error must not quote provider diagnostic text.
            fault_status = 400
            rejected = await turn("invalid-provider-request", expected="failed")
            fault_status = None
            assert not any(
                "Fixture authentication expired" in m["text"] for m in rejected["messages"]
            ), rejected
            assert (await api("agent"))["bindings"][0]["native_ref"] == before
            await turn("provider-request-restored")
            profile["revision"] = 2
            profile["params"]["max_output_tokens"] = 512
            migration = {"profile": profile, "expected_revision": 1, "request_id": "switch-profile"}
            requests_before = len(requests)
            await api("agent", migration)
            await api("agent", migration)
            assert len(requests) == requests_before, "Configuration invoked a model"
            migrated = (await api("agent"))["bindings"]
            assert len(migrated) == 2 and migrated[0]["state"] == "archived", migrated
            assert migrated[1]["native_ref"]["session_id"] != before["session_id"], migrated
            await stop()
            endpoint = await start()
            await turn("migrated")
            before = (await api("agent"))["bindings"][-1]["native_ref"]
            await turn("BACKGROUND_TEST")
            assert pidfile.exists(), "Native background shell was not executed"
            child = int(pidfile.read_text())
            try:
                os.kill(child, 0)
            except ProcessLookupError:
                pass
            else:
                raise AssertionError("Background process survived turn settlement")
            workers = await asyncio.gather(
                *[
                    api(
                        "tasks",
                        {"request_id": f"worker-{i}", "text": f"worker {i}", "resource": ""},
                    )
                    for i in range(2)
                ]
            )
            for _ in range(800):
                page = await api("tasks/query?scope=all&limit=50")
                tasks = page["items"]
                assert not any(t["phase"] == "failed" for t in tasks), page
                if len(tasks) == 2 and all(t["phase"] == "complete" for t in tasks):
                    break
                await asyncio.sleep(0.05)
            else:
                raise AssertionError(page)
            sessions = [before["session_id"]]
            for worker in workers:
                binding = (await api("agent?owner=" + worker["id"]))["bindings"][0]
                sessions.append(binding["native_ref"]["session_id"])
            assert len(set(sessions)) == 3, sessions
            plan = await api(
                "schedules",
                {
                    "request_id": "claude-plan",
                    "action": "create",
                    "spec": {
                        "title": "Claude contract schedule",
                        "goal": "Observe mock source",
                        "kind": "interval",
                        "every_minutes": 1,
                        "enabled": True,
                        "profile": "claude-test",
                    },
                },
            )
            schedule_threads = []
            for index in range(2):
                fired = await api(
                    "schedules",
                    {"request_id": f"fire-{index}", "action": "run", "schedule_id": plan["id"]},
                )
                for _ in range(400):
                    page = await api("tasks/query?scope=all&limit=50")
                    records = page.get("tasks", page.get("items", []))
                    task = next(t for t in records if t["id"] == fired["task_id"])
                    if task["phase"] == "complete":
                        break
                    assert task["phase"] != "failed", await api("tasks/" + fired["task_id"])
                    await asyncio.sleep(0.05)
                else:
                    raise AssertionError(task)
                b = (await api("agent?owner=" + fired["task_id"]))["bindings"][0]
                schedule_threads.append(b["native_ref"]["session_id"])
            assert len(set(schedule_threads + sessions)) == 5
            # Wait for the real scheduler clock, without invoking action=run.
            for _ in range(1400):
                clock = await api("schedules?selected=" + plan["id"])
                automatic = [
                    r for r in clock["runs"] if r["task_id"] and not r["id"].startswith("manual:")
                ]
                if automatic and automatic[0]["state"] == "complete":
                    break
                assert not any(r["state"] == "failed" for r in automatic), clock
                await asyncio.sleep(0.05)
            else:
                raise AssertionError("Native scheduled run did not fire from the clock")
            scheduled_binding = (await api("agent?owner=" + automatic[0]["task_id"]))["bindings"][0]
            assert scheduled_binding["native_ref"]["session_id"] not in schedule_threads
            await api(
                "schedules",
                {"request_id": "pause-clock", "action": "pause", "schedule_id": plan["id"]},
            )
            idle = 0
            for _ in range(800):
                snapshot = await api("snapshot")
                idle = idle + 1 if snapshot["turn"] is None else 0
                if idle >= 10:
                    break
                await asyncio.sleep(0.05)
            assert summary_checks > 0, "Summary policy not exercised"
            accepted = await api(
                "messages",
                {"conversation_id": "main", "request_id": "cancel", "text": "CANCEL_WAIT"},
            )
            print("Cancellation: awaiting mock request", flush=True, file=sys.stderr)
            await asyncio.wait_for(model_started.wait(), 30)
            print("Cancellation: request reached mock", flush=True, file=sys.stderr)
            await api(
                "turn/cancel",
                {"request_id": "cancel-action", "user_message_id": accepted["message_id"]},
            )
            print("Cancellation: awaiting disconnect", flush=True, file=sys.stderr)
            await asyncio.wait_for(model_stopped.wait(), 30)
            print("Cancellation: disconnected", flush=True, file=sys.stderr)
            for _ in range(400):
                snapshot = await api("snapshot")
                user = next(m for m in snapshot["messages"] if m["id"] == accepted["message_id"])
                if user["status"] == "cancelled":
                    break
                await asyncio.sleep(0.05)
            else:
                raise AssertionError(snapshot)
            await stop()
            native_files = list(native.rglob(before["session_id"] + ".jsonl"))
            assert native_files, "Native persisted session was not found"
            for native_file in native_files:
                native_file.unlink()
            endpoint = await start()
            count_before = len(requests)
            accepted = await api(
                "messages",
                {
                    "conversation_id": "main",
                    "request_id": "missing-native",
                    "text": "Do not replace missing native history",
                },
            )
            for _ in range(400):
                snapshot = await api("snapshot")
                message = next(m for m in snapshot["messages"] if m["id"] == accepted["message_id"])
                if message["status"] == "failed":
                    break
                await asyncio.sleep(0.05)
            else:
                raise AssertionError(snapshot)
            broken = (await api("agent"))["bindings"][-1]
            assert broken["state"] == "broken" and broken["native_ref"] == before, broken
            assert len(requests) == count_before, "Missing native history silently restarted"
            assert not errors, errors
            print(
                json.dumps(
                    {
                        "native_resume": True,
                        "migration_restart_and_idempotency": True,
                        "missing_native_rejected": True,
                        "independent_workers": True,
                        "independent_schedule_runs": True,
                        "native_clock_trigger": True,
                        "authentication_failure_recovery": True,
                        "provider_diagnostic_redacted": True,
                        "summary_checks": summary_checks,
                        "background_stopped": True,
                        "cancelled": True,
                        "model_requests": len(requests),
                        "paid_calls": 0,
                        "account_acceptance": False,
                    }
                )
            )
        finally:
            if sys.exc_info()[0] is not None:
                print(
                    "Fixture failure",
                    {"requests": len(requests), "errors": errors},
                    file=sys.stderr,
                    flush=True,
                )
            if process.returncode is None:
                await stop()
            if pidfile.exists():
                try:
                    os.kill(int(pidfile.read_text()), signal.SIGKILL)
                except ProcessLookupError:
                    pass
            server.close()
            await server.wait_closed()


if __name__ == "__main__":
    parser = argparse.ArgumentParser()
    parser.add_argument("--runtime", required=True)
    parser.add_argument("--claude", required=True)
    asyncio.run(run(parser.parse_args()))
