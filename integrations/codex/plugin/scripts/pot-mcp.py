#!/usr/bin/env python3
"""Conservative independent Codex MCP setup. Never prints raw client configuration."""

import argparse
import json
import os
from pathlib import Path
import re
import subprocess
import sys
import tomllib
from urllib.parse import urlsplit


def endpoint(value):
    try:
        parsed = urlsplit(value)
        valid = (
            parsed.scheme in {"http", "https"}
            and parsed.hostname
            and not parsed.username
            and not parsed.password
            and not parsed.query
            and not parsed.fragment
            and not any(c.isspace() or ord(c) < 32 for c in value)
        )
        _ = parsed.port
    except (ValueError, TypeError):
        valid = False
    if not valid:
        raise ValueError("invalid_endpoint")
    return value


def config_paths(codex_home, workspace):
    paths = [codex_home / "config.toml"]
    for parent in [*reversed(workspace.parents), workspace]:
        candidate = parent / ".codex/config.toml"
        if candidate not in paths:
            paths.append(candidate)
    return paths


def entries(paths):
    result = []
    for path in paths:
        if not path.exists():
            continue
        # Only server identity, transport and enabled status leave this parser.
        try:
            config = tomllib.loads(path.read_text())
            servers = config.get("mcp_servers", {})
            if not isinstance(servers, dict):
                raise ValueError("invalid_config")
            for name, server in servers.items():
                if not isinstance(server, dict):
                    raise ValueError("invalid_config")
                result.append(
                    (
                        name,
                        {
                            key: server[key]
                            for key in ("url", "command", "enabled")
                            if key in server
                        },
                    )
                )
        except (OSError, ValueError) as error:
            raise ValueError("invalid_config") from error
    return result


def decision(existing, name, url):
    same_name = [server for key, server in existing if key == name]
    same_url = [(key, server) for key, server in existing if server.get("url") == url]
    if len(same_name) == 1 and len(same_url) == 1 and same_url[0][0] == name:
        server = same_name[0]
        if server.get("enabled", True) and not server.get("command"):
            return "keep"
    if same_name or same_url:
        return "conflict"
    return "add"


def client(codex, *args, cwd=None):
    # Client output can contain credentials, headers, auth URLs or diagnostics.
    # Keep it out of helper logs and exceptions, including failures.
    result = subprocess.run([codex, *args], capture_output=True, text=True, timeout=120, cwd=cwd)
    if result.returncode:
        raise ValueError("client_command_failed")
    return result.stdout


def effective_entries(codex, workspace=None):
    try:
        rows = json.loads(client(codex, "mcp", "list", "--json", cwd=workspace))
        if not isinstance(rows, list):
            raise ValueError("invalid_client_metadata")
        result = []
        for row in rows:
            if not isinstance(row, dict) or not isinstance(row.get("name"), str):
                raise ValueError("invalid_client_metadata")
            transport = row.get("transport", {})
            if not isinstance(transport, dict):
                raise ValueError("invalid_client_metadata")
            result.append(
                (
                    row["name"],
                    {
                        "url": transport.get("url"),
                        "command": transport.get("command"),
                        "enabled": row.get("enabled", True),
                    },
                )
            )
        return result
    except (KeyError, TypeError, json.JSONDecodeError) as error:
        raise ValueError("invalid_client_metadata") from error


def configure(codex, codex_home, workspace, name, url, apply=False):
    endpoint(url)
    if not re.fullmatch(r"[A-Za-z0-9_-]{1,64}", name):
        raise ValueError("invalid_server_name")
    local = entries(config_paths(codex_home, workspace))
    effective = effective_entries(codex, workspace)
    plan = decision(local, name, url)
    effective_plan = decision(effective, name, url)
    # Both views must agree. Effective-only entries can be plugin or managed
    # definitions; local-only entries can be untrusted projects. Do not guess.
    if plan == "conflict" or effective_plan == "conflict" or plan != effective_plan:
        raise ValueError("connection_conflict_review_in_client")
    if plan == "keep":
        return "existing_connection_preserved"
    if apply:
        client(codex, "mcp", "add", "--help", cwd=workspace)
        # Recheck before the standard command, which overwrites existing names.
        if (
            entries(config_paths(codex_home, workspace)) != local
            or effective_entries(codex, workspace) != effective
        ):
            raise ValueError("configuration_changed")
        try:
            client(codex, "mcp", "add", name, "--url", url, cwd=workspace)
        except (OSError, subprocess.SubprocessError, ValueError) as error:
            raise ValueError("configuration_outcome_unknown_review_in_client") from error
        after = decision(effective_entries(codex, workspace), name, url)
        if after != "keep":
            raise ValueError("configuration_outcome_unknown_review_in_client")
        return "connection_added_authenticate_in_client"
    return "new_connection_requires_apply"


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--codex", default="codex")
    parser.add_argument(
        "--codex-home",
        type=Path,
        default=Path(os.environ.get("CODEX_HOME", Path.home() / ".codex")),
    )
    parser.add_argument("--workspace", type=Path, default=Path.cwd())
    parser.add_argument("--name", required=True)
    parser.add_argument("--url", required=True)
    parser.add_argument("--apply", action="store_true")
    args = parser.parse_args()
    # Preserve HOME and native login location. Explicit CODEX_HOME is the
    # client's documented configuration boundary, also inherited by its CLI.
    os.environ["CODEX_HOME"] = str(args.codex_home.resolve())
    try:
        print(
            configure(
                args.codex,
                args.codex_home.resolve(),
                args.workspace.resolve(),
                args.name,
                args.url,
                args.apply,
            )
        )
    except ValueError as error:
        codes = {
            "invalid_endpoint",
            "invalid_server_name",
            "invalid_config",
            "invalid_client_metadata",
            "client_command_failed",
            "connection_conflict_review_in_client",
            "configuration_changed",
            "configuration_outcome_unknown_review_in_client",
        }
        code = str(error) if str(error) in codes else "setup_stopped_review_connection_in_client"
        print(code, file=sys.stderr)
        return 1
    except (OSError, subprocess.SubprocessError):
        print("client_unavailable_or_io_error_review_connection_in_client", file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main())
