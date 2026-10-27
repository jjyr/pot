# Submitted message outbox

Clicking Send commits a message to this browser origin or desktop WebView before
clearing the submitted input. Assistant activity and connectivity do not disable
local submission. A known Control, Space, assistant and conversation, valid
content and working local storage are required. First use without a confirmed or
saved conversation cannot invent a destination. Settings and ordinary unsent
drafts never enter this queue.

```text
Explicit Send --> one IndexedDB transaction --> submitted message + originals
                         |                              |
                  submitted draft versions             v
                         |                      target FIFO head claim
                         v                              |
                 clear matching input                  v
                         |                   fresh identity/session/permission
                         v                              |
              prepared chat message row                v
                                            upload originals / receipt lookup
                                                       |
                                                       v
                                            same request ID --> acceptance
```

Text, references, attachment metadata and local originals commit together. Only
the submitted text/reference versions and attachment IDs leave the composer;
new typing and files added during the save remain. A failed transaction retains
the input. Queued rows use the same hidden preparation, readiness, measurement
and node transfer as other conversation content.

Reconnection and reopening the same origin resume submitted messages
automatically. Each captured destination has a FIFO lane, and different
assistants progress independently. Closed browsers do not execute background
work; persisted messages continue when the application opens again. Browser
storage eviction or clearing site data can remove local messages.

Waiting, sending, receipt reconciliation, acceptance and blocked delivery have
separate labels. The Saved messages action keeps local submissions inspectable
even after the assistant or conversation is removed. Its content uses the same
prepared message window and shows each original destination. Edit and Cancel apply only before dispatch claims the entry.
Their revision checks and the dispatch claim share IndexedDB write transactions;
whichever commits first determines the outcome. Once dispatch starts, the UI
cannot claim that cancelling a local record withdraws a message already received
by Control. A failed or blocked message retains its text and originals and
blocks later messages in that lane.

## Delivery authority and retry identity

The immutable destination includes the browser's host, owning Control, Space,
assistant and conversation. Switching assistants does not change it. Dispatch
requires the current confirmed directory and a fresh authoritative conversation
snapshot. It retains the existing authentication, operation-scope, capability
and session checks. Removed assistants, changed Controls, retired conversations,
revoked permission and incompatible attachments retain the submission with a
specific problem; they do not redirect it or repeatedly POST on authorization
failure. Retry is explicit after such a failure and keeps the same saved request
identity. Unstarted failed submissions can also be edited or cancelled.
Chat shows local submissions only in their captured conversation, including
accepted entries waiting for a transcript update. Entries from a retired
conversation remain available through Saved messages.

An authoritative running snapshot selects the existing steer operation. A turn
already captured while submitting guidance stays fixed. Once the first dispatch
binds a turn, reconnecting cannot steer a successor. The receiver owns whether
that input steers an active Main turn or joins its existing pending-user lane;
the browser does not launch another execution or use continuation to submit text.
Requests carry an `expected_control_id` precondition checked by the serving
application before dispatch. A replacement Control rejects the old target even
before its updated directory reaches the browser. This precondition does not
bind a boot, so the same Control can reconcile durable receipts after restart.

A durable request ID is allocated once. Before the first POST, the selected
operation and turn are saved. Expired claims and unknown HTTP results query that
same receipt before retrying the same payload and ID. An absent receipt does not
prove rejection: the first copy may still arrive. Receiver-side atomic durable
identity/content checks make an identical retry safe; the client does not claim
transport-level exactly-once execution.

Native conversation acceptance is retained in the conversation SQLite database:
`messages.request_id` for ordinary inputs, `assistant_steering` and its stable
message identity for guidance, and the existing retry metadata for retrying a
failed input. These receipts survive process boot changes and have no automatic
expiry. They are scoped to their owning conversation database. Agent Link direct
chat uses its existing Control-owned durable envelope and user-projection
receipts. Transport sessions and their request IDs are not substitutes for those
business receipts. Deleting or replacing a conversation does not authorize
retargeting an old browser request to a new conversation.

## Local schema and recovery

`ui-cache/0007_chat_outbox.mjs` adds submission records and separate original-Blob
records in the existing shared migration ledger. It preserves projection cache,
startup locator, ordinary drafts and existing pending confirmation fences. A
pre-existing draft confirmation fence keeps its explicit Check and retry
behavior; migration never treats ordinary draft content as a submitted message.
Business cache retention and clearing exclude both outbox stores. Failed upgrades
roll back; older schema clients cannot downgrade or erase the newer database.

Dispatchers use transactional, expiring claims and revision checks across tabs
and windows. After a claimant disappears, another client reconciles the saved
request before continuing. A late claimant cannot overwrite the successor's
local receipt. Duplicate network attempts caused by lease expiry still share the
receiver's durable request identity. Original attachment uploads use the existing
scoped stable upload IDs and chunk protocol, including when the original had not
yet uploaded before Send.

Accepted and cancelled submission metadata remains available in Saved messages.
Cancelling an unstarted submission releases its originals in the cancellation
transaction. Accepted originals remain until both durable business acceptance and
the local acceptance projection commit. A projection failure retains them and
exposes a local Retry; reopening can reconstruct that same accepted projection
without another POST. Local preview URLs have resource leases and are revoked
when their last mounted view releases them. Once local originals are released,
previews use the existing owner-bound Control attachment read.
