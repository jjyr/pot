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
      local + remote Spaces               one serving Control
```

## Presentation and platform boundary

[`components/ui.js`](../web/src/components/ui.js) owns the shell, assistant
directory, Chat and input lifecycle. [`components/pages.mjs`](../web/src/components/pages.mjs)
owns Tasks, schedules, Machines, Usage, configuration forms and return contexts.
[`components/task-conversation.mjs`](../web/src/components/task-conversation.mjs)
owns the task status, conversation history, menu and fixed composer, reusing the
shared reply/tool/composer components. Pages retains task requests and owner scope.
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
The canonical Companion assets and deterministic avatar colors are shared.

All hosts use DOM textareas and inputs. The WebView/browser owns shaping, caret,
selection, undo and IME; the shell owns the composer frame, send/stop actions,
drafts and reference completion. Stream updates retain the live editor node and
do not rewrite its value. Only explicit restoration, matching send receipts and
completion replace text. Composition events, `isComposing` and key code 229 keep
candidate confirmation from choosing a completion or sending a message.

The shared Chat composer accepts image/PDF files through DOM selection, drop and
screenshot paste. It uses the same bounded byte upload API in browsers and the
macOS WebView; native attachment downloads use the host save dialog. See
[Chat attachments](chat-attachments.md) for formats, capability checks and retention.

Drafts, cached conversations, positions and late-response fences include the
Control/Space and assistant identity. A matching authoritative receipt clears
only its submitted draft. Selecting another assistant does not change ownership
of a request already in flight. Task Usage uses the Task ID for accounting while
requests retain the owning assistant; its Back/history path preserves the
independently selected Chat. Task approvals follow the Task owner and exact Task,
including when a different assistant remains selected in Chat. See [Multi-Space UI](multi-space-ui.md).

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
the original Space, assistant, Task/run and exact request identity.

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
and Space before every request. Desktop calls Go through Wails and receives
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

## Read state and activity

Completed replies have a durable reply ID/count. Initial catalogue history becomes
the baseline rather than unread mail. A new live reply can rebound its avatar
once; reconnect catch-up and duplicate updates do not replay it. Native read
baselines are held by the Go attention service and shared between windows through
`pot:read`. Go accepts a read only for the observed latest reply in a visible,
focused Chat. Browsers keep baselines in local storage and synchronize same-origin
tabs. Keys include the Control identity so a rebuilt database cannot inherit an
unrelated baseline. Reduced Motion disables reactions and native tray animation.

## Browser crash reporting

[`web/src/errors.js`](../web/src/errors.js) loads before the application module.
Uncaught JS errors and unhandled rejections produce a non-modal DOM toast;
handled request failures retain their page presentation. The toast offers manual
refresh and diagnostic copying, hides after eight seconds unless hovered,
focused or expanded, and merges repeats without resetting the timer.

Up to 20 bounded records remain in memory and same-tab session storage. URL
queries/fragments and common credential patterns are redacted. Diagnostics do
not collect request bodies, fields or transcripts, and are never automatically
uploaded. Storage/clipboard failure does not interrupt reporting. A blocked JS
main thread cannot run exception handlers.
