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
through the same idempotent upload ID before network delivery; an expired
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

Explicit Send uses the [submitted message outbox](chat-outbox.md). Its transaction
transfers the submitted content and local originals before clearing matching
editor versions. Reopening automatically delivers these explicit submissions;
ordinary drafts remain unsent.

Drafts saved by an earlier build may contain an immutable pending confirmation
fence. Upgrade preserves these receipts independently of the outbox. They retain
their read-only automatic checks and explicit Check and retry action for the
original payload and request ID. An unresolved legacy fence blocks another
submission in its conversation until its result is reconciled. Migration never
converts ordinary draft text into an automatic send.
