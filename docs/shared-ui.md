# Shared App and browser UI

`pot-ui::application::Shell` implements the GPUI product pages, navigation,
composer and interaction state for native App and Web. Hosts supply services
through `Platform` and `Driver`; they do not maintain separate mobile or desktop
page implementations. Visual and interaction contracts belong in
[Design language](design-language.md).

```text
pot-ui: Shell, pages, composer, drafts, configuration, navigation
                         | Platform / Driver
            +------------+-------------+
            |                          |
       Native App                    Web
 AppKit text editing/IME      GPUI textarea/browser IME
 in-process + remote IO       same-origin fetch + SSE
 native window/downloads      browser history/downloads
 local + remote Spaces        one serving Control
```

## Presentation and platform boundary

`application` owns page entities, route caches, titlebar, editor sessions,
notifications and the composer. Chat, Tasks, Machines, Usage, approvals and
Markdown components live in the same crate. The `presentation` feature enables
GPUI; portable state remains usable without it. `pot-ui` contains no HTTP client,
filesystem, Swift or browser API integration.

Both targets load the shared assets and [`design.rs`](../crates/ui/src/design.rs)
tokens. [`pages.rs`](../crates/ui/src/pages.rs) supplies common controls, forms and
editor frames. [`pot-avatar`](../crates/avatar) composes raster avatars; shared
presentation caches the 64 color combinations. Layout follows available width;
touch density is independent of layout. The active window publishes width and
touch capability to the shared design context.

AppKit supplies text, byte-based caret/selection, composition status and editing
operations within the shared composer. Browser input uses the GPUI textarea.
Draft keys include client mode, Control and conversation; matching authoritative
receipts clear only the submitted draft. Reference completion is shared, leaving
marked text to the input method.

`Platform` supplies projections, capabilities, preferences and host services;
`Driver` dispatches intents through existing transports. Native adapters own window
controls, menu-bar status and desktop viewing. Browser adapters own authentication,
installation/push, downloads, preference bootstrap, history and viewport/input
updates. The shared shell owns destination selection, edge gestures and dirty-exit
decisions. See [Internationalization](internationalization.md) for startup preferences.

Native App aggregates local/remote Spaces; Web uses its serving Control. Objects,
requests, caches and late-response fences retain owner identity. See
[Multi-Space UI](multi-space-ui.md) and [UI events](ui-events.md).

### GPUI dependencies

Workspace Cargo manifests and `Cargo.lock` pin GPUI Kit and the GPUI Fast Git fork.
The `gpui-pre-*` compatibility crates re-export one core for both targets; macro
users alias `gpui-kit` as `gpui`. Follow the fork's `compat/README.md` when updating
patches. The fork uses WASM-safe scheduler time during wheel dispatch; replacing
it with standard time can trap browser input. Its locked source participates in
Web cache invalidation. Asset generation and build commands belong in
[Development](development.md).

## Configuration and asynchronous actions

Shell owns [editing sessions](design-language.md#editing-pages-replacing-complex-modals)
for AI, assistant, machine and schedule forms. Business components own field
entities, validation and requests. AI/machine/schedule frames use
`pages::editor_page`; assistant forms keep inline actions. Both hosts use the same
return context and dirty-exit guard.

Return contexts contain presentation and draft state, not backend snapshots.
Nested AI steps retain parent field entities and draft checkpoints; restoration
keeps live profiles, revisions and pending/retry ownership. Contexts belong to the
originating window and owner. A changed client mode/Runtime identity prevents
restoration into a different scope.

`web/editor-history.js` stores session/depth markers and ordinary route metadata.
During an active editor session, browser Back restores its current entry before
asking the shared leave guard, then traverses to an accepted destination. Declining
discard retains editor and history state. Credentials and field values stay in
memory, outside URLs/history; reload recovery is not part of this contract.

AI connections, profile selection, routing and provider allowances share a
configuration state machine. Requests/replies carry request and Control/assistant
identity; a late reply cannot replace another owner's drafts. Loading is independent
of response order. Field entities synchronize authoritative values on configuration
revision changes rather than every background refresh.

An uncertain configuration save retains its body and request ID for explicit
retry/reconciliation; an authoritative rejection permits correction. Checking or
saving does not send a model message. The portable chat state owns send identity,
receipt reconciliation and events; automatic native retries and explicit browser
retries remain transport policy.

The browser boundary permits the configuration, allowance and schedule routes
used by its pages. Execution, terminals, resource provisioning and Control
administration stay outside that allowlist. See [Deployment](deployment.md) for
endpoint authorization and transport responsibilities.

## Browser crash reporting

`web/errors.js` loads before the application module. Rust panics, uncaught JS
errors and unhandled rejections reach a non-modal DOM toast outside GPUI/WASM;
handled request errors retain their page presentation. The toast offers manual
refresh and diagnostic copying, hides after eight seconds unless hovered/focused/
expanded, and merges repeats without resetting the timer. Error log reopens details.

Up to 20 bounded records remain in memory and same-tab session storage. Diagnostics
also reach the console. URL queries/fragments and common credential patterns are
redacted; the reporter does not collect request bodies, form fields or transcripts.
Storage/clipboard failures do not interrupt reporting. There is no automatic upload
or refresh, and a blocked JS main thread cannot run exception handlers.
