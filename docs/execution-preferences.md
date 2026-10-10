# Routing rules in assistant AGENTS.md

Each conversational assistant has its own plain-text rules file at
`$POT_AGENT_HOME/assistants/<runtime-id>/<assistant-id>/AGENTS.md` (default Agent home:
`~/.pot/agent`). Global instructions, assistant rules and project instructions
are injected in that order when Runtime prepares an execution context.
The built-in dispatch instructions and fresh eligible Profile catalog remain
injected independently. A custom system prompt still receives file instructions.

Explicit task choices take precedence over saved rules; unmatched tasks use the
configured default on the dispatching Agent Runtime. Rules cannot enable a
service, allow a Profile or grant execution authority. `create_task` retains
`selection_reason`; dedicated preference tools and `preference_revision` have
been removed with user agreement. Existing tasks retain configuration snapshots.

Click the current assistant name to select/create an assistant or edit its name
and AGENTS.md. Edits remain drafts until Save. Each assistant retains its draft
when switching; saving compares the definition revision and the file content
hash. A conflict preserves the draft and reports that the saved version changed.
Agent edits use existing Host file tools and require owner approval. Explicit
operations targeting the assistant rules file require fresh review even with an
existing matching grant. This is not an OS filesystem isolation boundary:
authorized arbitrary Host shell operations retain their existing capabilities.

The initial assistant retains the current conversation and Tasks. A versioned,
idempotent upgrade appends the current saved preference text to its AGENTS.md,
preserving an existing file. The old metadata remains an inert data backup;
no old preference tool or active preference-record path remains.

Configured Profile identity/model/capabilities determine eligible candidates.
An unavailable required target must produce clarification or a visible failure;
no extra classifier model call or silent executor substitution is introduced.

Task-routing token accounting remains a subset of the invoking assistant's
chat usage, never an additional allocation. The Runtime usage ledger is shared;
Threads, routing defaults, task ownership and file rules are independent.

Historical acceptance of the superseded dedicated preference tools remains in
[Execution preference acceptance](validation/execution-preferences.md).
Current validation: [Multiple assistants](validation/multiple-assistants-20261010.md).
