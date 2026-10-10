# Shared native and browser UI

The `pot-ui` crate holds transport-independent page behavior and GPUI presentation.
Native App and Mobile call the same Chat, Tasks, Sandbox and Usage components.
Navigation chrome, native settings, Schedules, the Swift composer, desktop viewer
and browser authentication remain platform adapters.

## Boundaries

| Shared module | Responsibility |
| --- | --- |
| `state` | Pending request identity, same-ID retry, authoritative snapshot reconciliation, conversation-bound receipts, editor draft ownership and send eligibility |
| `pages` | Shared geometry, typography, cards, grouped choices, tooltips, icons and action dispatch |
| `tasks` / `page_state` | Task rows, status, search, filters, empty/error states, counts and authoritative pagination; viewing a Sandbox does not alter its execution default |
| `sandbox` | Environment list/selection, phase/default labels and capability-controlled desktop actions |
| `usage` / `usage_state` | Statistics, charts, grouping, pagination, quotas, tool output, allowance editing and exact K/M input parsing |
| `permissions` | Owner approval cards with execution environment, exact arguments/scopes and explicit decision/revocation actions shared by native and Web conversations |
| `chat` | User/assistant styling, Markdown, tool folding/status, copy actions, task-report links and message lifecycle labels |
| `markdown` | Stable text IDs, parsing/selection, code/link styling and HTTP(S) link dispatch |
| `design` | Existing Pot theme tokens and compact-viewport policy |
| `localization` | Locale-explicit shared catalogs and generated-wrapper translation; canonical user/model text remains literal |

GPUI presentation is an opt-in `presentation` feature. Page transitions, allowance parsing and localization stay
portable and require no GPUI, Swift, network client or browser API, including in
Linux backend CI. Native and WASM clients enable presentation for their targets.
The shared layer receives protocol data and dispatches actions; it never fetches
an endpoint, reads a native token, registers a Passkey or changes OS settings.

App supplies ureq transport, native navigation/history, Finder artifact actions,
its running-task summary extension, AppKit/Swift input and editor storage. Mobile
supplies same-origin fetch/cookies, browser history, GPUI Textarea editor entities,
Passkey/PWA/push and the mobile navigation shell. Browser JS delegates pending
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
tab. Environment administration and Usage editing remain owner CLI-only.
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
bounded in-process and HTTPS client adapters; Control membership and connection
management UI remain P3 work.

## Machines (2026-10-10)

`pot-ui::machines` replaces the standalone Sandbox destination in native and Web
navigation. `machine_state` resolves typed view selections independently from
execution defaults and retains targets present in offline cached inventory.
The local Sandbox detail reuses `pot-ui::sandbox` without its old sidebar;
Host and machine details use the common page components. Native desktop overlay
visibility follows the selected detail, including navigation-history restoration.
Web discovery works without a conversation capability. Native resource creation
remains separate from read-only machine discovery and Web permissions.

`machine_connections` displays Control membership and supports machine invitations
and member removal. The former pairing and per-target grant controls are removed.
Resource creation remains API-only. App connection setup and its local/remote icon
are tracked in #39; Web stays connected to its configured Control.
