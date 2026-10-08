# Agent permissions and coordination validation

Date: 2026-10-08. Scope: pot-agent permissions, shared execution, composition and
Task lifecycle for [issue #29](https://github.com/jjyr/pot/issues/29). Specifications:
[permissions](../permissions.md), [tools](../tool-composition.md),
[Tasks](../task-coordination.md). Codex permission/composition integration is deferred.

## Final automated results

Tests used temporary `POT_AGENT_HOME` directories and local model/guest fixtures.
They establish code/protocol behavior, not real-model or VM acceptance.

| Check | Result |
|---|---|
| Full backend suite | Passed; Runtime 160 passed, one paid-model test ignored |
| Workspace compilation and Clippy with warnings denied | Passed, including Native App |
| Native App/UI, Swift build, deterministic eval | Passed in pre-push checks |
| Guest permission / skill mount / executor lifecycle | 7 / 11 / 5 tests passed |
| Source lint, Web generation and localization | Passed; final localization checks covered 760 keys |
| Release/skill-authoring fixtures and Rust license checks | Passed |
| Diff whitespace checks | Passed |

Coverage includes owner denial before effects, one-time consumption, receipts,
Agent-specific grants across restart/rebinding, revocation, editable JSON and
migration, Host/guest isolation, guest path preparation, configuration protection,
worker restrictions and reviewed mappings. Composition fixtures cover private
intermediate results, incremental waits, approvals, role filters, limits and
interruption. Task fixtures cover queue preservation/discard, explicit resume,
no replay, cleanup capacity, durable messages, cursor waits and multiple clients.

## Qualification limits

- Native/Web interactive approvals and live model collaboration were not qualified.
- Live Linux mapping/mount protection and prepared macOS policy inheritance remain
  unqualified. Guest images/runners require rebuilding; macOS guests require
  reprovisioning. Unsupported guests fail without Host fallback.
- Prepared macOS Host mappings are unsupported. The existing TLS/RFB fixture failed
  twice during initial validation (connection reset/readiness timeout); this report
  does not establish prepared macOS desktop qualification.
- Composition token savings were not measured. Expanded schemas can exceed small
  context windows or large output reservations; the shutdown-only fixture reserves
  1,024 output tokens. The production context guard remains enforced.
- Archive remains phase-derived; no inactivity archive policy was implemented.

## Earlier checks on the same date

These observations describe intermediate revisions, not the final contract.

| Revision | Recorded result |
|---|---|
| Initial permissions with file tools and SQLite | Backend/build checks passed; localization 765 keys, skill mounts 10 tests. Interactive UI and live mappings unqualified. |
| Current-grants JSON refinement | Backend, Clippy, compilation and localization passed. Grants became Agent-specific across rebinding; pending approvals/history were not persisted. Lazy creation and isolated test homes resolved a Server TLS startup timeout. |
| Unified environments and policy defaults | Backend passed; guest permissions grew from 6 to 7 tests, skill mounts to 11, executor lifecycle stayed 5. Live guest protection remained unqualified. |
| Composition and naming | File tools removed; unprefixed product tools and `exec`/`wait` passed regression and license checks. |
| Main coordination before cancellation restoration | Runtime 158 passed, one ignored. Shutdown fixture context reservation and Web `/interrupt` allowlist issues were corrected. |
| Cancellation restored alongside interruption | Runtime 160 passed, one ignored; full backend checks passed. Empty interrupted queues, abandonment/restart and explicit reopening were covered. |
