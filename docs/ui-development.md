# UI development rules

These rules apply to the shared browser and desktop UI. Preserve the behavior
described here when changing components, asynchronous data flows or navigation.
Use the [internationalization rules](internationalization.md) for all changed
user-visible text, including accessible labels.

## Local-first display

Read committed IndexedDB projections before waiting for transport when opening a
page or restoring a conversation. Publish available local content immediately;
refresh it in the background and publish network results only after their local
transaction commits. Memory may retain committed projections for rendering, but
must not become a separate source of truth or a fallback for failed storage.

Apply this rule to the complete page read chain, not just the storage helper.
Cached assistants must not wait for machine labels, Agent Links or another Space
before they can appear. Publish independent sections progressively. Keep existing
content when enrichment is slow or fails; refresh keyed nodes without disturbing
focus, scroll position or prepared geometry. A cache miss may wait for transport,
but cannot block unrelated cached sections.

Separate stable display identity from live connection authority. A service restart
must not make otherwise valid display projections unreachable merely because its
boot ID changed. Scope retained data to its Control, Space, assistant and query;
preserve expiry, revocation and logout boundaries. Connection generations and
invalidation epochs must still fence late responses and stale writers.

Cached display never grants permission to execute an operation. Confirm current
identity, capabilities and configuration revisions before saving or running work.
Settings may show retained values and accept local edits while confirmation is
pending; fresh responses must not overwrite dirty fields. A cached extension
catalogue may contain safe display metadata, never credentials, authorization
URLs/codes or an offline authorization grant. Use shared repositories rather than
adding per-page caches. Follow [Shared UI](shared-ui.md#committed-local-projections)
for storage ownership and [Database migrations](database-migrations.md) for
persisted-format changes.

```text
Open page -> IndexedDB projection -> initial display
                     ^
Background refresh -> commit -> incremental display update

User operation -> current identity / permission / revision -> server
```

Verify warm-cache opening with transport deliberately held, service boot changes,
partial cache misses and failed refreshes using real IndexedDB. Assert that cached
content appears before transport completes and that stale data cannot enable
commands. Exercise a refresh while a field is dirty or focused. Writing a network
response to IndexedDB before rendering it alone does not establish local-first
display.

## Layout stability

Treat layout stability as correctness. Conversation components must prepare at
their actual available width, settle the resources needed for layout, and measure
before publishing their visible geometry. Commit the prepared presentation and
its measurements together. Cancelled preparations must not change live records,
event-handler targets or interaction state.

Define which element owns scrolling. A preview with its own content viewport must
not acquire a second outer scrollbar through its preparation wrapper. Fixed
viewports and bounded timeout placeholders must keep content accessible; arbitrary
clipping or guessed heights are not substitutes for correct measurement. Inline
links must preserve text baseline alignment and paragraph geometry.

Dynamic embedded documents retain their published viewport. Real width changes
and explicit expansion must prepare replacement geometry before committing it.
Background activity or metadata changes should invalidate only the presentations
that depend on them, rather than remeasure unrelated messages and embeds.

See [UI events](ui-events.md) for preparation and commit ownership, and
[Display resources](display-resources.md) for preview and viewport behavior.

## Stable interaction nodes

Preserve the DOM identity of active input fields, action buttons, keyed task rows
and interactive documents across routine updates. Sending, stopping and resuming
may change a button's mode without replacing the button or its icon nodes.

Background refreshes must not interrupt focus, selection, IME composition or
embedded application state. Selecting the current conversation must not restart
its editor lifecycle. Retained handlers must use the latest committed records;
an abandoned render candidate cannot redirect a live action.

## Quiet loading and feedback

Do not insert transient loading prose into pages, lists, file links or previews.
Keep existing content during refresh, use `aria-busy` where appropriate, and keep
control labels stable. Initial surfaces may remain empty or use the bounded
placeholder required by their layout contract. Do not add a separate line that
disappears when loading finishes.

Keep actionable failures and recovery controls visible. Explicit operation
progress, such as an upgrade download, is distinct from ordinary page loading.
Saving an attachment must not add a success notice to the conversation or preview.
Detailed delivery state belongs in the dedicated outbox interface, following
[Chat outbox](chat-outbox.md).

## Shared metadata and user intent

Use shared cached projections instead of building independent per-page catalogs
or starting upstream discovery merely to display a selector. Resolve model
directory identity through the selected connection and its registered type.
Assistant model fields must support both cached suggestions and free entry of an
opaque model ID. Refreshing suggestions must not replace the field, overwrite a
draft or discard the current selection. Follow [Model connections and metadata](model-directory.md)
for catalog precedence and filtering.

For chat submission and delivery changes, follow [Chat outbox](chat-outbox.md) for
durable acceptance, immutable request identity and receiver-owned cross-turn
queueing. A transient UI snapshot is not evidence that a submitted message was
accepted or permanently rejected.

## Behavioral validation

Validate the behavior affected by a change in a real browser when it depends on
layout, native controls or asynchronous rendering. Inspect measured and visible
geometry, scroll position, node identity, focus and composition as applicable;
screenshots alone cannot establish those properties.

Exercise relevant slow, failed, timed-out and superseded responses, including
refreshes while the user is interacting. Cover supported locales and narrow and
wide layouts when they can change the result. Use isolated fixtures for these
scenarios, without issuing real inference or modifying user configuration.

Compare unexpected failures with the unchanged baseline before attributing them
to the patch. Report remaining baseline failures and the runtime actually tested;
browser verification does not by itself establish native WebView or deployed
service acceptance. Keep test results in the task or PR, not in repository docs.
