# Host Agents and managed Sandboxes acceptance

Date: 2026-10-06. Replaces guest-Agent deployment acceptance for this increment. The [design](../agent-sandbox-design.md) and [architecture](../architecture.md) describe the implemented boundaries.

## Method

Built the Host App, Server and Swift Helper with the pinned repository toolchain. Bundled Node 24.21.0, Pi 0.87.1 and official Codex 0.159.1; npm lifecycle scripts were disabled. Ad hoc bundle signing and `codesign --verify --deep --strict` passed.

`scripts/tests/sandbox-runtime.py` starts the actual signed Helper and Host backend, with a fresh temporary Host home/product/native data directory, and two actual Linux ARM64 VMs. Both real Agent CLIs use a loopback mock model; no user credentials or paid model requests are used. The mock checks actual model-visible tool lists, including worker and read-only summary roles. Assertions inspect saved artifacts and authoritative HTTP responses. Cleanup stops the backend/VMs and removes the isolated data.

Guest image: `pot.local/sandbox:03814b02099f76c1ca8c`, compressed layer 459,499,302 bytes, rootfs tar 1,142,089,216 bytes. Each validation VM used 2 vCPUs, 2 GiB memory and an 8 GiB system disk.

## Results

| Contract | Result and evidence |
|---|---|
| Lazy environment startup | Host endpoint and a real Pi conversation work with default Sandbox stopped and no guest executor credential mounted. |
| Main Agent manages multiple environments | Pi creates the second definition through its management tool and executes independently in both VMs. Codex management/list and environment operations use Pot MCP. |
| Execution placement | Linux `uname`, write/read/bash and separate artifacts are observed in both guests; no corresponding file is written to the private Host Agent cwd. Native Host shell/patch/image/code/web/subagent and unmanaged MCP tools are absent from captured model requests. |
| Fixed workers | Real Pi and Codex workers execute only in the assigned environment. Pot worker loadouts have exactly seven environment operations. Worker management API calls and active-turn target/management bypass attempts are rejected. |
| Restart / native identity | Host backend restart preserves the original Codex thread identity. The original Pi worker resumes in its original Sandbox and workspace; persisted task assignment remains unchanged. |
| Read-only summaries | Completion summaries expose only task inspection Pot tools. Per-turn Codex MCP rediscovery restores main environment operations after a summary without replacing its native thread. |
| Cancellation / occupancy | Busy stop is rejected. Cancelling a shell also drains a detached `setsid` descendant; its delayed artifact never appears. The environment remains available after settled cancellation. |
| Environment independence | An active worker in B completes while A is stopped. Host chat and B tools remain usable; A's artifacts persist. |
| Optional desktop | An execution-only environment can start its desktop later. Actual viewer HTTP and static home page become ready; stop confirms browser cleanup. |
| Credential / ledger isolation | Guest tools cannot read a mounted executor token (none is mounted), cannot read the root-private executor ledger, receive no executor credential in their environment, and receive HTTP 404 for the credential path from the static server. |
| Failure / recovery unit contracts | Durable unknown operation quarantine survives reopening; existing worker leases cannot bypass it. Incomplete receipt retries fail without Host fallback. Failed inventory writes restore committed state. Invalid case-colliding IDs are rejected. |
| Build / checks | `mise run build`, `bundle`, `check`, `test`, `test:app` and `git diff --check` passed. Backend: 110 tests passed; App: 14 tests passed. Eight historical Pi fixtures remain ignored. |

The reproducible command is in [Development](../development.md). The final run completed 34 mock model requests, including four read-only summaries, with every assertion passing. Raw run output is written to the selected `--output` JSON and adjacent Helper log; transient build logs and test artifacts are outside Git.

## Fixes found by real validation

Codex can cache the summary-only MCP loadout across native resume. Giving each turn a distinct managed MCP endpoint configuration forces discovery again; native thread identity stays fixed.

macOS virtiofs maps Host ownership to the ordinary guest user. A mode-0600 credential in the artifact mount therefore did not establish isolation. Executor credentials now stay outside the Host mount, enter only the root guest process at startup, and are removed from child environments. Guest ledgers and command output use the root-private native `/run/pot-executor` directory. Host receipts remain the durable authority when a VM is recreated. The desktop static server runs unprivileged and serves `/opt/pot`, never the full data mount.

## Scope

This qualifies local Host Pi/Codex with multiple Linux VMs and the common execution protocol. It does not qualify macOS/Windows providers, Grok execution, direct Host Agent tools, real-provider inference/account flows after this refactor, or distribution signing/notarization. The native UI was compiled and its App tests passed; a full interactive appearance/IME matrix was not rerun.

Tasks sharing one VM share a guest account: task directories are organization, not mutual filesystem/process isolation. Detached tool processes are drained when a command finishes, so persistent background services are not part of this execution contract. Optional desktop lifetime follows explicit environment start/stop rather than a separate persistent desktop lease.

The old guest deployment has no migration/compatibility layer. Historical ignored tests and guest-Agent fixtures are not current routing acceptance. New environments use the Host data layout; old guest data is not deleted.

## Follow-up: retired design cleanup (2026-10-06)

The earlier counts above describe the pre-cleanup commit. Retired guest-Agent onboarding/state, Grok engine/ACP adapter/authorization, Pi/DeepSeek file import and direct smoke adapter, legacy session/usage importers, checksum fallback and internal model route are removed. Old guest validation scripts, `PotImageCheck`, configuration-import scripts and superseded POC/acceptance/design pages are deleted; current specifications and their links are updated. The eight ignored guest/Pi fixtures above are deleted rather than retained as compatibility tests.

Codex authorization now opens the official Host browser URL directly. Codex owns its loopback callback; Pot has no listener, callback forwarding endpoint or VM relay. Success requires a matching official completion event and official account verification. Unit coverage includes stale/mismatched completion, native authorization boundaries and cancellation; real account completion is not claimed.

`mise run check`, `test`, `test:app`, `build`, `bundle` and whitespace verification passed. Backend: 99 tests passed, zero ignored; App: 14 tests passed. New regression coverage checks three tasks sharing the selected default, explicit target selection, unchanged bindings after selection/restart, rejection of retired engines/transports, ignored retired configuration files and HTTP 404 for removed routes.

Rebuilt signed Helper/App and the same immutable guest image passed the actual two-VM Host Pi/Codex test again: 34 local mock requests, four summaries, lazy startup, worker scope, native resume/Host restart, active-turn isolation, cancellation, independent stop, artifacts and optional desktop. The test exited successfully after cleanup. No real-provider credentials or paid inference were used. Local artifacts: `/tmp/pot-cleanup-sandbox-acceptance.json` and `/tmp/pot-cleanup-e2e.log`. Full interactive UI and real-provider login/inference remain outside this follow-up's acceptance.
