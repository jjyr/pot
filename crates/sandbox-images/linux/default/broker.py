#!/usr/bin/env python3
"""Single-owner POC input broker: real RFB gate and serialized CDP driver."""

import asyncio, json, os, pathlib, secrets, struct, time, shutil
from aiohttp import web, ClientSession, WSMsgType

ROOT = pathlib.Path(os.environ["POT_INTEGRATED_ROOT"])
ROOT.mkdir(parents=True, exist_ok=True)
TOKEN = os.environ.get("POT_BROKER_TOKEN") or secrets.token_urlsafe(32)
LOCAL = os.environ.get("POT_BACKEND") == "local"
REMOTE = pathlib.Path(__file__).resolve().parents[1] / "remote-desktop/poc"
state = {
    "owner": "none",
    "phase": "disconnected",
    "epoch": 0,
    "environment": "local-swift-vm" if LOCAL else "remote-docker-p2",
    "observed": None,
}
lock = None
peers = set()


def log(event, **details):
    with (ROOT / "events.jsonl").open("a") as f:
        f.write(
            json.dumps(dict(time=time.time(), event=event, **details), ensure_ascii=False) + "\n"
        )


async def driver(expression=None):
    args = ["/usr/bin/python3", "/data/driver.py"] if LOCAL else [str(REMOTE), "driver"]
    if expression is not None:
        args += ["Runtime.evaluate", json.dumps({"expression": expression, "returnByValue": True})]
    p = await asyncio.create_subprocess_exec(
        *args, stdout=asyncio.subprocess.PIPE, stderr=asyncio.subprocess.PIPE
    )
    try:
        out, err = await asyncio.wait_for(p.communicate(), 20)
    except asyncio.TimeoutError:
        p.kill()
        await p.wait()
        raise RuntimeError("driver timeout")
    if p.returncode:
        raise RuntimeError(err.decode()[-1000:])
    return json.loads(out)


@web.middleware
async def auth(request, handler):
    # Locale is public presentation metadata; only this read is available to the welcome page.
    public_locale = request.path == "/api/locale" and request.method == "GET"
    if not public_locale and (request.path.startswith("/api/") or request.path == "/rfb"):
        if request.headers.get("Origin") not in (None, "http://" + request.host):
            raise web.HTTPForbidden()
        if not secrets.compare_digest(
            request.headers.get("X-Pot-Token", request.query.get("token", "")), TOKEN
        ):
            raise web.HTTPForbidden()
    try:
        return await handler(request)
    except (RuntimeError, ValueError, asyncio.TimeoutError) as e:
        log("error", reason=str(e))
        return web.json_response({"error": str(e)}, status=503)


VIEWER_HTML = pathlib.Path(
    os.environ.get("POT_VIEWER_HTML", str(pathlib.Path(__file__).parent / "assets/index.html"))
)
SUPPORTED_LOCALES = json.loads(VIEWER_HTML.with_name("locales.json").read_text())
locale_value = "en"


async def locale(request):
    global locale_value
    if request.method == "POST":
        value = (await request.json()).get("locale")
        if value not in SUPPORTED_LOCALES:
            raise web.HTTPBadRequest(text="invalid_locale")
        locale_value = value
    return web.json_response(
        {"locale": locale_value}, headers={"Access-Control-Allow-Origin": "http://127.0.0.1:8081"}
    )


async def status(request):
    return web.json_response(dict(state, connected=len(peers)))


cpu_previous = None
storage_cache = {"at": 0, "bytes": None}


def workspace_size():
    total = 0
    for directory, dirs, files in os.walk(ROOT / "workspace", followlinks=False):
        for name in files:
            try:
                entry = pathlib.Path(directory) / name
                if not entry.is_symlink():
                    total += entry.stat().st_size
            except OSError:
                pass
    return total


