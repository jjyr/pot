# Contextual onboarding validation — 2026-10-12

## Scope

Native Chat now offers AI Configuration and Connect server when its Agent is
unconfigured. Connect server opens the existing Machines form, and configured
local clients can explicitly open server chat from that form.

Unsuccessful task reports with recognized missing-desktop diagnostics display a
small card in assistant Chat. Its actions open the existing Sandbox creation
sheet or Machines connection form. The creation action does not select the new
Sandbox, start a desktop, send a chat message or resume a task. No additional
success feedback is introduced; ordinary creation errors and resource-download
authorization remain visible.

## Checks

- `mise run test:app`: passed, 22 Rust App tests plus native client safety tests.
  This compiled the Rust presentation and the complete Swift bridge.
- `mise run test:ui`: passed, 16 tests, including three new missing-desktop
  classification tests covering English/Chinese diagnostics, unrelated failures,
  successful reports, user text and unsettled reports.
- `mise run test:i18n`: passed, 818 catalog keys; native language behavior,
  third-language discovery and desktop web localization checks passed.
- Changed-source `scripts/dev-check lint` and `git diff --check`: passed.

## Limits

No paid model requests, real VM provisioning, remote machine enrollment or manual
light/dark UI acceptance were performed. Report classification recognizes the
existing unsuccessful product wrapper and a finite set of missing-desktop
phrases. Arbitrary model paraphrases and logically unsuccessful replies recorded
as completed tasks are not structured failure signals. Task capability acceptance,
transport and permission errors are not treated as missing execution targets.

The actions reuse current creation/connection capabilities; they introduce no
model-visible tool contract, automatic retry or remote desktop transport.
