# Pi + DeepSeek MVP validation

> Historical acceptance record: versions, allowances, and UI reflect each section's date. See [Development plan](../development-plan.md) for current capabilities and [Documentation index](../README.md) for operating contracts.

Date: 2026-10-03. Status: **single-session MVP complete, uncommitted**. User chose Pi + DeepSeek as the first slice, deferring AGY/Codex. This covers D1, not D2-D6 or public release completion.

## Operational flow

1. `scripts/pot prepare` prepares pinned resources; `scripts/configure-pi` imports only local DeepSeek credentials without requests.
2. `scripts/pot build && scripts/pot bundle && scripts/pot open` builds/opens development App.
3. Enter a task in native chat; Enter sends. Pi read/write/edit/bash/grep/find/ls run as a normal Linux VM user.
4. View streaming/tool start-completion; Open workspace button/native menu opens artifacts.
5. Stop task or Cmd+. cancels. Runtime waits for Pi abort acknowledgment/process exit; unconfirmed stop requires environment reopen and blocks more tasks.
6. Window close retains execution; Quit stops VM. Reopen preserves product history/files/the same native Pi session.

Pi 0.87.1, Node 22.23.2, guest APK/npm integrity locks pinned; first startup installs online, later uses caches. Runtime remains Rust + SQLite with JSONL RPC Pi. Native Pi context is separate from historical smoke adapter context; old UI records are not uploaded to Pi models.

## Observed results

| Check | Result |
|---|---|
| Native chat -> VM -> Pi -> DeepSeek | Pass, real model response, SSE UI updates |
| Real tools | Pi bash creates `mvp-check.md`, reads output, replies `DONE`; Finder can access file |
| Quit / reopen | Helper stops normally, Chromium profile `Normal`; history/files/allowances retained |
| Native session / semantic continuation | Same Pi session ID; recalls previous codeword `apricot` without revealing it in new prompt |
| Real cancellation | Pi runs `sleep 30` then file write; Cmd+. yields `Command aborted`, user message Cancelled, no target file after 30 seconds or growing model request count |
| Cancellation display | Pi wrapped abort as error; mapped to Cancelled using current state; real Pi local integration confirms no Failed on cancelled turns |
| Workspace entry | Native menu opens Finder with artifact visible |
| Regular automation | App 4 + protocol 1 + Runtime 8 = 13 pass |
| Real Pi / mock model | 1 additional pass: write/bash, restart continuation, bash child exit on cancel, subsequent send, request cap, refusal to silently rebuild missing sessions |
| Build | macOS App, ARM64 musl Runtime, Swift Helper; App ad hoc deep/strict validation passes |

Real validation adds **4** DeepSeek requests, 8719 input tokens including 6016 cache hits, 122 output. Tool task 2, reopen continuation 1, cancellation 1. No automatic retries or monetary estimate. Prior smoke adapter's 3 requests retained without reset.

Ignored evidence: `.local/d1/pi-mvp-verification.json`, `pi-tests.log`, `pi-integration.log`, `pi-build.log`. Machines maintains deployment/current running state.

## Automation entry points

```sh
cargo test --locked -p pot-app -p pot-runtime -p pot-protocol
POT_TEST_PI_BIN="$(command -v pi)" cargo test --locked -p pot-runtime real_pi_tools_resume_cancel_and_budget -- --ignored --nocapture
```

Second test requires local Pi 0.87.1, isolated temporary directories/fake credentials/local mock provider, without reading local Pi auth or real model calls. Normal tests skip it so not all development machines need Pi.

## Current boundaries

- One persistent session; multiple sessions/task cards/attachments/structured previews remain later work.
- Economical configuration: 512 output tokens/request, 6 requests/turn, 100 lifetime; context body 96 KB, auto retry/compaction disabled. Limits fail explicitly without silent history loss. Adjust via `configure-pi` and reopen without ledger reset.
- Pi tools can access normal guest user files/network. Separate D4 Agent/controller UID and CDP/X11 isolation unimplemented; not suitable for claimed untrusted-web takeover.
- Setup wizard/download resume/cross-machine cold install/release signing undelivered; development uses ad hoc signing.
- Recovery preserves partial product output and marks interrupted messages failed without prompt replay. MVP UI cannot repair corrupt/missing Pi session files.


## Sandbox browser lifecycle regression (2026-10-03)

Fix `desktop.sh` using Chromium exit as VM exit condition: browser close waits for Runtime, retaining desktop/noVNC. Shared `open-browser.sh` records browser PID; Fluxbox context menu offers Chromium/Terminal. Quit still closes current browser through CDP; already-closed browsers confirm stop directly.

Real VM manual regression (no model calls):

1. Take over and close Chromium: empty desktop/Pi Ready remain; Helper running, Runtime snapshot HTTP 200.
2. Desktop context menu -> Chromium reopens with original profile/session.
3. Quit after reopen: browser-stopped received, profile.exit_type Normal.
4. Quit with browser closed: same stop confirmation/Normal profile; next startup connects normally.

`sh -n`, product build, bundle complete; 0 model requests this round.
