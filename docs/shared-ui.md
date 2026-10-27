# Shared App and browser UI

The HTML/CSS/JS frontend in [`web/src`](../web/src) implements the main window,
tray panel and browser pages. Wails v3 hosts the macOS windows in the system
WebView; the independent `web-ui-server` serves the same embedded frontend to
browsers. Visual and interaction contracts belong in
[Design language](design-language.md).

```text
web/src: shell, pages, composer, drafts, configuration, navigation
                              | production client
               +--------------+----------------+
               |                               |
         Wails bindings                  same-origin HTTP/SSE
       Go desktop service                 browser server
      local node + Agent Link peers       one local node
```

## Presentation and platform boundary

[`components/ui.js`](../web/src/components/ui.js) owns the shell, assistant
directory, Chat and input lifecycle. [`components/pages.mjs`](../web/src/components/pages.mjs)
owns Tasks, schedules, Topology, Usage, configuration forms and return contexts.
[`components/task-conversation.mjs`](../web/src/components/task-conversation.mjs)
owns the task status, conversation history, menu and fixed composer, reusing the
shared reply/tool/composer components. Pages retains Task navigation, editor drafts and reading intent; domain repositories own shared requests and owner scope.
[`components/usage.mjs`](../web/src/components/usage.mjs) owns exact-count usage tooltips, the trend chart and independently loaded detail lists.
[`components/reply.mjs`](../web/src/components/reply.mjs) owns reply content,
activity and the single failure presentation; [`state/chat-replies.mjs`](../web/src/state/chat-replies.mjs)
binds optimistic and durable reply identities without changing execution state.
Reusable controls, Markdown, completion, resource and About components live beside
those files. Portable conversation/editor/ownership state lives in `web/src/state`.
The shared components use a client interface; they do not call Wails directly.

