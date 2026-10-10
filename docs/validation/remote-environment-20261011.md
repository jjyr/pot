> Historical: the peer pairing and machine-grant design in this report was replaced
> by the single-Control architecture. These observations do not validate its transport.

# Remote Environment adapter and connection UI (2026-10-11)

This is partial P3 acceptance. P3 has not received a completion commit.

## Delivered behavior

Local task Agents use an injected Runtime port backed by the Server's HTTPS
peer authority. Destination execution does not create an assistant DB or request
a model. Bindings fence Runtime, target, owner, logical turn and generation.
The destination supplies its workspace and project AGENTS.md; workers receive
neither the assistant's DB rules nor the caller's global/project rules. Remote skill catalog metadata is discovered on the destination; bundles remain
on that machine. Caller plugins are not shipped. Tool loadouts reflect the destination's
supported operations without altering immutable Thread configuration.

Caller intent is durable before execution sends. Failed sends use Inspect,
never another Execute. Only a signed completed receipt or signed not-accepted
receipt settles an operation. Pending receipts are inspected before renewal,
so an expired lease does not prevent checking an original outcome. Cancel intent
is durable before transport and remains blocked after failed cleanup. Local
cleanup still runs when remote cleanup fails. A checkpoint can reopen a
confirmed settled/cancelled logical turn with a fresh fenced dispatch identity.
Workspaces remain stable across follow-up turns; process scopes are per run.

App and Web share Machines connection controls: HTTPS address, optional private
CA PEM, pairing code, identity preview and confirmation. Edited input invalidates
the previous preview; uncertain Connect retains its original request identity.
On the destination machine, owners can grant Discover or Discover+Execute per
existing target for an explicit duration, revoke grants and disconnect peers.
No access is enabled by pairing. Browser controls exclude listener configuration
and raw RPC. Resources remain API-only.

App no longer starts the Apple Container Helper. The Provider starts its owned
Helper for explicit prepare/start, checks process health and uses the Helper's
SIGTERM stop-all path during shutdown. Inventory/status/stop never start it.
A missing installation is reported as unavailable.

## Evidence

The full portable backend suite passed after introducing the adapter and
capability filtering, including Agent 70, Runtime 159, and Server unit 17 with
one pre-existing ignored test. Targeted Server/Provider suites subsequently
passed. The later full-task regression uses two actual HTTP Runtime nodes, an
HTTPS peer listener, a deterministic loopback SSE model and actual remote Host
processes. It verifies owner-side approval, destination project-rule injection,
absence of caller private rules, no destination assistant DB, target-only file
effects, and a follow-up turn reusing the same workspace. Test model requests
for the task and automatic owner summaries are counted separately.

Caller tests cover failed cancel transport with restart-preserved intent and
refusal to classify unknown receipts as terminal. Agent tests cover destination
tool filtering and immutable Thread settings. Server tests cover the limited
browser connection route and rejection of raw operator actions.

Native App/Server compilation, Server/Runtime/Provider Clippy, pinned release
WASM build and localization coverage passed. Chrome's agent profile at
localhost:19740 verified the connection entry, form, scroll behavior, grant
scope/duration explanation and inline failed-preview feedback. No access grant
or environment execution was performed through the browser.

## Remaining scope

The actual Linux rootless Podman Provider is now qualified with the same
two-node task flow, including reading an installed destination project skill.
Remote installed plugins, artifact reads and desktop observation/control
transports remain unimplemented; unsupported grants are rejected. Remote Sandbox
lifecycle management is implemented as recorded below; create/select remain local
and desktop requires independent authority. Cross-machine deployment and
real models, native App clicking and actual Apple Helper/VM stop qualification
remain outstanding. Local deterministic tests do not establish
those results. P4 still needs its UI/tool pre-check.

## Subsequent qualification and reconnect behavior

The ignored Linux/Podman test passed on devpc using an isolated overlay store
and a derivative image with the current x86_64 musl executor and recipe scripts.
The latest run passed in 10.72 seconds. Existing workloads and default storage
were untouched. The test uncovered guest startup before permission preparation;
Sandbox readiness now precedes normalization, while continuation performs no
implicit startup. Fixture removal uses rootless namespace cleanup after Server
shutdown. These are real Provider tests with a deterministic model, not a paid
model or network acceptance between separate machines.

Remote Context now discovers destination project, user and builtin skills and
returns metadata with destination-visible paths. Worker skill selection applies
to that catalog; caller private skills stay excluded. The task test reads an
actual project SKILL.md through the remote tool. Unreadable namespace-owned skill
directories produce diagnostics without aborting discovery of valid neighbors.

