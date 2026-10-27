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
transaction; later text edits do not rewrite stored image bytes. Routine loading
and saving do not show a text status below the composer. Storage errors appear
on demand and retain the in-memory draft with an explicit Retry action and
diagnostic details. A failed load cannot replace typing made while that load or
its retry was pending. Closing a page before an unsaved write completes can still lose
those edits; clearing site storage or browser storage eviction also removes
local drafts.

Files pasted or dropped while a load or its Retry is pending merge with saved
attachments without replacing accepted local Files or their upload state. If
the combined draft exceeds eight attachments or 8 MiB, all originals remain
visible and saved. Chat explains the limit and blocks Send until the user
removes enough files; recovery never chooses which originals to discard.

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
After reload, bounded, read-only receipt checks for that original request
can resolve acceptance or rejection. An unresolved request continues to block
another submission in that conversation.

An interrupted save may have committed the request fence before any POST began.
For a restored unresolved request, the explicit Check and retry action first
checks the original receipt. Only an unconfirmed result resubmits the identical
saved payload with its original request ID and scope. An accepted or rejected
receipt resolves without another POST;
a failed receipt check keeps the fence. Automatic recovery, including reload,
focus and network recovery, remains read-only. New editor contents are never
substituted into that retry.

If acceptance cleanup cannot commit, the composer reports the unsaved state.
The older disk record still carries its original request fence, so another
reload reconciles that request rather than presenting its accepted text as a
new send. Retry persists the latest editor contents and removes only originals
that no longer belong to the current draft.
