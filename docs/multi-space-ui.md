# Multi-Space App UI

Shared assistant navigation and native multi-Space UI for [#47](https://github.com/jjyr/pot/issues/47), updated
2026-10-15. The [historical validation](validation/multi-space-ui-20261013.md) covers only its recorded scope.
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

Chat uses a persistent 220 px assistant pane beside the existing 56 px feature
rail. The pane is visible only in Chat; Tasks, Machines, Usage and Settings retain
the full content width. Selecting Chat restores the last conversation instead of
opening a separate assistant directory. Chat is a root destination with no parent
chevron. The pane can collapse; windows below 720 px use a toggleable overlay drawer.
The feature rail, assistant pane and Chat canvas have separate surface colors in
both themes. The pane uses a cool gray fill and a stronger selected-row fill;
the same tokens apply when the pane becomes a drawer. See the palette in
[Design language](design-language.md#color-and-surfaces).

List assistants together without Space groups. A row contains a procedural avatar,
name, ownership badge, one-line conversation preview and an unread reply count.
Same-name assistants include the Space name. Keep registry order stable; receiving
a reply must not reorder the list. Search filters names without changing selection.
Preserve each conversation's input draft, cached history and reading position when
switching. Keys include Control identity and assistant identity.

The ownership badge is a computer or cloud, with a tooltip and accessibility label
naming its Space; it does not describe execution placement. Disconnected Spaces
retain cached assistants and previews, marked offline. Replies, pending approvals,
configuration failures and background activity have separate status text. Once
activity finishes, restore the latest completed assistant response preview. The
compact operator catalogue supplies this preview, reply identity and durable completed-reply count for every
assistant; clients do not fetch every transcript or invoke a model to generate it.
Initial history does not count as unread. Opening a conversation clears its unread
indicator. Offline/approval/activity indicators do not change its avatar.

Default avatars are raster images of the canonical Pot Companion mark on a rounded
solid background. A stable hash of the durable random creation request identity
independently selects one of eight foreground colors and one of eight background
colors, giving 64 combinations. Colors stay fixed across themes, restart and rename.
Creation previews, assistant rows and Chat titles share the same image. The portable
`pot-avatar` crate composes the image; the shared UI caches the 64 possible rasters.

A new completed live reply animates the whole image, including its background,
for 650 ms: scale 1 → 1.16 → 0.94 → 1 and rotation 0° → −6° → 3° → 0°.
Nonlinear timing gives a slow expansion, fast contraction and short settle, with
one rebound and no layout movement. Background assistants also react. Initial
history, duplicate refreshes, clearing unread state and reconnect catch-up do not
replay reactions. Hidden pages do not queue reactions; Reduce Motion stays static.
Transient animation images are released after use. Model tools for changing
avatars and names are outside this increment.

### First use and creation

Fresh App and Control configuration contain zero registered assistants. AI setup
is available before registration. In Chat, an unconfigured destination offers
Set up AI and the secondary link “Already have Pot? Connect to a Pot server.”
(Chinese: “已有 Pot? 连接 Pot 服务器。”). A browser is already connected to its serving
Control and does not offer the native server-connection action. Connecting an
existing Control makes its assistants immediately selectable without local setup.

AI configuration is an independent Settings page, not a step in a creation wizard.
Saving configuration sends no inference request and creates no assistant. Entry
from Chat saves and returns there; entry from Settings remains in Settings. Chat
then offers a separate Create your first assistant state. It prefills Pot, the
current model, destination and avatar; one Create assistant click registers it
and opens Chat. Closing creation retains configuration. Restart never provisions
an assistant implicitly, and retries reuse the creation request identity.

The pane's + action reuses the same creation surface. More settings reveals the
name, model and destination; rules belong in the existing assistant editor after
creation. The destination defaults to the current assistant's Space, or the Space
whose AI setup was just completed. Destination changes load that Space's own
configuration and never copy configuration IDs or credentials. If it has no model,
show its setup action before creation. Existing assistant location remains read-only.
Creating from another assistant copies the selected resolved model configuration;
credentials remain shared by their owning Control. Selecting a row opens Chat,
not its editor. The Chat title shows the same avatar, name, ownership and explicit
assistant-edit action.

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
