# Execution preferences

Execution preferences are one dedicated, revisioned plain-text record in the main product database (`agent.execution_preferences.v1`). They are separate from general memory and the routing authorization policy. No structured categories, stored Profile-ID mappings, classifier call, or preference settings editor are required.

## User interaction

Chat is the management entry point. An explicit lasting instruction ("From now on, use Claude for coding") saves a preference without a second confirmation. A choice for this task, a correction to this task, and casual discussion do not modify persistent text. Ambiguous scope requires one clarification. Acknowledgements follow a successful write and explain that changes apply to new tasks.

When asked to show all preferences, the main Agent reads and displays the complete saved text rather than reconstructing it from conversation history. Selective changes replace the whole document while preserving unrelated text; clearing saves an empty document. There are no extra task-summary labels, rule-management buttons, or confirmation screens. Running-task summaries in Chat and task-list rows show only an executor icon in addition to their existing content. Open the task to inspect its specific Agent/model or use its existing Change Agent interaction.

Configured connections determine Agent/model selections. Claude, Gemini, DeepSeek, OpenCode and Custom API use Pot Agent; Codex uses official App Server. Antigravity and Claude native CLI are unsupported. Display names/model names are resolved against configured Profiles; ambiguous or unavailable names need clarification or connection recovery. A configured connection is not proof of successful inference or authentication.

## Tools and routing

The shared Pot/MCP tool schema exposes:

- `get_execution_preferences`: current `{preferences: {text, revision, updated_at_ms}, profiles}`. Catalog entries show configured identity/name/model, capability declarations, eligibility and a safe unavailability explanation, without credentials or launch commands.
- `update_execution_preferences`: `{text, expected_revision}`. Empty text clears. The main active turn is required; worker and summary roles cannot mutate preferences. Writes atomically commit a revision and an idempotent result receipt. A stale version returns a conflict and requires rereading.

Fresh preferences and the catalog enter each ordinary main turn, including resumed turns. After a same-turn edit or compaction, the Agent can refresh through the read tool. `create_task` accepts `preference_revision` and `selection_reason`. Nonempty preferences require the current revision; stale decisions fail visibly before task creation. Preference edits and task creation are serialized. Existing authorization, capabilities, concurrency and request-id deduplication still apply. Unauthorized Profiles are not enabled by saving preferences; allow-list changes remain in existing Task Configuration.

The main Agent interprets the deliverable and picks an eligible Profile ID. Explicit task choices override general preferences; unmatched tasks retain existing defaults. The selected configuration remains an immutable task binding. The preference revision and short explanation are creation-time metadata, not a description regenerated from later preferences. Changes to defaults or preference text do not alter old tasks, their follow-ups, or the main Chat Agent. Explicit task switching uses existing migration boundaries. Icon projection follows the actual binding after a switch, never the latest editable Profile.

Schedules retain their explicit Profile or existing default behavior; this feature adds no scheduled classification call. Unavailable preferred executors must not be silently replaced. Concurrency queues work with its selected executor.

Pot and Codex main Agents expose these tools. Workers and read-only summary turns cannot edit preferences.

## Usage

A main turn that attempts `create_task` is marked once in the shared usage ledger. `routing_total` is the subset of actual main-chat requests/turn records matching those markers. It includes the whole turn's understanding, replies and compaction, including requests preceding the tool call. It is not an independent allocation of routing reasoning tokens.

The Usage page and main-chat Usage show **Main-chat usage including task routing** only when matching records exist, with input/output/input-cache and incomplete-record counts. This subset is already included in the total and must never be added again. Global queries follow the selected date window/timezone; main-chat details use lifetime scope. Task details do not include main-chat routing usage. Pagination does not change totals. Missing usage remains incomplete, not measured zero. Existing unmarked historical turns are not guessed or backfilled.

Native Codex uses its existing cumulative per-turn deltas; Pot uses request-level gateway usage. No extra routing model call is introduced.

## Validation

See [Execution preference acceptance](validation/execution-preferences.md) for dated observations, semantic evaluation, commands, and remaining UI/provider coverage. Mock transport tests establish protocol behavior, not model understanding or real worker-provider availability.
