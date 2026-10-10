> Historical: the peer pairing and machine-grant design in this report was replaced
> by the single-Control architecture. These observations do not validate its transport.

# Peer authority foundation, 2026-10-11 (partial P3)

## Delivered scope

The Runtime server has a separate, explicitly configured HTTPS peer listener.
It exposes only identity, pairing and signed peer RPC. Native/UI routes, model
services and root credentials are not forwarded through this listener. Starting
`pot serve` does not start it or create business resources. A persisted listener
intent restores the configured address and TLS files at restart.

An Ed25519 identity persists independently of address and boot. Pairing requires
a short-lived random code, a signed caller identity, and the confirmed target
Runtime ID/fingerprint. The receiver persists redemption before returning the
peer credential. Exact retries recover the same credential; conflicting
requests fail. Consumed codes cannot enroll another request. Credentials and
keys are private files and absent from normal metadata/inventory.

Pairing creates no grants. Discover and Execute grants are separate, target
specific, expiring and revocable. This slice rejects management, artifact and
desktop grants instead of advertising unimplemented authority. Grants, leases
and enrollment use copy-on-write persistence: a failed write never publishes a
new in-memory permission. Browser/native credentials cannot authenticate peer
RPC; signatures bind recipient, request content and freshness.

Execution fixes peer, owner, run, generation and target. Destination paths are
normalized at the destination. A receipt commits before execution. Exact retries
return its state/result without replay; content conflicts fail. A pending receipt
without its active operation, including after reboot or failed result persistence,
is uncertain. Inspect remains available after revocation. Execution and renewal
fail under revoked/expired/boot-fenced leases. A monitor drains yielded sessions
on expiry/revocation without waiting for the next RPC; cleanup is scoped to the
original execution owner. Shutdown stops effects and joins peer operations.

Machines discovery includes outgoing peers and granted remote inventory. Failed
refresh retains last-confirmed inventory and reports offline; successful refresh
reflects revoked discovery grants. Disconnected peers cannot perform new calls.

Review also found and corrected an unconditional Sandbox lease acquisition for
Host task workers. Host workers now validate their typed binding without using
a Sandbox Provider; test context workspaces match the production worker path.

## Validation

Commands use the repository's pinned mise toolchain.

Seven dedicated peer tests exercise actual local HTTPS sockets, valid/invalid
certificate trust, stable signed identities, conflicting/duplicate pairing,
zero implicit authority, Host versus Sandbox grant scoping, lease expiry and
revocation, stale boots, failed persistence, command deduplication, scoped
background process cleanup, restart quarantine, offline cache and listener
restoration. The execution node has a caller-created Host and no assistant/model
service; commands run through the real Host executor. This is deterministic
local two-node acceptance, not a deployed remote-machine or VM qualification.

Final regression: Runtime 159 passed; Server 38 passed and one pre-existing
ignored test. The seven peer tests are included in the Server count and were
rerun after the final shutdown guard. `mise run check` passed, including the
Swift Helper build. Server/Runtime Clippy passed with `-D warnings`, and
`git diff --check` passed. Logs: `/private/tmp/pot-peer-regression.log`,
`/private/tmp/pot-peer-server-final.log`, `/private/tmp/pot-peer-final-tests.log`,
`/private/tmp/pot-peer-final-check.log` and `/private/tmp/pot-peer-clippy.log`.
Swift printed existing read-only manifest-cache warnings but completed its build.

## Remaining P3 work

Local Agent/task adapters do not yet use this transport. `create_task` continues
to reject remote assignments rather than starting partial remote work. Remote
project AGENTS.md discovery and destination-installed plugins need their own
scoped adapter. Remote Sandbox management, artifacts, desktops, connection/grant
UI and lazy Helper lifecycle remain incomplete. Real remote Host/Sandbox/model
qualification remains required. P3 is not complete and has no phase commit.
