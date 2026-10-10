# Multiple assistant acceptance — 2026-10-10

P2 of [#39](https://github.com/jjyr/pot/issues/39). Validation used an isolated
assistant Runtime and Chrome's **agent** profile at `http://localhost:19740`.
No production conversation, model credentials or VM were used.

## Automated results

- Core: 12 passed; Agent: 68 passed; Runtime: 147 passed; App: 18 passed.
- Server: 6 unit tests passed, 1 existing ignored; access 5, TLS 1, CLI 2,
  lifecycle 7 and multi-client 5 passed. Shared UI: 11 passed.
- `mise run check`, Swift client safety, localization (764 keys), JavaScript
  syntax and `git diff --check` passed. Debug WASM was built with pinned nightly
  and packaged with wasm-bindgen 0.2.129; compressed assets were regenerated.
- Independent owners preserve context/configuration/rules after restart.
  Actual task creation and claiming tests reject foreign task control, scope
  approvals to the assistant and its workers, and serialize shared Environment
  occupancy across assistants. Foreign approval inspection must not cancel an
  unrelated owner's pending request.
- Rules edits compare definition/file revisions; stale saves fail. Explicit
  Host operations referencing the assistant's rules file require fresh approval.
- Scoped forwarding removes outer route parameters while retaining HTTP
  upgrades. A nested task lookup regression confirms proper 404 extraction.

## Browser observations

The initial profile restriction was resolved by the user before acceptance.
Through visible canvas interaction, created A and B, edited A's rules, switched
between them, saved and reloaded A, and renamed A. B's rules remained empty.
Unsaved rules and chat composer text were retained per assistant when switching.
An external file edit caused stale Save to fail without overwriting the file or
losing the draft; switching back displayed the conflict and “Discard draft and
reload” restored the external version. Successful Save displayed “Saved”.

For task presentation only, two inert queued records with empty queues were
seeded in the stopped disposable server's stores. The Ongoing page displayed A
and B headings and their respective task rows, with no empty Pot group. Needs
attention displayed the empty state with no groups. Clicking B's task switched
the URL owner to B. This uncovered stale outer Axum path parameters; after the
fix the scoped snapshot returned HTTP 200 with the correct task conversation ID.
These display fixtures did not dispatch work or call a model.

## Limits

Native App source compiled and navigation/client safety tests passed; physical
native interaction was not exercised. Real model chat/worker execution and real
VM startup were not requalified in this phase. Remote adapter origin/reply
validation is a client foundation; peer enrollment, identity pinning, grants and
remote Environment execution remain P3.

Assistant Profile/provider configuration is copied initially and then stored
independently. Environment inventory, Runtime-wide capacity, permissions policy
and usage ledger are shared. Rules files include Runtime identity in their path.
The global Host permissions file retains its existing ownership convention;
it does not establish cross-Runtime peer authority.

Rule review detects explicit path references; authorized arbitrary Host shell
commands are not an OS filesystem confinement boundary. Combined name/rules
saves perform revision checks and attempt rollback on metadata failure, but do
not provide a cross-store crash journal or serialize arbitrary external writes.
