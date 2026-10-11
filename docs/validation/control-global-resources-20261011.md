# Control global resources — 2026-10-11

## Delivered

Before preparing a remote context, the owning Control snapshots its global user
and builtin skill bundles and the task's already pinned plugin configuration.
The task stores this snapshot in a private file and reuses it across follow-up
turns and restore. Contents, executable flags and reserved builtin names are bound
to a deterministic digest. Project rules/skills remain at the assigned workspace.

Control supplies the snapshot through the existing signed outward connection,
bound to the active execution lease. The destination stages a versioned cache,
verifies existing contents, and exposes the supplied skill paths in the execution
workspace. Discovery no longer adds destination-global user or builtin roots.
Missing, oversized, symlinked or corrupted resources fail explicitly. Transport
loss waits for resource delivery without a destination-local fallback.

Plugin definitions are transported and validated as pinned configuration here;
remote plugin invocation is a separate approved implementation step. No model
tool names or arguments changed in this resource-delivery step. Standalone/local
discovery remains independent. No resource management UI was added.

## Verification

- Runtime 165 tests passed. New tests verify fixed snapshots after Control edits,
  destination-global exclusion, project precedence, project AGENTS.md retention,
  reserved builtin resources, exact repeated cache installation, tampering,
  traversal and a symlinked cache destination.
- Server 25 unit tests passed, 1 existing ignored. The new real TLS-node test
  supplies a Control skill, reads project skills/rules, repeats the exact delivery
  and rejects installation after the execution lease is cancelled.
- Remote task integration: 3 passed, 1 opt-in Podman qualification ignored.
  Deterministic models verify project instructions, actual Host tool execution,
  offline startup and receipt reconciliation without duplicate effects.
- Final targeted TLS test and Runtime/Server all-target Clippy passed.

## Limits

The inline snapshot is bounded to 512 KiB and 2048 files inside the existing
1 MiB transport bound. Large-bundle chunking, actual multi-machine network/VM
qualification, remote plugin execution and remote Task Agent launch are not
established by these tests. TLS nodes ran on one host, and models were fixtures.
Read-only file permissions are not isolation from a cooperative task that owns
its workspace; changed cache contents are rejected on subsequent verification.
