#!/usr/bin/env python3
"""Simulate a prepared upgrade with two real macOS Apps and disposable roots.

This tests the native installation lifecycle, not public release discovery. Both
Apps must contain a pinned update key and the same storage/schema discriminator.
Signature, manifest and download trust are covered by the selfupdate Go tests.
"""

import argparse
import base64
import hashlib
from contextlib import closing
import http.client
import json
import os
from pathlib import Path
import platform
import select
import shutil
import signal
import socket
import subprocess
import sys
import tempfile
import time


class UnixHTTP(http.client.HTTPConnection):
    def __init__(self, path):
        super().__init__("localhost", timeout=60)
        self.path = str(path)

    def connect(self):
        self.sock = socket.socket(socket.AF_UNIX)
        self.sock.settimeout(self.timeout)
        self.sock.connect(self.path)


def call(path, method, url, body=None, token=None):
    connection = UnixHTTP(path)
    headers = {"Content-Type": "application/json"}
    if token:
        headers["Authorization"] = "Bearer " + token
    try:
        connection.request(method, url, json.dumps(body) if body else None, headers)
        response = connection.getresponse()
        data = json.loads(response.read())
        if response.status != 200:
            raise RuntimeError(f"{method} {url}: {response.status}: {data}")
        return data
    finally:
        connection.close()


def write(path, data):
    path.write_text(json.dumps(data))
    path.chmod(0o600)


def wait(predicate, timeout=75):
    deadline = time.monotonic() + timeout
    while time.monotonic() < deadline:
        value = predicate()
        if value:
            return value
        time.sleep(0.1)
    raise TimeoutError("native update did not reach its expected state")


def hosts(directory, ready=True):
    result = []
    for path in directory.glob("host-*.json"):
        try:
            host = json.loads(path.read_text())
            status = call(host["socket"], "GET", "/status")
            if not ready or status["ready"]:
                result.append(status)
        except (OSError, http.client.HTTPException, RuntimeError):
            pass
    return result


