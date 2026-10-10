# Assistant instructions in DB, 2026-10-10

The user approved moving an assistant's AGENTS.md into its owned database and
providing self-scoped read/update tools. This supersedes the file-edit mechanism
recorded in the earlier multiple-assistant acceptance report.

`read_agent_instructions({})` reads content and revision without approval.
`update_agent_instructions({text, expected_revision})` proposes a full replacement
and always requests owner approval. Both are Main-only and accept no owner or
Environment selector. The Agent dispatcher uses the same authorization path for
direct and composed calls; Runtime executes against the invoking assistant store.
The UI continues to use its existing rule editor and the same DB record.

Tests verify:

- Current assistant rule files import once, survive database reopen and remain
  inert after import. Existing saved routing rules still upgrade idempotently.
- Saves assign fresh revisions, so stale writes fail even after content reverts.
- Assistant records stay separate; a supplied foreign owner is rejected.
- Worker contexts contain applicable project directory policies, but no assistant
  or global Agent-home instructions and no assistant rule record.
- Read calls do not request approval. Write calls require review even with
  allow-by-default policy and an exact existing grant. Worker/Summary access fails.
- Real Runtime/Agent call chains with deterministic HTTP provider streams cover
  direct and JavaScript-composed reading, approval, denial and a concurrent UI save during approval. Denial leaves
  the DB unchanged; a stale approved proposal fails without overwriting the UI save.

Core 16, Agent 69, Runtime 156 and Server 26 tests pass (one existing Server test
is ignored). Workspace Rust and Swift checks pass. Tests use disposable stores
and mock providers, with no paid model calls. No new UI interaction was introduced;
the current browser/native editor was not requalified in this checkpoint.

Remote assistant transport and the new caller-created Runtime resource lifecycle
remain Issue #39 work. This checkpoint does not complete P3 or qualify remote rule
snapshot transport.

## Follow-up: simplified tool contract and independent project rules

The model update tool now accepts only `{text}`. Runtime captures the approval
baseline internally and rejects writes when the UI changes rules during review.
Legacy assistant-file import and preference conversion have been removed;
existing DB records are preserved. Added regression coverage verifies project
AGENTS.md injection for both assistants and workers, project refresh/removal
independently of DB edits, and rejection of legacy imports. Earlier observations
above describe the preceding implementation.
