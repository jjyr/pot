"""Trusted guest permission state and operation preparation; never a model tool."""

import json
import os
from pathlib import Path
import threading
import uuid

LOCK = threading.RLock()


def ordinary(path):
    if path.resolve() != path or path.is_symlink():
        raise ValueError("Permission path must not be a symlink")


def validate_config(value):
    if (
        not isinstance(value, dict)
        or not {"version", "grants"}.issubset(value)
        or set(value) - {"version", "grants", "policy"}
        or value["version"] != 1
        or not isinstance(value["grants"], list)
        or len(value["grants"]) > 1000
    ):
        raise ValueError("Invalid permission config")
    if "policy" in value:
        policy = value["policy"]
        if (
            not isinstance(policy, dict)
            or set(policy) != {"default"}
            or policy["default"] not in ("ask", "allow")
        ):
            raise ValueError("Invalid permission policy")


class PermissionState:
    def __init__(self, base):
        self.base = Path(base)
        self.directory = self.base / "agent"
        self.file = self.directory / "permissions.json"

    def ensure(self):
        ordinary(self.base)
        self.directory.mkdir(mode=0o755, parents=True, exist_ok=True)
        ordinary(self.directory)
        (self.directory / "skills").mkdir(mode=0o755, exist_ok=True)
        ordinary(self.directory / "skills")
        if not self.file.exists():
            self.save({"version": 1, "policy": {"default": "allow"}, "grants": []})
        ordinary(self.file)

    def load(self):
        self.ensure()
        value = json.loads(self.file.read_text())
        validate_config(value)
        return value

    def save(self, value):
        ordinary(self.directory)
        ordinary(self.file)
        temporary = self.directory / (".permissions-" + uuid.uuid4().hex)
        try:
            descriptor = os.open(temporary, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o644)
            with os.fdopen(descriptor, "w") as output:
                json.dump(value, output, indent=2)
                output.write("\n")
                output.flush()
                os.fsync(output.fileno())
            os.replace(temporary, self.file)
            descriptor = os.open(self.directory, os.O_RDONLY)
            try:
                os.fsync(descriptor)
            finally:
                os.close(descriptor)
        finally:
            temporary.unlink(missing_ok=True)

    def request(self, request, home):
        with LOCK:
            current = self.load()
            action = request.get("action")
            if action == "write":
                if current != request["expected"]:
                    raise ValueError("Guest permissions changed; refresh the request")
                value = request["config"]
                validate_config(value)
                self.save(value)
                current = value
            reply = {"config": current, "file": str(home / ".pot/agent/permissions.json")}
            if action == "prepare":
                args = dict(request["args"])
                tool = request["tool"]
                key = "workdir" if tool == "exec_command" else None
                if key:
                    path = Path(args.get(key, str(home)))
                    if ".." in path.parts:
                        raise ValueError("Parent path components are not allowed")
                    if not path.is_absolute():
                        path = home / path
                    path = (
                        path.resolve(strict=True)
                        if path.exists()
                        else path.parent.resolve(strict=True) / path.name
                    )
                    if tool == "exec_command" and not path.is_dir():
                        raise ValueError("Working directory required")
                    args[key] = str(path)
                if tool == "exec_command":
                    policy = request["policy"]
                    args.setdefault("shell", policy["executable"])
                    args.setdefault("login", policy["allow_login_shell"])
                    args.setdefault("tty", False)
                    if "yield_time_ms" not in args:
                        args.setdefault("timeout_ms", policy["default_timeout_ms"])
                reply["args"] = args
            elif action not in ("read", "write"):
                raise ValueError("Unknown permission action")
            return reply

    def mounts(self, home):
        self.ensure()
        # Both raw backing storage and HOME view are readonly inside tool namespaces.
        return [(str(self.base), str(self.base), True), (str(self.base), str(home / ".pot"), True)]
