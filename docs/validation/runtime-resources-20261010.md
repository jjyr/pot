# Caller-created Runtime resources — partial P3, 2026-10-10

This checkpoint covers resource definitions and assistant rules, not federation.
The CLI starts with `pot serve`; API callers create Host, assistant or Sandbox
resources. App uses the same API for its default assistant. New resource UI is
deferred by explicit user choice.

Automated coverage uses actual Server HTTP routing and isolated data roots:
empty startup; unauthenticated rejection; invalid assistant creation without
opening product storage; assistant creation without Host or default Sandbox;
conflicting retry rejection; identical retry result reuse; dynamic routes;
stable assistant identity and definition restore across restart; no model
input or active turn created by resource assembly.

Sandbox tests use a fake Provider and verify empty inventory, explicit stopped
definition creation, reopen without synthetic default, and no Provider starts
or tool dispatches. Assistant-rule tests verify Host and Sandbox project files,
DB/file independence and refresh, worker project rules without assistant rules,
no legacy file/preference import, direct/composed fresh approval, denial and
concurrent UI-edit protection without a model-supplied revision.

Outstanding: peer pairing/grants/transport/leases/reconciliation, unified
Environment/Runtime UI, lazy macOS Helper lifecycle and real Provider/model/App
acceptance. P3 is not complete and has no completed-phase commit yet.

Latest automated coverage: Core 16, Agent 69, Runtime 158, Sandbox 17, and
Server 30 passed (290 total), with one pre-existing ignored Server test. App
tests: 18 passed, plus Swift safety checks. Workspace check and backend Clippy
passed. Tests also verify current resource adoption, later Sandbox rename/delete
preservation, and observers following assistant creation after startup. These
results cover the local checkpoint and do not establish remote readiness.
