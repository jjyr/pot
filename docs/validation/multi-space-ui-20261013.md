# Native multi-Space UI validation — 2026-10-13

Scope: the native App's embedded local Control and one configured remote Control.
See [UI contract](../multi-space-ui.md). No compatibility adapter, model tool change,
special handoff protocol, or standalone assistant-message screen was added.

## Automated checks

- `cargo test --locked -p pot-app -p pot-ui -j4`: 29 App tests and 15 shared UI tests passed.
- `cargo clippy --workspace --all-targets --locked -j4 -- -D warnings`: passed.
- `cargo build --locked -p pot-app -j4`: passed, including embedded Web assets.
- `mise run lint`: Rust/Swift/Python checks passed.
- `mise run test:app-safety`: client safety regressions passed.
- `mise run test:i18n`: 810 keys, native and embedded Web checks passed.
- `mise run test:architecture`: six checks passed.
- `git diff --check`: passed.

The isolated catalogue test launches a real embedded Control and
`scripts/tests/multi-space-fixture.py` as its loopback remote. It verifies assistants,
machines, tasks, schedules, permissions and Usage fetches. Changing the fixture's
Control identity rejects a subsequent mutation before dispatch. Other tests cover
same object IDs in different Spaces, global pagination, same execution-machine IDs
with Space-specific filtering, unknown Usage for an unrefreshed range, raw bucket
aggregation, and filtering peer replies for Chat.

## Native interaction checks

A signed disposable `/tmp/Pot-Spaces-Test.app` used isolated data and a loopback
fixture. CUA accessibility inspection and screenshots verified:

- One assistant list with same-name local/remote assistants, location tooltips,
  and New assistant at the top.
- Creating a local test assistant opens its editor and AI Configuration entry.
- Opening the Chinese-named remote assistant displays Chat without a selector.
- An unsent Chinese/English draft survives assistant navigation and configuration
  location changes.
- Switching AI configuration location preserves the AI section. Leaving Settings
  for Tasks preserves the selected destination while restoring the original owner.
- Combined Tasks, friendly execution machine labels, ownership tooltip, and opening
  a remote task's detail page; combined Schedules and opening schedule details.
- Machines show local above remote with a separator. Stopping the fixture preserves
  cached remote machines with stale/offline status while local machines remain usable.
- Overall Usage has All/local/cloud filters; a specific cloud selection opens that
  Space's Usage limits. Aggregate limits cannot be edited without choosing a Space.
- Light and dark rendering of the exercised pages; Chinese titles and pasted drafts.

These interactions exposed and fixed a server page-size mismatch (maximum 50),
missing bundled icon assets, duplicate accessibility IDs, configuration-section
reset, and a settings-return navigation reset.

## Limits

The remote Control is a deterministic HTTP fixture, not a deployed cloud service.
No user credentials, paid model calls, or real task execution were used. Actual
relay/model delivery is covered separately by the backend validation; this report
checks its UI projection and routing contracts. This pass does not qualify the
full minimum-window, IME composition, reduced-motion, screen-reader or keyboard-only
matrix. SwiftFormat could not write its optional user cache; lint still passed.
Cargo reports the existing `block` dependency future-compatibility notice.
