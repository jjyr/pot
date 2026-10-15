# Shared App and browser UI

`pot-ui` implements the complete GPUI application. App and Web instantiate the
same `application::Shell`; neither host implements product pages or navigation
chrome. The current App design is the visual and interaction baseline.

```text
                         pot-ui
      +-----------------------------------------------+
      | Shell: titlebar, icon rail, parent navigation  |
      | Assistants | Chat | Tasks | Schedules          |
      | Machines   | Usage | General / AI / Task       |
      | Composer frame, drafts, reference completion   |
      | Configuration state, request ownership, retry  |
      +-----------------------+-----------------------+
                              | Platform / Driver
                  +-----------+-----------+
                  |                       |
             Native App                Browser
       AppKit text editing/IME     GPUI textarea + browser IME
       in-process / remote IO      same-origin fetch + SSE
       native window / Finder      URL history / downloads
       local Runtime preferences   installation / push / login
                  |                       |
           local + remote Spaces   fixed web-ui-server
                  |                       |
                  +-------- Control ------+
```

## Presentation

`application` owns root views, page entities, navigation caches, configuration
state, assistant and machine editors, titlebar content, notifications and the
composer. Existing `chat`, `tasks`, `machines`, `usage`, `permissions`, `resources`
and Markdown components remain inside the same crate. There is no separate UI
crate for either target.

The shared shell uses the App's 56-pixel icon rail and 44-pixel titlebar. Page
columns, cards, typography, message rows and the 760-pixel composer follow App
geometry. Below 900 pixels, the same pages use compact spacing and controls;
density depends on viewport width, not the hosting platform. Native titlebars
reserve space for macOS window controls. GPUI owns browser visible bounds and
safe areas.

AppKit supplies text, byte-based caret position, selection/composition status
and editing operations. It does not render the rail, composer frame, send button
or completion menu. The browser uses a GPUI textarea in the same frame. Drafts
are scoped by mode, Control identity and conversation. Matching authoritative
receipts clear only the submitted draft. Reference completion uses the same
catalog, query rules, menu and keyboard behavior on both hosts; marked text is
left to the input method.

Native and browser builds pin `gpui-kit` 0.7.3 with `gpui-fast`. Both hosts load
the shared application asset source and design tokens. The sidebar Companion
uses transparent idle/cooking exports from the existing native vector master;
`mise run icons` regenerates those shared assets. Platform font resolution
and operating-system window controls remain host responsibilities.

`vendor/gpui-fast` contains the crates.io 0.1.7 sources with one clock fix:
wheel hover suppression uses `scheduler::Instant`, which supports WASM. The
upstream `std::time::Instant` panics on the first browser wheel event and stops
input dispatch. Keep the Cargo patch until an upstream release includes this fix.

Provider marks and their attribution live in `crates/ui/assets/agents`; both
targets embed them through the shared page components.

## Platform boundary

`Platform` supplies projections, capabilities and native/browser services;
`Driver` dispatches user intents to the host's existing client transport.
`pot-ui` has no Server, HTTP client, browser API, filesystem or Swift dependency.
GPUI presentation remains opt-in through the `presentation` feature. Portable
chat, permission, task, machine and usage state remains available without GPUI.

App aggregates local and remote Spaces. Every assistant, task, schedule and
notification retains its Control and owner identity. Web connects to exactly
one Control through its configured web-ui-server. Both project those sources
into the same page data and renderers. Viewing a machine never changes an
execution default. Offline inventory and late-response identity fences remain
part of the client contract.

General settings show the same language and appearance controls. Capabilities
expose native resource-cache/local Runtime services or browser installation,
notifications and login services in the common settings page. Local execution
limits remain a local Runtime service; browser and remote configuration use
Control APIs. Browser desktop launch uses authenticated viewer tickets; App
uses its available native transport.

## Configuration and asynchronous actions

AI connections, profile selection, routing and provider allowances share a
configuration state machine. Hosts execute its scoped API requests and return
replies tagged with request and Control/assistant context. Replies from another
context cannot replace current configuration or drafts. Configuration loading
is independent of response order. Secrets remain transient request inputs.

A save whose transport outcome is uncertain retains its original body and
request ID for explicit retry. An authoritative rejection permits correction.
Saving or checking configuration does not send a model message. Send identity,
receipt reconciliation and conversation events use the same portable chat
state; automatic native retries and explicit browser retries are transport
policies exposed through the shared composer.

The authenticated browser boundary permits the exact configuration, allowance
and schedule routes used by these pages. Execution, terminals, resource
provisioning and Control administration remain outside that allowlist. Tokens,
cookies, installation, notifications and URL history belong to the browser
adapter, not product views.

See [multi-Space behavior](multi-space-ui.md), [UI specification](ui-ux-design.md),
[event updates](ui-events.md) and [deployment](deployment.md).

## Browser crash reporting

The browser loads `errors.js` before the application module. Rust's panic hook
reports directly to this DOM layer and retains the console panic hook; global
`error` and `unhandledrejection` listeners capture uncaught JavaScript failures.
These handlers show a red, non-modal toast independently of GPUI/WASM, so a
trapped application can still explain the failure and offer a manual refresh.
Handled request errors keep their existing presentation.

The toast hides after eight seconds, pauses while hovered, focused or expanded,
and merges repeated errors without restarting its timer. Details offer copying
the diagnostic log. A small Error log button reopens details after dismissal
or reload. Up to 20 bounded entries (time, kind, message, stack, page and repeat
count) remain in memory and same-tab session storage; new entries are also sent
to the browser console. Storage/clipboard failures do not interrupt reporting.
Logs omit page query parameters and fragments and redact common credential
patterns; no request bodies, form values or transcript data are collected.
There is no automatic log upload or refresh. A blocked JavaScript main thread
cannot run these hooks; this mechanism detects exceptions, not all hangs.
