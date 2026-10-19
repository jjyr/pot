# Chat message references

Selecting user or assistant prose in the current Chat adds a reference card to
the composer without changing its text or moving focus. Separate selections add
separate cards; duplicate message/quote pairs are ignored. A selection spanning
messages creates one reference per message. Tool details, attachment controls and
existing reference cards are excluded. Each card shows its source and up to two
lines of the selected text, with the full excerpt in its tooltip. Remove affects
only that reference. Clicking the card scrolls to the original message.

References are in-memory drafts scoped by Control, assistant and conversation,
alongside the text and attachment drafts. Navigation retains them; client restart
does not restore unsent drafts. Sent references persist above the user's message
text and retain their original message IDs. A lost receipt, explicit retry, queued
user batch or active-turn guidance retains the same accepted references. Later
draft changes survive late acknowledgements. A reference alone does not send a
message; the user supplies text or an attachment.

## Storage and model input

`SendMessage`, `SteerRequest` and the resulting user `Message` carry
`references: [{message_id, quote?}]`. Omission of `quote` references the whole
message. IDs are internal associations used for validation and UI navigation;
they are not sent to the model. The normal selection UI supplies selected
rendered text; Markdown offsets are not stored. There are at most eight
references, each quote at most 4096 UTF-8 bytes,
with at most 16 KiB of quote text in one input. Control verifies that every source
is a user or assistant message in the same owning conversation. `reply_to`
continues to identify the input responsible for an assistant output.

```text
DOM selection -> accepted user message -> internal source IDs + quotes
                                                   |
                                      owning transcript lookup
                                                   v
                                  source role + quote + source context
                                                   |
                                   current user input's quoted data
                                                   v
                                                 model
```

The owning conversation resolves references from its transcript before token
estimation and model requests, including compaction requests. Each resolved entry
contains `role`, an optional selected `quote`, `context` and a `truncated` flag
when applicable. Context is at most 8192 UTF-8 bytes per entry. Long sources use
a window around a literal selection; rendered Markdown selections without a
literal source match retain their quote alongside the source's opening context.
Whole-message references use the opening context when the source exceeds the
limit. Truncation preserves UTF-8 boundaries and is explicit to the model.

All four provider encoders prepend a `quoted_messages` data block only to the
user input carrying those references. The block contains quoted content and
source roles, with no Pot message IDs or lookup instructions. JSON escaping keeps
quoted delimiters inside the data. Quotes and source context are reference data,
never additional instructions or authority. No model-side message reader is
exposed. Inputs without references receive no extra text, and historical
assistant messages and native replay receive no annotations.

Agent history retains the internal IDs and references of individual user inputs;
compaction retains the latest accepted batch together. Resolved source text is
request-local and is not added to the canonical message or checkpoint. The
transcript can resolve references after restart or after the original leaves
retained model history. Native provider IDs and signed replay remain independent.
Returned model text is streamed and saved verbatim, with no metadata filtering.
Existing stored conversations and checkpoints are not rewritten.
