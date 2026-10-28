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

## Image previews

Saved resource images and uploaded chat image attachments start in the expanded
inline presentation. Clicking the image or its Enlarge image action opens the
shared native modal preview. Raster Markdown file references use the same modal.
The preview uses the caller's authorized original URL and existing lease, with
the same original-file download operation; it does not fetch a thumbnail or add
another resource API.

Each opening fits the available width and starts at the top of a long image.
The size selector also offers Fit window and Original size. Zoom buttons, `+` /
`-` keys and Ctrl + wheel zoom around the pointer (the viewport center for
buttons/keys). An ordinary wheel scrolls the image; mouse/pen dragging pans it.
Touch uses native scrolling; no custom two-finger gesture is installed. Scroll
chaining to the underlying chat is contained. The native modal traps focus;
Close preview and Escape return focus to the opener without scrolling it.
Closing removes the preview image, observer and event handlers. Removing the
owning component also closes its preview before releasing the original lease.

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

The conversation UI can show a pending reference and query its binding metadata
while a reply is streaming. This never initiates a source read or authorizes an
unbound path. Pending references retained while prose is appended follow the
committed text revision and reject older metadata completions, without renewing
their preparation deadline or replacing an existing saved preview.
A ready binding can prepare its saved preview before the reply
ends; missing bindings retain the bounded preparation frame while metadata is
pending. Completing the reply retires stale metadata-read completions and applies
the terminal binding state inside that same frame.

```text
execution completion -> explicit Markdown nodes -> provenance + pending references
                                                         |
                                              background guarded reads
                                                         |
                                                immutable resource Store
                                                         |
message/ref ID -> owner/Space checked status -> existing ResourceView + Download
```

Main assistants and local Tasks can deliver ordinary OS-readable files by
absolute path, including source,
documentation and reports outside their execution workspace or in another
assistant's directory. No copy, per-file registration or prior tool read is
required. Relative paths resolve against the captured execution directory. That
directory remains a provenance and resolution anchor, not an absolute-path scope.
Sharing uses the host's file reader independently of Trusted/Protected command
policy; command sandbox, grants, Runtime prohibitions and owner refusals retain
their existing meaning. File links grant no command execution authority.

Node, owner, original Task/run and workspace directory identities remain bound
and verified. A main message can reference an owned Task with
`pot-file://TaskID/relative`; its original Task/run/node binding is captured at
completion. Workers can reference only their own Task. These identity-bearing
references remain confined to the verified Task root, including symlink targets;
ordinary absolute paths do not claim Task identity or resource API ownership.
Remote sources are never substituted with same-named local files.

File sharing excludes only these exact product authentication file identities:

- This Runtime root's `runtime.json` (local API bearer token).
- This Runtime root's `machines.sqlite`, `machines.sqlite-wal`,
  `machines.sqlite-shm` and `machines.sqlite-journal` (node signing private key).
- The active `secrets.FileKey` path, or for `SystemKey` the configured
  `POT_SECRET_KEY_FILE`; without that override, the non-macOS default
  `~/.pot/secrets/master.key`. macOS SystemKey uses Keychain, not a file.

Checks use canonical targets and opened file identity, so symlink and hard-link
aliases do not bypass these exceptions. Classification uses metadata, never
credential bytes. There are no directory, hidden-file, extension or general
configuration bans. This is not universal secret detection: arbitrary credentials,
copies, other Runtime roots and opaque embedding `KeySource` locations are not
discovered or content-scanned. Hosts using an opaque key source must manage its
file exposure separately.

Reads require ordinary files, retain the 64 MiB limit and compare descriptor
revisions before and after reading. Path targets and HTML dependency sets are
rechecked before registration. Replaced execution directory identities fail
closed. HTML assets remain under the execution root for entries inside it; for
external entries, assets are limited to the canonical entry's directory. A
dependency symlink cannot escape that base or include an excluded credential.

All captured sources use the same file-sharing rules, including old messages.
An explicit retry of a failed reference reads the current file under these rules;
no upgrade or history load scans or retries historical failures. Files that were
never successfully read have no historical bytes to recover. A completion callback
cannot replace an already-bound source. Existing ready resources and recovered
receipts retain their original saved bytes after the source changes or disappears,
including snapshots created with the former own-AGENTS mapping. Stored receipt
identities are preserved for lookup only; they never select reading authority.

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

The render document contains its native viewport scroll chain while that axis
has scrollable content. Read-only layout observers recognize nested scroll
surfaces before input, and passive input observers recognize scrollable target
ancestors, so reaching either edge stays inside the preview. They do not cancel
input or change author event handlers. Short documents without scrollable
content retain ordinary browser scroll handoff.
Moving the pointer outside the frame immediately allows chat scrolling. This
does not rewrite arbitrary third-party scroll widgets, closed shadow trees or
author-managed gestures; nested frames remain prohibited by CSP.

