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
`references: [{message_id, quote?}]`. Omission of `quote` references the complete
message by ID. The normal selection UI supplies the selected rendered text;
Markdown offsets are not stored. No complete source-message copy is added to the
reply. There are at most eight references, each quote at most 4096 UTF-8 bytes,
with at most 16 KiB of quote text in one input. Control verifies that every source
is a user or assistant message in the same owning conversation. `reply_to`
continues to identify the input responsible for an assistant output.

```text
DOM selection -> owner-scoped reference draft -> accepted user message
                                                    |
                                       original message IDs + quotes
                                                    v
                                    durable Agent history / checkpoint
                                                    |
                                    provider text metadata annotations
                                                    v
                                  model -> read_chat_messages -> original
```

Agent history preserves the transcript IDs of user inputs and assistant outputs,
including the initial welcome. Members of a user batch keep their individual
identities, text and references; compaction retains the latest batch together.
All four provider encoders emit text-based `pot_message_metadata` annotations
containing the message's own ID and any references. Canonical text remains
unchanged. Native provider IDs and signed reasoning replay remain separate from
Pot identities. Selected quotes and fetched originals are reference data, not new
instructions or authority.

Main exposes `read_chat_messages({message_ids})` for one to eight original
messages. Its binding fixes the assistant and current execution generation;
callers cannot select another owner or conversation. Workers, peer correspondence
and automatic summaries cannot use this reader. It reads the durable transcript,
so the model can resolve an ID-only reference after context compaction instead of
automatically expanding every reference into another copy of the source.

Responses annotations are attached once to an existing native message with text,
retaining its phase, rather than inserted as a separate metadata-only assistant
turn. Empty assistant messages and tool/reasoning-only native outputs receive no
annotation; their native fields and signed content remain intact. Encoding does
not mutate canonical text or replay bytes.

These annotations remain model-visible text. Returned tags are preserved as
ordinary protocol content in deltas, saved replies and native replay, regardless
of their message ID or phase. This input correction cannot guarantee that a
model or gateway will stop returning metadata. Existing stored conversations and
checkpoints are not rewritten.
