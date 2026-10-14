# Shared native and browser UI

The `pot-ui` crate holds transport-independent page behavior and GPUI presentation.
Native App and Web use shared Chat, Tasks, Machines and Usage components.
Navigation chrome, native settings, Schedules, the Swift composer, desktop viewer
and browser authentication remain platform adapters.

The workspace pins `gpui-kit` 0.7.3 with its `gpui-fast` feature. Native App,
shared presentation and Web/WASM inherit that dependency, selecting GPUI Fast
for the core, platform, components and assets through the Kit facade. Existing
`gpui_kit` imports (including the local `gpui` alias) therefore use Fast types.
Kit still compiles upstream `gpui-pre` dependencies because its Cargo features
are additive; their presence in the lockfile does not select the upstream backend.

## Boundaries

| Shared module | Responsibility |
| --- | --- |
| `state` | Pending request identity, same-ID retry, authoritative snapshot reconciliation, conversation-bound receipts, editor draft ownership and send eligibility |
| `pages` | Shared geometry, typography, cards, grouped choices, tooltips, icons and action dispatch |
| `tasks` / `page_state` | Task rows, status, search, filters, empty/error states, counts and authoritative pagination; viewing a Sandbox does not alter its execution default |
| `machines` / `machine_state` | Machine cards with Host/Sandbox status, exact-location task filters and capability-controlled desktop actions |
| `usage` / `usage_state` | Statistics, charts, grouping, pagination, quotas, tool output, allowance editing and exact K/M input parsing |
| `permissions` | Assistant/machine Host or Sandbox approval cards, work/permanent decisions and revocation shared by native and Web; Machines configures policies, limits and ownership |
| `chat` | User/assistant styling, Markdown, tool folding/status, copy actions, task-report links and message lifecycle labels |
| `markdown` | Stable text IDs, parsing/selection, code/link styling and HTTP(S) link dispatch |
| `design` | Existing Pot theme tokens and compact-viewport policy |
| `localization` | Locale-explicit shared catalogs and generated-wrapper translation; canonical user/model text remains literal |

GPUI presentation is an opt-in `presentation` feature. Page transitions, allowance parsing and localization stay
portable and require no GPUI, Swift, network client or browser API, including in
Linux backend CI. Native and WASM clients enable presentation for their targets.
The shared layer receives protocol data and dispatches actions; it never fetches
an endpoint, reads a native token, retrieves a browser Token or changes OS settings.

App supplies in-process/HTTP request and SSE transports, native navigation/history, native file-save actions,
its running-task summary extension, AppKit/Swift input and editor storage. Mobile
supplies same-origin fetch/cookies, browser history, GPUI Textarea editor entities,
Token/PWA and the mobile navigation shell. Browser JS delegates pending
request construction, receipt validation and snapshot reconciliation to Rust.
Native keeps its existing automatic same-ID transport retries; Mobile keeps
explicit retry after an uncertain send. These are adapter policies around the
same request identity and receipt rules.

## Desktop appearance contract

The message element IDs, geometry, padding, colors, Markdown styles, copy-row
reservation and tool labels were moved from App without changing the desktop
layout. Finder and running-task elements attach through native extensions.
The existing Swift composer is intentionally retained so its IME, sizing,
shortcuts and appearance do not change during this migration.

On narrow browser viewports the same renderer bounds message width to the
available column and shows copy actions without hover. Mobile shares catalogs
with an explicit locale, so changing its language cannot change Mac preferences.
Its reduced-motion preference is passed to GPUI. Embedded chat icons avoid an
external CDN dependency. Physical phone/IME/accessibility qualification remains
separate from emulated viewport observations.

Browser Sandbox has a desktop launch action; the scoped viewer opens in a separate
tab. Execution target administration and Usage editing remain owner CLI-only.
Desktop authority comes from login-bound Server tickets, not shared components.
Shared locale formatting uses the existing plural catalog and decimal precision,
while leaving parameter contents literal. Allowance saves read the current
editor at click time, avoiding a stale value captured during rendering.

See [chat validation](validation/shared-ui-20261007.md) and
[page migration validation](validation/shared-pages-20261007.md).

