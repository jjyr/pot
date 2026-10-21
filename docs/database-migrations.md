# Database migrations

`pkg/storage` owns every product SQLite schema. Business components use
`storage.OpenDatabase(path, storage.<Kind>Database)` and receive a connection only
after pending migrations have committed. They own queries, transactions, identity
checks and restart recovery, but do not create or alter schema. Components using
a caller-owned connection can call `storage.Migrate(ctx, db, kind)` before opening
transactions or starting receivers. `OpenSQLite` is a low-level primitive for
tests and external consumers managing their own schemas.

```text
Service startup (exclusive data-root lock)
                   |
          storage.OpenDatabase
                   |
       identify database + check history
                   |
      embedded migrations -> sql-migrate
                   |          |
                   |     SQLite transaction
                   |     SQL + version receipt
                   v
          business queries / recovery
```

## Schema ownership and versions

All migration SQL is embedded from `pkg/storage/migrations/<kind>/`. A sequence
belongs to the complete database, including tables used by other components:

| Kind | Product location | Contents |
|---|---|---|
| `control` | `control.sqlite` | Assistant registry, providers, encrypted secrets, integration accounts, Agent Link connections and host access configuration |
| `tasks` | `tasks.sqlite` | Tasks, schedules, inbox, mailbox, plugin checkpoints, routing and resource receipts |
| `runner` | `runner.sqlite` | Runs, execution receipts, events, resources and workspaces |
| `conversation` | Each conversation database | Messages, context, configuration, steering and instruction updates |
| `machines` | `machines.sqlite` | Autonomous signing identity, local labels and archived membership records |
| `usage` | `usage.sqlite` | Requests, routing, allowances, tools and compactions |
| `operations` | `operations.sqlite` | Operation receipts and scope/input bindings |

Kind is explicit; filenames are not used to infer schema. Independent services
sharing a file must use the same kind. No schema declarations live in business
packages. The Go architecture check rejects schema DDL and direct `OpenSQLite`
calls outside `pkg/storage` in production Go code. Schema DDL belongs only in
the migration SQL files, including for the storage package itself.

Each directory contains a contiguous sequence, starting at
`0001_baseline.sql`. Add the next four-digit version, for example
`0003_task_workspace.sql`. Versions are independent for each database kind and
are independent of application releases and JSON payload `schema_version` fields.
Multiple pending versions run in order, so users may skip application releases.

