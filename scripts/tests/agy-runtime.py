#!/usr/bin/env python3
"""AGY Driver regression with a fake transport; no official CLI/model acceptance claim."""

import argparse
import asyncio
import json
import os
from pathlib import Path
import sys
import tempfile
import urllib.request

FIXTURE = r"""
import json, os, signal, sys, time, uuid
from pathlib import Path
args=sys.argv[1:]
if args == ['--help']:
    print('--print --input-format --output-format stream-json --conversation --model --disable-slash-commands')
    sys.exit(0)
text=sys.stdin.read()
assert args[args.index('--model')+1] == 'agy-mock'
assert '--dangerously-skip-permissions' not in args
assert '--mcp-config' not in args
assert 'POT_TASK_API' not in os.environ and 'POT_TASK_TOKEN' not in os.environ
role='summary' if text.startswith('This is a Pot result summary.') else 'worker' if text.startswith('This is a Pot background task.') else 'main'
with Path(os.environ['AGY_ROLES']).open('a') as f: f.write(json.dumps({'role':role,'prompt':text})+'\n')
record=Path(os.environ['AGY_CALLS'])
with record.open('a') as f: f.write(json.dumps(args)+'\n')
conversation=args[args.index('--conversation')+1] if '--conversation' in args else str(uuid.uuid4())
def emit(v): print(json.dumps(v), flush=True)
if 'ERROR' in text:
    emit({'event':'result','result':{'status':'ERROR','error':'SECRET_DIAGNOSTIC','response':'SECRET_DIAGNOSTIC'}})
    sys.exit(1)
if 'MISMATCH' in text: conversation=str(uuid.uuid4())
if role == 'worker' and '.pot-schedule-result.json' in text:
    Path('.pot-schedule-result.json').write_text(json.dumps({'summary':'AGY fixture observation','fingerprint':'stable-fixture','evidence':['fixture://source'],'artifacts':[]}))
emit({'event':'init','conversation_id':conversation,'init':{'tools':[]}})
if 'CANCEL_WAIT' in text:
    Path(os.environ['AGY_PID']).write_text(str(os.getpid()))
    signal.signal(signal.SIGINT, lambda *_: sys.exit(0))
    while True: time.sleep(0.05)
if 'INVALID_JSON' in text:
    print('SECRET_DIAGNOSTIC',flush=True); sys.exit(1)
emit({'event':'step_update','step_update':{'conversation_id':conversation,'step_type':'agent_response','text_delta':'POT_AGY_OK','usage':{'input_tokens':9999}}})
emit({'event':'result','result':{'conversation_id':conversation,'status':'SUCCESS','response':'POT_AGY_OK','num_turns':1,'usage':{'input_tokens':100,'output_tokens':10,'thinking_tokens':5,'cache_read_tokens':80,'total_tokens':110}}})
if 'TRAILER' in text: print('SECRET_DIAGNOSTIC',flush=True)
"""


