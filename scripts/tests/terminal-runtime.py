#!/usr/bin/env python3
"""Linux guest PTY process-group teardown, with no agents or external services."""

import argparse
import asyncio
import base64
import json
import os
from pathlib import Path
import re
import signal
import struct
import tempfile
import urllib.request


async def run(runtime):
    assert Path("/proc/self/stat").exists(), "Run this teardown check in the Linux guest"
    with tempfile.TemporaryDirectory(prefix="pot-terminal-contract-") as temp:
        root = Path(temp)
        env = {
            "PATH": os.environ["PATH"],
            "HOME": temp,
            "POT_DATA_DIR": str(root / "data"),
            "POT_WORKSPACE": str(root / "workspace"),
            "POT_RUNTIME_BIND": "127.0.0.1:0",
        }
        tracked = []
        process = None

        async def start():
            nonlocal process
            endpoint_file = root / "data/runtime.json"
            endpoint_file.unlink(missing_ok=True)
            process = await asyncio.create_subprocess_exec(
                str(Path(runtime).resolve()),
                env=env,
                stdout=asyncio.subprocess.DEVNULL,
                stderr=asyncio.subprocess.PIPE,
            )
            for _ in range(200):
                if endpoint_file.exists():
                    return json.loads(endpoint_file.read_text())
                assert process.returncode is None, (await process.stderr.read()).decode()
                await asyncio.sleep(0.025)
            raise AssertionError("Runtime did not start")

        endpoint = await start()

        async def api(path, body=None):
            def call():
                request = urllib.request.Request(
                    f"http://127.0.0.1:{endpoint['port']}/api/v1/{path}",
                    data=None if body is None else json.dumps(body).encode(),
                    headers={
                        "Authorization": "Bearer " + endpoint["token"],
                        "Content-Type": "application/json",
                    },
                )
                opener = urllib.request.build_opener(urllib.request.ProxyHandler({}))
                with opener.open(request, timeout=10) as response:
                    return json.load(response)

            return await asyncio.to_thread(call)

        def frame(payload, opcode=2):
            mask = os.urandom(4)
            size = len(payload)
            head = (
                bytes([0x80 | opcode, 0x80 | size])
                if size < 126
                else (bytes([0x80 | opcode, 0x80 | 126]) + struct.pack("!H", size))
            )
            return head + mask + bytes(b ^ mask[i % 4] for i, b in enumerate(payload))

        async def running_children():
            terminal = await api("terminals", {"cols": 80, "rows": 24})
            reader, writer = await asyncio.open_connection("127.0.0.1", endpoint["port"])
            key = base64.b64encode(os.urandom(16)).decode()
            writer.write(
                (
                    f"GET /api/v1/terminals/{terminal['id']}/socket HTTP/1.1\r\n"
                    f"Host: 127.0.0.1:{endpoint['port']}\r\nUpgrade: websocket\r\n"
                    "Connection: Upgrade\r\nSec-WebSocket-Version: 13\r\n"
                    f"Sec-WebSocket-Key: {key}\r\nAuthorization: Bearer {endpoint['token']}\r\n\r\n"
                ).encode()
            )
            await writer.drain()
            head = await asyncio.wait_for(reader.readuntil(b"\r\n\r\n"), 5)
            assert head.startswith(b"HTTP/1.1 101"), "PTY WebSocket handshake failed"
            # Job control gives each job its own group. The background shell forks a
            # grandchild and ignores HUP/TERM; teardown must include the whole session.
            command = (
                'sh -c \'trap "" HUP TERM; sleep 60 & '
                'printf "POT_BG:%s:%s\\n" "$$" "$!"; wait\' & '
                'printf "POT_SHELL:%s\\n" "$$"; '
                'sh -c \'printf "POT_FG:%s\\n" "$$"; exec sleep 60\'\n'
            )
            writer.write(frame(command.encode()))
            await writer.drain()
            output = bytearray()
            async with asyncio.timeout(10):
                while True:
                    first, second = await reader.readexactly(2)
                    size = second & 127
                    if size == 126:
                        size = struct.unpack("!H", await reader.readexactly(2))[0]
                    elif size == 127:
                        size = struct.unpack("!Q", await reader.readexactly(8))[0]
                    assert size <= 65536 and not second & 128
                    payload = await reader.readexactly(size)
                    if first & 15 == 9:
                        writer.write(frame(payload, 10))
                        await writer.drain()
                    elif first & 15 == 2:
                        output.extend(payload)
                    bg = re.search(rb"POT_BG:(\d+):(\d+)\r?\n", output)
                    shell = re.search(rb"POT_SHELL:(\d+)\r?\n", output)
                    fg = re.search(rb"POT_FG:(\d+)\r?\n", output)
                    if bg and shell and fg:
                        pids = [int(shell[1]), int(bg[1]), int(bg[2]), int(fg[1])]
                        tracked.extend(pids)
                        assert all(alive(pid) for pid in pids), "Children were not running"
                        return terminal["id"], pids, writer

        def alive(pid):
            try:
                return Path(f"/proc/{pid}/stat").read_text().rsplit(") ", 1)[1].split()[0] != "Z"
            except FileNotFoundError:
                return False

        async def settled(pids):
            for _ in range(100):
                if not any(alive(pid) for pid in pids):
                    return
                await asyncio.sleep(0.025)
            raise AssertionError("PTY children remain running after confirmed close")

        try:
            terminal, pids, writer = await running_children()
            await api(f"terminals/{terminal}/close", {})
            await settled(pids)
            assert not (await api("terminals"))["sessions"]
            writer.close()
            await writer.wait_closed()
            _, pids, writer = await running_children()
            process.terminate()
            await asyncio.wait_for(process.wait(), 15)
            await settled(pids)
            writer.close()
            await writer.wait_closed()
            previous_boot = endpoint["boot_id"]
            endpoint = await start()
            assert endpoint["boot_id"] != previous_boot
            assert not (await api("terminals"))["sessions"]
            assert (await api("usage/summary?scope="))["lifetime"]["requests"] == 0
            print(
                json.dumps(
                    {
                        "foreground_stopped": True,
                        "background_groups_and_grandchildren_stopped": True,
                        "runtime_shutdown_stopped_children": True,
                        "restart_has_no_pty": True,
                        "model_calls": 0,
                    }
                )
            )
        finally:
            if process and process.returncode is None:
                process.terminate()
                await asyncio.wait_for(process.wait(), 15)
            for pid in tracked:
                if alive(pid):
                    os.kill(pid, signal.SIGKILL)


if __name__ == "__main__":
    parser = argparse.ArgumentParser()
    parser.add_argument("--runtime", required=True)
    asyncio.run(run(parser.parse_args().runtime))