[`sql-migrate`](https://github.com/rubenv/sql-migrate) executes and records each
file in a transaction. `pot_migrations` stores `id` and `applied_at`. The ID is
`<version>_<name>_<kind>_<SHA-256 of the complete file>`. This binds version,
database kind and exact SQL to the same atomic receipt. Inspect it with:

```sql
SELECT id, applied_at FROM pot_migrations ORDER BY id;
```

Applied history must match an exact prefix of the embedded sequence. A newer
database, changed applied file, missing predecessor or different database kind
stops startup before another migration runs. Never edit, rename or delete a
released migration; add a new one. Resolve concurrent branch version collisions
before release. No runtime downgrade or Down migration is supported.

## Changing persisted data

Every change to tables, columns, indexes, views, triggers or stored JSON contracts
must include its migration in the same change, with updated callers and tests.
Persisted Go struct changes count even when SQLite table definitions stay the
same. New installations execute the whole sequence; do not also update the
baseline to resemble the latest schema.

Use transactional Up SQL only:

```sql
-- +migrate Up
-- +migrate StatementBegin
ALTER TABLE example ADD COLUMN revision INTEGER NOT NULL DEFAULT 0;
UPDATE example
SET body = json_set(body, '$.schema_version', 2), revision = 1;
-- +migrate StatementEnd
```

`StatementBegin`/`StatementEnd` keep scripts and trigger bodies together for the
SQL parser. Nontransactional migrations, Down SQL, empty files, duplicate
versions and version gaps are rejected. Do not include transaction-control SQL,
`VACUUM`, connection PRAGMAs or external effects in migrations. For JSON contract
changes, explicitly convert old fields and versions, including nested snapshots;
do not merely relabel the version. Keep historical conversion logic in this DB
layer. If a future conversion needs a domain-specific operation, inject a narrow
interface rather than import product services into `pkg/storage`.

Keep Task/run identities, destinations, delivery states, command receipts and
uncertain effects intact. Migration never launches work, retries a tool, resets
an uncertain operation or invokes an agent. Restart recovery runs afterward under
its existing rules. Ordinary recovery DML still runs on every restart; one-time
data transformations belong in migration SQL.

Test upgrades from the preceding format, skipped versions, preserved data,
repeated startup and failure/retry. A failing file rolls back its SQL and version
receipt; earlier committed files stay applied. Startup fails and retries the
pending file on the next run. Database files migrate independently; there is no
transaction spanning an entire data root. Same-process migration calls are
serialized; product roots retain their exclusive process lock. External callers
must provide equivalent process exclusion.

## Initial adoption and operating upgrades

The first migration adopts the last format supported before this mechanism:
Task 8, Schedule 2, Runner 6 and conversation `user_version` 1, plus current
unversioned component tables. Empty databases are initialized normally. Legacy
version tables are checked before writes and removed by the baseline; the
conversation user version is retired. Subsequent migrations convert Task/Runner record payloads to versions 9/7,
preserve run identities and receipts, and retain the previous archive state of
completed/cancelled Tasks. New Tasks use explicit assistant-managed archiving. Earlier formats that were already rejected remain
unsupported; they require explicit conversion migrations, not an edited version
marker. Legacy inbox queue tables are also rejected rather than overwritten.

`tasks/0002_plugin_event_payload.sql` moves the existing plugin payload cleanup
into a recorded one-time migration, preserving bindings, checkpoints and receipt
states. It no longer runs on every mailbox open.

`tasks/0007_github_notification_progress.sql` retires GitHub history-import
requests while preserving notification cursors, pending groups and inbox receipts.
It also adds persistent background failure diagnostics.

Before an upgrade that can change migration history, stop all processes using
the root and take a consistent backup of the complete root, preserving identity
and the secret master key. The [local self-update path](updates.md) requires an
identical migration fingerprint and only rolls back the program package, retaining
newer execution receipts. Start the new build against the same root; no separate migration
command is required. For rollback, restore the complete pre-upgrade backup and
matching binary together. Do not erase migration records, change checksums or
force an older binary to accept upgraded data. Failure diagnostics identify the
database and migration; resolve the cause and restart with the same migration.

## Key-dependent Agent identity conversion

Control migration `0009_message_node_identity` performs typed JSON and route
conversion and writes a pending receipt only when historical credentials or
invitation claims need the existing master key. `secrets.Open` calls
`storage.FinalizeNodeIdentity` before returning. The finalizer transforms only
Agent Link pair intents, preserves the versioned invitation claim binding, reseals
under the same key/reference, and removes the receipt in one transaction. A
missing key or interrupted conversion leaves the receipt for a safe restart.
Normally deleted credentials of fully revoked links remain deleted. A custom
Agent Link SecretStore must complete its own conversion before starting the
host; `agentlink.New` fences an incomplete pending stage.

Tasks migration `0006_message_node_identity` preserves historical input digest
bindings with an explicit per-message format receipt when the original full
parent is not reconstructable. The frozen hash codec still checks every input
field. This is an internal storage hash format, not legacy protocol acceptance.
Typed mailbox and Task address conversions retain message IDs, delivery states,
consumer membership and peer context. Historical model messages, plugin data,
provider replay and arbitrary tool output remain untouched.

Historical Agent Link claim hashes also retain their exact format through
`agent_link_claim_digest_formats`. A Pair request binds its original display
names, while the receiver may save a later registry name. Migration therefore
cannot reconstruct every original claim preimage. The frozen claim codec checks
the original capability, identities, names and host key exactly; new claims use
the current NodeID hash format.

`control/0002_agent_links.sql` adds connection and invitation metadata with
references into the encrypted secret store. `control/0003_agent_link_tcp.sql`
retires HTTP invitations and credentials, and marks existing connections as
requiring a new TCP invitation without deleting mailbox messages or receipts.
`control/0004_agent_link_host.sql` records the peer listener and verified endpoint;
`control/0005_agent_network.sql` converts the access mode to `address`.
`control/0006_peer_network.sql` clears old HTTP publication addresses, because
their ports cannot be assumed to serve the new TCP protocol.

`machines/0002_peer_tcp.sql` preserves signing identities, membership epochs and
original join requests while removing old HTTP endpoints and CA settings. Existing
Runners update their endpoint against the saved Control identity; migration never
re-enrolls a machine or replays execution. See [Peer protocol](peer-protocol.md).

`tasks/0009_background_instances.sql` separates installed plugin identity from
background instance identity and adds optional model connection ownership. It
keeps existing binding IDs and checkpoints, fills instance provenance in stored
plugin event envelopes, and retains message IDs, delivery states and receipts.

## Assistant character avatars

Control migration `0015_character_avatars` records each assistant's assigned
character from its original creation name, with a deterministic kitchen character
for other names. It replaces old generated images and default-image receipts with
character composites while retaining custom uploads, identity revisions and request
bindings. Renaming does not change this saved role. The frozen `0011` renderer
remains confined to storage so skipped-version upgrades can recognize old pixels.

## Autonomous collaboration upgrade

`machines/0003_autonomous_node_identity` preserves the stable signing identity
and labels and archives former distributed membership facts. Local recovery keeps
its original receipts; retired remote launches remain fenced.
`control/0017_agent_link_nodes` expires assistant-scoped invitations and requires
fresh node-wide admission, preserving mailbox history and keys.
`control/0018_agent_link_delegation_stop` retains cancellation intent and chat
sender bindings. `tasks/0010_codex_link` stores persistent Codex assistant sessions
and accepted jobs. `tasks/0011_agent_link_task_targets` adds optional target/source
metadata without changing existing task/run identities or effect receipts.

`control/0019_codex_addresses` adds the independent set of Codex peer endpoints
enabled by copying a discovered address. It preserves the existing configured
endpoint, host identity, encrypted secret references and connection history.
Listener restoration happens after migration and never replays model work.

Profile file format 2 adds explicit inherited idle compaction timing to saved
connections and save receipts. The upgrade preserves routes, credential references
and receipt hashes. Conversation migration 0004 persists the last ordinary request
anchor, prefix and attempted-history identity; existing conversations initialize
it on their next successful ordinary request. Usage migration 0002 retains private
compaction evidence linked to the existing operation and request ledger.

`control/0020_agent_link_direct_chat` separates node connection identities from
assistant directories and persists independent user-chat sessions. It retains
established connection IDs, pins and historical sender bindings, expires old
invitations and fences unfinished admissions. `tasks/0013_agent_link_direct_chat`
adds a typed user-chat route index while preserving existing immutable envelopes.

`tasks/0014_github_notification_filters` adds idempotent configuration write receipts
and converts GitHub notification whitelists to explicit null/empty semantics.
Ambiguous old broad reason lists become mention-only; explicit nonempty filters,
disablement, recipients, cursors, thread markers and inbox receipts survive.
Unaccepted pending groups and incomplete scans are retired. See [GitHub
notifications](plugins.md#github-notifications) for account binding, the current
subscription contract and configuration-boundary behavior.

`control/0021_agent_link_addresses` records independently enabled Pot peer endpoints, preserving the existing configured endpoint. Startup restores listeners after migration without replaying agent work.

Profile format 3 replaces idle-delay settings with `prompt_cache_minutes`, an
estimated cache lifetime. Explicit durations and immutable save receipt hashes
are preserved; retired field names are removed. Conversation migration 0005 adds
request-start time and nullable cache read/write evidence. Old timing anchors are
disarmed until a new ordinary response supplies evidence; transcript and usage
records remain unchanged.

Usage migration 0003 persists whether a request reported cache-read usage. Existing
positive cache counts remain known; historical zero counts remain unknown because
the old ledger did not distinguish missing data from a reported zero. Request
identities, usage amounts, reservations and compaction evidence remain intact.

`tasks/0015_plugin_mcp_subscriptions` converts the previous GitHub binding into a
plugin-owned subscription collection with a persistent initialization marker.
The original target, disablement, explicit filters and account become one
`legacy-github` subscription; the host root becomes an enabled shared scanner.
Cursor/thread deduplication, accepted inbox messages and write receipts survive;
incompatible unaccepted pending groups and unfinished scans are retired. The
root remains present after deleting the last subscription, preventing default
recreation on restart or reconnect.

MCP protocol implementation and SDK asset updates do not change the stored
independent-server catalogue or GitHub subscription format. The stored catalogue
remains a product projection (name, description, input schema); complete protocol
metadata and result content are handled by `pkg/mcp` at invocation time. Updating
a plugin's code/assets changes its ordinary digest-pinned resource snapshot.
Any future persisted catalogue or snapshot schema change still requires its own
migration and upgrade coverage.
