#!/usr/bin/env python3
"""Serve identical synthetic Chat fixtures against two exact Git source versions."""

import argparse
from functools import partial
from http.server import SimpleHTTPRequestHandler, ThreadingHTTPServer
import io
from pathlib import Path
import shutil
import subprocess
import tarfile

ROOT = Path(__file__).resolve().parents[2]
FIXTURES = {
    "task-communication": [
        "scripts/tests/fixtures/task-communication.html",
        "scripts/tests/task-communication-fixture.mjs",
    ],
    "communication": [
        "scripts/tests/fixtures/communication-review.html",
        "scripts/tests/pot-ui-communication-review-fixture.mjs",
    ],
    "chat-bubbles": [
        "scripts/tests/fixtures/chat-bubbles.html",
        "scripts/tests/pot-ui-chat-bubbles-fixture.mjs",
    ],
}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--before", required=True)
    parser.add_argument("--after", required=True)
    parser.add_argument("--fixture", choices=FIXTURES, default="communication")
    parser.add_argument("--port", type=int, default=17723)
    args = parser.parse_args()
    sources = ROOT / f"output/pr-images/{args.fixture}-review-sources"
    for label, ref in [("before", args.before), ("after", args.after)]:
        commit = subprocess.check_output(
            ["git", "rev-parse", "--verify", ref + "^{commit}"], cwd=ROOT, text=True
        ).strip()
        target = sources / label
        if target.exists():
            shutil.rmtree(target)
        target.mkdir(parents=True)
        archive = subprocess.check_output(
            ["git", "archive", commit, "web", "pkg/storage/migrations/ui-cache"], cwd=ROOT
        )
        with tarfile.open(fileobj=io.BytesIO(archive)) as tree:
            tree.extractall(target, filter="data")
        for fixture in FIXTURES[args.fixture]:
            destination = target / fixture
            destination.parent.mkdir(parents=True, exist_ok=True)
            shutil.copyfile(ROOT / fixture, destination)
        print(f"{label}: {commit}", flush=True)
    handler = partial(SimpleHTTPRequestHandler, directory=str(sources))
    with ThreadingHTTPServer(("127.0.0.1", args.port), handler) as server:
        print(
            f"http://127.0.0.1:{server.server_port}/after/{FIXTURES[args.fixture][0]}?locale=en&theme=light",
            flush=True,
        )
        try:
            server.serve_forever()
        except KeyboardInterrupt:
            pass


if __name__ == "__main__":
    main()
