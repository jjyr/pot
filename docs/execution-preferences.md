# Routing rules in assistant AGENTS.md

Each conversational assistant owns AGENTS.md text in its database, with an
opaque revision that changes on every save. It is part of the assistant's state,
independent of execution environment. Main contexts receive their own rules;
workers receive task briefs and applicable project directory instructions, not
assistant or global Agent-home rules. See [Agent instructions](agent-instructions.md).
The built-in dispatch instructions and fresh eligible Profile catalog remain
injected independently. A custom system prompt still receives file instructions.

Explicit task choices take precedence over saved rules; unmatched tasks use the
configured default on the dispatching Agent Runtime. Rules cannot enable a
service, allow a Profile or grant execution authority. `create_task` retains
`selection_reason`; dedicated preference tools and `preference_revision` have
been removed with user agreement. Existing tasks retain configuration snapshots.

Click the current assistant name to select/create an assistant or edit its name
and AGENTS.md. Edits remain drafts until Save. Each assistant retains its draft
when switching; saving compares the definition revision and the rules revision. A conflict preserves the draft and reports that the saved version changed.
Agent edits use `read_agent_instructions` and `update_agent_instructions`.
Updates require fresh owner approval; the server captures the review baseline, and existing grants
cannot skip review. UI and model edits share the DB record and conflict checks.
Host file tools no longer edit the assistant's live rule record.

The initial assistant retains the current conversation and Tasks. Assistant
rules are read exclusively from the current DB record. Old assistant files and
saved preference metadata are not imported or converted. Existing DB rules are
preserved. Project AGENTS.md files continue to be discovered independently for
both assistants and workers.

Configured Profile identity/model/capabilities determine eligible candidates.
An unavailable required target must produce clarification or a visible failure;
no extra classifier model call or silent executor substitution is introduced.

Task-routing token accounting remains a subset of the invoking assistant's
chat usage, never an additional allocation. The Runtime usage ledger is shared;
Threads, routing defaults, task ownership and assistant rules are independent.

Historical acceptance of the superseded dedicated preference tools remains in
[Execution preference acceptance](validation/execution-preferences.md).
Current validation: [Multiple assistants](validation/multiple-assistants-20261010.md).