async def metrics(request):
    global cpu_previous
    raw = [int(v) for v in pathlib.Path("/proc/stat").read_text().splitlines()[0].split()[1:9]]
    total = sum(raw)
    idle = raw[3] + raw[4]
    percent = None
    if cpu_previous and total > cpu_previous[0]:
        percent = round(100 * (1 - (idle - cpu_previous[1]) / (total - cpu_previous[0])), 1)
    cpu_previous = (total, idle)
    memory = {
        line.split(":")[0]: int(line.split()[1]) * 1024
        for line in pathlib.Path("/proc/meminfo").read_text().splitlines()
    }
    disk = shutil.disk_usage("/")
    if time.monotonic() - storage_cache["at"] > 30:
        storage_cache["at"] = time.monotonic()
        storage_cache["bytes"] = await asyncio.to_thread(workspace_size)
    metadata = (
        json.loads((ROOT / "environment.json").read_text())
        if (ROOT / "environment.json").exists()
        else {}
    )
    if os.environ.get("POT_CONTAINER") == "podman":
        # /proc reports host-wide resources in containers; use the private cgroup.
        group = pathlib.Path("/sys/fs/cgroup")
        limit = (group / "memory.max").read_text().strip()
        quota, period = (group / "cpu.max").read_text().split()
        cpus = int(quota) / int(period) if quota != "max" else None
        usage = dict(line.split() for line in (group / "cpu.stat").read_text().splitlines())
        now = time.monotonic()
        used = int(usage["usage_usec"])
        percent = None
        previous = getattr(metrics, "container_cpu", None)
        if previous and cpus and now > previous[0]:
            percent = round(100 * (used - previous[1]) / ((now - previous[0]) * 1e6 * cpus), 1)
        metrics.container_cpu = (now, used)
        started = int(pathlib.Path("/proc/1/stat").read_text().rsplit(")", 1)[1].split()[19])
        return web.json_response(
            dict(
                metadata,
                cpus=cpus,
                cpu_percent=percent,
                memory_used=int((group / "memory.current").read_text()),
                memory_total=int(limit) if limit != "max" else None,
                disk_used=None,
                disk_total=None,
                workspace_bytes=storage_cache["bytes"],
                uptime_seconds=max(
                    0,
                    int(
                        float(pathlib.Path("/proc/uptime").read_text().split()[0])
                        - started / os.sysconf("SC_CLK_TCK")
                    ),
                ),
            )
        )
    return web.json_response(
        dict(
            metadata,
            cpu_percent=percent,
            memory_used=memory["MemTotal"] - memory["MemAvailable"],
            memory_total=memory["MemTotal"],
            disk_used=disk.used,
            disk_total=disk.total,
            workspace_bytes=storage_cache["bytes"],
            uptime_seconds=int(float(pathlib.Path("/proc/uptime").read_text().split()[0])),
        )
    )


async def open_browser(request):
    async with lock:
        if state["owner"] != "human":
            raise web.HTTPConflict(text="take_over_required")
        process = await asyncio.create_subprocess_exec(
            "/bin/su",
            "potdesktop",
            "-c",
            "/usr/local/bin/pot-browser",
            stdout=asyncio.subprocess.DEVNULL,
            stderr=asyncio.subprocess.DEVNULL,
        )
        # Retain/reap the launcher without tying the request to the browser lifetime.
        task = asyncio.create_task(process.wait())
        browser_tasks.add(task)
        task.add_done_callback(browser_tasks.discard)
    return web.json_response({"opened": True})


browser_tasks = set()


async def control(request):
    action = (await request.json())["action"]
    async with lock:
        if action == "take":
            state.update(phase="taking_over")
            log("taking_over")
            # Acquiring lock drains in-flight driver requests before granting human input.
            state.update(
                owner="human",
                phase="human_control" if peers else "disconnected",
                epoch=state["epoch"] + 1,
            )
        elif action == "return":
            state.update(owner="none", phase="returning")
            log("returning")
            for peer in list(peers):
                await peer.release()
                await peer.fence()
            try:
                browser_pid = (
                    (ROOT / "profile/browser.pid").read_text().strip()
                    if (ROOT / "profile/browser.pid").exists()
                    else ""
                )
                browser_alive = (
                    browser_pid.isdigit() and pathlib.Path("/proc", browser_pid).exists()
                )
                state["observed"] = await driver() if browser_alive else {"browser": "closed"}
            except Exception:
                state.update(owner="human", phase="human_control" if peers else "disconnected")
                raise
            state.update(
                owner="agent",
                phase="viewing" if peers else "disconnected",
                epoch=state["epoch"] + 1,
            )
        else:
            raise web.HTTPBadRequest()
        log("control", **state)
    return web.json_response(state)


