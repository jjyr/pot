"""Trusted execution argument normalization. Authorization belongs to Control."""

from pathlib import Path


def ordinary(path):
    if path.resolve() != path or path.is_symlink():
        raise ValueError("Resource path must not be a symlink")


def prepare(request, home):
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
    return {"args": args}
