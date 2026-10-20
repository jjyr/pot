# Product goals

Pot is a personal assistant with persistent conversations and background Tasks.
Multiple chat assistants own their conversations, rules, Tasks and schedules.
Task Agents execute a concrete brief on one fixed Runtime; they have no
independent assistant persona or assistant AGENTS.md.

The App starts/exits with its own autonomous node. Agent Link connects other Pot,
Codex and compatible agents for conversation and task delegation. Each node owns
its model configuration, workspaces, credentials, execution and approvals. [Architecture](architecture.md) defines ownership;
[Design language and interactions](design-language.md) defines the Chat/Tasks/Machines flows.

Simple searches, focused research and design discussions are completed directly
in chat, including tool-assisted current-data lookups and diagrams. Work requiring
independent execution, such as feature implementation or extensive investigation,
is delegated with a model, execution target and goal. Guidance is marked received,
then applied; completion reports describe the result and include file links only
when needed.
Files, history and task bindings survive restart. Unknown external effects are
reconciled rather than repeated. Model changes preserve context at safe boundaries.

Schedules create independent Tasks with time zones, occurrence deduplication,
missed-run policy and overlap prevention. Offline machines do not execute; powering
on a sleeping machine is outside the current contract. Secrets and service
credentials remain outside public projections and ordinary model context.

Acceptance must exercise reports with follow-up/restart, cancellation without
replay, schedules across missed runs, and supported desktop takeover. Fixture tests
prove contracts only; actual machine/model/platform qualification is recorded
separately. Remote desktop transport and further platform work remain deferred; see
[the development plan](development-plan.md).
