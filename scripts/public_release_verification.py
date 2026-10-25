"""Verify a public version, its trusted signed feed and the listed download bytes."""

import argparse
import base64
from concurrent.futures import ThreadPoolExecutor, as_completed
from datetime import datetime, timezone
import hashlib
import json
import os
from pathlib import Path
import re
import shutil
import subprocess
import sys
import time
import urllib.error
import urllib.request

from release_contract import ROOT, version_number

REPOSITORY = "jjyr/pot"
METADATA_LIMIT = 1024 * 1024


class Incomplete(Exception):
    """A required public resource is not available yet."""


def require(condition, message):
    if not condition:
        raise ValueError(message)


class HTTPSOnly(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, req, fp, code, msg, headers, newurl):
        require(newurl.startswith("https://"), "insecure download redirect")
        return super().redirect_request(req, fp, code, msg, headers, newurl)


class Network:
    """Anonymous HTTPS reads with bounded retries for explicit transient responses."""

    def __init__(self):
        self.opener = urllib.request.build_opener(HTTPSOnly())
        self.retries = []

    def open(self, url):
        require(url.startswith("https://"), "HTTPS required")
        for attempt in range(3):
            try:
                return self.opener.open(
                    urllib.request.Request(url, headers={"User-Agent": "pot-verify-release"}),
                    timeout=90,
                )
            except urllib.error.HTTPError as error:
                retry = error.code in {429, 502, 503, 504} or (
                    error.code == 403 and error.headers.get("X-RateLimit-Remaining") == "0"
                )
                delay = error.headers.get("Retry-After", "2")
                delay = int(delay) if delay.isdigit() else 2
                if not retry or attempt == 2 or delay > 30:
                    if error.code == 404 or retry:
                        raise Incomplete(
                            f"HTTP {error.code}: public resource unavailable"
                        ) from None
                    raise ValueError(f"HTTP {error.code}: anonymous access refused") from None
                self.retries.append({"http_status": error.code, "delay_seconds": delay})
                time.sleep(delay)

    def data(self, url):
        with self.open(url) as response:
            data = response.read(METADATA_LIMIT + 1)
        require(len(data) <= METADATA_LIMIT, "release/feed metadata exceeds size limit")
        return data

    def download(self, artifact):
        size, expected = artifact["size"], artifact["sha256"]
        digest, total = hashlib.sha256(), 0
        with self.open(artifact["url"]) as response:
            while chunk := response.read(1024 * 1024):
                total += len(chunk)
                require(
                    total <= size,
                    f"download exceeds signed size: expected {size}, received at least {total}",
                )
                digest.update(chunk)
        require(total == size, f"truncated download: expected {size}, received {total}")
        actual = digest.hexdigest()
        require(
            actual == expected, f"download SHA256 conflict: expected {expected}, received {actual}"
        )
        return {"url": artifact["url"], "size": total, "sha256": actual}


def validate_release(release, version):
    require(release["tag_name"] == "v" + version, "public release version/tag mismatch")
    if release["draft"] or release["prerelease"]:
        raise Incomplete("stable release must be public and neither draft nor prerelease")
    return {key: release[key] for key in ["id", "tag_name", "draft", "prerelease"]}


def verify_feed(envelope, version, key):
    # Reuse the existing Ed25519 verifier; never trust a key supplied by the feed.
    result = subprocess.run(
        ["go", "run", "./scripts/sign-release", "verify"],
        cwd=ROOT,
        input=envelope,
        capture_output=True,
        env=dict(os.environ, POT_UPDATE_PUBLIC_KEY=key, CGO_ENABLED="0"),
        timeout=120,
    )
    require(result.returncode == 0, "Ed25519 feed signature verification failed with trusted key")
    feed = json.loads(result.stdout)
    require(feed["version"] == version, "signed feed version mismatch")
    artifacts = feed["artifacts"]
    require(isinstance(artifacts, list) and artifacts, "signed feed has no downloads")
    prefix = f"https://github.com/{REPOSITORY}/releases/download/v{version}/"
    seen = set()
    for item in artifacts:
        url = item["url"]
        require(
            isinstance(url, str) and url.startswith(prefix),
            "signed download URL is outside this release",
        )
        name = url.removeprefix(prefix)
        require(
            re.fullmatch(r"[A-Za-z0-9][A-Za-z0-9._-]*", name), "invalid signed download filename"
        )
        require(name not in seen, "duplicate signed download")
        seen.add(name)
        require(type(item["size"]) is int and item["size"] > 0, "invalid signed download size")
        require(
            isinstance(item["sha256"], str) and re.fullmatch(r"[a-f0-9]{64}", item["sha256"]),
            "invalid signed SHA256",
        )
    return feed