def exercise(old, new, scenario, feed):
    # macOS Unix socket paths are limited to 104 bytes. Its default per-user
    # temporary directory can exceed that once a second data root is appended.
    with tempfile.TemporaryDirectory(prefix="pot-native-update-", dir="/private/tmp") as temporary:
        base = Path(temporary).resolve()
        install = base / "Applications with spaces/Pot.app"
        install.parent.mkdir()
        subprocess.run(["ditto", old, install], check=True)
        root = base / "data"
        root.mkdir(mode=0o700)
        working = base / "working directory"
        working.mkdir()
        updates = Path(str(install) + ".updates")
        with (base / "app.log").open("w+") as log:
            process = subprocess.Popen(
                [install / "Contents/MacOS/pot-app", "-root", root],
                stdout=log,
                stderr=log,
                cwd=working,
                env={**os.environ, "POT_NATIVE_UPDATE_SENTINEL": "preserved"},
            )
            second = None
            try:
                initial = wait(lambda: next(iter(hosts(updates)), None))
                endpoint = json.loads((root / "runtime.json").read_text())
                identity = (root / "identity.json").read_bytes()
                second_root = base / "second data"
                second_root.mkdir(mode=0o700)
                second = subprocess.Popen(
                    [install / "Contents/MacOS/pot-app", "-root", second_root],
                    stdout=log,
                    stderr=log,
                    cwd=working,
                )
                wait(lambda: len(hosts(updates)) == 2)
                second_endpoint = json.loads((second_root / "runtime.json").read_text())
                second_identity = (second_root / "identity.json").read_bytes()
                original = json.loads((install / "Contents/Resources/update.json").read_text())
                target = json.loads((new / "Contents/Resources/update.json").read_text())
                assert original["storage_format"] == target["storage_format"]
                assert tuple(map(int, target["version"].split("."))) > tuple(
                    map(int, original["version"].split("."))
                )
                stage = updates / ("stage-" + target["version"]) / "Pot.app"
                stage.parent.mkdir()
                shutil.copytree(new, stage, symlinks=True)
                subprocess.run(["codesign", "--verify", "--deep", "--strict", stage], check=True)
                assert not (stage / "Contents/MacOS/pot").exists()
                if scenario == "launch-failure":
                    # A controlled filesystem fault after staging: the real new
                    # App cannot exec. The installer must restore/relaunch old.
                    (stage / "Contents/MacOS/pot-app").chmod(0o644)
                elif scenario == "startup-failure":
                    # A late package fault that lets exec succeed, but makes
                    # the new executable reject its own installation at boot.
                    broken = dict(target, storage_format="fixture-invalid-format")
                    write(stage / "Contents/Resources/update.json", broken)
                    subprocess.run(["codesign", "--force", "--sign", "-", stage], check=True)
                shutil.copyfile(feed, updates / "verified-feed.json")
                (updates / "verified-feed.json").chmod(0o600)
                write(updates / "prepared.json", {"Version": target["version"], "Path": str(stage)})
                write(updates / "state.json", {"version": target["version"], "phase": "ready"})
                result = call(
                    endpoint["socket"],
                    "POST",
                    "/api/v1/updates",
                    {
                        "action": "install",
                        "version": target["version"],
                        "request_id": "native-" + scenario,
                        "interrupt": True,
                    },
                    endpoint["token"],
                )
                assert result["supported"]

                def finished():
                    state = json.loads((updates / "state.json").read_text())
                    return state if state["phase"] in ("current", "failed", "blocked") else None

                state = wait(finished)
                expected = target["version"] if scenario == "success" else original["version"]
                assert state["phase"] == ("current" if scenario == "success" else "failed"), state
                if scenario != "success":
                    assert "rolled back" in state.get("error", ""), state
                    assert (updates / ("failed-" + target["version"])).is_dir()
                current = wait(
                    lambda: next(
                        (
                            h
                            for h in hosts(updates)
                            if h["version"] == expected and h["root"] == str(root)
                        ),
                        None,
                    )
                )
                restarted = call(current["socket"], "GET", "/restart")
                assert restarted["app"] and restarted["pid"] != process.pid
                assert str(root) in restarted["args"]
                assert Path(restarted["directory"]) == working
                assert "POT_NATIVE_UPDATE_SENTINEL=preserved" in restarted["environment"]
                assert current["root"] == initial["root"] == str(root)
                after = json.loads((root / "runtime.json").read_text())
                assert after["boot_id"] != endpoint["boot_id"]
                assert after["runtime_id"] == endpoint["runtime_id"]
                assert identity == (root / "identity.json").read_bytes()
                other = wait(
                    lambda: next(
                        (
                            h
                            for h in hosts(updates)
                            if h["version"] == expected and h["root"] == str(second_root)
                        ),
                        None,
                    )
                )
                other_command = call(other["socket"], "GET", "/restart")
                assert other_command["pid"] != second.pid
                assert other_command["pid"] != restarted["pid"]
                second_after = json.loads((second_root / "runtime.json").read_text())
                assert second_after["boot_id"] != second_endpoint["boot_id"]
                assert second_after["runtime_id"] == second_endpoint["runtime_id"]
                assert second_identity == (second_root / "identity.json").read_bytes()
                assert (
                    json.loads((install / "Contents/Resources/update.json").read_text())["version"]
                    == expected
                )
                process.wait(timeout=10)
                assert process.returncode == 0
                second.wait(timeout=10)
                assert second.returncode == 0
                subprocess.run(["codesign", "--verify", "--deep", "--strict", install], check=True)
                print(
                    f"NATIVE_UPDATE_OK {scenario}: {original['version']} -> {expected}; "
                    "two real hosts, new PID/boot, same identity/root/arguments, valid bundle signature"
                )
            except BaseException:
                for path in (base / "app.log", updates / "worker.log", updates / "state.json"):
                    if path.exists():
                        print(f"{path.name}:\n{path.read_text()[-12000:]}")
                raise
            finally:
                # Every endpoint here belongs to this test's fresh installation.
                # Never enumerate or signal an App outside that installation.
                for host in hosts(updates, ready=False):
                    command = call(host["socket"], "GET", "/restart")
                    if command["app"] and command["executable"] == str(
                        install / "Contents/MacOS/pot-app"
                    ):
                        with closing(select.kqueue()) as watch:
                            watch.control(
                                [
                                    select.kevent(
                                        command["pid"],
                                        filter=select.KQ_FILTER_PROC,
                                        flags=select.KQ_EV_ADD | select.KQ_EV_ONESHOT,
                                        fflags=select.KQ_NOTE_EXIT,
                                    )
                                ],
                                0,
                            )
                            try:
                                call(host["socket"], "POST", "/prepare")
                                call(host["socket"], "POST", "/exit")
                            except (OSError, http.client.HTTPException, RuntimeError):
                                os.kill(command["pid"], signal.SIGTERM)
                            if not watch.control(None, 1, 15):
                                raise RuntimeError("native test App did not exit during cleanup")
                if process.poll() is None:
                    process.terminate()
                process.wait(timeout=15)
                if second is not None:
                    if second.poll() is None:
                        second.terminate()
                    second.wait(timeout=15)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("old_app", type=Path, nargs="?")
    parser.add_argument("new_app", type=Path, nargs="?")
    parser.add_argument(
        "--build", action="store_true", help="Build both test versions with an ephemeral update key"
    )
    parser.add_argument(
        "--feed", type=Path, help="Pinned-key signed target manifest for existing Apps"
    )
    args = parser.parse_args()
    if platform.system() != "Darwin":
        parser.error("native macOS required")
    if args.build:
        if args.old_app or args.new_app:
            parser.error("--build does not take existing App paths")
        repo = Path(__file__).resolve().parents[2]
        with tempfile.TemporaryDirectory(prefix="pot-update-builds-") as temporary:
            directory = Path(temporary)
            public = subprocess.check_output(
                ["go", "run", "./scripts/sign-release", "keygen", directory / "key.pem"],
                cwd=repo,
                text=True,
            ).strip()
            apps = []
            for version in ("0.1.0", "0.2.0"):
                subprocess.run(
                    [sys.executable, repo / "scripts/build-pot-app"],
                    cwd=repo,
                    check=True,
                    env={
                        **os.environ,
                        "POT_APP_RELEASE": "1",
                        "POT_APP_VERSION": version,
                        "POT_UPDATE_PUBLIC_KEY": public,
                    },
                )
                app = directory / (version + ".app")
                subprocess.run(["ditto", repo / ".local/pot/Pot.app", app], check=True)
                apps.append(app)
            sys.path.insert(0, str(repo / "scripts"))
            from pot_packaging import schema_catalog, ledger_digest

            archive = directory / "target.zip"
            subprocess.run(["ditto", "-c", "-k", "--keepParent", apps[1], archive], check=True)
            metadata = json.loads((apps[1] / "Contents/Resources/update.json").read_text())
            payload = json.dumps(
                {
                    "version": metadata["version"],
                    "storage_format": metadata["storage_format"],
                    "protocol": metadata["protocol"],
                    "schemas": schema_catalog(),
                    "ledger_digest": ledger_digest(),
                    "artifacts": [
                        {
                            "kind": "app",
                            "platform": metadata["platform"],
                            "url": f"https://github.com/jjyr/pot/releases/download/v{metadata['version']}/target.zip",
                            "sha256": hashlib.sha256(archive.read_bytes()).hexdigest(),
                            "size": archive.stat().st_size,
                        }
                    ],
                },
                separators=(",", ":"),
            ).encode()
            signature = (
                subprocess.check_output(
                    ["go", "run", "./scripts/sign-release", "sign"],
                    input=payload,
                    cwd=repo,
                    env={
                        **os.environ,
                        "POT_UPDATE_PUBLIC_KEY": public,
                        "POT_UPDATE_PRIVATE_KEY": (directory / "key.pem").read_text(),
                    },
                )
                .strip()
                .decode()
            )
            feed = directory / "update.json"
            write(feed, {"payload": base64.b64encode(payload).decode(), "signature": signature})
            for scenario in ("success", "launch-failure", "startup-failure"):
                exercise(*apps, scenario, feed)
    else:
        if not args.old_app or not args.new_app or not args.feed:
            parser.error("provide old_app, new_app and --feed, or --build")
        for scenario in ("success", "launch-failure", "startup-failure"):
            exercise(args.old_app.resolve(), args.new_app.resolve(), scenario, args.feed.resolve())


if __name__ == "__main__":
    main()
