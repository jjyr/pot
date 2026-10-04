#!/usr/bin/env python3
"""Exercise native Codex through the actual Runtime with isolated homes and no paid calls."""

import argparse
import asyncio
import importlib.util
import json
import os
import sqlite3
from pathlib import Path
import tempfile
import urllib.request

spec = importlib.util.spec_from_file_location(
    "contract", Path(__file__).with_name("codex-contract.py")
)
contract = importlib.util.module_from_spec(spec)
spec.loader.exec_module(contract)


async def run(args):
    mock = contract.Mock(runtime_tools=True)
    server = await asyncio.start_server(mock.handle, "127.0.0.1", 0)
    port = server.sockets[0].getsockname()[1]
    process = None
    with tempfile.TemporaryDirectory(prefix="pot-codex-runtime-") as temp:
        root = Path(temp)
        native = root / "native"
        native.mkdir()
        data = root / "data"
        data.mkdir()
        (native / "config.toml").write_text(
            'model = "pot-mock"\nmodel_provider = "pot_mock"\n'
            '[model_providers.pot_mock]\nname = "Pot mock"\n'
            f'base_url = "http://127.0.0.1:{port}/v1"\n'
            'wire_api = "responses"\nrequires_openai_auth = false\n'
            "[mcp_servers.unmanaged]\n"
            f'url = "http://127.0.0.1:{port}/mcp/x"\n'
            '[mcp_servers.unmanaged.http_headers]\nAuthorization = "Bearer probe-x"\n'
        )
        environment = {
            "PATH": os.environ["PATH"],
            "HOME": temp,
            "CODEX_HOME": str(native),
            "POT_DATA_DIR": str(data),
            "POT_WORKSPACE": str(root / "workspace"),
            "POT_RUNTIME_BIND": "127.0.0.1:0",
        }

        async def start():
            nonlocal process
            (data / "runtime.json").unlink(missing_ok=True)
            process = await asyncio.create_subprocess_exec(
                str(Path(args.runtime).resolve()),
                env=environment,
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
                with urllib.request.urlopen(request, timeout=5) as response:
                    return json.load(response)

            return await asyncio.to_thread(call)

        async def turn(text):
            receipt = await api(
                "messages", {"conversation_id": "main", "request_id": text, "text": text}
            )
            for _ in range(600):
                snapshot = await api("snapshot")
                user = next(m for m in snapshot["messages"] if m["id"] == receipt["message_id"])
                if user["status"] in ("complete", "failed", "cancelled"):
                    assert user["status"] == "complete", snapshot
                    assert any("POT_CODEX" in m["text"] for m in snapshot["messages"]), snapshot
                    return
                await asyncio.sleep(0.05)
            raise AssertionError("Turn did not finish")

        try:
            profile = {
                "id": "codex-test",
                "revision": 1,
                "name": "Codex test",
                "engine": "codex",
                "provider": "pot_mock",
                "model": "pot-mock",
                "auth_ref": "cli:codex",
                "params": {"max_output_tokens": 256, "reasoning_effort": None},
                "tool_policy": {
                    "worker_orchestration": False,
                    "summary_read_only": True,
                    "unattended": "guest_vm",
                },
            }
            bin_dir = root / "bin"
            bin_dir.mkdir()
            (bin_dir / "codex").symlink_to(Path(args.codex).resolve())
            environment["PATH"] = str(bin_dir) + os.pathsep + environment["PATH"]
            await stop()
            endpoint = await start()
            await api("agent", {"profile": profile, "expected_revision": 0, "request_id": "setup"})
            await stop()
            endpoint = await start()
            await turn("LANE_A first")
            before = (await api("agent"))["bindings"][0]["native_ref"]
            await turn("LANE_A second")
            await stop()
            endpoint = await start()
            await turn("LANE_A restart")
            after = (await api("agent"))["bindings"][0]["native_ref"]
            assert before == after and before["thread_id"], (before, after)
            with sqlite3.connect(data / "pot.sqlite") as db:
                receipts = db.execute(
                    "SELECT COUNT(*) FROM metadata WHERE key LIKE 'orchestration.receipt:%'"
                ).fetchone()[0]
                assert receipts == 3, receipts
            with sqlite3.connect(data / "usage.sqlite") as db:
                rows = db.execute(
                    "SELECT input,output,state FROM requests WHERE scope='main'"
                ).fetchall()
                assert rows == [(40, 10, "native_reported")] * 3, rows
            profile["revision"] = 2
            profile["params"]["max_output_tokens"] = 512
            migration = {"profile": profile, "expected_revision": 1, "request_id": "switch-profile"}
            requests_before = len(mock.requests)
            await api("agent", migration)
            await api("agent", migration)
            assert len(mock.requests) == requests_before, (
                "Configuration unexpectedly invoked a model"
            )
            migrated = (await api("agent"))["bindings"]
            assert len(migrated) == 2 and migrated[0]["state"] == "archived", migrated
            before = migrated[1]["native_ref"]
            assert before["thread_id"] != migrated[0]["native_ref"]["thread_id"], migrated
            await stop()
            endpoint = await start()
            await turn("LANE_A migrated")
            mock.background_pid = root / "background.pid"
            await turn("BACKGROUND_TEST")
            assert mock.background_pid.exists(), "Native shell was not executed"
            native_pid = int(mock.background_pid.read_text())
            try:
                os.kill(native_pid, 0)
            except ProcessLookupError:
                pass
            else:
                raise AssertionError("Native background process survived completed turn")
            workers = await asyncio.gather(
                *[
                    api(
                        "tasks",
                        {"request_id": f"worker-{i}", "text": f"LANE_B worker {i}", "resource": ""},
                    )
                    for i in range(2)
                ]
            )
            for _ in range(400):
                page = await api("tasks/query?scope=all&limit=50")
                records = page.get("tasks", page.get("items", []))
                if records and all(t["phase"] == "complete" for t in records):
                    break
                assert not any(t["phase"] == "failed" for t in records), page
                await asyncio.sleep(0.05)
            else:
                raise AssertionError((workers, page))
            worker_bindings = [await api("agent?owner=" + w["id"]) for w in workers]
            threads = [w["bindings"][0]["native_ref"]["thread_id"] for w in worker_bindings]
            assert len(set(threads + [before["thread_id"]])) == 3, threads

            plan = await api(
                "schedules",
                {
                    "request_id": "codex-plan",
                    "action": "create",
                    "spec": {
                        "title": "Codex contract schedule",
                        "goal": "Observe mock source",
                        "kind": "interval",
                        "every_minutes": 1,
                        "enabled": False,
                        "profile": "codex-test",
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
                schedule_threads.append(b["native_ref"]["thread_id"])
            assert len(set(schedule_threads + threads + [before["thread_id"]])) == 5
            # Require a stable idle boundary after worker report delivery.
            idle = 0
            for _ in range(200):
                snapshot = await api("snapshot")
                idle = idle + 1 if snapshot.get("turn") is None else 0
                if idle >= 10:
                    break
                await asyncio.sleep(0.05)
            assert mock.summary_calls > 0, "Read-only summary policy was not exercised"
            accepted = await api(
                "messages",
                {"conversation_id": "main", "request_id": "cancel-test", "text": "CANCEL_WAIT"},
            )
            await asyncio.wait_for(mock.model_started.wait(), 10)
            await api(
                "turn/cancel",
                {"request_id": "cancel-action", "user_message_id": accepted["message_id"]},
            )
            await asyncio.wait_for(mock.model_stopped.wait(), 10)
            for _ in range(200):
                snapshot = await api("snapshot")
                message = next(m for m in snapshot["messages"] if m["id"] == accepted["message_id"])
                if message["status"] == "cancelled":
                    break
                await asyncio.sleep(0.05)
            else:
                raise AssertionError(snapshot)
            await stop()
            native_files = list(native.rglob("*" + before["thread_id"] + ".jsonl"))
            assert native_files, "Native persisted thread was not found"
            for native_file in native_files:
                native_file.unlink()
            endpoint = await start()
            count_before = len(mock.requests)
            accepted = await api(
                "messages",
                {
                    "conversation_id": "main",
                    "request_id": "missing-native",
                    "text": "Do not replace missing native history",
                },
            )
            for _ in range(200):
                snapshot = await api("snapshot")
                message = next(m for m in snapshot["messages"] if m["id"] == accepted["message_id"])
                if message["status"] == "failed":
                    break
                await asyncio.sleep(0.05)
            else:
                raise AssertionError(snapshot)
            broken = (await api("agent"))["bindings"][-1]
            assert broken["state"] == "broken" and broken["native_ref"] == before, broken
            assert len(mock.requests) == count_before, "Missing native history silently restarted"
            assert not mock.errors, mock.errors
            print(
                json.dumps(
                    {
                        "initial_setup_without_pi": True,
                        "native_resume_after_restart": True,
                        "migration_restart_and_idempotency": True,
                        "missing_native_rejected": True,
                        "mcp_receipts": receipts,
                        "summary_policy_checks": mock.summary_calls,
                        "independent_worker_threads": True,
                        "independent_schedule_runs": True,
                        "cancellation": True,
                        "background_process_stopped": True,
                        "model_requests": len(mock.requests),
                        "paid_calls": 0,
                        "guest_acceptance": False,
                    }
                )
            )
        finally:
            if process.returncode is None:
                await stop()
            server.close()
            await server.wait_closed()


if __name__ == "__main__":
    parser = argparse.ArgumentParser()
    parser.add_argument("--runtime", required=True)
    parser.add_argument("--codex", required=True)
    asyncio.run(run(parser.parse_args()))
