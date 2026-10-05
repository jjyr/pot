---
{ tags: [ POC, Containerization, Virtualization ] }
---
# Pot research POC

> Old `poc/` sources are removed; commands apply only to the linked historical commit.

> Historical record: versions, allowances, UI reflect section dates. Current scope/contracts: [Development plan](../development-plan.md), [Documentation index](../README.md).

[poc/baseline](https://github.com/jjyr/pot/tree/5ed4f40277bceb2f1a62f90c2fdf789f54f3bdb2/poc/baseline) holds inspectable implementation/evidence for [architecture research and POC validation](baseline-report.md). Basic POC, not complete product. Tested 2026-10-03.

> Imported from notes preserving experiment conditions. Commands run in repository `poc/baseline`; some scripts depend on historical `/tmp/pot-research-20261003` caches. New POC first step removes fixed paths.

## Contents and scope

| File | Validation |
|---|---|
| runtime.py / test_runtime.py | Real SQLite/crashes/outbox/epochs/schedules/auth/preferences/correction-forgetting/cooperative takeover; 22 tests |
| test_adversarial.py | Early handback/unknown action-rule rejection/recovery invalidation; 4 tests, pre-fix failures retained |
| pi_turn_fixture.py | Real Pi tools/session persistence-restart/active abort-steer; local fixed-response model fixture |
| local_oauth_poc.py | Local HTTP OAuth state/PKCE/one-use/expiry/revoke; 3 tests, not GitHub |
| probe_engines.py | Actual Codex/Pi/Claude processes/OpenCode HTTP probes without model turns |
| probe_agy.py | Google Antigravity SDK lifecycle, invalid fixture key, no turns |
| native-gpui / native-swift | GPUI 0.2.2/Swift C ABI/NSStatusItem/same-window WKWebView-noVNC |
| rfb_fixture.py | Local synthetic framebuffer for display/key transport |
| rfb_vm_proxy.py | Loopback WebSocket -> temporary VM VNC, no product auth |
| guest/desktop-poc.sh | Xvfb/Chromium/x11vnc/Chinese fonts, exits after five minutes |
| containerization.patch | Apple library 0.47.0 cctl temporary state/init pin/network switch |
| virtualization.entitlements | POC ad hoc virtualization entitlement, not distribution |
| evidence/ | Tests/drivers/VM/screenshots/source manifest, successes and failures |

## Minimal tests: no downloads or model calls

Run Python 3.9+ here:

```sh
PYTHONDONTWRITEBYTECODE=1 python3 test_runtime.py
PYTHONDONTWRITEBYTECODE=1 python3 local_oauth_poc.py
PYTHONDONTWRITEBYTECODE=1 python3 test_adversarial.py
```

Expected 22+3+4 tests OK. Temporary DB/loopback fixtures leave user accounts untouched. Runner updates evidence/runtime-tests.json; not semantic-memory/hard-isolation acceptance.

## Native / VM prerequisites

Temporary root `/tmp/pot-research-20261003`. Caches not copied into notes; after cleanup recreate pinned resources. Sources contain fixed paths; relocation must update Python ROOT/Swift output-URL/build.rs/Apple patch together, no arbitrary-directory guarantee.

- Apple Containerization 0.47.0 commit `bc994b88df46207fad7775b0eabc51947e315881`, clone `$POT_POC_ROOT/containerization`, apply patch.
- Kata 3.32.0 `kata-static-3.32.0-arm64.tar.zst`, official release/hash from report, extract `$POT_POC_ROOT/kernel`.
- init `ghcr.io/apple/containerization/vminit:0.47.0`, Alpine `docker.io/library/alpine:3.22`; index digests in evidence/vm-results.json, use digest pins for strict reproduction.
- noVNC v1.6.0 clone `$POT_POC_ROOT/noVNC`.
- Swift/Xcode macOS SDK/Rust/Cargo; locked GPUI with runtime_shaders.
- AppKit/Virtualization require system-authorized context; this restricted run needed approval, not disabling SIP.

```sh
export POT_POC_ROOT=/tmp/pot-research-20261003
mkdir -p "$POT_POC_ROOT/vm-data" "$POT_POC_ROOT/swift-cache" "$POT_POC_ROOT/swift-config" "$POT_POC_ROOT/swift-security"
# First git apply <absolute-directory>/containerization.patch in the containerization checkout
GIT_CONFIG_COUNT=1 GIT_CONFIG_KEY_0=http.version GIT_CONFIG_VALUE_0=HTTP/1.1 \
SWIFTPM_MODULECACHE_OVERRIDE="$POT_POC_ROOT/swift-modules" \
CLANG_MODULE_CACHE_PATH="$POT_POC_ROOT/clang-modules" \
swift build --package-path "$POT_POC_ROOT/containerization" --disable-sandbox \
 --scratch-path "$POT_POC_ROOT/swift-build" --cache-path "$POT_POC_ROOT/swift-cache" \
 --config-path "$POT_POC_ROOT/swift-config" --security-path "$POT_POC_ROOT/swift-security" --product cctl -j 4
codesign --force --sign - --entitlements virtualization.entitlements "$POT_POC_ROOT/swift-build/debug/cctl"
```

Actual cctl path `swift-build/debug/cctl`; do not guess arm64 subdirectories for this Xcode. Passthrough **must not include `--`**, or guest tries executing `--`.

```sh
"$POT_POC_ROOT/swift-build/debug/cctl" run \
 --kernel "$POT_POC_ROOT/kernel/opt/kata/share/kata-containers/vmlinux-6.18.35-197-debug" \
 --image docker.io/library/alpine:3.22 --id pot-poc-write --memory 1024 \
 --mount "$POT_POC_ROOT/vm-data:/data" /bin/sh -c 'printf "persisted-by-pot-poc\n" > /data/proof.txt'
"$POT_POC_ROOT/swift-build/debug/cctl" run \
 --kernel "$POT_POC_ROOT/kernel/opt/kata/share/kata-containers/vmlinux-6.18.35-197-debug" \
 --image docker.io/library/alpine:3.22 --id pot-poc-read --memory 1024 \
 --mount "$POT_POC_ROOT/vm-data:/data" /bin/sh -c 'test "$(cat /data/proof.txt)" = persisted-by-pot-poc && echo PERSISTENCE_PASS'
```

Networking additionally uses `POT_POC_NETWORK=1` / `--ns 1.1.1.1`; guest wget Alpine HTTPS APKINDEX. Default offline.

Real desktop: copy `guest/desktop-poc.sh` to `$POT_POC_ROOT/vm-data/desktop-poc.sh`; network-enabled cctl run, 3072 MiB, shared mount above, `/bin/sh /data/desktop-poc.sh`. Read eth0 from vm-data/desktop/network.txt, start:

```sh
POT_VNC_HOST=<guest-ip> PYTHONDONTWRITEBYTECODE=1 python3 rfb_vm_proxy.py
```

Build/start UI in another terminal:

```sh
swiftc -swift-version 5 -module-cache-path "$POT_POC_ROOT/swift-modules" \
 -emit-library native-swift/Bridge.swift -o "$POT_POC_ROOT/libpot_native.dylib"
CARGO_HOME="$POT_POC_ROOT/cargo-home" CARGO_TARGET_DIR="$POT_POC_ROOT/cargo-target" RUSTC_WRAPPER= \
 cargo build --locked --manifest-path native-gpui/Cargo.toml -j 4
"$POT_POC_ROOT/cargo-target/debug/pot-native-poc"
```

Native UI checks/screenshots/exits within 25 seconds, writes temp root. Fixture/VM proxy share loopback17410; stop fixture first. Built-in keys plus VM Ctrl+L validate address bar. VM exits after five minutes; Ctrl+C proxy. --no-sandbox/no-password VNC is not product security/auth.

## Driver handshake reproduction

Scripts require isolated directories/pinned binaries; missing dependencies fail and do not establish capabilities.

- Pi 0.87.1 PATH; script sets `PI_CODING_AGENT_DIR`, disables extensions/skills/unnecessary traffic.
- Claude SDK0.3.288 matching darwin-arm64 native at `claude-sdk/node_modules/@anthropic-ai/claude-agent-sdk-darwin-arm64/claude`; isolated config, no API/OAuth keys.
- OpenCode1.18.34 `opencode-bin/opencode`, temporary XDG_CONFIG_HOME/XDG_DATA_HOME/XDG_CACHE_HOME/OPENCODE_CONFIG_DIR, `serve --hostname 127.0.0.1 --port 17409`; no user config.
- Codex0.159.3 PATH; existing state backfill blocked this run, codex-backfill.txt. Never delete user DB/locks for tests. `POT_SKIP_CODEX=1` preserves failure/runs others.
- AGY task-local venv `google-antigravity==0.1.20`, deny-all policies/invalid key/no chat. If proxy misroutes loopback WebSocket to SOCKS, unset/set NO_PROXY for this child only, not globally.

```sh
POT_SKIP_CODEX=1 PYTHONDONTWRITEBYTECODE=1 python3 probe_engines.py
env -u HTTP_PROXY -u HTTPS_PROXY -u ALL_PROXY -u http_proxy -u https_proxy -u all_proxy \
 NO_PROXY='*' PYTHONDONTWRITEBYTECODE=1 "$POT_POC_ROOT/agy-venv/bin/python" probe_agy.py
```

These are Mac protocol/SDK tests. Linux real turns/active cancel/resume/permission callbacks still require dedicated credentials/full adapters.

## Evidence and licenses

Separate native-viewer.png/native-vm-viewer.png for fixture/VM. sources-manifest.json records URLs/snapshot hashes; source in temp root and links in report. VM records OCI index digests; 818 MiB install is not final image size.

gpui0.2.2 Apache-2.0, Apple library Apache-2.0, noVNC MPL-2.0; check other drivers/dependencies by delivered files. Own POC/small upstream patch retain Apple license, no full sources/binaries/real credentials/caches. Product separately needs NOTICE/SBOM/provider distribution auth/signing.

## Pi tool loop / recovery revalidation

Install Pi 0.87.1 and run `PYTHONDONTWRITEBYTECODE=1 python3 pi_turn_fixture.py`. No fixed download-root dependency; creates/cleans temporary config/workspace/session/SSE. Real file tools/restart/cancel-steer, fixed model without reasoning. pi-turn-fixture.json is not real-provider end-to-end acceptance.

## Codex Linux VM basic protocol validation

`guest/codex_app_server_probe.py`, `evidence/codex-linux-app-server.json` (5/5). Download official ARM64 musl asset from `evidence/codex-linux-release.json`, verify SHA256, extract `vm-data/codex-poc/`, copy probe.py.

Use cctl network/mount/kernel above, 2048 MiB/fresh VM ID, `/bin/sh -c 'set -e; apk add python3; PYTHONDONTWRITEBYTECODE=1 python3 /data/codex-poc/probe.py'`. PTY required; fresh guest home, no host ~/.codex mount/HOME-CODEX_HOME override/credentials. Result shared app-server-results.json.

Passes initialize/reinitialize error/ephemeral thread start-read/no-active-turn interrupt error. No model turns, not active cancellation/persistent resume/tool isolation. PATH bubblewrap warning retained; full product tools need pinned dependencies.