[`styles.css`](../web/src/styles.css) owns semantic colors, system typography,
breakpoints and control geometry. All hosts use the same assets and layout.
Width controls navigation. Touch density applies below 720 px or with a coarse
pointer; the tray keeps its explicit compact layout.
Default assistant avatars use the [approved kitchen characters](design/pot-brand/README.md#assistant-characters).
`pkg/assistantavatar` composites the assigned artwork over one of eight soft pastel
backgrounds as an opaque 128px PNG. The creation request chooses the background;
the saved character remains stable through renames, uploads and resets. Creation persists the PNG in the assistant files alongside its profile, using the
same file for default images and custom uploads. The initial database baseline
does not adopt old development avatar records; see
[Database migrations](database-migrations.md). The settings editor requests the original
image from `/api/v1/assistants/default-avatar?assistant_id=...` for reset previews.
Directory, chat title, settings and tray render the stored PNG through one image
component. Unloaded identities use a neutral placeholder.

## Markdown diagrams

[`components/markdown.mjs`](../web/src/components/markdown.mjs) renders closed
`mermaid` fences through [`components/mermaid.mjs`](../web/src/components/mermaid.mjs)
in Chat, Task details and Markdown tool results. Other fences and incomplete
streamed fences whose type is not yet Mermaid remain code. A known Mermaid fence
waits for closure and diagram readiness before publication, with the bounded
fallback described below. The component loads the pinned, bundled Pele ESM
module only when a diagram enters the document; no CDN is needed.

Each `pot-mermaid` element owns its Pele mount and destroys it on disconnection,
including when a message is recycled or its diagram source changes. Appended
reply text reparses only the final Markdown block; completed paragraphs, code,
tables, image previews and diagrams retain their nodes and local state. A late module load
cannot mount a detached or superseded element. Container resizing and pending
font loading trigger Pele's layout refresh. Semantic CSS variables keep diagrams
in the current light/dark theme, and shrinking diagrams support zoom and pan.
Source is available in a disclosure and opens automatically on failure; errors
stay within that diagram. Only closed fences are drawn, and rendering is bounded
to 20,000 source characters, 1,000,000 SVG characters and 500 flowchart edges.
Diagram links and images are disabled, and raw Markdown HTML remains literal.
Pele uses its own layouts rather than Mermaid's exact appearance.

## Generated image previews

Typed assistant resource replies and uploaded attachment previews use the shared
`ResourceView` component. Media playback, isolated HTML and explicit download
behavior are defined in [Display resources](display-resources.md).

The shared Markdown renderer displays owner-bound `pot-file://TASK_ID/PATH`
images inline in Chat, Task conversations and Markdown tool results. Both image
syntax (`![description](pot-file://...)`) and ordinary artifact links ending in
`.png`, `.jpg`, `.jpeg`, `.webp` or `.gif` show a preview, with enlarge and original
download controls. Other file links retain their download behavior. Remote HTTP
images remain explicit links and are never fetched automatically.

The production client reads the existing authenticated Task file chunks through
HTTP or the native bridge, retaining the original node and assistant for every
chunk. It validates offsets, total size and revision before creating a Blob URL.
Previews accept PNG, JPEG, WebP and GIF signatures, up to 16 MiB per image;
unsupported, oversized and unavailable originals show a localized download
fallback. No new file routes or model prompt instructions are needed.

Connected `pot-file-image` elements share pending reads for the same node, assistant,
Task and path. Disconnection releases the lease, aborts unused reads and revokes
unused Blob URLs; a synchronous streaming replacement reuses its lease. Preview
dialogs close when their message leaves the document. Loading, error and dialog
controls follow the current UI language, including during asynchronous reads.

## Business repositories

`state/domains/index.mjs` supplies one Chat, directory, Tasks, schedules and Providers
repository per client. Production and fixtures consume these same interfaces.
A domain query exposes `watch(listener) -> unsubscribe`, a committed `data`
value, `status`, `freshness`, `error`, `lastConfirmed` and derived `fresh`.
Convenience methods such as `tasks.watchDetail(task, scope, listener)` and
`providers.watchCatalog(scope, listener)` select the shared query. They own the
first database read, background confirmation, pagination, invalidation dependencies
and subsequent notifications. Unsubscribing one view does not cancel another
view's IO. Unused completed query entries are bounded to 128; active queries and
in-flight reads remain leased until their consumers finish.

```text
shell / page / background strip
              | watch + typed business command
    Chat | directory | Tasks | schedules | Providers
              | shared queries + command attempts
         resource / transcript repository
              | transaction commit
             IndexedDB
```

The directory combines serving Spaces, local and linked assistants and machine
labels, removing execution and approval authority from retained identities. Tasks
shares catalogue reads, metadata, guidance and the transcript reducer with the
Chat background strip. Schedules owns collection pagination and preview/save/run
commands. Providers owns the shared catalog, routing and allowance settings,
connection DTOs and quota monitor commands. Provider catalogs normalize away
assistant/editor context; sensitive editor revision reads remain transient.

Chat opens a scoped surface session through `chat.open(scope, {surface, context})`.
`query` restores committed content and saved view hints; `watch` owns transport
and same-client/cross-tab commit notifications; `refresh` commits an explicit
snapshot read. `history` chooses indexed coverage or shared HTTP and returns a
committed window for pagination and reset restoration. `saveView` stores reading,
layout and disclosure hints against that window. The domain privately owns cache
construction, persistent identities and IO. UI context supplies the controller's
anchor/position, and a history validity guard only fences superseded navigation.
The UI never provides an IO callback or receives a cache/key. Each surface has its
own reading intent. Closing one leaves shared history IO available to another;
retire, scope change and deletion fence late publication and cache resurrection.

Chat and Task output use one preparation path for new output, history pages,
cached initial content, virtual remounts and expanded work records. A candidate
renders in a connected, inert surface outside the live history, with the actual
column width, captured data and renderer props. Recursive component readiness,
image decoding and font readiness precede measurement. The candidate's border
size and margins are known before the batch enters the live flow. Background
tasks, approvals, history notices, the Task goal and Task files join that batch.
The previous visible version remains usable while a candidate waits. Only the
initial Chat/Task reveal waits behind a hidden layout of the real width; ordinary
updates and pagination keep the history visible.

A window measures the entire loaded collection before registering its canvas;
unknown rows never enter the index with estimated heights. Initial measurement
uses cohorts of up to 96 rows, retaining up to 64 ready destination candidates
between cohorts. Other candidates release their DOM and leases after measurement,
leaving numeric geometry. The final candidate set covers the measured viewport,
12-row overscan, navigation destination and native focus/selection pins. A viewport
containing more than 96 very short rows can therefore require more than 96 final
candidates; this does not retain offscreen DOM for the entire loaded collection.
At most 32 component loads run concurrently across the window and its regions.
It retains one running snapshot and the newest pending revision, so
continuous tokens do not indefinitely replace an otherwise publishable batch.
Width, font, locale, scope and navigation changes retire incompatible candidates.
Identity profiles, Task mappings/titles, reference source text, inline approvals
and machine labels join captured renderer props and invalidate the appropriate
layout generation. Task geometry also belongs to its request scope, issuing
Control and boot; changing that ownership retires its old resource DOM.
Changed children and scalar text enter the live view from their connected
candidate. Stable native containers and unchanged content can remain in place;
commit does not reconstruct new content from strings. Each hidden presentation
owns separate IDs and matching ARIA references.
Logical component slots carry their original five-second deadline across token
updates and candidate replacement. A committed removal retires that slot's
deadline, so a new component added later receives its own five seconds; retiring
an uncommitted candidate does not end the live component's lifetime. A known
Mermaid fence stays unready until it closes and its diagram renders; its
temporary long source is not a preview.
Closing the fence can replace that waiting candidate without restarting its
deadline. Other Markdown parsing and code formatting finish synchronously in the
candidate.

Ready media reflows from intrinsic dimensions and current ancestor CSS when the
layout generation changes. A measurement surrogate must not carry its old rendered
width or height into the new generation. The fixed deadline frame belongs to an
unresolved component's lifetime and remains distinct from this ready-media reflow.
Ready HTML uses its actual browsing context at the candidate width in a transparent
top-layer viewport, retaining its DOM parent, document state and lease. An inert
projection of the old measured frame shows the loading status during this bounded
handoff and never supplies a new reading anchor. When the candidate's border box
and inner viewport dimensions match the live iframe, the iframe stays in place
with its current input surface. Outer result and dependency text still render,
measure and transfer from the hidden candidate; these revisions do not require
another native viewport handoff. Changed viewport dimensions use the actual
browsing context before measurement. Only an already focused native
document retains its existing focus; new candidates stay inside the inert hidden
surface. Native viewport restoration does not restore an earlier focus snapshot:
Tab or click destinations chosen during preparation remain authoritative through
commit, resize cancellation and owner retirement. Commit and cancellation restore
the native viewport before retiring the surface. A restored iframe does not
immediately publish input readiness: its existing loading hold covers the final
flow viewport until matching animation-frame observations span the browser's
rendering update. Source, browsing-context and resource-generation identity must
remain current; geometry or native resize revisions restart that boundary.
The window remains unprepared and busy until presentation completes. Publishing
readiness removes the sibling hold without another iframe style, popover or inert
transition. An unfocused returning iframe is temporarily excluded from sequential
Tab navigation; a focused document keeps its current keyboard target. New hidden
candidates remain inside the inert preparation surface. A new viewport handoff,
reset, suspension or owner retirement cancels the previous presentation wait.
Cancellation retains the unfinished native reflow budget until commit;
this does not renew an unresolved component's content deadline. Resize, result
and dependency notices carry resource revisions through
the same preparation batch; a late revision invalidates an older measurement.

Tool candidates inherit the live tool's one-time approval auto-open state and
its actual open/raw disclosure when no explicit reading memory overrides it;
they never write shared disclosure intent. A first pending approval can still open
its tool; later explicit close/open actions remain authoritative at commit.
An unexpected preparation/commit exception retires unfinished candidates and
releases transient layout holds before propagating the error. A failed transaction
does not report a prepared layout or reuse slots already consumed by that commit.
This includes errors in the first layout check and later animation/timer callbacks.
Observer callbacks cannot silently retry a failed transaction; the caller's next
explicit message update is its retry entry point and receives a fresh candidate.

At the deadline an unresolved block receives a fixed, scrollable outer frame of
at least 240 pixels; an inline reference keeps its measured link box. Binding,
bytes, decoding, diagram completion and interactive-document resizes may update
the interior later without changing the committed canvas. A Mermaid fence that
closes after this fallback also retains the frame. A reader's explicit expansion,
source disclosure or width/font change requests another measured layout epoch.
Streaming file references may query binding metadata before the reply finishes;
only a ready saved-resource binding authorizes a byte preview.

Preparation measures static surrogates for unchanged live resources rather than
moving or restarting them. Commit patches the original Markdown views and moves
new prepared nodes into place, preserving completed paragraphs, selection, code
scrolling, leases and diagram instances. Surfaces retire after the actual nodes
have transferred. Atomic DOM moves are preferred; the fallback restores focus
and selection and defers disconnect cleanup to distinguish a move from removal.
Real removal retires the component generation and releases its resources.

One conversation controller chooses the geometry correction for the entire
batch; nested adapters never write the native scroller. Its context snapshots
expose the newest reading intent while subscribers receive measured positions.
This prevents a delayed body from restoring an older Latest command. Native
contact retains its original live touch target through `touchend`. A shorter
canvas that would clamp a moving native scroller waits for the inertia tail to
settle. Native `scrollend` ends input ownership where available after its generation
survives the next rendering step; a geometry write's queued end cannot retire a
newer native frame. The fallback
retires its idle timer on every new input generation and scroll event.

Saved height hints describe a committed layout, not preparation evidence. A
pending layout saves the existing native reading position without height hints.
The persisted view/message formats are unchanged; preparation state and component
slots are transient UI data.

UI components keep focus, unsaved values, expanded disclosures, filters and the
history sentinel. They pass business inputs to typed commands instead of building
domain URLs or repeating mutation/refresh sequences. Command attempts share
pending state and immutable input/request identity across consumers; explicit
retry retains that identity. The production adapter commits accepted response
receipts and invalidations before notifying domain queries. A manual retry after
a local commit failure retries that accepted receipt without sending the command
again. Provider monitor revisions and exact-intent recovery retain their existing
compare-and-swap behavior.

Permissions, pairing/authentication, binary resources,
Topology and detailed Usage retain their specialized interfaces. These combine
live authority, transport lifecycle or route-specific aggregation that is outside
the migrated display domains; they still use the shared storage boundary where
applicable. They must not create a second directory, Task or Provider cache.

## Committed local projections

`state/resource-cache.mjs` routes allowed display GETs from both `client.read` and
`client.request` through `state/projection-store.mjs`. HTTP/native responses and
successful display mutations commit to IndexedDB before returning query values.
Domain invalidations persist epoch counters; a response fetched against an older
epoch must revalidate before it can publish. Memory holds only committed query
copies inside the repository. Production never falls back to a volatile store
when IndexedDB fails.

```text
HTTP / native / SSE / successful mutation
                  |
          repository + reducer
                  |
      IndexedDB transaction complete
                  |
       query projection + notification
                  |
              DOM views
```

Keys include the local node, boot, presentation scope, assistant and full query.
Provider summaries share one local entry across assistant contexts. Credential
bytes and authorization URLs/codes are excluded from persisted provider,
integration and Agent Link display projections. Authentication, pairing, live
permissions, editor revision reads, binary resources and conversation transport
keep their specialized interfaces. The machine editor stores only its derived
policy summary; a failed live read exposes that summary as read-only and never
stores pending approvals or restores authorization. See [UI events](ui-events.md) for transcript
commit boundaries.

A returning page first queries its committed local data and refreshes in the
background. Queries carry `lastConfirmed` and fresh/stale/retained state. Dynamic
lists remain fresh for 5 seconds, configuration for 30 seconds; freshness checks
on access create no polling. A retained directory cannot restore execution,
approval counts or operation capabilities. Task and schedule controls wait for
current authoritative metadata; approvals always read the server. Provider
selection and creation flows explicitly wait for current configuration revisions.
The server remains the business and permission authority for every command.

Projections retain at most 256 entries and 64 MiB, with a seven-day expiry;
command response receipts expire after one day and share those limits. Writes
evict older confirmed projections when needed. Oversized values and transaction
or quota failures retain the previous committed value and show a localized retry
notice. A failed invalidation remains pending until it can commit. There is no
memory fallback, offline command queue or automatic side-effect replay. If the
server accepted a command but its local receipt failed to commit, a manual retry
in the same client retries only the local commit using the accepted response.
Reload discards that in-memory recovery receipt; existing server request identity
and uncertain-result rules still apply.

Task lists read the local operator catalogue; assistant count does not multiply
reads. Task transcripts use `state/task-repository.mjs`: event cursor, input
bookmarks and active-tool checkpoint commit with their changed message records.
The current projection holds at most 640 messages within the shared 16 MiB
window budget. Each Task retains up to 2048 records / 32 MiB on disk, with 64 MiB
across Tasks. Message text, tool arguments/result and combined output streams are
bounded to 1 MiB of UTF-8 content each; truncated cached output is labeled.
Task result metadata and retained guidance also have bounded display projections;
guidance keeps at most 256 entries within 16 MiB, with the same truncation notice.
The server retains the original output. A visible upper history boundary automatically
queries an indexed page of 80 records, including when the initial window is
shorter than the viewport. Prepending preserves the visible message anchor and
pixel offset. One page may be in flight; unchanged cursors do not retry. Failures
show a local Retry action and await explicit input. Latest returns to the committed
current window and cancels pending historical reads. New live output preserves a
reader's historical window. Navigation, disposal and a new run fence late results.

If a historical range was evicted, the existing after-only event API requires the
first reconstruction to start at cursor zero and stop before the requested
boundary. Its working window remains bounded to 640 messages / 16 MiB. Processed
ranges commit to the historical cache, so neighboring pages reuse that coverage
while retained. Eviction removes only the affected coverage rather than discarding
all ranges. Coverage metadata retains at most 128 intervals; discarded coverage
requires a conservative backfill. Backfill never replaces the live cursor/checkpoint or replays commands.
A new run has its own conversation boundary.

Task and usage page offsets, reading positions and disclosures remain view state.
The Chat background-task strip consumes committed query updates and holds only
its current view projection; restored states omit running animation and clocks
until confirmed. Background refresh preserves keyed rows, focus and editor sessions. Dirty forms
keep their leave protection and have no cross-tab draft persistence. Cross-window
notifications carry commit identities and invalidation domains; recipients query
the database instead of consuming broadcast response bodies. Browser logout
retires existing cache writers and clears business projections and transcripts,
while retaining authentication storage, preferences and notification claims.

## Native editing and ownership

All hosts use DOM textareas and inputs. The WebView/browser owns shaping, caret,
selection, undo and IME; the shell owns the composer frame, send/stop actions,
drafts and reference completion. Stream updates retain the live editor node and
do not rewrite its value. Only explicit restoration, matching send receipts and
completion replace text. Composition events, `isComposing` and key code 229 keep
candidate confirmation from choosing a completion or sending a message.

When an interrupted turn has a continuation and Chat is not running, the empty
composer's main button shows a right-pointing play icon after the assistant's
local draft has finished loading. A pending or failed draft load cannot enable
restoration. Activating it resumes
the original turn through the continuation command and its captured owner, input
and attempt identities. Text (after trimming), attachments or message references
switch the button back to sending; removing them restores play. Restoration
disables the main button until its response arrives. Recovery errors remain
visible above the composer, including unconfirmed tool results. Running turns
retain their existing stop and guidance actions.

Assistant references use compact `@name` text in the native textarea and an
inert, scroll-synchronized highlight layer. Identity ranges serialize to the same
full-address links used in messages and drafts; ordinary typed names never resolve
to identities. Completion, restored drafts and pasted links share this display.
Editing or deleting a reference replaces the whole label, while copying a complete
reference preserves its address. Chat and Task guidance use the same editor.

Phone and tablet browsers leave Enter, including modified Enter and Enter with
reference completions open, to native textarea editing and IME. Ordinary Enter
inserts a line break; sending requires activation of the send button. Their
composers advertise `enterkeyhint="enter"` and ignore form submission events.
Desktop Enter/Shift+Enter behavior is unchanged. Input policy uses the existing desktop/browser host identity, UA Client Hints' mobile flag,
mobile/Android/iOS user agents, and iPadOS's MacIntel plus multiple touch points
identity. It does not use viewport width or pointer/hover media queries, so
resizing a desktop window or using a touch laptop does not change Enter behavior.
Browsers that conceal all mobile identity fall back to desktop behavior.

The shared Chat composer accepts image/PDF files through DOM selection, drop and
screenshot paste. It uses the same bounded byte upload API in browsers and the
macOS WebView; native attachment downloads use the host save dialog. See
[Chat attachments](chat-attachments.md) for formats, capability checks and retention.

Drafts, cached conversations, positions and late-response fences include the
local node and assistant identity. A matching authoritative receipt clears
only its submitted draft. Selecting another assistant does not change ownership
of a request already in flight. Task Usage uses the Task ID for accounting while
requests retain the owning assistant; its Back/history path preserves the
independently selected Chat. Task approvals follow the Task owner and exact Task,
including when a different assistant remains selected in Chat. See [Node UI](node-ui.md).

Pending approvals appear in one transient region immediately after the latest
assistant message. New messages move this region to the new anchor, preserving
expanded cards, focus and decision errors. Without an assistant message it sits
at the end of the conversation. It scrolls with Chat and never opens a modal.
New request IDs trigger one accessible notice; unchanged invalidations do not
repeat it. This projection comes from pending permissions, creates no conversation
message or model request, and does not change reply/unread counts. Reload restores
unresolved cards from the authoritative pending requests.

Chat and Task details use the same expandable
[`approval-card.mjs`](../web/src/components/approval-card.mjs) component, including
Task name, machine, exact operation, explanation, decision controls and errors.
Cards start collapsed and expand in place. A successful Allow/Deny removes the
resolved card in both views; the last decision removes the notice and assistant
badge. Failed submissions keep the operation and retry controls. Decisions retain
the original node, assistant, Task/run and exact request identity.

The assistant catalogue exposes `pending_approvals` independently of replies.
Permission invalidations refresh the directory even when another Chat is selected;
waiting assistants show the count in their row. Selecting one locates its approval
region. Task lists, details and the background strip show Waiting for approval,
and the strip puts these Tasks first. Chat tool rows retain their execution state
while the dedicated region owns approval controls; Task controls attach only to
the exact Task call/turn, with a shared standalone card if no row is available.

Task details retain their entry route in navigation history. Opening a Task from
Chat makes Back return to that assistant's Chat with its draft and reading position;
opening from Tasks makes Back return to the list. Nested Task Usage and browser
Back/Forward preserve this origin.

[`adapters/production.mjs`](../web/src/adapters/production.mjs) captures the owner
and local presentation scope before every request. Desktop calls Go through Wails
and receives
`pot:snapshot`, `pot:subscription`, `pot:changed` and `pot:read` events. Browser
uses same-origin fetch and EventSource. The Go desktop service owns window/tray
lifecycle, the native file save dialog and desktop viewing. Browser adapters own
authentication, installation, notifications and browser downloads. App window
close hides the window while the local Runtime continues; Quit stops it.

The App does not publish or configure the browser endpoint. Browser access belongs
to the standalone server and its operator controls; see [Deployment](deployment.md).

## Configuration and asynchronous actions

Shared editor sessions capture their caller, owner, Runtime boot identity,
revision and request identity. Back/Cancel and navigation use the same dirty-exit
guard. Pending saves block leaving while their result is handled. Browser history
contains only route/session markers, never credentials or form values. Reload
recovery is outside this memory-only editor contract.

Nested Provider setup suspends the actual assistant form, including draft fields,
expanded disclosures, focus and scroll. Save refreshes Provider choices and
restores that form. Cancel returns through the same guard. Global navigation
also checks suspended dirty editors. Source focus is restored only on wide
windows, avoiding automatic keyboard activation on phones.

Assistant model selection and nullable parameter overrides belong to that
assistant. Saving them does not mutate the shared Provider. Providers retain their
own concrete default and configured model IDs; API model checking does not save
configuration or send a model message. The backend validates revisions and applies
pending assistant configuration at a fresh input boundary.

A failed save retains its draft and request identity. Partially acknowledged form
changes are recorded so retry does not repeat completed writes. Runtime identity
changes reject an old session. Authoritative responses refresh live data without
replacing another owner's draft. While a Main turn is running, a nonempty composer sends guidance to that exact
turn; an empty composer shows Stop. Guidance retries retain their original turn
identity even if the turn ends before the receipt arrives. The conversation menu
exposes Pause/Resume; pausing takes effect at a safe model boundary and preserves
in-flight work. Task Configuration edits Provider allowances with revision CAS,
independently of concurrency and assistant model selection.

Inline machine permission selections retain a separate draft during background
updates. A dirty selection keeps its original policy revision for compare-and-swap
saves. An external change exposes a reload action; only that explicit reload or a
successful save adopts the latest policy. Refreshing status never silently rebases
an unsaved choice onto a different revision.

Conversation and Task stream contracts are in [UI events](ui-events.md).

Chat and Task transcripts share `components/work-record-window.mjs` and
`state/work-records.mjs`. Consecutive process records in one stream/turn use one
collapsed Work records disclosure: tool operations and messages are counted
separately. Its single summary line includes the first two communication
counterparts' profile avatars or existing Task identity marks, with an overflow
count. Full-address identity owns deduplication; the current actor is omitted.
Missing profile images use the shared person icon, never a provider logo.
Expanding reveals all counterparts and the original mixed timeline. Each record
opens its own body/result, with raw parameters and code in secondary Call details.

Tools, Task creation/management, ordinary peer messages and normal compaction
can share a group. Prose, user inputs, deliveries/presentations, a new turn,
unknown/stopped execution, failed compaction and actual approvals remain boundaries.
Task live status, user-facing result reports, artifacts and approval controls keep
their existing independent locations. Tool failures stay in the timeline and also
mark the collapsed summary. The activity line uses tool-category metadata only,
never inferred intent or source code, and disappears when activity ends.

`state/script-runs.mjs` folds command-session polls and exec/wait receipts without
adding another visible history group. Surviving parent and child calls retain
their global source order, even when prose or other work interleaves. Known
orchestration wrappers with children do not add an atomic tool count; real session
input/control actions do. Outputs and chunks update their source call. Message
IDs from existing receive envelopes and send receipts correlate duplicate
projections, which remain inspectable as receipts without another body/message
count. Missing IDs never trigger text-based deduplication. Raw facts and model
history remain unchanged. Acceptance is not delivery or completion; receive is not
read. No group elapsed total is calculated by summing concurrent calls.

The adapter retains group keys by membership and reading anchor, and original
record nodes across append, prepend and splits. Disclosure state survives virtual
recycling within the existing conversation-scoped memory; it is not a persisted
history field. A collapsed group exposes one real member's native message ID and
sequence as its reading representative, plus the exact first/last native sequence
of its current members. Appending/prepending retains a valid representative;
clipping or splitting selects a member still in that group. A fresh projection
prefers the saved native reading ID, so restoration can resolve the header alias
even when the virtual group key changes. The range does not make all collapsed
members readable. These fields are derived only in the UI; saved positions reuse
the existing native anchor fields and raw message records stay unchanged.
The existing virtualizer/controller alone owns scrolling. Task
inputs whose API provides only a scalar sender keep their existing independent
reference/guidance projection rather than inventing a full endpoint. Adapters
without individual tool or plan events do not acquire synthesized progress.

`state/task-identity.mjs` is shared with Task views: complete
`node_id / kind / agent_id` addresses identify Tasks. The shared `taskShortID`
helper displays `#` followed by the first four
characters of the real `address.agent_id` (or the entire ID when shorter).
Equal prefixes deliberately share the same short label, including equal IDs on
different nodes. Full addresses, catalogue scope and owner still determine
identity and navigation; labels never route requests. Only a confirmed TaskInfo
address mapping permits icon color. Linked
catalogue IDs are never treated as remote address IDs. UTF-8 FNV-1a32 selects one
of the six fixed families in `state/task-palette.json`; only the small Task mark
uses the same family across light, dark and system themes. Missing mappings use
neutral marks, and short labels remain visible when names are long.

`components/task-identity.mjs` renders the original `icon('tasks')` inside a
22 px tinted container and a neutral monospace short label. Communication
endpoints, expanded records and background rows use the same mark and
authoritative address mapping. The small tinted background has no colored
outline; task names and labels stay neutral. Runtime activity and semantic
status remain separate from the fixed identity color. A confirmed endpoint and
its background row open the same existing Task detail with the catalogue ID;
unknown addresses retain a neutral mark without inventing a navigation target.
Each background row keeps the icon, neutral monospace short ID, ellipsized title
and right-aligned activity/status on one line, including narrow layouts. IDs align
directly after the icons, with a slightly larger gap before the titles.
Communication endpoints use the same icon, ID and title order.
Source and elapsed time remain in the accessible description and Task details.
The original three-row preview and
expand/collapse control retain task ordering and counts without a second summary.
Keyed projections retain live controls through title, status, locale and
short-label updates.

## Read state and activity

Completed replies have a durable reply ID/count. Initial catalogue history becomes
the baseline rather than unread mail. A new live reply can rebound its avatar
once; reconnect catch-up and duplicate updates do not replay it. Native read
baselines are held by the Go attention service and shared between windows through
`pot:read`. Go accepts a read only for the observed latest reply in a visible,
focused Chat. Browsers keep baselines in local storage and synchronize same-origin
tabs. Keys include the local node identity so a rebuilt database cannot inherit an
unrelated baseline. Reduced Motion disables reactions and native tray animation.

Each sidebar assistant name has a monochrome cooking Pot while its main turn or
one of its background Tasks is executing. The compact assistant catalogue's
`executing` projection combines those sources without loading message histories;
queued, allowance-waiting, approval-waiting Tasks and paused work do not cook.
Task, local executor and Agent Link invalidations refresh that catalogue,
including assistants whose Chat is not selected. Offline cached entries never animate work.
Execution takes priority over older unread replies. When execution stops, an
unread reply shows the settled lid, a success check and occasional gentle steam;
the existing reply read receipt removes the Pot. Counts and approval controls
remain independent. Reduced Motion keeps distinct static states, and a hidden
document pauses these animations. Refreshing projections retains the indicator's
DOM and does not replay completion motion.

## System notifications

General settings has one **System notifications** switch, enabled by default.
Delivery intent, API support and OS/browser permission are separate. The switch
belongs to this installation; it cannot grant permission or change another
installation. Authorization requires the explicit **Allow notifications** action
in browsers. Denial, an unanswered prompt and disabled OS banners never count as
an unsupported API and never trigger a different permission channel.

The local browser constructor is preferred whenever supported, including an
installed desktop PWA. An available `PushManager` or standalone display does not
move a local-capable browser to Push. iOS and Android expose notification interfaces
without supporting the page constructor; these environments use standard Web
Push when the required Service Worker APIs are present. On iOS 16.4 or later,
Pot must be opened as a Home Screen application over trusted HTTPS. Ordinary iOS
browsers show Safari's Share → Add to Home Screen instructions in the same setting.
Unsupported and insecure environments show their actual limitation.

The existing non-modal desktop browser introduction appears only after the main
interface is ready, with undetermined permission and enabled delivery intent.
Only its authorization click requests permission. A permanent, atomic IndexedDB
claim prevents repeat introductions across tabs and node identities; blocked
storage skips discovery and leaves settings available. The native main window's
first directory load and paint still signals its shared authorization coordinator;
tray startup cannot request permission. Native automatic/manual requests share one
in-flight attempt and the durable attempted marker. Native and Web preferences
remain independent; native preference/authorization history stays in the private
`system-notifications.json`, while browser intent stays in origin local storage.

### Events and independent delivery services

Business owners publish full lifecycle facts after their authoritative writes
commit. `internal/hooks.Feed` is the shared, typed, process-local subscription
boundary; each subscriber receives its own events. The authenticated owner
`/api/v1/lifecycle` SSE connection transports the complete fact across processes.
It does not send a topic hint followed by catalogue, outcome or permission GETs.
`internal/systemnotifications` converts those facts to bounded notifications,
persists their IDs/content and offers independent consumers. It has no Push
sender, subscription, permission, device registration or local/Push branch.

```text
Task terminal transaction / pending approval / eligible reply commit
                              |
                    typed lifecycle facts
             /api/v1/lifecycle (authenticated SSE)
                              |
                 notification selection + event log
                   independent subscriptions
                       /                     \
              native consumer          browser recipients
           pref/window/claim           one selected channel
                     |                   /             \
             native notifier      local receipts     Push receipts
                                        |                |
                                page Notification  pwapush.Service
                                                  subscription/VAPID
                                                  HTTP/retry/expiry
                                                         |
                                                 push provider -> SW
```

- Reply facts come from the first eligible completed reply/outbox write after
  commit. Catalogue and lifecycle presentation share the same 120-code-point
  whitespace-collapsed preview. Empty/silent Task reports do not create replies.
- Task `complete` and `interrupted` facts come from the existing terminal
  transaction's hook, including Tasks whose execution is remote. Schedule
  notification policy remains part of the prepared authoritative outcome.
  `failed` first allows assistant recovery and does not notify ahead of it;
  only an unresolved completed assistant reply alerts. Cancellation/start never
  alert. The outcome API remains available to its existing UI consumers, but
  notification production does not read it.
- Approval facts come from committed pending state and contain only IDs, owner
  and exact Task, with no tool arguments. Remote executor approvals use a small
  authenticated Agent Link event targeted to the original delegating node. The
  receiver validates the already committed delegation and current capability.
- Linked user replies publish when their incoming reply transaction commits.
  Linked user Task ownership maps through the saved user route, independently
  of whether that conversation has sent text.

The stream begins with current access metadata: Control/boot, authorized owners,
capabilities and access epochs. Registry and Agent Link mutations update this
metadata directly. This does not read reply counters, Task cursors or pending
approval lists. Retained data cannot grant authority; deleted/revoked owners,
capability changes, old boots and old peer approval epochs retire queued events.
The consumer makes no HTTP authority recheck or business-resource scan.

The lifecycle feed has no durable replay. Its bounded per-subscriber buffers
close a slow subscription rather than silently treating missed facts as current;
reconnection starts a new authority lifetime without scanning history. Memory
channels do not cross processes and are not persistent delivery queues.
Notification event/device receipts are durable once conversion and queueing
commit. This is not an exactly-once broker: a disconnected consumer does not
backfill historical business events. Each notification-log subscription has its
own in-memory cursor; failed queue writes retain its current record for retry.
A new subscriber starts at the log tail. Installation activation sequences,
committed with intent changes, prevent delayed consumption from delivering old
notifications to a new or re-enabled installation.

`internal/browsernotifications` stores browser intent, session bindings, reading
leases and per-installation receipts. One event consumer queues each installation
once on its chosen channel. Local receivers and `internal/pwapush.Service` consume
their own receipts. Push owns subscription registration, private VAPID material,
encrypted HTTP sending, retries and invalidation. Composition/startup connects
these services; business publishers do not know any delivery mechanism.

Events use stable Control identity as canonical Space. Browser presentation may
call that Space `local`; current confirmed identity is mapped at lease/click
boundaries. The shared `View.Viewing` predicate suppresses an assistant's events
while its corresponding Chat is focused, or an exact Task's events while that
Task is focused. A different Chat, background or minimized window permits delivery.
Browser sessions renew reading leases every 10 seconds, expiring at 25 seconds;
monotonic sequences reject late updates. Native delivery uses actual Wails window
visibility/focus directly, without browser registration, leases or receipts.
Main/tray share the existing native preference, authorization and durable claims.
An OS send error retains uncertainty rather than blindly resending.

Browser event/device receipts distinguish suppressed, queued, sending, accepted,
failed, unknown and expired. Reservation records uncertainty before an external
effect; acceptance afterward does not prove the OS showed a banner. Devices cannot
consume each other's work. Network errors/interruption never trigger a blind
resend or another channel. Push owns at most three attempts for explicit temporary
provider rejection, with backoff bounded to one minute; 404/410 clears that
subscription. Local age/lease limits are 25 seconds; Push queue age/TTL is five
minutes. Event history is bounded to seven days. Access authority lifetimes fence
queued events from disconnected/replaced owners.

Local browser receivers retain one Web Lock per installation during transient
IO retry. The first receiver establishes its baseline while holding the lock;
standby takeover retains current queued work. Permission rejection and disabled
intent stop retry. `pagehide`, offline and freeze abort notification IO and the
lease timer; `pageshow`, online and resume restore only notification transport.
Every setup continuation checks current lifetime/intent before writes or more IO.
Disposal/suspension keeps an enabled background Push installation, while explicit
off removes only that device. Chat subscribe/attach/recovery is independent.

A live page is required for local browser delivery. Native consumption continues
while windows are hidden and stops on Quit. The backend Push consumer continues
with closed/suspended pages; it does not rely on page SSE/timers or silent Push.

### Push installation and operation

The browser host's authenticated `/api/v1/notifications/` routes register, update,
remove and acknowledge installations. They require the current HttpOnly session
cookie, same-origin checks and current Control/owner scope. Devices are bound to
that session and access configuration generation, with the cookie's expiry. A
registration reads and validates its body before taking the server authority
lock, then rechecks current authentication and commits while holding that same
lock used by logout and access replacement. Revocation cannot be followed by a
late registration from a request authorized before its body was read. A
logout retires local installation writers before revoking that session's devices;
late responses cannot re-enable them. Token replacement/disable revokes all of
that browser host's devices, including after restart. Turning the notification
switch off removes only the current device registration. The browser may retain
its inert Push subscription for explicit re-enable without another permission
prompt. Reload reuses an existing subscription with the current VAPID key and never
unsubscribes it on startup. A subscription bound to an old/different VAPID key
requires explicit setup and is not adopted into the new service.

VAPID is generated once and stored privately in the browser service's
`control.sqlite`, separate from the retired `browser-push.sqlite` and legacy
configuration secrets. Keep the browser service's complete private root across
restarts/backups; replacing that root replaces its installation and VAPID identity.
The additive Control migration preserves existing registry, credentials, chats,
Tasks and receipts. It does not read, import or activate old Push subscription
files. Notification installation/click IndexedDB state has its own versioned
migration in `pkg/storage/migrations/ui-notifications/`; it contains no access
token, subscription secret or tool arguments.

Only supported browser push providers (Apple, FCM, Mozilla and Windows) are
accepted. Endpoint scheme/host, P-256 point and authentication key lengths are
validated. The HTTP transport rejects redirects, proxies and non-public DNS
addresses, including tailnet/private/link-local destinations, and pins resolved
addresses for the connection. Payloads contain bounded display content and an
exact structured destination, with no access token or subscription keys. A 404/410
clears and invalidates that device subscription; a late response cannot invalidate
a replacement. Re-registering the same invalid subscription requests local
cleanup and an explicit setup action, rather than silently looping.

Before HTTP, Push reads the subscription and its reserved device revision,
session binding, channel, permission and enabled state in one database snapshot.
A reservation retired by replacement or revocation expires without using a newer
subscription. No writer or authority lock spans HTTP; an already captured send's
uncertain result remains non-retryable and cannot invalidate a replacement.

The module Service Worker retains the immutable asset cache and excludes API,
authentication and native bridge traffic. Push promptly calls `showNotification`
after verifying the current installation ID, Control and enabled channel. Clicks
focus an existing same-origin Pot root window or open the fixed `/` root; payloads
cannot choose a URL. A pending exact Chat/Task target survives authentication and
is consumed only after the current main directory is ready. Logout clears it;
a missing or stale destination uses the existing localized navigation error.

Tailscale plus trusted HTTPS needs no public inbound Pot listener. The backend
needs outbound HTTPS to the browser's push service. The phone must be able to
connect back to Pot through its tailnet when opening a notification. Delivery is
subject to browser/OS scheduling, Focus and power policies and is not strictly
instantaneous. Pot does not play audio; sounds and banners follow system settings.

## Browser crash reporting

[`web/src/errors.js`](../web/src/errors.js) loads before the application module.
Uncaught JS errors and unhandled rejections produce a non-modal DOM toast;
handled request failures retain their page presentation. The toast offers manual
refresh and diagnostic copying, hides after eight seconds unless hovered,
focused or expanded, and merges repeats without resetting the timer.

Browser ResizeObserver loop notifications without an exception are excluded from
the crash toast and log: they indicate deferred resize delivery. Browser
diagnostics remain available; thrown exceptions and unhandled rejections still
report normally.

Up to 20 bounded records remain in memory and same-tab session storage. URL
queries/fragments and common credential patterns are redacted. Diagnostics do
not collect request bodies, fields or transcripts, and are never automatically
uploaded. Storage/clipboard failure does not interrupt reporting. A blocked JS
main thread cannot run exception handlers.

## Browser cold start

After a successful visit, the browser stores a minimal last-confirmed startup
locator: Control, boot, Space, assistant and reading surface. This locator only
selects local namespaces. It contains no credential, capability or assertion of
current authentication, permission or connection. Existing directory projections,
conversation windows, reading positions and [assistant drafts](assistant-drafts.md)
can render before either `/access/status` or `/api/v1/runtime` completes.

```text
installed shell -> startup locator -> committed directory/chat/view/draft -> UI
                           |                                      |
                           +-> access -> current Control ----------+
                                          |                       |
                                  fresh directory + chat session -> online actions
```

While verification is pending or the device is offline, cached conversations are
readable, cached assistants can be selected, and drafts can be edited and saved.
Sending and online management require current access and Control verification,
fresh advertised capabilities, and the existing operation/session checks. Cached
capabilities never grant authority. Notification permission invitations also wait
for verified online startup. Network recovery revalidates identity and
refreshes the directory and conversation without replacing the editor or its
reading anchor. Interrupted sends keep their existing immutable confirmation
fences; recovery never automatically submits or retries a message.

Each browser connection has a transient generation. Suspension revokes operation
authority and aborts the previous generation's reads. Access and runtime checks
restore transport reads only; an authoritative directory refresh in that
generation restores operation capability checks for its confirmed Spaces and
assistants. Failed scopes and retained linked assistants remain unavailable
without blocking confirmed local work. Retained names and cached
capabilities cannot satisfy this barrier. Chat additionally waits for its current
subscription's committed live snapshot; the adapter keeps that confirmation
per assistant and connection generation, including across asynchronous draft
preparation. Online navigation and existing management
forms remain unavailable while the directory barrier is pending, and the adapter
also rejects mutations. An already open assistant editor checks its current
capability again before saving and remains unavailable if that capability was
revoked. Late reads from a retired connection cannot publish a
directory or unlock a later connection. This state is never persisted.

A changed Control retires the old UI and writers before opening the new namespace.
Drafts remain under their original Control/Space/assistant/conversation. An access
token supplied in the URL does not open the previous cached identity. Explicit
logout clears the startup locator together with disposable business projections,
while preserving separately scoped drafts. First use, evicted data or unavailable
browser storage requires a network connection. Startup and storage failures expose
retry; authentication and permission failures retain their normal access flows.

The Go embedded asset tree is also the shell version boundary. Its generated
worker contains every embedded frontend dependency and frontend migration, each
with a SHA-256 digest. Installation downloads and verifies the complete tree into
a separate cache. Failure or interruption cannot activate that version. Existing
documents continue using their installed HTML, modules, CSS and fonts from the
same cache; a complete new worker waits until old controlled documents close.
There is no forced worker activation or live page replacement. Activation removes
obsolete UI asset caches. A missing or corrupt member can only be repaired from a
network response matching its installed digest; another release's file is refused.
API, access, native transport and foreign-origin requests never enter this cache.

Browser storage eviction can remove local availability. An origin needs one
successful online shell installation before offline reopening works. Offline
support covers cached chat windows and local drafts, not all history, attachments'
remote content or management pages. It adds neither background connections nor an
outbox. Native startup continues through the native runtime adapter.
