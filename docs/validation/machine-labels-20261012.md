# Machine labels and composer references — 2026-10-12

## Contract

Control owns editable machine labels. Control defaults to `desktop`, `task-runner`
and its OS; enrollment seeds only the remote OS. Saving an empty set is explicit
and survives inventory refresh, repeated enrollment and process restart.

App/Web autocomplete `/machine:ID`, `/tag:NAME` and `/skill:NAME`. The inserted
text is the durable reference, included in drafts, receipts and exact retries.
Only the current user message constrains newly delegated tasks. Matching labels
are combined with AND, an explicit machine cannot be substituted, and ordinary
execution failures handle insufficient capabilities. No capability classifier,
automatic fallback, new tool schema, or container lifecycle policy was added.

## Evidence

- Protocol tests cover reference parsing, Unicode caret positions, explicit machine
  conflicts, label intersections and Control preference.
- Control tests cover current-turn selection, system-report exclusion, task retry,
  Skill persistence after restore, and unchanged existing assignments.
- Server tests cover default labels, edits and deletion, repeated registration,
  inventory refresh, persistence, and authenticated browser route allow-list.
- The production two-Runtime integration suite creates remote Host tasks through
  label selection, using a deterministic model and real child processes. All five
  Host cases passed, including plugins, offline start, receipt recovery and renewed
  leases. Two existing Linux Podman cases remain platform-gated.
- Rust App/Control/Server/UI Clippy passed with warnings denied; the native Swift
  bridge compiled and native safety tests passed, including UTF-16 reference ranges.
- Release WASM built successfully. Headed Chromium exercised phone (390px) and
  desktop (1280px) layouts against the isolated shared-UI fixture. Arrow/Enter
  selection, Escape, click insertion and actual message submission were checked;
  the recorded send contained both the chosen label and Skill reference exactly.
- Machines displayed labels and its editor used the shared Control command path.

Screenshots are local ignored artifacts under `output/playwright/labels-*.png`.
The UI fixture does not establish authenticated production browser integration;
route and dispatch integration tests cover those contracts separately. No physical
phone keyboard, native App clicking, real model or Linux container qualification
is claimed by this report.
