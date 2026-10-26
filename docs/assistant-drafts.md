# Assistant drafts

Chat retains one current unsent draft per assistant on the current browser
origin or desktop WebView. Its key contains the owning Control identity, Space
and assistant. The record also binds the draft to its conversation. Returning to
an assistant or reopening the same origin restores text, message references and
attachment cards. Drafts are local to that device; they are not synchronized
between devices or live editors in multiple tabs.

```text
Composer text / references ---- short debounce ---+
Attachment / send changes ---- immediate --------+--> draft metadata
                                                 |    (Control, Space, assistant)
Selected File ---------------- first save -------+--> separate Blob records
                                                         |
Reload --> conversation check --> editor + File previews + upload validation
```

Text and reference edits save after 300 ms. Switching assistants flushes the
previous draft, and attachment changes save promptly. Hidden-page events only
supplement these saves. Metadata and original Blobs commit in one IndexedDB
transaction; later text edits do not rewrite stored image bytes. The composer
shows whether it is loading, still unsaved, saved, or unable to save. Storage
errors retain the in-memory draft with an explicit Retry action and diagnostic
details. A failed load cannot replace typing made while that load or its retry
was pending. Closing a page before an unsaved write completes can still lose
those edits; clearing site storage or browser storage eviction also removes
local drafts.

Restored originals become `File` objects and local preview URLs. Waiting and
interrupted uploads resume only when the selected assistant has connection,
transfer permission and a compatible model. Previously ready files validate
through the same idempotent upload ID before Send becomes available; an expired
unreferenced upload can be recreated from its local original. Failed uploads
retain the original and expose an explicit retry with the same ID. Chooser
intents retain their existing cancellation behavior: navigating away before a
native chooser returns cancels that selection.

The current conversation owns references and any pending request. A committed
reset to a different conversation discards the prior conversation's text,
references, attachments and send fence. A late load or acknowledgement for the
retired conversation cannot populate the new one. Ordinary projection-cache
retention and cache clearing preserve drafts. Explicit attachment removal,
confirmed submission and assistant deletion remove only their corresponding
local originals; unrelated assistants and databases are untouched.

## Submission and interrupted confirmation

Before sending, Chat commits the immutable request ID, conversation/turn target,
submitted text/reference versions and attachment IDs together with the current
draft. A storage failure prevents that POST. This record is a confirmation fence,
not an outbox: reopening Chat never automatically submits a message.

Only confirmed business acceptance clears submitted content. New typing,
reference edits and files added during submission survive the older response.
Normal successful responses do not wait for Agent completion. Explicit rejection
retains the draft, and an unknown result retains the fixed request identity.
After reload, only bounded, read-only receipt checks for that original request
can resolve acceptance or rejection. Check submission repeats that read without
creating a new request ID or issuing a POST. An unresolved request continues to
block another submission in that conversation.

If acceptance cleanup cannot commit, the composer reports the unsaved state.
The older disk record still carries its original request fence, so another
reload reconciles that request rather than presenting its accepted text as a
new send. Retry persists the latest editor contents and removes only originals
that no longer belong to the current draft.
