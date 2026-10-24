# Display resources

Resource presentation and download are separate operations. Main Chat and Task
conversations share the image/GIF, audio, video, PDF, text, HTML and file-card
renderers. Uploaded image/PDF attachments use the same renderer through their
existing owner-bound byte adapter; model input capabilities remain independent.
Raster Markdown references retain their existing image renderer. Raw Markdown
HTML remains literal and remote images remain explicit links.

Text resources whose immutable `entry` ends in `.md` or `.markdown` (case
insensitive) default to the shared safe Markdown reader when their MIME type is
`text/plain` or `text/html`. The latter handles HTML sniffed from Markdown that
begins with raw HTML; it never grants Markdown an interactive HTML frame.
Explicit `text/markdown` and `text/x-markdown` MIME types also select Markdown
(parameters are ignored). Other inert `text/*` types, including JavaScript and CSS source, use the plain
text reader. Binary types take precedence over an entry suffix. Display labels and derived download
names never choose a viewer. Existing registered Markdown with `text/plain`
works without registration again or changes to stored manifests. Registration
continues to record its detected MIME type; no persisted format changes.

The reader offers **Show source / Show Markdown**. Switching preserves the loaded
source and only changes presentation; selecting and copying source retains its
text. A newly mounted/reopened view defaults to Markdown. Expansion, download,
locale changes and load errors retain their existing behavior. Other plain text,
including `.txt` and logs, stays plain text with no Markdown guessing or switch.
The existing 16 MiB chunk-preview limit and UTF-8 Blob text decoding apply to
both text views; originals up to the 64 MiB registration limit remain downloadable.
Neither view changes the original bytes or the download filename.

Markdown uses the DOM renderer, never the interactive HTML document or bridge.
Raw HTML is literal, unsafe link protocols are rejected, and remote images are
explicit links without automatic requests. Markdown gets no workspace-file
adapter. Undeclared relative images and links remain literal text and do not
fetch local or remote files. The existing bundle contract only permits HTML
bundles; Markdown does not add an asset-bundle contract or bypass path limits.

## Message file references

Assistants deliver files using explicit Markdown links or images, for example
`[Report](artifacts/report.md)`, `[Source](/project/a.go:120)` or
`![Result](<artifacts/image (1).png>)`. Labels are titles. HTTP/HTTPS (including
localhost) remain ordinary links. Bare paths, inline code, fenced code and
indented code do not enqueue reads. The inline-node parser supports Unicode,
escaped punctuation, spaces, balanced destination parentheses and angle-wrapped
paths; reference-style definitions are not a file-delivery interface.

Execution preparation pins the actual node, assistant, Task/run and directory
identity before model or tool execution. A completed message binds its references
to that captured source. A durable projection queue in `control.sqlite`
resolves files asynchronously into the existing immutable resource Store. Neither
streaming tokens nor history loads initiate source reads. Reference identity binds
message ID, text revision, source span and href; repeated paths in one revision
share an idempotent resource receipt. The projection does not modify message text,
model correspondence or model history, and original file bytes never become model
input. Old messages without captured provenance remain unavailable; the UI cannot
supply replacement paths or manufacture a source binding.

```text
execution completion -> explicit Markdown nodes -> provenance + pending references
                                                         |
                                              background confined reads
                                                         |
                                                immutable resource Store
                                                         |
message/ref ID -> owner/Space checked status -> existing ResourceView + Download
```

Relative paths resolve under the captured execution directory. Absolute paths must
map into that same root. The only extra mapping is the originating main assistant's
exact configuration `AGENTS.md`; sibling configuration files are excluded and
symlinks are refused for this mapping. Task reads retain the existing verified
workspace handle and directory identity checks. A main message can reference an
owned Task with `pot-file://TaskID/relative`; its original Task/run/node binding is
captured at completion. Workers can reference only their own Task. File reads use
regular-file checks, bounded reads and before/after revisions; HTML dependency
sets are rechecked before registration. Replaced directory identities fail closed.

Remote original-execution reads are currently unavailable in Control. A bound
remote source reports `source_offline`; it is never substituted with a local path.
Linked transcripts without a captured source binding report unavailable. This
change does not add a remote file-transfer operation or infer a workspace from the
currently selected UI assistant.