File-reference modal viewers lock the launching chat's scroll ancestors and the
document until they close, then restore their offsets, original overflow styles
and opener focus. An unhandled trusted Escape inside the frame sends a
source/token-checked close notice for its containing viewer; inline previews
have no modal to close. This adds no method to the public `window.pot` API and
does not cancel or synthesize the original key event.

## Persistent offline display copies

Successful browser and native reads save independent, read-only display copies
in the shared UI IndexedDB database. Ready message-file bindings are indexed by
host origin/base and runtime identity, Control, Space, owner, Task, message and
text digest. A resource byte key additionally includes its immutable ID, entry,
complete file manifest and the member's digest, size and MIME type. Stored bytes
are SHA-256 checked before use. A new message revision, Control or owner cannot
select an older binding or a same-named path. Task path-image reads without an
immutable message binding keep their existing transient behavior.

Markdown/text, raster images, PDFs and successfully read sent image/PDF
attachments can reopen from committed local bytes. A readonly hit neither waits
for a network confirmation nor requires another write. Pending references,
partial reads and aborted transactions are never offline copies. A cached binding
can have missing bytes after eviction; that reference fails locally without
removing the conversation or other resources. Existing readable bytes survive a
failed refresh or quota error. Audio/video retain metadata-only streaming and
are not made offline by this cache. Downloads still recheck the original owner
through the live read/save operation; offline display grants no download,
request, execution or approval authority.

The display budget is independent of history, composer and outbox stores:
128 MiB of accounted payload/identity bytes, 512 copies, and 30 days from the last
successful save. Expired copies are unreadable and are swept on subsequent saves;
capacity evicts the oldest saved copies. IndexedDB/index overhead and browser
storage eviction are managed by the browser. At most 1024 scope-generation fences
are retained (bounded identity lengths); reaching that bound atomically clears
only display copies and advances a global writer epoch. Original draft/outbox
Blobs and conversation/startup data are never adopted, moved or deleted by display
retention. `cacheStats.resourceCopies` records the accounted bytes and count.

Ready notifications are separate from stored bytes and fire only after the
IndexedDB transaction completes. Tabs reread the same committed store. Explicit
retirement, an observed read-authentication failure, or a confirmed directory
removal/Control replacement deletes that scope's copies and retires active
frames and URLs across tabs. A later live authorized binding/original read may
reopen the scope at its new generation; older requests cannot commit after that
recovery. Directory observations retain their request's connection generation
and directory authority revision through local enumeration and the retirement
transaction; a retired response cannot revoke a
replacement connection's copies or return as a current directory read.
Logout retires readers before clearing display stores and advances the durable
global epoch in that same transaction, so a delayed cross-tab forget message
cannot allow a pre-logout writer to restore bytes. Each resource's connected
views share original-byte reads and Blob URLs, released after the last lease; no URL or per-mount bridge token is persisted.

Complete HTML has a separate bounded display-document read:
`GET /api/v1/resources/{id}/render-document`. The owner/Space-checked service
rechecks every manifest digest and reruns the existing static dependency collector
using only those members. Missing members, external dependencies, CSS imports,
modules and recognized dynamic dependencies fail locally. It never fetches URLs
or revisits a workspace directory. The returned versioned template contains all
supported dependencies as data URLs, its original manifest, a SHA-256 digest and
a public token placeholder. The rendered template is limited to 8 MiB to fit the
shared bounded JSON operation port. Unknown runtime dependency attempts remain
blocked by CSP and report the existing local dependency notice. This is a saved
bundle transformation, not an iframe DOM snapshot or a promise that arbitrary
HTML dependencies have been saved.

```text
ready immutable binding -> verified chunks / complete render-document
                                      |
                         IndexedDB transaction complete
                                      |
                            readonly display lease
                    +-----------------+------------------+
                    |                                    |
          text / raster / PDF                 committed HTML template
                                                         |
                                  fixed trusted resource-document.html
                                                         |
                               remove iframe allow-same-origin first
                                                         |
                                   self-navigation to author Blob
                                   opaque sandbox + resource CSP
```

HTML uses the fixed packaged `resource-document.html` only during initial mount.
This trusted static factory may initially share the host origin so its navigation
can be served by the verified offline shell. Its exact response alone permits
`sandbox allow-scripts allow-same-origin`; the main application policy is unchanged.
The parent and factory check the expected window, exact origin and fresh one-use
factory token. Before sending any author bytes, the parent removes
`allow-same-origin`; the factory independently checks that removal. It only creates
a Blob and navigates itself, never inserts or evaluates author bytes in the
trusted document. The author document's effective sandbox is `allow-scripts`,
its origin is opaque, and its resource CSP still denies network, forms, objects,
nested frames and base URLs. A separate fresh author token retains the existing
source/window-checked resize/result bridge. Initial offline remounts get a new
document; width/font/locale preparation and background confirmations preserve
an already active document, window, value and focus. The factory URL, pending
messages and generated Blob URL belong to the original component generation and
lease and are retired with it.
