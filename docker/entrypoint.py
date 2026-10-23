"""Run one local Runtime and its authenticated browser gateway as one service."""

import os
from pathlib import Path
import signal
import subprocess
import sys
import time
from urllib.parse import urlsplit

POT = "/opt/pot/bin/pot"
HOME = "/home/pot"
ROOT = "/home/pot/.pot"
BROWSER = "/home/pot/.pot-web"
WORKSPACE = "/home/pot/workspaces"
RESOURCES = "/opt/pot/Resources"
LISTEN = "0.0.0.0:17703"


def main():
    os.umask(0o077)
    if sys.argv[1:] != ["server"]:
        os.execv(POT, [POT, *sys.argv[1:]])

    origin = os.environ["POT_WEB_ORIGIN"]
    url = urlsplit(origin)
    if (
        url.scheme not in {"http", "https"}
        or not url.hostname
        or url.username
        or url.password
        or url.path
        or url.query
        or url.fragment
    ):
        raise ValueError("POT_WEB_ORIGIN must be an exact http(s) origin without a path")
    # Existing roots are checked by Pot. Do not repair ownership, replace keys,
    # rewrite configuration or choose a more permissive execution policy.
    if not os.access(HOME, os.W_OK):
        raise PermissionError("/home/pot must be writable by container UID/GID 1000:1000")
    Path(WORKSPACE).mkdir(mode=0o700, exist_ok=True)
    children = []
    stopping = False

    def stop(_number, _frame):
        nonlocal stopping
        stopping = True

    signal.signal(signal.SIGTERM, stop)
    signal.signal(signal.SIGINT, stop)

    def start(arguments):
        child = subprocess.Popen([POT, *arguments], start_new_session=True)
        children.append(child)
        return child

    result = 0
    try:
        runtime = start(["serve", "-root", ROOT, "-workspace", WORKSPACE, "-resources", RESOURCES])
        deadline = time.monotonic() + 60
        while not stopping:
            if runtime.poll() is not None:
                return runtime.returncode or 1
            ready = subprocess.run(
                [POT, "status", "-root", ROOT],
                stdout=subprocess.DEVNULL,
                stderr=subprocess.DEVNULL,
                timeout=5,
            )
            if ready.returncode == 0:
                break
            if time.monotonic() >= deadline:
                raise TimeoutError("Runtime did not become ready within 60 seconds")
            time.sleep(0.2)
        if stopping:
            return 0
        start(
            [
                "web-ui-server",
                "-root",
                BROWSER,
                "-control",
                "local",
                "-control-root",
                ROOT,
                "-listen",
                LISTEN,
                "-origin",
                origin,
            ]
        )
        while not stopping:
            for child in children:
                if child.poll() is not None:
                    # Even a clean unexpected exit must restart the whole service.
                    return child.returncode or 1
            time.sleep(0.2)
    finally:
        # Browser first, then Runtime. Allow Runtime to finish its own cleanup.
        for child in reversed(children):
            if child.poll() is None:
                child.terminate()
        deadline = time.monotonic() + 35
        for child in reversed(children):
            try:
                child.wait(timeout=max(0.1, deadline - time.monotonic()))
            except subprocess.TimeoutExpired:
                os.killpg(child.pid, signal.SIGKILL)
                child.wait()
                result = 1
    return result


if __name__ == "__main__":
    sys.exit(main())
