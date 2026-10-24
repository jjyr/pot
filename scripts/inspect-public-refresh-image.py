import hashlib, json, subprocess, sys
from pathlib import Path

root = Path(sys.argv[1] if len(sys.argv) > 1 else ".pot-artifacts/release-before")
root.mkdir(parents=True, exist_ok=True)


def curl(url, token=None):
    config = "url = " + json.dumps(url) + "\nconnect-timeout = 10\nmax-time = 40\nipv4\n"
    if token:
        config += (
            "header = "
            + json.dumps("Authorization: Bearer " + token)
            + '\nheader = "Accept: application/vnd.oci.image.index.v1+json, application/vnd.docker.distribution.manifest.list.v2+json, application/vnd.oci.image.manifest.v1+json"\n'
        )
    return subprocess.check_output(
        ["curl", "--location", "--silent", "--show-error", "--fail", "--config", "-"],
        input=config.encode(),
    )


token = json.loads(curl("https://ghcr.io/token?scope=repository:jjyr/pot:pull&service=ghcr.io"))[
    "token"
]


def fetch(path):
    return curl("https://ghcr.io/v2/jjyr/pot/" + path, token)


raw = fetch("manifests/0.0.1")
digest = "sha256:" + hashlib.sha256(raw).hexdigest()
(root / "image-index.json").write_bytes(raw)
(root / "image-digest.txt").write_text(digest + "\n")
print("Current image index:", digest, flush=True)
index = json.loads(raw)
results = []
for descriptor in index["manifests"]:
    platform = descriptor.get("platform", {})
    if platform.get("os") != "linux":
        continue
    manifest = json.loads(fetch("manifests/" + descriptor["digest"]))
    config = json.loads(fetch("blobs/" + manifest["config"]["digest"]))
    result = {
        "platform": platform,
        "digest": descriptor["digest"],
        "labels": config["config"]["Labels"],
    }
    results.append(result)
    print(json.dumps(result), flush=True)
(root / "image-platforms.json").write_text(json.dumps(results, indent=2) + "\n")
assert {r["platform"]["architecture"] for r in results} == {"amd64", "arm64"}