Caller journals persist a waiting reason. Active tasks project Waiting with a
separate remote_waiting flag; App/Web distinguish it from allowance waiting.
Task snapshots and status events explain the pending original receipt. No model
request occurs before an offline destination is ready. A lost connection after
an effect produces only Inspect retries, never Execute retries. Reconnect with
the original valid lease resumes the result; revoked/expired authority never
creates a replacement lease. Uncertain receipts remain waiting and cancellable.
Restart still marks an interrupted active task as interrupted; this does not
introduce automatic task replay after process restart.

Tests cover offline startup (zero model requests), in-flight disconnect (one
model request until reconciliation), Waiting task/snapshot projection, automatic
connection recovery and a single append effect. Authority tests cover listener
loss/recovery, signed rejection, revocation without replacement, settled
checkpoint reopening and preservation of cached original results. Reopened
attempt IDs are stable valid UUIDs derived from SHA-256; they no longer parse a
Base64 digest as a UUID. Malformed completed receipts never settle an operation.
Authenticated rejection replies are signed and bound to the original request;
unsigned HTTP diagnostics remain insufficient to prove a no-effect outcome.

## Remote Sandbox lifecycle management follow-up

The user approved remote start/stop/recover/update/delete for an existing Sandbox,
with create/select remaining local and desktop governed independently. The peer
protocol now exposes a separate `manage_sandbox` capability for Sandbox targets;
management grants confer no execution authority. The connection editor offers this
grant only on Sandbox targets.

Main tool dispatch preserves Runtime and explicit Sandbox identities before local
assignment resolution. Workers still cannot manage environments. Caller intents
and destination acceptance/results are durable. Lost sends and process restarts
query only the original operation. Signed absence is terminal only after the
acceptance window has expired, preventing a delayed request from racing a new
operation. An uncertain management receipt fences new effects on that target.
Grant expiry/revocation prevents acceptance and is checked before lifecycle effects;
original receipts remain queryable. Resource downloads require destination-owner
approval. Active remote execution leases/unsettled receipts block management;
delete also serializes with all destination assistants' persisted task/schedule
assignments. Retired Sandbox identities cannot be reused by resource creation.

Three management tests pass, covering HTTPS independent grants, denial without
Execute authority, busy execution leases, revocation, exact receipt caching,
conflicts, restart-preserved unsent intent, peer-scoped receipt queries, uncertain
target fencing, acceptance-window races, deletion and retired identities. Lifecycle
operations in these tests use a fixture Provider, not an Apple Helper or actual VM.
The multiple-assistant task test also verifies deletion guards inspect other owners.
Related backend regression passes: Runtime 159, Sandbox 18, Server unit 21
(one pre-existing ignored), access 5, TLS 1, CLI 2, lifecycle 10, multi-client 5,
and remote-environment 3 (one opt-in actual Podman qualification). App 19,
shared UI 12, 805-key localization, workspace check and targeted Clippy pass.
The acceptance-window follow-up is covered by the three dedicated management
tests. This does not complete P3 or validate browser-issued grants.

## Actual Podman management qualification

The extended opt-in Linux rootless Podman qualification passed on devpc in
14.18 seconds, using the same isolated storage and image recorded above. After
two task turns, it remotely starts, stops, updates the existing Provider config,
recovers, and deletes the test Sandbox through signed management receipts. The
Provider config is read from inventory; rootless Podman has no disk quota support
and requires `disk_gib: 0`.

The first real run exposed a peer cleanup bug: destination cancellation called
`drain`, which waited for active requests but did not close retained Sandbox
execution scopes. It now calls `finish` before `drain`. A new test verifies one
peer owner's cancellation releases only its own scope/lease, preserving the other
owner until that owner is cancelled. The actual follow-up stop/update/recover/delete
qualification confirms that cleanup removes the stale leases. Runtime now has
160 passing tests including that focused regression; Server unit 22 pass plus
one pre-existing ignored test, and targeted Clippy passes. The failed-run container
was identified by its qualification-root bind mount before stop/removal; only the
four exact failed fixture roots were removed with namespace-aware cleanup. The
final successful run removed its roots, and the isolated container store is empty.

This still uses a deterministic model and two Runtime processes on one Linux host.
It is not cross-machine, paid-model, Apple Helper/VM, or browser-grant acceptance.

The updated shared grant control also builds successfully in the pinned release
WASM bundle. No browser management grant was issued during this follow-up.