def download_artifacts(network, artifacts, on_result):
    """Hash each signed download without saving, extracting or executing it."""
    errors = []
    with ThreadPoolExecutor(max_workers=3) as pool:
        pending = {
            pool.submit(network.download, artifact): artifact["url"] for artifact in artifacts
        }
        for future in as_completed(pending):
            url = pending[future]
            try:
                receipt = future.result()
            except Exception as error:
                receipt = error
                errors.append(error)
            on_result(url, receipt)
    if errors:
        kind = Incomplete if all(isinstance(error, Incomplete) for error in errors) else ValueError
        raise kind("signed downloads did not all pass; see download_failures in report")
    return {"verified_downloads": len(artifacts)}


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("version", type=version_number)
    parser.add_argument(
        "--public-key",
        default=os.environ.get("POT_UPDATE_PUBLIC_KEY"),
        help="Trusted pinned base64 Ed25519 key; or POT_UPDATE_PUBLIC_KEY",
    )
    parser.add_argument("--output", type=Path, default=Path(".local/release-verification"))
    parser.add_argument("--markdown", action="store_true")
    args = parser.parse_args(argv)
    args.output.mkdir(parents=True, exist_ok=True)
    report = {
        "verified_at": datetime.now(timezone.utc).isoformat(),
        "version": args.version,
        "scope": "public_version_signed_feed_and_listed_downloads",
        "trusted_key_sha256": hashlib.sha256((args.public_key or "").encode()).hexdigest(),
        "status": "incomplete",
        "active_checks": [],
        "checks": [],
        "downloads": {},
        "limitations": [
            "Checks only the public release version, trusted Ed25519 feed and its listed download bytes.",
            "Does not inspect or execute packages, check build source/schema, OCI or Homebrew, or qualify Apple Developer ID/notarization.",
        ],
    }
    network = Network()

    def checkpoint():
        target = args.output / "report.json"
        temporary = target.with_suffix(".json.tmp")
        temporary.write_text(json.dumps(report, indent=2) + "\n")
        temporary.replace(target)

    def check(name, operation):
        report["active_checks"].append(name)
        checkpoint()
        try:
            evidence = operation()
            report["checks"].append({"name": name, "status": "passed", "evidence": evidence})
            print(f"PASSED {name}", flush=True)
            return evidence
        except Exception as error:
            status = "incomplete" if isinstance(error, Incomplete) else "failed"
            report["checks"].append({"name": name, "status": status, "detail": str(error)})
            print(f"{status.upper()} {name}: {error}", flush=True)
            return None
        finally:
            report["active_checks"].pop()
            checkpoint()

    def preflight():
        require(sys.version_info >= (3, 12), "Python 3.12 or newer is required")
        require(shutil.which("go"), "Go is required for the existing Ed25519 signature verifier")
        require(
            args.public_key and len(base64.b64decode(args.public_key, validate=True)) == 32,
            "trusted pinned public key required",
        )
        return {"signature_verifier": "scripts/sign-release verify", "anonymous_network": True}

    if check("preflight", preflight):
        release = check(
            "release",
            lambda: validate_release(
                json.loads(
                    network.data(
                        f"https://api.github.com/repos/{REPOSITORY}/releases/tags/v{args.version}"
                    )
                ),
                args.version,
            ),
        )
        if release:
            feed = check(
                "signed_feed",
                lambda: verify_feed(
                    network.data(
                        f"https://github.com/{REPOSITORY}/releases/download/v{args.version}/update.json"
                    ),
                    args.version,
                    args.public_key,
                ),
            )
            if feed:
                report["download_failures"] = []
                report["downloads_pending"] = [item["url"] for item in feed["artifacts"]]

                def receipt(url, result):
                    report["downloads_pending"].remove(url)
                    if isinstance(result, Exception):
                        report["download_failures"].append({"url": url, "detail": str(result)})
                        print(f"FAILED download {url.rsplit('/', 1)[-1]}", flush=True)
                    else:
                        report["downloads"][url] = result
                        print(f"VERIFIED {url.rsplit('/', 1)[-1]}", flush=True)
                    checkpoint()

                check("downloads", lambda: download_artifacts(network, feed["artifacts"], receipt))
    names = {item["name"] for item in report["checks"]}
    for name in ["release", "signed_feed", "downloads"]:
        if name not in names:
            report["checks"].append(
                {"name": name, "status": "blocked", "detail": "prerequisite did not pass"}
            )
    states = {item["status"] for item in report["checks"]}
    report["status"] = (
        "failed" if "failed" in states else "incomplete" if "incomplete" in states else "passed"
    )
    report["network_retries"] = network.retries
    checkpoint()
    if args.markdown:
        lines = [f"Release v{args.version}: {report['status']}", ""]
        lines += [
            f"- {c['name']}: **{c['status']}**" + (": " + c["detail"] if "detail" in c else "")
            for c in report["checks"]
        ]
        lines += ["", "Scope:", ""] + ["- " + item for item in report["limitations"]]
        (args.output / "report.md").write_text("\n".join(lines) + "\n")
    print(f"{report['status'].upper()}: {args.output / 'report.json'}")
    return {"passed": 0, "failed": 1, "incomplete": 2}[report["status"]]
