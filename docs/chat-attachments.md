# Chat attachments

Chat accepts PNG, JPEG, WebP and PDF originals, independently of message text.
The composer shows image previews and document cards above its textarea, a round
plus control at the left and Send at the right. Chat has no model selector or
current-model label. Sent images remain inline; documents show their names and
sizes. Both support preview and download. The macOS host uses its native save
dialog for downloads; browser hosts download a Blob reconstructed from the same
owner-bound read API.

## Capabilities and native model input

Control derives independent `image` and `pdf` booleans from the final execution
Provider/model descriptor and implemented protocol encodings. The existing
models.dev directory supplies image/PDF modality defaults, and model-catalog
`input_modalities` overrides take precedence. Unknown facts default to false.
The four implemented adapters support native input:

| Protocol | Images | PDF |
|---|---|---|
| Chat Completions | `image_url` data URL | `file` with filename and PDF data URL |
| Responses | `input_image` | `input_file` with filename and PDF data URL |
| Anthropic Messages | image base64 source | document base64 source |
| Gemini | image `inlineData` | PDF `inlineData` |

Parts remain on their original user message, including attachment-only inputs.
A true flag permits an attempt; the configured endpoint decides actual model
access, format limits and acceptance. Provider errors use normal reply errors.
No document text extraction, OCR substitution or format conversion is performed.
Other document/image formats are disabled. A PDF flag does not require an image
flag, and an image flag does not enable PDF.

Picker options restrict known unsupported types; drop and paste explain rejected
types. The plus control always stays visible. It is disabled when neither type is usable, with a hover and
keyboard-focus explanation of the model limitation. If PDF alone is supported,
the plus control remains enabled and the image menu item explains its disabled
state. Capability changes retain incompatible
draft attachments with an explanation and disable Send until they are removed.
Control repeats validation at upload, acceptance and model request construction.
Active-turn guidance uses that turn's fixed execution configuration; pending
configuration applies to the next fresh input. Historical files remain available
for preview/download after a model change. When an earlier attachment cannot be
read by a later model, both its UI card and the model context explicitly say its
contents are unavailable; new incompatible input is rejected.

## Ownership and data flow

```text
Browser / macOS DOM File
         |  bounded byte chunks, captured Space + assistant
         v
Agent Link operation port (chat.attachments, owner scope)
         |
         v
Owning Control attachment store <----- preview / download chunks
         | immutable metadata + stable ID
         v
Conversation, user inbox, steering, checkpoint
         | resolve originals only for the model request
         v
Native provider image / PDF parts
```

`POST /api/v1/attachments` accepts `begin`, `chunk`, `finish` and `remove`
operations. A client-generated upload ID makes begin and identical chunk retries
idempotent. Chunks are at most 192 KiB before base64 encoding, below the ordinary
16 MiB transport limit. Finish validates the declared size and actual image/PDF
signature and image dimensions. Only finished, canonical metadata can enter a
message. `GET /api/v1/attachments/{id}?offset=...` reads bounded, immutable
chunks. Both APIs require the existing authenticated channel and assistant
ownership through the `chat.attachments` operation. The composer exposes uploads
only when this capability is advertised; model image/PDF flags further restrict
accepted formats. Peer adapters do not currently advertise attachment transfer.
The Web publication explicitly allowlists these routes. Native remote Spaces
route them through the selected Control, with no failure fallback to local
storage. Client absolute paths are never accepted.

Originals live in the Control's private `attachments/` directory. User messages,
queued inputs, guidance and checkpoints hold references, not original base64.
The main model loop resolves bytes at its owning Control, including after restart
and during compaction. Uploads are not automatically copied into execution
workspaces or granted to tool processes.

## Limits, cleanup and retries

A message may carry eight files totaling at most 8 MiB. Images are limited to
8192 pixels per dimension and 32 megapixels; originals are preserved without
lossy compression. Control bounds attachment storage to 1 GiB and 4096 records.
Unreferenced uploads expire after seven days, swept at startup and before new
uploads. Cleanup consults durable conversation projections and retains all
referenced originals. Acceptance briefly reserves validated originals against
concurrent deletion; historical files cannot be removed through the draft API.
Model input is separately bounded to 64 MiB of attachment originals in retained
context and a 96 MiB encoded request. PDF context estimation charges decoded
bytes with an image floor; actual provider token usage remains authoritative.

Drafts and in-progress File objects follow the existing in-memory composer
lifetime, scoped by Control/Space, assistant and conversation. Navigation keeps
them; restarting the client restores sent history, not unsent editor contents.
Local file selection and removal do not depend on agent activity, a pending send
receipt or the stream connection. The chooser captures its Space, owner and
conversation before opening. Changing conversations or leaving chat cancels that
selection with an explanation; late chooser results are ignored. Closing the
chooser also retires its intent. Selecting files creates visible in-memory cards
before upload eligibility is checked. Missing connection or upload permission
retains the files with a waiting explanation. Restoring eligibility starts one
upload attempt per waiting file; unsupported model types remain visible and block
Send until removed or supported again. Upload failures expose an explicit retry;
rendering progress does not erase selection-limit warnings.

Uploads expose progress, cancellation/removal and retry with the same ID and
confirmed offset. Removing a pending send's submitted original only changes the
local draft; Control retains that immutable send snapshot. Its later confirmation
clears only submitted IDs, preserving files selected afterwards. Browser File
objects cannot survive closing or reloading the whole page; no unsent Blob data
is persisted. A lost send receipt reconciles the same request identity.
A confirmed provider rejection restores the latest attachment draft when it does
not overwrite newer typing. Retrying unchanged input uses
`POST /api/v1/turn/retry` to resume the original failed message and checkpoint;
it creates neither another upload nor another user message. The retry receipt is
idempotent. Unknown network/model outcomes retain the existing recovery fence
and are never automatically replayed.
