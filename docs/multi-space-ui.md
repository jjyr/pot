# Multi-Space App UI

Native App implementation for [#47](https://github.com/jjyr/pot/issues/47), updated
2026-10-13. See [validation](validation/multi-space-ui-20261013.md) for tested scope.
The backend contract is [Assistant messages](assistant-messages.md).

## Navigation and ownership

App presents its embedded local Space and its configured remote Space together.
A Space is identified by the Control's stable identity. Local/cloud indicates
Space ownership, not where a particular tool or task executes. The current scope
is one local and one remote Control.

There is no user-facing local/remote mode switch, Space selector above the
assistant list, or assistant/Space dropdown in Chat. Selecting an assistant opens
its conversation and determines the destination of subsequent chat operations.
Opening a machine never changes the selected assistant or task routing.

Client object references include Control identity and object identity. Names,
list positions and the current chat selection must not determine another object's
destination. Late responses must not replace another object's state. Preserve
drafts, caches and navigation state separately for each owner. Never fall back to
local execution when a remote operation fails.

## Assistants

Show assistants in one list without Space groups. Place New assistant at the top,
in the position previously proposed for a switching dropdown. Each row shows the
assistant name with a local computer or cloud icon. Hover and keyboard-accessible
help name the concrete Space. Accessibility labels include the same information.
Same-name assistants also show a short Space label so disambiguation does not
require hovering. Connection state is separate from the ownership icon.

```text
Assistants                            + New assistant
-----------------------------------------------------
Daily assistant           [local]
Development assistant     [local]
Work assistant            [cloud]
Research assistant        [cloud]   Offline
```

Clicking a row opens that assistant's Chat. Its header displays its name and
ownership icon without a switching dropdown. Preserve assistant editing through
an explicit edit action; selecting an assistant does not open its editor.

New assistant defaults to the current assistant's Space and explicitly offers a
location field. Creation opens that assistant's editor, with AI configuration
loaded from its destination. Changing the destination does not copy configuration IDs. An
existing assistant's location is read-only; editing it does not migrate it.

## Chat and background coordination

Users coordinate work through ordinary Chat, including asking one assistant to
contact another. There is no Assistant messages page, inbox navigation, message
history panel or handoff wizard. Do not expose internal mail envelopes, relay
queues or processing transcripts as a separate user workflow.

Use the existing chat/tool presentation to explain the recipient and delivery
result when an assistant sends a message. Normal chat conveys useful progress,
results and actionable failures. Delivery acceptance does not mean that the
recipient has finished the work. Peer content must remain distinguishable from
user instructions; do not insert internal messages as user-authored turns.

Background conversations retain their independent model context. Showing useful
progress/results in Chat requires an explicit integration with the owning
conversation; existing relay delivery alone does not provide that UI behavior.
Do not automatically forward every internal exchange or create courtesy-message
notification loops.

## Machines

Use one scrolling page with two groups, local first and cloud second, separated
by a quiet divider. The cloud heading includes its configured Space name. Keep
existing machine cards, connectivity, Host/Sandbox expansion and supported desktop
actions. Do not repeat Space ownership icons on every machine in a named group.

```text
Local
  This Mac                            Online
  Development machine                 Online

-----------------------------------------------------

Cloud · My server
  Cloud machine                       Online
  Build machine                       Offline
```

Grouping follows Control membership. A remote execution machine joined to the
local Control belongs in the Local group. Task details identify the actual
execution machine independently of the assistant's Space.

When no remote Space is configured, show Connect cloud in the lower section.
When it disconnects, retain cached machines, mark the group stale/offline, and
disable operations that require that connection. Machine actions and permission
changes bind to their explicit owning Space and assistant, never whichever chat
was most recently selected.

## Tasks and schedules

Tasks combines both Spaces with the existing Ongoing, Needs attention and Archived
groups. Within each state, group rows by assistant; show its ownership icon next
to its name. Search, ordering, counts and pagination apply to the combined list,
not independently paginated lists concatenated in the renderer. Use stable
Space/object identities for ties and selection.

Opening a task routes to its owning Control and assistant. Details show the
assistant's ownership icon and actual execution machine/Host/Sandbox. Work created
by a recipient is a task belonging to that recipient, not a relocated source task.
Navigation back restores the combined list's filters and position.

Schedules likewise combine assistants across both Spaces and show ownership beside
the assistant name. Editing, pausing and deleting target the original owner.
Schedule history opens tasks through their full ownership references. A disconnected
Space means status is stale; it does not mean a cloud schedule has been paused.

## Settings and Usage

Language, appearance and other App preferences remain global. AI and task
configuration explicitly select the configuration location. This selector changes
only the configuration being edited, not the active chat. Keep unsaved drafts
separate by Space and configuration identity. Credentials stay with their current
owners and are not copied between Spaces.

Overall Usage defaults to both Spaces, with a location filter and per-Space
breakdown. Combine underlying totals and model/tool/date buckets, not precomputed
percentages. Apply the same date range and time zone before aggregation. Conversation
Usage stays scoped to that conversation. Partial or stale data must not look like
a complete current total; distinguish unknown usage from zero.

## Connectivity and notifications

Retain cached list items after disconnection and identify which Space could not be
refreshed. Keep the other Space usable. Unavailable destinations produce an explicit
error and never redirect actions. A changed Control identity must not inherit an
old Control's cached objects or pending operations.

Notifications identify the assistant and its local/cloud ownership. Notify for
useful completion, required user approval and actionable failures, not every peer
message. Notification navigation uses the same explicit ownership references.
Show a pending-delivery explanation when relevant; any quit warning about message
delivery must depend on actual pending messages, not merely having a remote Space.

## Implementation and validation boundaries

`crates/app/src/spaces.rs` refreshes native Space catalogues independently and
retains cached data on refresh errors. `crates/ui/src/application/spaces.rs`
owns portable projections and merges task pages and raw Usage buckets.
`crates/ui/src/application/spaces_ui.rs` renders the unified views and binds
actions to explicit owners through the platform adapter.
Existing scoped Chat clients preserve drafts; configuration navigation captures and
restores the originating assistant and task. Usage limits require a specific Space.
The shared asset source includes the local and cloud icons.

This implementation adds no model-visible tools or special handoff protocol.
The shared message record gains equality comparison only; its wire format is unchanged.

Use the existing shared UI components and [design language](design-language.md).
The browser stays bound to its serving Control and does not become a multi-Space
relay. Shared component changes must preserve that single-Control responsibility
without maintaining obsolete native mode-switch interfaces.

Validate duplicate assistant/object names and IDs across Spaces, late responses,
offline cached data, Control identity changes, combined filtering/pagination,
destination-specific mutations, draft preservation and notification routing.
Check real native rendering in light/dark and at 800 × 700, keyboard access,
tooltips, Chinese text, IME and reduced motion. UI verification requires no paid
model calls. Record implemented scope and actual validation separately from this
approved design.
