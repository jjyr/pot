#!/usr/bin/env python3
"""Qualify a locally loaded image using only disposable containers and a volume."""

import argparse
import http.client
import json
import subprocess
import time
import uuid


def docker(*arguments, check=True):
    return subprocess.run(
        ["docker", *arguments], check=check, text=True, capture_output=True, timeout=90
    )


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--image", required=True, help="Already built/loaded native image")
    parser.add_argument(
        "--sandbox",
        choices=("required", "unavailable"),
        default="required",
        help="Require Protected execution, or explicitly qualify namespace refusal/fail-closed mode",
    )
    args = parser.parse_args()
    suffix = uuid.uuid4().hex
    name, volume = "pot-smoke-" + suffix, "pot-smoke-home-" + suffix
    # Exercise HTTPS public-origin semantics over the same internal HTTP hop
    # used behind a TLS-terminating reverse proxy.
    origin = "https://pot-smoke.invalid"
    port = None

    def inspect():
        return json.loads(docker("inspect", name).stdout)[0]

    def wait(predicate, description, seconds=65):
        deadline = time.monotonic() + seconds
        while time.monotonic() < deadline:
            if predicate():
                return
            time.sleep(0.25)
        raise AssertionError("Timed out: " + description)

    def start():
        nonlocal port
        docker(
            "run",
            "--detach",
            "--name",
            name,
            "--pull=never",
            "--health-interval=1s",
            "--health-start-period=5s",
            "--mount",
            f"type=volume,src={volume},dst=/home/pot",
            "--publish",
            "127.0.0.1::17703",
            "--env",
            "POT_WEB_ORIGIN=" + origin,
            args.image,
        )
        port = int(inspect()["NetworkSettings"]["Ports"]["17703/tcp"][0]["HostPort"])

        def healthy():
            state = inspect()["State"]
            assert state["Running"], "Container exited before becoming ready"
            return state["Health"]["Status"] == "healthy"

        wait(healthy, "healthy services")

    def cli(*arguments):
        return docker("exec", name, "pot", *arguments).stdout.strip()

    def api(path):
        return json.loads(cli("api", "-root", "/home/pot/.pot-go", path))

    def python(code):
        return docker("exec", name, "python3", "-c", code).stdout.strip()

    def request(path, method="GET", cookie=None, token=None, request_origin=None, body=None):
        headers = {"Host": "pot-smoke.invalid"}
        if cookie:
            headers["Cookie"] = cookie
        if token:
            headers["Authorization"] = "Bearer " + token
        if method != "GET":
            headers["Origin"] = request_origin or origin
        if body is not None:
            headers["Content-Type"] = "application/json"
            body = json.dumps(body)
        connection = http.client.HTTPConnection("127.0.0.1", port, timeout=10)
        try:
            connection.request(method, path, body=body, headers=headers)
            response = connection.getresponse()
            return response.status, dict(response.getheaders()), response.read()
        finally:
            connection.close()

    def login(token):
        status, headers, _ = request("/access/token", "POST", token=token)
        assert status == 200, "Token login failed"
        cookie = headers["Set-Cookie"]
        assert "HttpOnly" in cookie and "SameSite=Strict" in cookie and "Secure" in cookie
        return cookie.split(";", 1)[0]

    def fingerprint():
        return python(
            "from pathlib import Path; import hashlib; "
            "p=Path('/home/pot/.pot/secrets/master.key'); "
            "assert p.stat().st_mode & 0o077 == 0 and len(p.read_bytes()) == 32; "
            "print(hashlib.sha256(p.read_bytes()).hexdigest())"
        )

    created_volume = False
    try:
        image = json.loads(docker("image", "inspect", args.image).stdout)[0]
        assert image["Os"] == "linux" and image["Architecture"] in {"amd64", "arm64"}
        engine_arch = docker("info", "--format", "{{.Architecture}}").stdout.strip()
        assert image["Architecture"] == {"x86_64": "amd64", "aarch64": "arm64"}.get(
            engine_arch, engine_arch
        ), "Smoke requires an image native to the Docker Engine"
        assert image["Config"]["User"] == "1000:1000", "Image must default to non-root"
        docker("volume", "create", volume)
        created_volume = True
        start()
        config = inspect()
        assert not config["HostConfig"]["Privileged"]
        assert not config["HostConfig"]["CapAdd"]
        assert config["HostConfig"]["NetworkMode"] != "host"
        assert {m["Destination"] for m in config["Mounts"]} == {"/home/pot"}
        assert python("import os; print(os.geteuid())") == "1000"
        assert request("/")[0] == 200, "Embedded frontend missing"
        assert request("/api/v1/runtime")[0] == 401
        token = cli("access", "-browser-root", "/home/pot/.pot-web", "token")
        # A token cannot replace a session or serve as a Runtime operator key.
        assert request("/api/v1/runtime", token=token)[0] == 401
        assert request("/access/token", "POST", token="invalid")[0] == 401
        cookie = login(token)
        live_cookie = login(token)
        status, _, body = request("/api/v1/runtime", cookie=cookie)
        assert status == 200
        runtime_id = json.loads(body)["runtime_id"]
        assert request("/api/v1/runtime/resources", cookie=cookie)[0] == 404
        assert (
            request(
                "/access/logout", "POST", cookie=cookie, request_origin="https://other.invalid"
            )[0]
            == 403
        )
        assert request("/access/logout", "POST", cookie=cookie)[0] == 200
        assert request("/api/v1/runtime", cookie=cookie)[0] == 401
        policy = api("/api/v1/permissions")["policy"]["machines"][runtime_id]
        assert policy["execution_access"] == "ask", "New node must remain Protected"
        provider = {
            "request_id": "container-smoke-provider",
            "profile_id": "container-smoke-provider",
            "name": "Synthetic smoke provider",
            "model": "unused-model",
            "endpoint": "http://127.0.0.1:9",
            "protocol": "openai_chat",
            "api_key": "synthetic-smoke-key-never-used",
            "max_output_tokens": 128,
        }
        assert request("/api/v1/providers", "POST", cookie=live_cookie, body=provider)[0] == 200
        key_hash = fingerprint()
        python(
            "from pathlib import Path; "
            "Path('/home/pot/workspaces/restart-proof').write_text('persisted'); "
            "assert Path('/home/pot/.pot-go/identity.json').stat().st_mode & 0o077 == 0; "
            "assert Path('/home/pot/.pot-web/browser-access.json').stat().st_mode & 0o077 == 0"
        )
        probe = docker("exec", name, "pot-sandbox-check", check=False)
        print(probe.stdout + probe.stderr, end="")
        if args.sandbox == "required":
            assert probe.returncode == 0, "Protected execution qualification failed"
        else:
            assert probe.returncode == 2, "Expected a known namespace refusal with no workload"
        # Recreate, preserving only the named volume, just like an image upgrade.
        docker("stop", "--time", "45", name)
        assert inspect()["State"]["ExitCode"] == 0, "Unclean service shutdown"
        docker("rm", name)
        start()
        assert api("/api/v1/runtime")["runtime_id"] == runtime_id
        assert cli("access", "-browser-root", "/home/pot/.pot-web", "token") == token
        assert fingerprint() == key_hash, "Credential master key changed"
        assert "container-smoke-provider" in cli(
            "api", "-root", "/home/pot/.pot-go", "/api/v1/providers"
        )
        assert (
            python(
                "from pathlib import Path; print(Path('/home/pot/workspaces/restart-proof').read_text())"
            )
            == "persisted"
        )
        assert request("/api/v1/runtime", cookie=cookie)[0] == 401, "Logout revocation lost"
        assert request("/api/v1/runtime", cookie=live_cookie)[0] == 200, "Session lost on restart"
        assert api("/api/v1/permissions")["policy"]["machines"][runtime_id] == policy
        cli("access", "-browser-root", "/home/pot/.pot-web", "regenerate-token")
        assert request("/api/v1/runtime", cookie=live_cookie)[0] == 401
        assert request("/access/token", "POST", token=token)[0] == 401
        token = cli("access", "-browser-root", "/home/pot/.pot-web", "token")
        login(token)
        # Unexpected death of either service terminates its companion, so the
        # container restart policy can recover the complete single-node service.
        python(
            "from pathlib import Path; import os, signal; "
            "pids=[int(p.name) for p in Path('/proc').iterdir() if p.name.isdigit() "
            "and (p/'cmdline').exists() and b'web-ui-server' in (p/'cmdline').read_bytes().split(b'\\0')]; "
            "assert len(pids)==1; os.kill(pids[0], signal.SIGTERM)"
        )
        wait(lambda: not inspect()["State"]["Running"], "companion shutdown", seconds=45)
        assert inspect()["State"]["ExitCode"] != 0
        print(
            "Docker frontend/authentication, Protected policy, key/data/session persistence, "
            "rotation and lifecycle passed; sandbox=" + args.sandbox
        )
    except Exception:
        # Only this task's synthetic container is inspected. Never print tokens,
        # database contents or the environment, including on assertion failures.
        logs = docker("logs", "--tail", "60", name, check=False)
        print(logs.stdout + logs.stderr, end="")
        raise
    finally:
        docker("rm", "--force", name, check=False)
        if created_volume:
            docker("volume", "rm", volume)


if __name__ == "__main__":
    main()