## Multiple assistants

App and Web share the assistant name entry, selector/new assistant form and
name/AGENTS.md editor. Drafts are retained by owner and saves use definition and
content revisions. Shared task rows carry assistant identity and group matching
Tasks beneath assistant names. Native and browser transports capture the owner
before asynchronous writes; late snapshots are discarded on owner changes.
App navigation history includes Runtime and owner identity. The Server exposes
bounded in-process and HTTPS client adapters; Control membership, client transports and App mode isolation are implemented.
Remote desktop transport remains deferred.

## Machines

`pot-ui::machines` supplies top-level machine cards and expandable Host/Sandbox
rows. View selection is separate from execution defaults; cached offline inventory
remains visible. View Tasks filters by the exact Runtime and target before pagination.
Only implemented client transports expose desktop entries. Resource creation is
API-only; technical management stays in Chat. No plugin/session/artifact panels or
management shortcut buttons are present.

App configures one Control and switches local/remote with one icon. Web stays fixed
to its configured Control. Transport captures mode/Runtime/owner before each request
and rejects late results after navigation changes.

## Responsive browser shell

Web selects its shell from the visible viewport width: below 900 CSS pixels,
Chat/Tasks/Machines/More use bottom navigation; at 900 pixels and above, a 176-pixel
sidebar also exposes Usage and Schedules directly. The selected destination is
highlighted. Task detail includes a return-to-tasks action without resetting the
list query. Content and composer share an 820-pixel maximum column; page density
and task metadata adapt to the available space.

The shell uses GPUI's fully visible bounds, including safe-area and visual-viewport
changes, instead of the browser layout height alone. Under 500 pixels of visible
height the composer reduces from 80 to 48 pixels. Content scrolls independently
from navigation and input. Existing editor entities and owner-bound drafts survive
resize and page changes. Canvas safe areas are handled once by GPUI; the HTML
login form applies its own CSS safe-area padding.

See [responsive browser validation](validation/responsive-web-20261012.md) for
emulated desktop/phone coverage and physical-device limits.

## Composer references

Both composers complete `/machine:`, `/tag:` and `/skill:` references from the
selected assistant's Control catalog. Type `/` to search, use Up/Down and Enter to
insert, Escape to dismiss, or click/tap a result. Selection inserts editable text;
Enter accepting a result does not send the message. Input-method composition is
left to the platform. Drafts and exact send retries include reference text.
See [Agent context](agent-context.md#message-references-and-machine-labels) for scope.

The native Machines page connects only to Control through the Cloud section's
Connect server entry. It has no duplicate global Connect machine button. Each
remote Control group offers Add execution machine for Control-owned invitations
and membership management. Runner enrollment remains an outbound join to Control;
the App never connects directly to a Runner. The server address form contains only
Control connection fields, and backend validation rejects non-Control endpoints.

Machines shows each machine's labels. Edit labels lives inside each machine's
settings disclosure and opens a dedicated sheet for that machine. Label and
membership commands target the selected group's backend, independently of the
active chat location. The label sheet renders labels as removable
chips; spaces, commas and Enter commit a draft, duplicates and empty labels are ignored,
and Backspace on an empty draft removes the last chip. Pasted lists use the same
delimiters. Blur and Save commit any remaining draft, and explicit Save persists the
machine's labels. Nonempty drafts offer matching labels from the machine directory
and standard `task-runner`, `desktop`, `macos`, `linux` and `windows` labels, excluding
already selected labels. Prefix matches rank first; Up/Down and Enter select a
suggestion, Escape dismisses suggestions, and custom labels remain supported.
IME composition is not tokenized until committed. Private CA input is
under a collapsed Advanced connection options disclosure; folding it preserves the PEM.
Permissions and resource limits are
collapsed behind the machine settings disclosure. Resource creation stays API-only.

## Event-driven updates

Native and Web use coalesced Control invalidations and conversation SSE instead of
healthy-state UI polling. See [UI events](ui-events.md) for scope, cursor/snapshot
ordering, cancellation, recovery and the remaining backend lifecycle monitor.
