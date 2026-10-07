# macOS prepared-instance implementation acceptance

Date: 2026-10-07. Architecture commit: `94c5905`. macOS changes are a subsequent working-tree stage.

Implemented: separate `pot-sandbox-macos` crate and explicit Server registration; signed public-VZ Swift helper; fixed private prepared-bundle enrollment through owner CLI; pinned-CA HTTPS plus per-instance bearer authentication; root guest broker/standard-account native runner bootstrap; Provider-derived workspace/instruction/skill/task paths; exclusive scopes; whole-VM stop on normal/cancel closure; durable Helper generation; orderly shutdown of idle owned VMs; capability-aware desktop UI/state polling. Android/iOS remain absent.

Evidence:

| Check | Result and scope |
|---|---|
| `mise run check` | Passed: workspace Rust compilation, formatting/script/i18n checks, native Swift build |
| `mise run test` | Passed: backend suites, including 131 Runtime tests (1 ignored), 7 Manager tests and 4 macOS Provider tests |
| Backend Sandbox/Provider/Runtime/Server Clippy, all targets with `-D warnings` | Passed |
| Provider contract tests | Private complete-bundle files and no overwrite/path escape; missing optional Helper; stale-socket uncertainty; IPC version/request mismatch; owned fake Helper stopped on closure/cancellation/shutdown and original bundle retained |
| Runtime cross-OS path regression | Mac task artifacts, AGENTS.md and skill locations use `/Volumes/PotWorkspace/workspace`, without starting a VM |
| `scripts/tests/macos-provider.py` | Passed: local real TLS server/enrollment CA and IP identity mismatch, auth rejection, duplicate no-replay receipts, exclusive owner and workspace escape rejection, expired/unknown operations, guest cleanup correctly returns unconfirmed; runner is fake |
| `scripts/tests/macos-helper.py` | Passed against the real compiled/signed native Helper: private IPC, bounded protocol, version rejection, exclusive controller lock, stopped status and missing-platform rejection; no VM boot |
| Second-stage real Linux VM regression | Passed against the latest Server and signed Helper: all 17 scenario checks, two Linux VMs, Pot/Codex, 47 local mock requests, cancellation/busy-stop, restart, skills, artifacts and optional desktop shutdown |

Local model fixtures and isolated test instance data are used; no paid model requests, user tokens, OS installation or Xcode account actions. Machine-specific preparation/resource notes belong under `~/Workspace/Machines`, not this report.

**Not verified:** real macOS boot/restart, graceful/forced stop, mounted VirtioFS UID/GID and readonly skill behavior, root/standard-user secret separation, native command/stdin/patch inside that guest, full Xcode sample build/test/artifact persistence, detached/launchd work and crash/escape recovery. No prepared macOS/Xcode instance was supplied for this run. The native IPC smoke and fake-provider/broker tests do not establish those guest properties. macOS execution is an implemented prepared-instance integration awaiting qualification, not production acceptance.

No IPSW installer, first-run automation, qualified template cloning/reset, DHCP discovery, egress firewall or public macOS desktop capability is delivered. TLS enrollment currently uses a dedicated CA and bearer token rather than mTLS. Persisted project state is shared by sequential owners; stopping a boot does not clean user-installed persistence on disk. Source recipes/metadata and capabilities are not evidence of a ready Xcode image. See [setup and current limits](../macos-sandbox.md).

## Subsequent persistent-VM policy revision

The owner requested persistent macOS execution without VM termination per turn. The current implementation replaces the whole-VM scope cleanup described above with `session-closure`: the broker confirms managed sessions ended but reports `settled=false`/no process-containment guarantee. Manager explicitly distinguishes this policy from Linux cgroup containment. VM state remains running after scope closure; explicit stop/delete and backend shutdown retain their lifecycle behavior. Guest protocol is now `pot-macos-executor-v2`; old v1 brokers must be reprovisioned. Follow-up tests cover this distinction, ownership release without VM stop, and failed termination retaining the runner for retry. Real macOS/Xcode VM qualification remains pending.

Follow-up validation: Manager/macOS Provider/Runtime suites and local TLS Guest protocol regression passed after the persistent-session revision. The previous real Linux VM run predates this policy revision; no new macOS VM execution is claimed.
