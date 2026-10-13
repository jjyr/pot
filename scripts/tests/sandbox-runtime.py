#!/usr/bin/env python3
"""Real Host pot-agent + two Linux VMs; loopback models, isolated data, no paid calls."""

import argparse
import base64
import struct
import asyncio
import json
import os
from pathlib import Path
import re
import tempfile
import urllib.error
import urllib.request
import uuid


class Model:
    def __init__(self):
        self.requests = []
        self.errors = []
        self.worker_loadouts = 0
        self.summary_requests = 0

    async def handle(self, reader, writer):
        try:
            head = await reader.readuntil(b"\r\n\r\n")
            lines = head.decode().split("\r\n")
            headers = dict(line.lower().split(":", 1) for line in lines[1:] if ":" in line)
            body = json.loads(await reader.readexactly(int(headers["content-length"])))
            inputs = body.get("input", body.get("messages", []))
            user_index = max(
                (i for i, item in enumerate(inputs) if item.get("role") == "user"), default=0
            )
            current = inputs[user_index:]
            text = json.dumps(current[0], ensure_ascii=False)
            # Extract the JSON value directly from user text, regardless of transport wrapping.
            marker = None
            for item in (
                current[0].get("content", []) if isinstance(current[0].get("content"), list) else []
            ):
                if isinstance(item, dict):
                    matches = re.findall(r"(?m)^PROBE:(.*)", item.get("text", ""))
                    if matches:
                        marker = matches[-1]
            if marker is None and isinstance(current[0].get("content"), str):
                matches = re.findall(r"(?m)^PROBE:(.*)", current[0]["content"])
                if matches:
                    marker = matches[-1]
            if marker is None:
                actions = []  # auxiliary title / completion-summary requests
                worker = False
            else:
                probe, _ = json.JSONDecoder().raw_decode(marker)
                actions, worker = probe["actions"], probe.get("worker", False)
            tools = []
            for tool in body.get("tools", []):
                if tool.get("type") == "namespace":
                    tools.extend(tool["name"] + "__" + t["name"] for t in tool.get("tools", []))
                else:
                    tools.append(
                        tool.get("name", tool.get("function", {}).get("name", tool.get("type", "")))
                    )
            forbidden = {
                "shell",
                "shell_command",
                "view_image",
                "node_repl",
                "spawn_agent",
                "send_input",
                "wait_agent",
                "web_search",
            }
            assert not any(t.rsplit("__", 1)[-1] in forbidden for t in tools), tools
            assert not any("unmanaged" in t for t in tools), tools
            if "Pot task completion notification." in text:
                pot_names = {t.rsplit("__", 1)[-1] for t in tools}
                assert pot_names == {"list_tasks", "get_task"}, pot_names
                self.summary_requests += 1
            if worker:
                expected = {"exec_command", "write_stdin", "apply_patch"}
                expected |= {"exec", "wait"}
                names = {t.rsplit("__", 1)[-1] for t in tools}
                assert names == expected, names
                self.worker_loadouts += 1
            system = json.dumps(
                [item for item in inputs if item.get("role") in ("system", "developer")],
                ensure_ascii=False,
            )
            if marker is not None:
                assert "<name>skill-creator</name>" in system, system
                assert "<name>skill-installer</name>" in system, system
            self.requests.append({"tools": tools, "worker": worker})
            for output in current:
                if output.get("role") == "tool" or output.get("type") == "function_call_output":
                    raw = output.get("output", output.get("content", ""))
                    try:
                        value = json.loads(raw) if isinstance(raw, str) else raw
                        if isinstance(value, dict) and (
                            value.get("isError") or value.get("exit_code", 0) not in (None, 0)
                        ):
                            print(
                                "TOOL_FAILURE",
                                json.dumps(value, ensure_ascii=False)[:5000],
                                flush=True,
                            )
                    except ValueError:
                        pass
            count = sum(item.get("role") == "tool" for item in current)
            action = actions[count] if count < len(actions) else None

            if action and action["args"].get("session_id") == "$last_session":

                def sessions(value):
                    if isinstance(value, str):
                        try:
                            return sessions(json.loads(value))
                        except (ValueError, TypeError):
                            return []
                    if isinstance(value, dict):
                        found = (
                            [value["session_id"]]
                            if isinstance(value.get("session_id"), str)
                            and value["session_id"] != "$last_session"
                            else []
                        )
                        return found + [sid for child in value.values() for sid in sessions(child)]
                    if isinstance(value, list):
                        return [sid for child in value for sid in sessions(child)]
                    return []

                ids = sessions(current[1:]) or sessions(inputs)
                assert ids, current
                action = json.loads(json.dumps(action))
                action["args"]["session_id"] = ids[-1]

            call_id = "probe-" + str(count)
            delta = {"role": "assistant", "content": "VERIFIED"}
            if action:
                assert action["name"] in tools, tools
                delta = {
                    "role": "assistant",
                    "tool_calls": [
                        {
                            "index": 0,
                            "id": call_id,
                            "type": "function",
                            "function": {
                                "name": action["name"],
                                "arguments": json.dumps(action["args"]),
                            },
                        }
                    ],
                }

            def chunk(delta, finish=None):
                return {
                    "id": "chat-" + str(len(self.requests)),
                    "object": "chat.completion.chunk",
                    "created": 1,
                    "model": body["model"],
                    "choices": [{"index": 0, "delta": delta, "finish_reason": finish}],
                }

            events = [
                chunk(delta),
                chunk({}, "tool_calls" if action else "stop"),
                {
                    "choices": [],
                    "usage": {"prompt_tokens": 20, "completion_tokens": 5, "total_tokens": 25},
                },
            ]
            response = (
                "".join(f"data: {json.dumps(e)}\n\n" for e in events) + "data: [DONE]\n\n"
            ).encode()
            writer.write(
                f"HTTP/1.1 200 OK\r\nContent-Type: text/event-stream\r\nContent-Length: {len(response)}\r\nConnection: close\r\n\r\n".encode()
                + response
            )
            await writer.drain()
        except Exception as error:
            self.errors.append(repr(error))
            print("MODEL ERROR", repr(error), flush=True)
        finally:
            writer.close()
            await writer.wait_closed()


