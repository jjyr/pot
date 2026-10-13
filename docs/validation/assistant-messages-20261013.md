# Assistant messages and App relay validation

Date: 2026-10-13. Contract: [Assistant messages](../assistant-messages.md).

## Scope

Implemented shared `pot-protocol::message` contracts, durable
`pot-assistant::message` mailboxes, the transport-independent `pot-relay` library,
Control/Server adapters, approved discovery/send tools and App lifecycle wiring.
No UI redesign, task migration protocol, compatibility aliases or account/pairing
system is included.

## Checks

- `cargo test --locked -p pot-assistant -p pot-relay -p pot-protocol -p pot-control
  -p pot-server --no-default-features --lib -j 4`: 210 passed; one existing
  Tailscale FFI test ignored because it requires `mise run build:tailscale`.
- `cargo test --locked -p pot-server --no-default-features --test lifecycle -j 4`:
  11 passed. Server tests used an isolated temporary `POT_SECRET_KEY_FILE` as
  prescribed by the development test workflow, not the user's Keychain.
- `cargo test -p pot-core --lib --locked -j 4`: 19 passed, including tool/plugin
  contract checks. Together the backend runs cover 240 passing tests.
- Focused Assistant/Relay and deterministic message reply tests passed after the
  final context/receipt changes.
- `cargo check -p pot-app --locked -j 4`: passed, including native bridge and
  embedded Web asset generation.
- `cargo clippy --workspace --all-targets --locked -j 4 -- -D warnings`: passed.
- `mise run test:architecture`: six checks passed, including the Relay dependency
  boundary. Source formatting/lint and `git diff --check` passed.

Commands ran through the repository's pinned mise tools. Cargo reports an existing
future-compatibility notice for the external `block` dependency.

## Evidence and limits

SQLite and relay tests cover identical versus conflicting retries, durable queued
outbound messages after reopen, interrupted processing after restart, reply
participant binding, bounded automatic exchanges, recipient-scoped history,
bidirectional forwarding, lost acknowledgements, offline endpoints and changed
Control identities. No uncertain Agent attempt is automatically replayed.

Control integration uses the real execution manager, model transport and tools
against a deterministic local provider. It verifies Assistant discovery, explicit
reply sending, preserved reply context and separation from the owner's main chat.
Additional checks cover assistant-owned instruction/task persistence and scoped
lookup of active message sessions. Worker and summary tool restrictions remain
enforced.

Server lifecycle tests use actual local HTTP and native dispatch. Unauthenticated
or invalid-credential requests fail; unknown senders are rejected; duplicate
cross-Control deliveries retain one queued message without invoking an
unconfigured model.

These are local automated checks, not qualification against two deployed cloud
hosts or a running App UI. Relay fault tests inject endpoints backed by real SQLite
mailboxes. No paid model calls or UI interaction were required. Browser/CLI relay
hosting, file transfer and new UI surfaces remain outside this scope.