async def run(args):
    process = None
    with tempfile.TemporaryDirectory(prefix="pot-agy-driver-") as temp:
        root = Path(temp)
        data = root / "data"
        data.mkdir()
        fixture = root / "agy-fixture"
        fixture.write_text(f"#!{sys.executable}\n" + FIXTURE)
        fixture.chmod(0o700)
        calls = root / "calls"
        pidfile = root / "pid"
        roles = root / "roles"
        env = {
            "PATH": os.environ["PATH"],
            "HOME": temp,
            "POT_DATA_DIR": str(data),
            "POT_WORKSPACE": str(root / "workspace"),
            "POT_RUNTIME_BIND": "127.0.0.1:0",
            "AGY_CALLS": str(calls),
            "AGY_PID": str(pidfile),
            "AGY_ROLES": str(roles),
            "POT_TASK_API": "must-not-reach-cli",
            "POT_TASK_TOKEN": "must-not-reach-cli",
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

        async def settled(accepted, expected):
            for _ in range(400):
                snapshot = await api("snapshot")
                user = next(m for m in snapshot["messages"] if m["id"] == accepted["message_id"])
                if user["status"] in ("complete", "failed", "cancelled"):
                    assert user["status"] == expected, snapshot
                    assert "SECRET_DIAGNOSTIC" not in json.dumps(snapshot), snapshot
                    return snapshot
                await asyncio.sleep(0.05)
            raise AssertionError("Turn did not settle")

        async def turn(text, expected="complete"):
            accepted = await api(
                "messages", {"conversation_id": "main", "request_id": text, "text": text}
            )
            return await settled(accepted, expected)

        try:
            profile = {
                "id": "agy-test",
                "revision": 1,
                "name": "AGY test",
                "engine": "agy",
                "provider": "google",
                "model": "agy-mock",
                "auth_ref": "cli:agy",
                "launch_spec": {
                    "transport": "agy_json",
                    "executable": str(fixture),
                    "cwd_policy": "workspace",
                    "argv": [{"kind": "runtime_arguments"}],
                },
                "params": {"max_output_tokens": 256, "reasoning_effort": None},
                "tool_policy": {
                    "worker_orchestration": False,
                    "summary_read_only": True,
                    "unattended": "guest_vm",
                },
            }
            await api(
                "agent",
                {
                    "profile": profile,
                    "expected_revision": 0,
                    "request_id": "setup",
                    "follow_worker": True,
                },
            )
            await turn("first")
            before = (await api("agent"))["bindings"][0]
            assert before["native_ref"]["conversation_id"]
            assert not before["spec"]["capabilities"]["tool_policy_enforcement"]
            await turn("second")
            await stop()
            endpoint = await start()
            await turn("restart")
            assert (await api("agent"))["bindings"][0]["native_ref"] == before["native_ref"]
            usage = (await api("usage/summary?scope="))["lifetime"]
            assert (usage["input"], usage["output"], usage["cache"]) == (300, 30, 240), usage
            await turn("ERROR", "failed")
            await turn("INVALID_JSON", "failed")
            await turn("MISMATCH", "failed")
            count_before = sum(
                m["role"] == "assistant" for m in (await api("snapshot"))["messages"]
            )
            trailer = await turn("TRAILER", "failed")
            assert sum(m["role"] == "assistant" for m in trailer["messages"]) == count_before, (
                trailer
            )
            assert (await api("agent"))["bindings"][0]["native_ref"] == before["native_ref"]
            fixture.rename(root / "saved")
            count = len(calls.read_text().splitlines())
            await turn("missing-command", "failed")
            assert len(calls.read_text().splitlines()) == count
            assert "AGY command" in (await api("agent"))["needs_rebind"]
            (root / "saved").rename(fixture)
            await turn("restored")
            accepted = await api(
                "messages",
                {"conversation_id": "main", "request_id": "cancel", "text": "CANCEL_WAIT"},
            )
            for _ in range(200):
                if pidfile.exists():
                    break
                await asyncio.sleep(0.025)
            assert pidfile.exists()
            pid = int(pidfile.read_text())
            await api(
                "turn/cancel",
                {"request_id": "cancel-action", "user_message_id": accepted["message_id"]},
            )
            await settled(accepted, "cancelled")
            try:
                os.kill(pid, 0)
            except ProcessLookupError:
                pass
            else:
                raise AssertionError("AGY process survived cancellation")
            await turn("after-cancel")
            assert (await api("agent"))["bindings"][0]["native_ref"] == before["native_ref"]
            invocations = [json.loads(line) for line in calls.read_text().splitlines()]
            assert "--conversation" not in invocations[0]
            assert all("--conversation" in v for v in invocations[1:])
            workers = await asyncio.gather(
                *[
                    api(
                        "tasks",
                        {
                            "request_id": f"worker-{i}",
                            "text": f"worker {i}",
                            "resource": "",
                            "requirements": {"tools": True},
                        },
                    )
                    for i in range(2)
                ]
            )
            for _ in range(400):
                page = await api("tasks/query?scope=all&limit=50")
                assert not any(t["phase"] == "failed" for t in page["items"]), page
                role_records = [json.loads(v) for v in roles.read_text().splitlines()]
                if (
                    len(page["items"]) == 2
                    and all(t["phase"] == "complete" for t in page["items"])
                    and any(v["role"] == "summary" for v in role_records)
                    and (await api("snapshot"))["turn"] is None
                ):
                    break
                await asyncio.sleep(0.05)
            else:
                raise AssertionError("Workers or summary did not complete")
            native_ids = [before["native_ref"]["conversation_id"]]
            for worker in workers:
                ref = (await api("agent?owner=" + worker["id"]))["bindings"][0]["native_ref"]
                native_ids.append(ref["conversation_id"])
            assert len(set(native_ids)) == 3, native_ids
            assert (await api("agent"))["bindings"][0]["native_ref"] == before["native_ref"]
            assert all(
                "Pot task and schedule tools are not provided" in v["prompt"]
                for v in role_records
                if v["role"] == "main"
            )
            assert all(
                "do not delegate work" in v["prompt"] for v in role_records if v["role"] == "worker"
            )
            assert all(
                "do not run commands" in v["prompt"] for v in role_records if v["role"] == "summary"
            )
            plan = await api(
                "schedules",
                {
                    "request_id": "agy-plan",
                    "action": "create",
                    "spec": {
                        "title": "AGY fixture schedule",
                        "goal": "Scheduled worker test",
                        "kind": "interval",
                        "every_minutes": 1,
                        "enabled": False,
                        "profile": "agy-test",
                        "requirements": {"tools": True},
                    },
                },
            )
            fired = await api(
                "schedules", {"request_id": "agy-fire", "action": "run", "schedule_id": plan["id"]}
            )
            for _ in range(400):
                page = await api("tasks/query?scope=all&limit=50")
                scheduled = next(t for t in page["items"] if t["id"] == fired["task_id"])
                assert scheduled["phase"] != "failed", scheduled
                if scheduled["phase"] == "complete":
                    break
                await asyncio.sleep(0.05)
            else:
                raise AssertionError("Scheduled worker did not complete")
            scheduled_ref = (await api("agent?owner=" + fired["task_id"]))["bindings"][0][
                "native_ref"
            ]["conversation_id"]
            assert scheduled_ref not in native_ids
            usage = await api("usage/summary?scope=")
            assert len(usage["agent_models"]) == 1, usage
            breakdown = usage["agent_models"][0]
            assert breakdown["agent"] == "agy" and breakdown["model"] == "agy-mock", breakdown
            for field in ("input", "output", "cache", "requests", "unknown"):
                assert breakdown[field] == usage["total"][field], usage
            main_usage = await api("usage/summary?scope=main")
            assert all(
                r["agent"] == "agy" and r["profile_id"] == "agy-test"
                for r in main_usage["requests"]
            ), main_usage
            print(
                json.dumps(
                    {
                        "transport": "fake",
                        "model_calls": 0,
                        "official_cli_acceptance": False,
                        "account_acceptance": False,
                        "resume_restart": True,
                        "cancel_resume": True,
                        "command_recovery": True,
                        "redaction": True,
                        "usage_final_only": True,
                        "workers_and_summary": True,
                        "scheduled_worker": True,
                        "independent_native_sessions": True,
                        "pot_tools_not_supplied": True,
                        "agent_model_usage": True,
                    }
                )
            )
        finally:
            if process and process.returncode is None:
                await stop()


if __name__ == "__main__":
    parser = argparse.ArgumentParser()
    parser.add_argument("--runtime", required=True)
    asyncio.run(run(parser.parse_args()))