def action(tool, **args):
    return {"name": tool, "args": args}


def prompt(actions, worker=False):
    if not worker:
        actions = json.loads(json.dumps(actions))
        for action in actions:
            if action["name"] in {"exec_command", "write_stdin", "apply_patch"}:
                action["args"].setdefault(
                    "execution_target", "sandbox:" + action["args"].get("sandbox_id", "default")
                )
    return "PROBE:" + json.dumps({"actions": actions, "worker": worker})


async def run(args):
    model = Model()
    mock = await asyncio.start_server(model.handle, "127.0.0.1", 0)
    port = mock.sockets[0].getsockname()[1]
    resources = Path(args.resources).resolve()
    helper = Path(args.helper).resolve()
    with tempfile.TemporaryDirectory(prefix="pot-sbx-", dir="/tmp") as temp:
        root = Path(temp)
        importer = (
            Path(__file__).resolve().parents[2]
            / ".local/poc/cargo-target/debug/pot-resource-import"
        )
        assets = Path(args.resource_assets).resolve()
        seed = await asyncio.create_subprocess_exec(
            str(importer), str(root / "resource-cache"), str(assets)
        )
        assert await seed.wait() == 0, "Verified offline provisioning failed"
        for sandbox_id in ("default", "linux-b"):
            config_path = root / "sandboxes" / sandbox_id / "sandbox.json"
            config_path.parent.mkdir(parents=True, exist_ok=True)
            config_path.write_text(json.dumps({"cpus": 2, "memory_gib": 2, "disk_gib": 8}))
        environment = {
            "PATH": str(resources / "node/bin") + ":" + "/usr/bin:/bin:/opt/homebrew/bin",
            "HOME": temp,
            "POT_AGENT_SKILLS_DIR": str(root / "skills-user"),
        }
        log_path = Path(args.output).resolve().with_suffix(".helper.log")
        log = log_path.open("wb")
        process = await asyncio.create_subprocess_exec(
            str(helper), temp, str(resources), env=environment, stdout=log, stderr=log
        )
        endpoint = None

        async def wait_endpoint(old_boot=None):
            nonlocal endpoint
            for _ in range(400):
                path = root / "product/runtime.json"
                if path.exists():
                    candidate = json.loads(path.read_text())
                    endpoint = candidate
                    try:
                        snap = await api("snapshot")
                        if old_boot is None or snap["boot_id"] != old_boot:
                            return snap
                    except (OSError, urllib.error.URLError):
                        pass
                assert process.returncode is None, log_path.read_text()[-4000:]
                await asyncio.sleep(0.05)
            raise AssertionError("Host Runtime unavailable")

        async def api(path, body=None, token=None):
            def request():
                req = urllib.request.Request(
                    f"http://127.0.0.1:{endpoint['port']}/api/v1/{path}",
                    data=None if body is None else json.dumps(body).encode(),
                    headers={
                        "Authorization": "Bearer " + (token or endpoint["token"]),
                        "Content-Type": "application/json",
                    },
                )
                with urllib.request.urlopen(req, timeout=180) as response:
                    return json.load(response)

            return await asyncio.to_thread(request)

        async def ipc(method):
            reader, writer = await asyncio.open_unix_connection(str(root / "helper.sock"))
            writer.write((json.dumps({"id": str(uuid.uuid4()), "method": method}) + "\n").encode())
            await writer.drain()
            reply = json.loads(await reader.readline())
            writer.close()
            await writer.wait_closed()
            assert "error" not in reply, reply
            return reply

        async def submit(text):
            body = {"conversation_id": "main", "request_id": str(uuid.uuid4()), "text": text}
            for _ in range(400):
                try:
                    return await api("messages", body)
                except urllib.error.HTTPError as error:
                    if error.code != 409:
                        raise
                    await asyncio.sleep(0.05)
            raise AssertionError("Main lane remained occupied")

        async def finish(receipt, expected="complete"):
            for _ in range(4000):
                snapshot = await api("snapshot")
                msg = next(m for m in snapshot["messages"] if m["id"] == receipt["message_id"])
                if msg["status"] in ("complete", "failed", "cancelled"):
                    assert msg["status"] == expected, snapshot
                    return snapshot
                await asyncio.sleep(0.05)
            raise AssertionError("Turn failed to settle")

        async def turn(actions):
            return await finish(await submit(prompt(actions)))

        async def exercise_shell_sessions(prefix):
            await turn(
                [
                    action(
                        "exec_command",
                        login=False,
                        cmd=f"printf '%s\\n' 'printf x >> \"$HOME/{prefix}-captures\"' 'export POT_BASELINE=BASELINE' 'pot_baseline() {{ printf FUNCTION; }}' > .bash_profile",
                    )
                ]
            )
            for _ in range(2):
                await turn(
                    [
                        action(
                            "exec_command",
                            cmd=f"printf '%s:' \"$POT_BASELINE\" > {prefix}-baseline; pot_baseline >> {prefix}-baseline",
                        )
                    ]
                )
            assert (default / f"{prefix}-baseline").read_text() == "BASELINE:FUNCTION"
            assert (default / f"{prefix}-captures").read_text() == "x"
            await turn(
                [
                    action(
                        "exec_command",
                        cmd='while IFS= read -r line; do eval "$line"; done',
                        tty=True,
                        login=False,
                        yield_time_ms=10,
                    )
                ]
            )
            await turn(
                [
                    action("manage_sandbox", action="select", sandbox_id="linux-b"),
                    action(
                        "write_stdin",
                        session_id="$last_session",
                        rows=35,
                        cols=110,
                        chars=f"stty size > {prefix}-terminal; printf BOUND >> {prefix}-terminal; exit\n",
                        yield_time_ms=1000,
                    ),
                    action("manage_sandbox", action="select", sandbox_id="default"),
                    action("exec_command", login=False, cmd="rm .bash_profile"),
                ]
            )
            assert (default / f"{prefix}-terminal").read_text() == "35 110\nBOUND"
            assert not (second / f"{prefix}-terminal").exists()
            report[f"{prefix}_pty_cross_turn_and_snapshot"] = True

        async def reject(path, body, token=None):
            try:
                await api(path, body, token)
            except urllib.error.HTTPError as error:
                assert error.code in (401, 403, 409, 500), error.code
                return
            raise AssertionError("Forbidden operation accepted")

        report = {}
        try:
            await wait_endpoint()
            initial = await api("sandboxes")
            assert initial["items"][0]["state"]["phase"] == "stopped", initial
            assert not (root / "sandboxes/default/workspace/executor-token").exists()
            await api(
                "sandboxes",
                {
                    "action": "update",
                    "sandbox_id": "default",
                    "name": "Primary Linux",
                    "config": {"cpus": 2, "memory_gib": 2, "disk_gib": 8},
                },
            )
            configured = (await api("sandboxes"))["items"][0]
            assert configured["name"] == "Primary Linux" and configured["config"]["memory_gib"] == 2
            assert configured["running_tasks"] == 0 and configured["tasks"] == []
            assert configured["state"]["phase"] == "stopped"
            report["scoped_settings_without_start"] = True
            report["lazy_start"] = True
            await api(
                "providers",
                {
                    "request_id": str(uuid.uuid4()),
                    "profile_id": "pot-test",
                    "name": "Pot fixture",
                    "expected_revision": 0,
                    "endpoint": f"http://127.0.0.1:{port}/v1/chat/completions",
                    "model": "pot-mock",
                    "protocol": "openai_chat",
                    "api_key": "fixture",
                    "follow_main": True,
                },
            )
            await turn([])
            assert (await api("sandboxes"))["items"][0]["state"]["phase"] == "stopped"
            await turn(
                [
                    action(
                        "manage_sandbox",
                        action="create",
                        sandbox_id="linux-b",
                        name="Second Linux",
                    ),
                    action(
                        "apply_patch",
                        patch="*** Begin Patch\n*** Add File: pot.txt\n+POT_DEFAULT\n*** End Patch",
                    ),
                    action("exec_command", cmd="uname -s > os.txt; cat pot.txt"),
                    action("exec_command", cmd="cat pot.txt"),
                    action(
                        "apply_patch",
                        patch="*** Begin Patch\n*** Add File: pot.txt\n+POT_SECOND\n*** End Patch",
                        sandbox_id="linux-b",
                    ),
                ]
            )
            default = root / "sandboxes/default/workspace/workspace"
            second = root / "sandboxes/linux-b/workspace/workspace"
            assert (default / "pot.txt").read_text() == "POT_DEFAULT\n"
            assert (second / "pot.txt").read_text() == "POT_SECOND\n"
            assert (default / "os.txt").read_text().strip() == "Linux"
            assert not (root / "product/agent-workspace/pot.txt").exists()
            report["pot_main_two_sandboxes"] = True
            await turn(
                [
                    action(
                        "exec_command",
                        cmd=r"""python3 - <<'SKILLS'
from pathlib import Path
import os, errno, subprocess
base=Path.home()/'.pot/agent/skills'
for name in ['skill-creator', 'skill-installer']:
    assert (base/name/'SKILL.md').is_file()
    subprocess.run(['python3', str(base/'skill-creator/scripts/quick_validate.py'), str(base/name)], check=True)
bundle=base/'probe-user'
bundle.mkdir(parents=True,exist_ok=True)
(bundle/'SKILL.md').write_text('---\nname: probe-user\ndescription: Skills mapping acceptance\n---\nREADONLY_PAYLOAD\n')
Path('skills-main.uid').write_text(str(os.getuid()))
for name in ['skill-creator', 'skill-installer']:
    try:
        (base/name/'SKILL.md').write_text('forged')
        raise AssertionError('Builtin skill writable')
    except OSError as error:
        assert error.errno == errno.EROFS, error
draft=Path('draft/probe-installed')
draft.mkdir(parents=True)
(draft/'SKILL.md').write_text('---\nname: probe-installed\ndescription: Installation acceptance\n---\nInstalled instructions\n')
subprocess.run(['python3', str(base/'skill-installer/scripts/install_skill.py'), '--local', str(draft)], check=True)
Path('builtin-skills-readonly.txt').write_text('EROFS')
SKILLS""",
                    )
                ]
            )
            assert (default / "builtin-skills-readonly.txt").read_text() == "EROFS"
            assert (root / "skills-user/probe-installed/SKILL.md").is_file()
            report["builtin_skills_readonly_mapping"] = True
            report["packaged_skill_helpers"] = True
            await turn(
                [action("exec_command", cmd="id -u > skills-main.uid", sandbox_id="linux-b")]
            )
            assert (root / "skills-user/probe-user/SKILL.md").is_file()
            report["main_manages_user_skills"] = True

            await turn(
                [
                    action(
                        "exec_command",
                        cmd='read value; printf "%s" "$value" > session.txt',
                        yield_time_ms=10,
                    ),
                    action("manage_sandbox", action="select", sandbox_id="linux-b"),
                    action(
                        "write_stdin",
                        session_id="$last_session",
                        chars="BOUND\n",
                        close_stdin=True,
                        yield_time_ms=1000,
                    ),
                    action(
                        "apply_patch",
                        patch="*** Begin Patch\n*** Add File: selected.txt\n+SECOND\n*** End Patch",
                    ),
                    action("manage_sandbox", action="select", sandbox_id="default"),
                ]
            )
            assert (default / "session.txt").read_text() == "BOUND"
            assert not (second / "session.txt").exists()
            assert (second / "selected.txt").read_text() == "SECOND\n"
            report["pot_sessions_stay_bound_across_default_change"] = True
            await exercise_shell_sessions("pot")
            # Persistent worker credential and immutable assignment, including API bypass attempts.
            task = await api(
                "tasks",
                {
                    "request_id": str(uuid.uuid4()),
                    "text": prompt(
                        [
                            action(
                                "apply_patch",
                                patch="*** Begin Patch\n*** Add File: worker.txt\n+WORKER\n*** End Patch",
                            ),
                            action(
                                "exec_command",
                                cmd=r"""python3 - <<'SKILLS'
from pathlib import Path
import errno,os
base=Path.home()/'.pot/agent/skills'
assert not os.access('/run/pot-skill-sources',os.R_OK)
assert os.getuid() != int(Path('/data/workspace/skills-main.uid').read_text())
status=dict(line.split(':',1) for line in Path('/proc/self/status').read_text().splitlines() if ':' in line)
assert int(status['NoNewPrivs']) == 1
assert int(status['CapEff'],16) == 0
for name in ['probe-user','probe-installed','skill-creator','skill-installer']:
    path=base/name/'SKILL.md'
    assert path.is_file(),path
    try:
        path.write_text('forged')
        raise AssertionError('Task modified a readonly skill')
    except OSError as error:
        assert error.errno == errno.EROFS,error
try:
    (base/'new-task-skill').mkdir()
    raise AssertionError('Task created a global skill')
except OSError as error:
    assert error.errno == errno.EROFS,error
Path('skills-task.txt').write_text('READONLY')
SKILLS""",
                            ),
                        ],
                        True,
                    ),
                    "profile": "pot-test",
                    "sandbox_id": "linux-b",
                },
            )
            task_id = task.get("id", task.get("task", {}).get("id"))
            assert task_id, task
            for _ in range(1000):
                info = next(
                    t
                    for t in (await api("tasks/query?scope=all&limit=50"))["items"]
                    if t["id"] == task_id
                )
                if info.get("phase") in ("failed", "interrupted"):
                    raise AssertionError(info)
                if info.get("task", info).get("phase") == "complete":
                    break
                await asyncio.sleep(0.05)
            else:
                raise AssertionError(info)
            task_workspace = second / "tasks" / task_id
            assert (task_workspace / "worker.txt").read_text() == "WORKER\n"
            worker_token = json.loads(
                (root / "product/tasks" / task_id / "runtime.json").read_text()
            )["token"]
            await reject("sandboxes", {"action": "stop", "sandbox_id": "linux-b"}, worker_token)
            await reject(
                "execution",
                {
                    "name": "exec_command",
                    "call_id": "override",
                    "args": {"cmd": "true", "sandbox_id": "default"},
                },
                worker_token,
            )
            assert (task_workspace / "skills-task.txt").read_text() == "READONLY"
            assert "READONLY_PAYLOAD" in (root / "skills-user/probe-user/SKILL.md").read_text()
            report["task_skills_readonly_mapping"] = True
            report["pot_worker_fixed_assignment"] = True
            await reject("sandboxes", {"action": "delete", "sandbox_id": "linux-b"})
            await api("sandboxes", {"action": "stop", "sandbox_id": "default"})
            await api("sandboxes", {"action": "stop", "sandbox_id": "linux-b"})
            await api(
                "providers",
                {
                    "profile_id": "api-fixture",
                    "name": "Second API fixture",
                    "model": "pot-mock",
                    "endpoint": f"http://127.0.0.1:{port}/v1/chat/completions",
                    "api_key": "test-only",
                    "protocol": "openai_chat",
                    "expected_revision": 0,
                    "request_id": "api-fixture-setup",
                    "follow_main": True,
                },
            )
            await turn([])
            assert all(
                item["state"]["phase"] == "stopped" for item in (await api("sandboxes"))["items"]
            )
            report["api_profile_chat_without_sandbox"] = True
            await turn(
                [
                    action("list_sandboxes"),
                    action("manage_sandbox", action="select", sandbox_id="default"),
                    action(
                        "apply_patch",
                        patch="*** Begin Patch\n*** Add File: api-fixture.txt\n+API_DEFAULT\n*** End Patch",
                    ),
                    action("exec_command", cmd="cat api-fixture.txt"),
                    action(
                        "exec_command",
                        cmd="echo API_SECOND > api-fixture.txt",
                        sandbox_id="linux-b",
                    ),
                ]
            )
            before = (await api("agent"))["thread"]
            await turn([action("exec_command", cmd="echo RESUMED >> api-fixture.txt")])
            assert (default / "api-fixture.txt").read_text() == "API_DEFAULT\nRESUMED\n"
            assert (second / "api-fixture.txt").read_text().strip() == "API_SECOND"
            report["api_profile_main_and_resume"] = True
            await exercise_shell_sessions("api_fixture")
            task2 = await api(
                "tasks",
                {
                    "request_id": str(uuid.uuid4()),
                    "text": prompt(
                        [
                            action(
                                "apply_patch",
                                patch="*** Begin Patch\n*** Add File: api-worker.txt\n+API_WORKER\n*** End Patch",
                            )
                        ],
                        True,
                    ),
                    "profile": "api-fixture",
                    "sandbox_id": "default",
                },
            )
            task2_id = task2.get("id", task2.get("task", {}).get("id"))
            for _ in range(1000):
                info = next(
                    t
                    for t in (await api("tasks/query?scope=all&limit=50"))["items"]
                    if t["id"] == task2_id
                )
                if info.get("phase") in ("failed", "interrupted"):
                    raise AssertionError(info)
                if info.get("task", info).get("phase") == "complete":
                    break
                await asyncio.sleep(0.05)
            else:
                raise AssertionError(info)
            assert (default / "tasks" / task2_id / "api-worker.txt").read_text() == "API_WORKER\n"
            report["api_profile_worker_loadout"] = True
            # Exercise the real read-only summary before restoring main execution tools.
            for _ in range(400):
                if model.summary_requests and (await api("snapshot"))["turn"] is None:
                    break
                await asyncio.sleep(0.05)
            else:
                raise AssertionError("Read-only completion summary did not settle")
            # Cancellation must contain double-fork / setsid descendants as well as the shell.
            receipt = await submit(
                prompt(
                    [
                        action(
                            "exec_command",
                            cmd="setsid sh -c 'sleep 6; echo ESCAPED > escaped.txt' >/dev/null 2>&1 & echo STARTED > started.txt; sleep 60",
                            timeout_ms=120000,
                        )
                    ]
                )
            )
            for _ in range(400):
                if (default / "started.txt").exists():
                    break
                await asyncio.sleep(0.05)
            else:
                raise AssertionError("Sandbox cancellation command not started")
            await reject("sandboxes", {"action": "stop", "sandbox_id": "default"})
            await reject(
                "sandboxes",
                {
                    "action": "update",
                    "sandbox_id": "default",
                    "name": "Busy",
                    "config": {"cpus": 2, "memory_gib": 2, "disk_gib": 8},
                },
            )
            await api(
                "turn/cancel",
                {"request_id": str(uuid.uuid4()), "user_message_id": receipt["message_id"]},
            )
            await finish(receipt, "cancelled")
            await asyncio.sleep(7)
            assert not (default / "escaped.txt").exists(), (
                "Detached descendant survived cancellation"
            )
            report["cgroup_cancellation_busy_stop"] = True
            boot = (await api("snapshot"))["boot_id"]
            await ipc("backend-restart")
            await wait_endpoint(boot)
            await turn([action("exec_command", cmd="cat api-fixture.txt")])
            after = (await api("agent"))["thread"]
            assert before == after, (before, after)
            report["host_restart_thread_resume"] = True
            # Resume the original worker after Host restart; target remains immutable.
            await api(
                "tasks/" + task_id + "/messages",
                {
                    "request_id": str(uuid.uuid4()),
                    "text": prompt(
                        [
                            action(
                                "apply_patch",
                                patch="*** Begin Patch\n*** Add File: worker-resumed.txt\n+FIXED\n*** End Patch",
                            )
                        ],
                        True,
                    ),
                },
            )
            for _ in range(400):
                if (task_workspace / "worker-resumed.txt").exists():
                    break
                await asyncio.sleep(0.05)
            else:
                raise AssertionError("Worker did not resume in its assigned Sandbox")
            assert (
                next(
                    t
                    for t in (await api("tasks/query?scope=all&limit=50"))["items"]
                    if t["id"] == task_id
                )["sandbox_id"]
                == "linux-b"
            )
            report["worker_restart_assignment"] = True
            # Keep a worker busy in B while stopping A, and probe target override during its active turn.
            long_task = await api(
                "tasks",
                {
                    "request_id": str(uuid.uuid4()),
                    "text": prompt(
                        [
                            action(
                                "exec_command",
                                cmd="echo RUNNING > running.txt; sleep 8; echo DONE > done.txt",
                            )
                        ],
                        True,
                    ),
                    "profile": "api-fixture",
                    "sandbox_id": "linux-b",
                },
            )
            long_id = long_task["id"]
            long_workspace = second / "tasks" / long_id
            for _ in range(400):
                if (long_workspace / "running.txt").exists():
                    break
                await asyncio.sleep(0.05)
            else:
                raise AssertionError("Concurrent worker did not start")
            long_token = json.loads(
                (root / "product/tasks" / long_id / "runtime.json").read_text()
            )["token"]
            await reject(
                "execution",
                {
                    "name": "exec_command",
                    "call_id": "wrong-target",
                    "args": {"cmd": "true", "sandbox_id": "default"},
                },
                long_token,
            )
            await reject(
                "execution",
                {
                    "name": "manage_sandbox",
                    "call_id": "manage",
                    "args": {"action": "stop", "sandbox_id": "linux-b"},
                },
                long_token,
            )
            await api("sandboxes", {"action": "stop", "sandbox_id": "default"})
            await turn([action("exec_command", cmd="cat api-fixture.txt", sandbox_id="linux-b")])
            for _ in range(400):
                if (long_workspace / "done.txt").exists():
                    break
                await asyncio.sleep(0.05)
            else:
                raise AssertionError("Stopping A interrupted worker B")
            inventory = await api("sandboxes")
            assert (
                next(i for i in inventory["items"] if i["id"] == "default")["state"]["phase"]
                == "stopped"
            ), inventory
            assert (default / "api-fixture.txt").exists()
            report["stop_independence_artifacts"] = True
            # Desktop is separate from Agent execution; static HTTP must never expose executor credentials.
            desktop = await api("sandboxes", {"action": "desktop", "sandbox_id": "default"})
            assert desktop["state"]["phase"] == "running"
            reader, writer = await asyncio.open_unix_connection(root / "helper.sock")
            writer.write(
                (
                    json.dumps({"id": "desktop-probe", "method": "status", "sandbox_id": "default"})
                    + "\n"
                ).encode()
            )
            await writer.drain()
            ip = json.loads(await reader.readline())["result"]["ip"]
            writer.close()
            await writer.wait_closed()

            async def desktop_ready():
                def request():
                    with urllib.request.urlopen(f"http://{ip}:17681/", timeout=2) as response:
                        return response.status == 200

                return await asyncio.to_thread(request)

            for _ in range(300):
                try:
                    if await desktop_ready():
                        break
                except OSError:
                    pass
                await asyncio.sleep(0.1)
            else:
                raise AssertionError("Optional desktop unavailable")
            # Qualify the new host gateway against the real guest RFB service.
            ticket = await api("desktop/sessions", {"sandbox_id": "default"})
            desktop_path = ticket["path"]
            reader, writer = await asyncio.open_connection("127.0.0.1", endpoint["port"])
            key = base64.b64encode(os.urandom(16)).decode()
            writer.write(
                (
                    f"GET {desktop_path}rfb HTTP/1.1\r\nHost: 127.0.0.1:{endpoint['port']}\r\nOrigin: http://127.0.0.1:{endpoint['port']}\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Key: {key}\r\nSec-WebSocket-Version: 13\r\n\r\n"
                ).encode()
            )
            await writer.drain()
            assert b"101 Switching Protocols" in await reader.readuntil(b"\r\n\r\n")
            pending = bytearray()

            async def rfb_read(size):
                while len(pending) < size:
                    head = await reader.readexactly(2)
                    assert head[0] & 15 == 2, head
                    length = head[1] & 127
                    if length == 126:
                        length = int.from_bytes(await reader.readexactly(2), "big")
                    elif length == 127:
                        length = int.from_bytes(await reader.readexactly(8), "big")
                    pending.extend(await reader.readexactly(length))
                result = bytes(pending[:size])
                del pending[:size]
                return result

            async def rfb_send(data):
                mask = os.urandom(4)
                length = (
                    bytes([len(data) | 128])
                    if len(data) < 126
                    else b"\xfe" + struct.pack(">H", len(data))
                )
                writer.write(
                    b"\x82"
                    + length
                    + mask
                    + bytes(byte ^ mask[index % 4] for index, byte in enumerate(data))
                )
                await writer.drain()

            assert await rfb_read(12) == b"RFB 003.008\n"
            await rfb_send(b"RFB 003.008\n")
            count = (await rfb_read(1))[0]
            assert 1 in await rfb_read(count)
            await rfb_send(b"\x01")
            assert await rfb_read(4) == b"\x00" * 4
            await rfb_send(b"\x01")
            init = await rfb_read(24)
            assert int.from_bytes(init[:2], "big") > 0
            await rfb_read(int.from_bytes(init[20:24], "big"))

            def display_control(action):
                req = urllib.request.Request(
                    f"http://127.0.0.1:{endpoint['port']}{desktop_path}api/control",
                    data=json.dumps({"action": action}).encode(),
                    headers={
                        "Content-Type": "application/json",
                        "Origin": f"http://127.0.0.1:{endpoint['port']}",
                    },
                )
                with urllib.request.urlopen(req, timeout=15) as reply:
                    return json.load(reply)

            await asyncio.to_thread(display_control, "take")
            await rfb_send(bytes([4, 1, 0, 0, 0, 0, 0, 65]))
            await asyncio.to_thread(display_control, "return")
            await api("desktop/sessions/close", {"session_id": ticket["session"]["id"]})
            writer.close()
            await writer.wait_closed()
            assert (
                next(i for i in (await api("sandboxes"))["items"] if i["id"] == "default")["state"][
                    "phase"
                ]
                == "running"
            )
            report["managed_desktop_gateway_rfb_and_persistent_vm"] = True
            await turn(
                [
                    action(
                        "exec_command",
                        cmd="test ! -e /data/executor-token && test ! -r /run/pot-executor/operations.sqlite && python3 - > desktop-check.txt <<'CHECK'\nimport urllib.request, urllib.error, os, pathlib\nfor process in pathlib.Path('/proc').iterdir():\n    if process.name.isdigit():\n        try:\n            assert process.joinpath('comm').read_text().strip() not in ('chrome', 'chromium'), 'Browser auto-launched'\n        except FileNotFoundError:\n            pass\nassert 'POT_EXECUTOR_TOKEN' not in os.environ\nassert urllib.request.urlopen('http://127.0.0.1:8081/index.html').status == 200\ntry:\n    urllib.request.urlopen('http://127.0.0.1:8081/executor-token')\n    raise AssertionError('Credential exposed')\nexcept urllib.error.HTTPError as error:\n    assert error.code == 404\nprint('DESKTOP_OK')\nCHECK\n",
                    )
                ]
            )
            assert (default / "desktop-check.txt").exists(), "Guest credential boundary failed"
            assert (default / "desktop-check.txt").read_text().strip() == "DESKTOP_OK"

            def no_token_http():
                try:
                    urllib.request.urlopen(f"http://{ip}:8081/executor-token", timeout=2)
                except urllib.error.HTTPError as error:
                    assert error.code == 404
                except urllib.error.URLError:
                    pass  # server deliberately binds guest loopback
                else:
                    raise AssertionError("Executor credential exposed")

            await asyncio.to_thread(no_token_http)
            await api("sandboxes", {"action": "stop", "sandbox_id": "default"})
            assert (root / "sandboxes/default/workspace/browser-stopped").exists()
            report["optional_desktop_and_shutdown"] = True
            assert not model.errors, model.errors
            assert model.worker_loadouts >= 4, model.worker_loadouts
            report["model_requests"] = len(model.requests)
            report["summary_requests"] = model.summary_requests
            Path(args.output).write_text(json.dumps(report, indent=2) + "\n")
            print(json.dumps(report, indent=2), flush=True)
        finally:
            if process.returncode is None:
                process.terminate()
                try:
                    await asyncio.wait_for(process.wait(), 60)
                except asyncio.TimeoutError:
                    process.kill()
                    await process.wait()
            log.close()
            mock.close()
            await mock.wait_closed()
            if model.errors:
                print("MODEL ERRORS", model.errors, flush=True)


if __name__ == "__main__":
    parser = argparse.ArgumentParser()
    parser.add_argument("--helper", required=True)
    parser.add_argument("--resources", required=True)
    parser.add_argument("--resource-assets", default=".local/releases/resources")
    parser.add_argument("--output", default="/tmp/pot-sandbox-acceptance.json")
    asyncio.run(run(parser.parse_args()))