async def write(request):
    body = await request.json()
    epoch = state["epoch"]
    owner = state["owner"]
    async with lock:
        if owner != "agent" or state["owner"] != "agent" or epoch != state["epoch"]:
            log("driver.rejected", owner=state["owner"])
            raise web.HTTPConflict(text="Human owns input")
        value = body["text"]
        assert isinstance(value, str) and len(value) < 4096
        log("driver.begin")
        result = await driver(
            'document.querySelector("#text").value='
            + json.dumps(value)
            + ';document.querySelector("#text").dispatchEvent(new Event("input"));JSON.stringify(window.potState)'
        )
        log("driver.end")
        return web.json_response(result)


class Peer:
    def __init__(self, up):
        self.up = up
        self.keys = set()
        self.qkeys = {}
        self.pointer = (0, 0)
        self.fence_wait = None
        self.fence_marker = None
        self.tail = b""

    async def fence(self):
        marker = secrets.token_bytes(16)
        self.fence_marker = marker
        self.fence_wait = asyncio.get_running_loop().create_future()
        await self.up.send_bytes(struct.pack(">B3xIB", 248, 0x80000001, len(marker)) + marker)
        await asyncio.wait_for(self.fence_wait, 5)
        log("viewer.fence.ack")

    def received(self, data):
        if self.fence_wait is None or self.fence_wait.done():
            return data
        self.tail += data
        if len(self.tail) > 16 * 1024 * 1024:
            raise RuntimeError("Fence buffer limit")
        index = self.tail.find(self.fence_marker)
        if index >= 9 and self.tail[index - 9] == 248 and self.tail[index - 1] == 16:
            # This fence belongs to the broker, not noVNC. Consume its response.
            payload = self.tail[: index - 9] + self.tail[index + 16 :]
            self.tail = b""
            self.fence_wait.set_result(True)
            return payload
        return b""

    async def release(self):
        for key in self.keys:
            await self.up.send_bytes(struct.pack(">BBHI", 4, 0, 0, key))
        self.keys.clear()
        for packet in self.qkeys.values():
            await self.up.send_bytes(packet[:2] + b"\x00\x00" + packet[4:])
        self.qkeys.clear()
        await self.up.send_bytes(struct.pack(">BBHH", 5, 0, *self.pointer))


