"""Read-only final v0.0.1 public-byte verification; no publishing or signing."""

import hashlib
import json
import os
from pathlib import Path
import subprocess
import sys

root = Path.cwd()
sys.path.insert(0, str(root / "scripts"))
from release_contract import release_assets, update_artifacts
from pot_packaging import storage_format
import runpy

source, directory = sys.argv[1:]
out = Path(directory)
out.mkdir(parents=True, exist_ok=True)
release = json.loads(subprocess.check_output(["gh", "api", "repos/jjyr/pot/releases/422513414"]))
assert release["target_commitish"] == source and not release["draft"]
actual_source = subprocess.check_output(
    ["gh", "api", "repos/jjyr/pot/commits/refs/tags/v0.0.1", "--jq", ".sha"], text=True
).strip()
assert actual_source == source
names = sorted(
    {
        name
        for channel in ("server", "desktop", "macos")
        for name in release_assets("0.0.1", channel)
    }
)
assets = {asset["name"]: asset for asset in release["assets"]}
verified = []
for name in names:
    asset = assets[name]
    destination = out / name
    subprocess.run(
        [
            "curl",
            "--disable",
            "--fail",
            "--silent",
            "--show-error",
            "--location",
            "--connect-timeout",
            "15",
            "--max-time",
            "240",
            "--retry",
            "2",
            "--output",
            str(destination),
            "https://github.com/jjyr/pot/releases/download/v0.0.1/" + name,
        ],
        check=True,
    )
    with destination.open("rb") as content:
        sha = hashlib.file_digest(content, "sha256").hexdigest()
    assert asset["digest"] == "sha256:" + sha and destination.stat().st_size == asset["size"], name
    verified.append({key: asset[key] for key in ("id", "name", "size", "digest")})
    print("Verified anonymous bytes:", name, flush=True)
for manifest in out.glob("SHA256SUMS-*"):
    subprocess.run(["sha256sum", "--check", manifest.name], cwd=out, check=True)
for sidecar in out.glob("*.sha256"):
    subprocess.run(["sha256sum", "--check", sidecar.name], cwd=out, check=True)
env = dict(
    os.environ,
    POT_UPDATE_PUBLIC_KEY=(root / ".pot-artifacts/release-before/public-key.txt")
    .read_text()
    .strip(),
)
payload = subprocess.check_output(
    ["mise", "exec", "--", "go", "run", "./scripts/sign-release", "verify"],
    input=(out / "update.json").read_bytes(),
    cwd=root,
    env=env,
)
(out / "feed-verified.json").write_bytes(payload)
feed = json.loads(payload)
assert feed["version"] == "0.0.1" and feed["storage_format"] == storage_format()
expected = {(kind, platform): name for kind, platform, name in update_artifacts("0.0.1")}
assert len(feed["artifacts"]) == len(expected)
for artifact in feed["artifacts"]:
    name = expected.pop((artifact["kind"], artifact["platform"]))
    assert artifact["url"] == "https://github.com/jjyr/pot/releases/download/v0.0.1/" + name
    assert artifact["sha256"] == assets[name]["digest"].removeprefix("sha256:")
    assert artifact["size"] == assets[name]["size"]
runpy.run_path(str(root / "scripts/refresh-release"))["verify_candidate_builds"](out, source)
(root / ".pot-artifacts/public-assets-verified.json").write_text(
    json.dumps({"source": source, "assets": verified, "feed": feed}, indent=2) + "\n"
)
print("All 15 public assets, manifests, sidecars and Ed25519 feed verified")

from release_contract import generate_cask

generate_cask("0.0.1", out)
