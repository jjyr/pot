# Shared native and browser UI

The `pot-ui` crate holds transport-independent chat behavior and GPUI presentation.
Native App and Mobile call the same message renderer rather than maintaining two
message trees. This is the first migration phase, centered on chat; whole-page
navigation, Tasks/Sandbox/Usage pages and native settings remain platform shells.

## Boundaries

| Shared module | Responsibility |
| --- | --- |
| `state` | Pending request identity, same-ID retry, authoritative snapshot reconciliation, conversation-bound receipts, editor draft ownership and send eligibility |
| `chat` | User/assistant styling, Markdown, tool folding/status, copy actions, task-report links and message lifecycle labels |
| `markdown` | Stable text IDs, parsing/selection, code/link styling and HTTP(S) link dispatch |
| `design` | Existing Pot theme tokens and compact-viewport policy |
| `localization` | Locale-explicit shared catalogs and generated-wrapper translation; canonical user/model text remains literal |

GPUI presentation is an opt-in `presentation` feature. State/localization stay
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

See [2026-10-07 validation](validation/shared-ui-20261007.md).