async def rfb(request):
    ws = web.WebSocketResponse()
    await ws.prepare(request)
    async with ClientSession() as session:
        try:
            async with session.ws_connect(
                os.environ.get("POT_UPSTREAM_WS", "http://127.0.0.1:17680/websockify"),
                protocols=["binary"],
            ) as up:
                peer = Peer(up)
                peers.add(peer)
                state["phase"] = "human_control" if state["owner"] == "human" else "viewing"
                log("viewer.connected", owner=state["owner"])

                async def downstream():
                    async for msg in up:
                        if msg.type == WSMsgType.BINARY:
                            payload = peer.received(msg.data)
                            if payload:
                                await ws.send_bytes(payload)
                    await ws.close()

                task = asyncio.create_task(downstream())
                buf = bytearray()
                handshake = [12, 1, 1]
                try:
                    async for msg in ws:
                        if msg.type != WSMsgType.BINARY:
                            continue
                        if not buf:
                            buffer_epoch = state["epoch"]
                            buffer_owner = state["owner"]
                        elif buffer_epoch != state["epoch"]:
                            raise RuntimeError(
                                "Control changed during partial RFB message; reconnect"
                            )
                        buf.extend(msg.data)
                        if len(buf) > 4 * 1024 * 1024:
                            raise RuntimeError("RFB buffer limit")
                        while buf:
                            if handshake:
                                n = handshake[0]
                                kind = -1
                            else:
                                kind = buf[0]
                                fixed = {0: 20, 3: 10, 4: 8, 5: 6, 150: 10, 255: 12}
                                if kind in fixed:
                                    n = fixed[kind]
                                elif kind == 2:
                                    if len(buf) < 4:
                                        break
                                    n = 4 + 4 * int.from_bytes(buf[2:4], "big")
                                elif kind == 6:
                                    if len(buf) < 8:
                                        break
                                    n = 8 + abs(int.from_bytes(buf[4:8], "big", signed=True))
                                elif kind == 248:
                                    if len(buf) < 9:
                                        break
                                    n = 9 + buf[8]
                                elif kind == 251:
                                    if len(buf) < 8:
                                        break
                                    n = 8 + 16 * buf[6]
                                else:
                                    raise RuntimeError("Unknown RFB client message " + str(kind))
                            if len(buf) < n:
                                break
                            packet = bytes(buf[:n])
                            del buf[:n]
                            if handshake:
                                handshake.pop(0)
                            async with lock:
                                caps = (
                                    kind == 6
                                    and len(packet) >= 12
                                    and int.from_bytes(packet[4:8], "big", signed=True) < 0
                                    and (int.from_bytes(packet[8:12], "big") & 0x01000000) != 0
                                )
                                if (
                                    kind in (4, 5, 6, 255, 251)
                                    and not caps
                                    and (
                                        state["owner"] != "human"
                                        or buffer_owner != "human"
                                        or buffer_epoch != state["epoch"]
                                    )
                                ):
                                    log("viewer.input.rejected", kind=kind, owner=state["owner"])
                                    continue
                                if kind == 4:
                                    key = int.from_bytes(packet[4:8], "big")
                                    if packet[1]:
                                        peer.keys.add(key)
                                    else:
                                        peer.keys.discard(key)
                                if kind == 255:
                                    code = packet[8:12]
                                    if int.from_bytes(packet[2:4], "big"):
                                        peer.qkeys[code] = packet
                                    else:
                                        peer.qkeys.pop(code, None)
                                if kind == 5:
                                    peer.pointer = struct.unpack(">HH", packet[2:6])
                                if kind in (4, 5, 255):
                                    log("viewer.forward", kind=kind, hex=packet.hex())
                                await up.send_bytes(packet)
                finally:
                    async with lock:
                        try:
                            await peer.release()
                        except Exception:
                            pass
                        peers.discard(peer)
                        state["phase"] = "disconnected" if not peers else state["phase"]
                        log("viewer.disconnected", owner=state["owner"])
                    task.cancel()
                    await asyncio.gather(task, return_exceptions=True)
        except Exception as e:
            log("rfb.error", reason=str(e))
    await ws.close()
    return ws


app = web.Application(middlewares=[auth], client_max_size=8192)
app.router.add_get("/api/locale", locale)
app.router.add_post("/api/locale", locale)
app.router.add_get("/api/status", status)
app.router.add_get("/api/metrics", metrics)
app.router.add_post("/api/browser", open_browser)
app.router.add_post("/api/control", control)
app.router.add_post("/api/driver", write)
app.router.add_get("/rfb", rfb)
app.router.add_static("/novnc/", pathlib.Path("/opt/pot/novnc") if LOCAL else ROOT / "novnc")
app.router.add_get("/", lambda r: web.FileResponse(VIEWER_HTML))
app.router.add_get("/i18n.js", lambda r: web.FileResponse(VIEWER_HTML.with_name("i18n.js")))
app.router.add_get(
    "/locales.json", lambda r: web.FileResponse(VIEWER_HTML.with_name("locales.json"))
)
(ROOT / "url").write_text("http://127.0.0.1:17681/#" + TOKEN)
os.chmod(ROOT / "url", 0o600)


async def shutdown(app):
    for peer in list(peers):
        await peer.up.close()


app.on_shutdown.append(shutdown)


async def setup(app):
    global lock
    lock = asyncio.Lock()


app.on_startup.append(setup)
log("broker.ready")
web.run_app(
    app,
    host=os.environ.get("POT_BROKER_BIND", "127.0.0.1"),
    port=17681,
    print=None,
    shutdown_timeout=3,
)
