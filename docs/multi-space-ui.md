# Multi-Space App UI

Native App combines its embedded local Space and one configured remote Space.
Web remains bound to its serving Control. Shared layout, assistant rows and editing
behavior belong in [Design language](design-language.md); platform adapters belong
in [Shared UI](shared-ui.md). The messaging contract is
[Assistant messages](assistant-messages.md).

## Navigation and ownership

A Space uses its Control's stable identity. Local/cloud describes ownership,
independently of where tasks or tools execute. App has no local/remote mode switch
or Space selector above Chat. Selecting an assistant opens its conversation and
sets the owner of subsequent chat operations. Viewing a machine changes neither
the selected assistant nor routing.

Object references include Control and object identity. Names, list positions or
the current Chat must not determine another object's destination. Keep drafts,
caches, events, pending requests and response fences separate by owner. A changed
Control identity cannot inherit an old Control's objects. Failed remote operations
never fall back to local execution.

## Assistants

The directory combines assistants without Space groups. When more than one Space
is configured, muted text beside each assistant name identifies its Space, not
execution placement. A single configured Space shows no ownership label. Count
configured Spaces even when offline or without assistants, so connection loss
does not remove identity cues; adding execution machines within one Space does not
enable the labels. Adding or removing a Space updates all rows immediately.
Cached offline assistants remain visible. Conversation selection restores that
owner's draft/history/reading position. Directory search/scroll belongs to the
window because the list spans owners.

The compact operator catalogue supplies previews, reply identity and durable
completed-reply counts without fetching every transcript or invoking a model.
Initial history is not unread. Row content and avatar reactions follow
[Assistants](design-language.md#assistants).

### First use and creation

Fresh configuration has no registered assistants. AI setup and registration are
separate operations. First-use Chat requests an atomic bootstrap of Pot in its
active Space once a default Provider exists, preserving existing assistants.
Nested connection setup in assistant settings resumes that draft. One-click
creation uses the selected assistant's Space, or the local/serving Space without
a selection. Creation destinations load their own configuration and credentials stay with the owning Control.
Creating an assistant uses its destination Space's default Provider and that
Provider's concrete default model, independently of the current assistant;
existing assistant location is read-only. See
[creation behavior](design-language.md#assistants) and
[first connection](design-language.md#settings-and-first-connection).

## Tasks, schedules and Machines

Tasks combines both Spaces, then applies status grouping, search, ordering and
pagination to the combined list. `GET /api/v1/tasks/catalog` is an authenticated
operator read across a Control's assistants, ordered by updated time descending,
then Task ID and assistant ID. It returns at most 50 rows with complete counts and
totals; agent tools continue to use the assistant-scoped `/tasks/query` endpoint.
The UI reads a sufficient prefix per Space rather than loading every assistant's
archive. A completed source can render before a slow Space finishes; the loading
caption identifies provisional totals and pagination waits for all sources.
Rows group by assistant with ownership badges;
Space/object identity disambiguates selection. Task details route to the owning
Control/assistant and identify the actual execution machine and working directory.
A task created by a message recipient belongs to that recipient. Back restores
the combined list's filters and position.

Schedules similarly combine owners. Edit, pause, delete and history navigation
retain the original owner. A disconnected Space makes its status stale; it does
not imply a schedule is paused.

Machines groups by Control membership, local first and cloud second. A remote
execution machine joined to the local Control appears in Local. The cloud section
shows Connect cloud when unconfigured and retains cached inventory when offline.
Machine permission changes bind to their explicit Space and Runtime,
independently of the current Chat. Default execution policy is visible on each
machine, with permanent command rules collapsed and active task grants shown only
when present. Tasks has its own destination; machine cards have no task links.
Machines expose execution authorization without VM lifecycle, resource or guest
desktop controls.
Machine labels and membership use native operator controls; browser sessions
cannot reach private enrollment/administration endpoints.

## Settings and Usage

Language and appearance are App-wide preferences. AI/task configuration explicitly
selects a configuration location without selecting another conversation. Drafts
remain separate by Space and configuration identity; credentials are not copied.

Overall Usage defaults to both Spaces, with a location filter and per-Space
breakdown. Aggregation combines raw totals and model/tool/date buckets under the
same date range and time zone, rather than adding precomputed percentages.
Conversation Usage remains scoped to its owner. Partial/stale data is distinguished
from complete totals, and allowance changes require a specific Space.

## Connectivity and notifications

Space catalogues refresh independently. Keep cached items and identify the failed
Space while the other remains usable; disable operations requiring an unavailable
connection. Missing destinations expose errors rather than redirecting actions.

Notifications retain assistant/Space identity and use the same owner-aware
navigation. Assistant-to-assistant coordination uses ordinary Chat/tool results,
without a mailbox page or handoff wizard. Accepted delivery is not completed work;
relay delivery does not automatically forward all peer exchanges into Chat or
create courtesy notifications. Background conversations keep independent context.

## Implementation

[`internal/spaces`](../internal/spaces) maintains native local/remote catalogues,
identity checks and last-known caches. The desktop service supplies those
projections through Wails; [`web/src/state/aggregate.mjs`](../web/src/state/aggregate.mjs)
merges owner-tagged Task/Schedule pages and raw Usage buckets.
[`web/src/components/pages.mjs`](../web/src/components/pages.mjs) captures each
object's Space/assistant for actions and fences late responses. The production
browser adapter retains its single-Control boundary. This UI adds no separate
model-visible handoff protocol.
