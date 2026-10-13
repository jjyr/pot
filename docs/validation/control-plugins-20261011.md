# Control-selected remote plugins — 2026-10-11

## Scope and implementation

This subphase implements named plugin execution through the existing authenticated
outward Control connection. Task Agent loops still run at Control; remote Agent
startup is P4. No UI or model-visible tool name/argument change was introduced.
The user explicitly approved remote execution of Control-pinned plugins with
unchanged tools, owner approvals, fixed worker targets and hidden internal envelopes.

The Control snapshot now includes Shell settings and model observation mode along
with global Skills and pinned plugins. The destination lease persists its accepted
resource digest outside the task workspace. A different snapshot cannot replace an
accepted run's configuration. Before prepare/execute, the cache must match that
digest; recomputing a digest for a different valid cache does not bypass the binding.
Core-only infrastructure RPCs without a snapshot use protocol defaults, never node
settings or cached plugins. Production remote Agent calls supply their snapshot.

Destination named tools map only to supplied enabled bundles. Internal plugin_call
is rejected at the RPC boundary. Host-only versus Sandbox support remains explicit;
unknown Host dependency requirements fail. Missing Node fails on process launch;
Chromium/Chrome errors remain explicit in the supplied browser plugin. Sandbox
execution retains the existing guest plugin protocol/dependency checks. Remote
session continuation is verified at the bound executor, with owner/run isolation.
Control approval storage is retained; node permission settings do not grant owner
approval. The Runtime no longer filters Control plugin definitions using destination
supported_operations.

## Validation

- Runtime unit suite: 165 passed.
- Server unit suite: 26 passed, 1 pre-existing ignored.
- Remote execution target integration: 4 passed, 1 opt-in Linux/Podman ignored.
- Runtime/Server all-target Clippy with warnings denied; formatting/diff checks.
- TLS outward two-node fixture runs a Control-modified plugin through an actual Node
  process, preserving mixed observation mode and nondefault Shell timeout. Duplicate
  call receipts do not increment plugin state. A second call continues its session;
  another task cannot use the handle.
- Disabled/unknown tools, direct envelopes, unsupported Host dependencies and
  Sandbox-only packages on Host fail. Revoked runs cannot execute new calls.
- A new configuration on an accepted lease fails. A different internally valid
  task cache fails the lease digest check; restoring the original snapshot works.
- Deterministic model integration verifies Control's cua_repl definition remains in
  remote task model input, with project AGENTS.md/Skills and owner approvals. Existing
  offline-before-sampling and original-receipt recovery regressions remain covered.
- Full model -> owner review -> remote Node plugin -> model result integration
  runs cua_repl with owner review. The original cross-turn assertion searched the entire request for “42” and did not establish session continuity; corrected assertions inspect parsed tool-call results: the same accepted run continues 41 to 42, while a later run explicitly rejects the previous session. Review
  includes the fixed computer-use plugin identity and original task owner.

Logs: /private/tmp/pot39-plugins-final-tests.log,
/private/tmp/pot39-plugins-final-integration.log,
/private/tmp/pot39-plugins-final-clippy.log.

## Limits

These are isolated processes on one macOS host and fixture model responses. They do
not establish actual cross-machine acceptance, real VM/Sandbox browser execution,
paid provider behavior, desktop streaming, or remotely running Task Agents. Those
remain tracked separately in #39.
