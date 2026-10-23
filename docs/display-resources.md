# Display resources

Resource presentation and download are separate operations. Main Chat and Task
conversations share the image/GIF, audio, video, PDF, text, HTML and file-card
renderers. Uploaded image/PDF attachments use the same renderer through their
existing owner-bound byte adapter; model input capabilities remain independent.
Raster Markdown references retain their existing image renderer. Raw Markdown
HTML remains literal and remote images remain explicit links.

## Tools and durable replies

`register_resource` imports an existing workspace-relative file and returns its
resource identity and immutable manifest. Main may select one of its own Tasks
with `task_id`; a Worker can import only its own Task workspace. HTML bundles
declare their additional workspace-relative `assets`. Registration does not show
the resource. Source reads retain the original Task launch and revision when the
workspace is remote; failed remote reads never fall back to a local path.

The optional `name` is a display label, preserved verbatim, including Unicode,
slashes and backslashes. It has no filename character restrictions or separate
255-byte/character limit; overall transport and resource limits still apply.
Omitting it or passing an empty string uses the source basename. The label never
selects a workspace path, stored blob path or ZIP entry.

Download filenames are derived separately with `core.DownloadFilename`. A single
file keeps its original entry basename when portable; its label does not rename
the download. An HTML bundle uses the label with its final extension replaced by
`.zip` (dotfile labels keep their stem). Separators, control characters (including NUL) and Windows-forbidden
filename characters become underscores; trailing dots/spaces are removed and
Windows device names are prefixed with an underscore. Empty or dot-only names
fall back to `resource`. Filenames fit 255 UTF-8 bytes, truncating only at rune
boundaries and retaining the final extension when it fits. An extension that alone
exceeds the limit retains its prefix, with room for a nonempty stem. The manifest's
`download.path` carries this suggested basename to browser and native clients;
HTTP headers and native save dialogs apply the same conversion at their boundary.
Original `entry`/`files[].path` values and ZIP entries retain their declared paths
and existing path validation.

`present_resource` accepts a registered `resource_id`, `mode: preview | download`
and optional caption. Preview displays the appropriate component; download shows
a file delivery card. Both require a separate user action to save a file.
Download failures have their own localized retry notice and leave a working
preview intact.
Multi-file HTML originals download as a ZIP containing all declared originals.
The isolated rendering transformation never replaces the downloadable originals.

```text
local workspace / original run -- snapshot --> node resource store
                                                   |
                                        register_resource -> resource ID
                                                   |
                                        present_resource -> durable reply
                                                   |
                                shared ResourceView + explicit Download
```

Control stores manifests and idempotent registration receipts in `control.sqlite`
and originals in private `resources/` directories. A retry of the same registered
call returns its original receipt even if the source has changed or disappeared.
Changed inputs under the same receipt identity fail. Resource reads require the
owning assistant and authenticated channel; IDs are references, not credentials.
Referenced originals are retained independently of Task workspace lifetime.
Retiring an assistant makes its resource metadata, originals and registration
receipts inaccessible, while retaining private history on disk.
Storage is bounded to 1 GiB and 4096 resources; exhaustion rejects registration
without deleting earlier deliveries. Files/resources are at most 64 MiB; HTML
bundles are at most 16 MiB and 64 files. Preview and browser codec limits can be
stricter than registration limits. Unsupported previews retain a download action.
Raster originals are limited to 16 MiB, 8192 pixels per dimension and 32 megapixels.

A successful Main presentation atomically persists its completed tool result and
an assistant message with a typed `presentation`. Resource-only replies are
included in history, unread counts and sidebar previews. Provider/model history
keeps tool receipts and does not automatically receive the original bytes.
Task views derive the same presentation from the run's durable tool event.
Ordinary screenshots and arbitrary HTML-looking tool output are not promoted.

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
permissions are disabled. Local declared script/style/image assets are embedded
as data URLs; CSS `url(...)` references are resolved within the manifest. External
dependencies and relative JavaScript module imports are unsupported; bundle them
before registration. HTML output and dependency expansion are bounded.

The only component bridge methods are `window.pot.resize(height)` (clamped to
180–960 px) and `window.pot.result(value)` (at most 16 KiB of serialized JSON).
Results are displayed locally below the component. They do not send a message,
invoke a model or execute a tool. The parent verifies the frame's window, a fresh
per-mount token and the allowed message action. Reloading a component cannot
replay a privileged action because no privileged action exists on this bridge.