Reference states are `pending`, `resolving`, `ready`, `not_found`, `forbidden`,
`source_offline`, `too_large`, `changed`, `unsupported_dependency` and `unavailable`.
Failures remain local to each reference and can be retried; retries recheck source
and permissions. Ready references continue to show the saved version after source
mutation or deletion. There is no implicit refresh to the current file version.

Inline references open a file viewer, with optional source-line positioning.
HTML references with a line number use inert source text rather than an interactive
frame.
A sole reference in a paragraph uses the existing image, bounded Markdown/text,
PDF, audio/video or sandboxed HTML preview. Unsupported types show a download
card. Media never autoplay and no file is automatically saved to the device.
Repeated ready resources do not stack full previews in the same message. Component
updates preserve message identity and retain mounted media and resource leases.

New model tool catalogs and dispatchers expose neither `register_resource` nor
`present_resource`. Internal Store registration remains available to host services.
Historical completed presentation results and typed presentation messages continue
to render without rewriting their resources. The built-in main prompt directs
file delivery through Markdown links using the same source and preview rules.

Resource labels never choose a viewer or executable capability. Download names
are derived separately with `core.DownloadFilename`; separators, control and
nonportable filename characters are sanitized, names fit 255 UTF-8 bytes, and
single-file downloads retain the entry basename. HTML bundles download as ZIPs
containing the original files. Rendering transformations never replace originals.

Control stores resource manifests and registration receipts in `control.sqlite`
and originals in private `resources/` directories. Resource reads require the
owning assistant and authenticated channel; IDs are references, not credentials.
Retiring an assistant makes its resources and references inaccessible. Resources
survive Task workspace cleanup. Storage remains bounded to 1 GiB/4096 resources,
64 MiB per file/resource and 16 MiB/64 files per HTML bundle. Raster limits remain
16 MiB, 8192 pixels per dimension and 32 megapixels. Preview limits can be stricter.

`GET /api/v1/message-files/{message}?task_id=` returns only stored references for
the authenticated assistant and optional owned Task conversation.
`POST /api/v1/message-files/{message}/{ref}?task_id=` retries that saved reference.
Neither API accepts a source path, source node or workspace from the frontend.

## Reading and platform adapters

`GET /api/v1/resources/{id}` reads the manifest. `/chunk?path=&offset=` provides
bounded 192 KiB chunks with file digest revisions. `/content?path=` serves inert
original bytes and supports HTTP Range; `/download` saves the original or ZIP.
`/render?token=` serves an isolated HTML document. All reads retain assistant and
Space ownership. The authenticated browser proxy forwards range requests and
response metadata. Native media use a same-origin asset handler backed by the
captured Space's chunk API; native downloads use the existing save dialog.

Audio/video load metadata and play only on user interaction. The browser/WebView
determines codec support. GIFs have pause/play controls and begin paused when
reduced motion is requested. Component nodes survive unchanged message updates;
disconnecting a message stops media, disposes frames and releases reads/Blob URLs.
Playback position and HTML state are transient and reset when a virtualized view
is destroyed or the page is reloaded.

## Interactive HTML boundary

HTML is a separate response document with both an iframe `allow-scripts` sandbox
and a response CSP sandbox, without `allow-same-origin`. It is never inserted
into the application's DOM. The host permits only same-origin/blob frame
navigation. The child policy denies network subresources, connections, nested
frames, objects, forms and base URLs. Camera, microphone, location and clipboard
permissions are disabled. Local statically discovered script/style/image assets are embedded
as data URLs; CSS `url(...)` references are resolved within the manifest. External dependencies, CSS imports, srcset, nested documents and JavaScript
modules and statically recognized dynamic dependencies report a dependency failure.
Runtime attempts to load additional dependencies remain blocked by CSP and show a
local dependency notice in the component. Arbitrary script expressions are not
executed or guessed by the dependency collector.
Missing static assets report a file failure. Static dependency traversal is bounded
to eight levels; cycles fail explicitly. No dependency resolution starts commands,
fetches a network URL or guesses a local service. HTML output and expansion remain
bounded.

The only component bridge methods are `window.pot.resize(height)` (clamped to
180–960 px) and `window.pot.result(value)` (at most 16 KiB of serialized JSON).
Results are displayed locally below the component. They do not send a message,
invoke a model or execute a tool. The frame also reports CSP dependency violations as a local notice. The parent
verifies the frame's window, a fresh per-mount token and the allowed message
action. Reloading a component cannot
replay a privileged action because no privileged action exists on this bridge.
