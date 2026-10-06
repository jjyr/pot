#!/usr/bin/env python3
"""Run file operations as the unprivileged Sandbox user, never the executor identity."""

import json
from pathlib import Path
import sys


def run(request):
    op = request["operation"]
    args = request["args"]
    path = Path(request["path"])
    if op == "read":
        with path.open("rb") as stream:
            data = stream.read(2 * 1024 * 1024 + 1)
        if len(data) > 2 * 1024 * 1024:
            raise ValueError("File too large; use a bounded shell operation")
        lines = data.decode("utf-8").splitlines(keepends=True)
        offset = max(0, int(args.get("offset", 1)) - 1)
        text = "".join(lines[offset : offset + min(2000, max(1, args.get("limit", 2000)))])
        return {"text": text[:8000], "truncated": len(text) > 8000}
    if op in ("write", "edit"):
        content = args["content"] if op == "write" else path.read_text()
        if op == "edit":
            if not args["oldText"] or content.count(args["oldText"]) != 1:
                raise ValueError("Edit requires one unique oldText match")
            content = content.replace(args["oldText"], args["newText"], 1)
        if len(content.encode()) > 2 * 1024 * 1024:
            raise ValueError("Content too large")
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text(content)
        return {"written": True}
    if op == "ls":
        entries = []
        size = 0
        for entry in sorted(p.name for p in path.iterdir()):
            size += len(json.dumps(entry).encode()) + 2
            if len(entries) >= 200 or size > 48000:
                return {"entries": entries, "truncated": True}
            entries.append(entry)
        return {"entries": entries, "truncated": False}
    raise ValueError("Unknown file operation")


if __name__ == "__main__":
    try:
        result = run(json.load(sys.stdin))
    except Exception as error:
        result = {"error": str(error)[:1024]}
    print(json.dumps(result, ensure_ascii=False))
