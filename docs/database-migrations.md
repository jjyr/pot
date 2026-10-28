# Database migrations

`pkg/storage` owns every product SQLite schema. Database owners call
`storage.OpenDatabase(path, storage.<Kind>Database)` and receive a connection only
after pending migrations commit. A caller-owned connection can use
`storage.Migrate(ctx, db, kind)` before starting transactions or receivers.
`OpenSQLite` is a low-level primitive for tests and external consumers managing
their own schemas. Schema DDL belongs in `pkg/storage/migrations/`.

Control owns one single-connection pool for `tasks.sqlite` and lends it to the
Task service, run journal, Mailbox and scheduler. Borrowers never close it; Control
closes it after all consumers stop. Cross-component transactions pass the existing
`sql.Tx` instead of reacquiring the pool. Query-only snapshots use
`sql.TxOptions{ReadOnly: true}`; write transactions retain `BEGIN IMMEDIATE`.

```text
Service startup (exclusive data-root lock)
                    |
       read-only existing-file history check
                    |
         storage.OpenDatabase / Migrate
                    |
         exact history prefix or empty DB
                    |
       sql-migrate parsed SQL -> one pending-batch transaction
                    |             |
                    |       all pending SQL + version receipts
                    v
           business queries / recovery
```

## First public baseline and development roots

Before the first public release, the historical SQLite migration chains were
folded once into their final empty-database state. Each kind now starts with
`0001_initial_schema.sql`. New databases execute one transaction per kind rather
than replaying intermediate table rebuilds and historical JSON conversions.
Final tables, indexes, views, triggers, constraints, defaults and initial records
are preserved.

**Existing development databases from before this reset are unsupported**, even
if they completed the old chain or their tables resemble the final schema.
Their checksum-bound history differs from the new lineage. Databases with old
version markers, untracked tables or empty history beside existing tables are
also rejected. The loader does not adopt them, replace their receipts, drop their
tables or convert their data. `OpenDatabase` checks existing files with a read-only
connection before configuring the writable connection's journal mode. `Migrate`
checks before issuing any migration writes. This is a per-database refusal, not
an atomic operation over a whole root.

To try this build, keep the existing instance on its matching binary and root.
When ready to retire that instance, stop every process using the old root and
make a consistent backup of the **complete root**, including node identity,
secret master key, assistant files and databases. Retain the matching old binary
with the backup. Start this build with a separate, previously unused root, for
example `pot serve -root /absolute/path/to/new-development-root`, following
[Deployment](deployment.md) for listener settings. A new root has a new identity
and fresh configuration/history; it does not import old chats, Tasks or execution
receipts. Do not copy old SQLite files into it, erase migration receipts, edit
version markers or repoint an existing service without an explicit operator
choice. There is no automatic data-preserving conversion across this reset.
Restoring the old complete root requires its matching binary.

This is a **one-time pre-release reset**. After the initial public baseline,
persisted changes resume the append-only migration and upgrade-test policy below.
It is not permission to fold future released histories.

## Schema ownership and versions

| Kind | Product location | Contents |
|---|---|---|
| `control` | `control.sqlite` | Assistant registry, providers, encrypted secrets, integration accounts, Agent Link connections and host access configuration |
| `tasks` | `tasks.sqlite` | Tasks, receipts, runs, checkpoints, events, workspaces, schedules, inbox, mailbox and plugin checkpoints |
| `runner` | Retired `runner.sqlite` | Inactive local-run import format; old pre-reset files remain unsupported |
| `conversation` | Each conversation database | Messages, context, configuration, steering and pending instruction-file exports |
| `machines` | `machines.sqlite` | Autonomous signing identity, labels and archived membership records |
| `usage` | `usage.sqlite` | Requests, routing, allowances, tools and compactions |
| `operations` | `operations.sqlite` | Operation receipts and scope/input bindings |

Kind is explicit, independent of filenames. Services sharing a file must select
the same kind. The architecture check rejects production schema DDL and direct
`OpenSQLite` calls outside `pkg/storage`.

All backend SQL is embedded from `pkg/storage/migrations/<kind>/`. Each directory
contains a contiguous sequence beginning with `0001_initial_schema.sql`. Add the
next four-digit version, for example `0002_example.sql`. Versions are independent
per kind, application release and JSON payload `schema_version`.

[`sql-migrate`](https://github.com/rubenv/sql-migrate) parses the embedded files and defines stable migration IDs. The centralized
runner applies the entire pending batch and all `pot_migrations` receipts in one
`BEGIN IMMEDIATE` transaction, including initial ledger creation. `migrations/ledger.sql` preserves the original
sql-migrate receipt layout; startup rejects altered ledger columns/keys or
triggers. The build fingerprint includes it, and the updater separately signs
and checks its digest rather than treating it as an append-only product migration. Receipts contain `id` and `applied_at`;
IDs bind `<version>_<name>_<kind>_<SHA-256 of the complete file>`. The new initial
schema name and hash cannot be confused with the old `0001_baseline` receipt.
Inspect history with:

```sql
SELECT id, applied_at FROM pot_migrations ORDER BY id;
```

Applied history must match an exact prefix of the embedded sequence. A newer
database, edited applied file, missing predecessor or different kind stops startup
before another migration runs. An empty receipt table is retryable only when no
product schema or legacy version markers exist. Never edit, rename or delete a
released migration. Resolve concurrent branch version collisions before release.
No runtime downgrade or Down migration is supported.

The frontend's rebuildable business projection cache has a separate IndexedDB sequence in
`pkg/storage/migrations/ui-cache/`. Its existing `0001_chat_cache.mjs` remains the
initial version. Browser and Wails asset hosts expose only these JavaScript
migrations, never backend SQL. Clearing browser storage does not change
authoritative history or initiate work.

`0002_indexed_windows.mjs` adds indexed sequence/tool/surface access and incremental
count/byte metadata. Its one-time upgrade scans existing records to initialize
accounting while preserving messages, positions and disclosures.
`0003_domain_projections.mjs` adds display projections and invalidation epochs;
`0004_task_checkpoints.mjs` adds atomic Task transcript checkpoints.
`0006_startup_identity.mjs` adds a separate last-confirmed browser startup locator
without altering cached history, views, drafts or original Blobs. The locator is
disposable and grants no authentication or permission.
`0005_assistant_drafts.mjs` adds per-assistant composer metadata and a separate
original-Blob store. These stores are excluded from rebuildable cache retention
and clearing; see [Assistant drafts](assistant-drafts.md). The shared
IndexedDB opener applies pending versions in the browser upgrade transaction;
a failed upgrade rolls back its stores and converted values. Open clients close
on `versionchange`, and a later open can retry an aborted upgrade. Existing
databases upgrade in place without wiping unrelated browser state. The same
schema serves the separate bounded Task transcript database. Retention, logout
and failure behavior are described in
[Shared UI](shared-ui.md#committed-local-projections).

## Future persisted changes and operating upgrades

Every change to tables, columns, indexes, views, triggers or stored JSON contracts
must append a migration in the same change, with updated callers and upgrade
coverage. Persisted Go structs count even when table definitions stay the same.
New installations execute the initial schema followed by pending migrations;
do not rewrite the released initial schema to resemble a later state.

Use transactional Up SQL only:

```sql
-- +migrate Up
-- +migrate StatementBegin
ALTER TABLE example ADD COLUMN revision INTEGER NOT NULL DEFAULT 0;
UPDATE example
SET body = json_set(body, '$.schema_version', 2), revision = 1;
-- +migrate StatementEnd
```

`StatementBegin`/`StatementEnd` keep scripts and trigger bodies together.
Nontransactional migrations, Down SQL, empty files, duplicate versions and gaps
are rejected. Do not include transaction-control SQL, `VACUUM`, connection
PRAGMAs or external effects. Convert old JSON fields and versions explicitly,
including nested snapshots; do not merely relabel versions. Keep conversion logic
in storage, without importing product services.

Preserve Task/run identities, destinations, delivery states, command receipts and
uncertain effects. Migrations never launch work, retry tools or invoke agents.
Ordinary restart recovery runs afterward under its existing rules.

Test preceding-format and skipped-version upgrades, preserved data, reopening,
constraints and failure/retry. A failing file or commit rolls back **every pending file in that database batch**
and their receipts; history committed by an earlier successful startup remains.
A process interruption leaves the same retry boundary. Database files migrate
independently. Same-process calls are serialized, `BEGIN IMMEDIATE` rechecks
history under the write lock, and product roots retain their exclusive process
lock. Concurrent starts cannot apply a conversion twice. Reentry uses exact
ledger prefixes rather than blindly adding `IF NOT EXISTS` to SQL.

The [self-update path](updates.md) coordinates local hosts, checks authenticated
migration lineage and runs normal startup migrations automatically. It does not
create database backups or implement Down migrations. Before any new binary sees
data, all old hosts must have closed stores and released root locks. If a schema
migration has committed and readiness later fails, maintenance stays blocked on
the new package for forward recovery. Never force an older binary to accept newer
data, erase migration history, restore older execution receipts or replay work.
Operator-managed backups are a separate operational choice, not a prerequisite
or automatic phase of installation.

## Conversation completion recovery

`conversation/0002_turn_completion` initializes the private
`conversation.completion` journal. Before final persistence, it records the
original accepted turn and terminal execution phase/error. This receipt permits
only idempotent completion writes and inbox acknowledgement, never another model
or tool invocation. Clearing the receipt and the active turn shares one local
transaction after acknowledgement succeeds. A crash before that commit repeats
only completion; executions without a receipt retain the existing uncertainty
and recovery fences. A successfully acknowledged inbox batch may already be
`interrupted` after a restart and is settled under its original batch identity.

## Shared native adapter storage

`control/0003_agent_adapter` renames Codex host and endpoint tables to shared
adapter tables and records the runtime kind. `tasks/0002_agent_adapter` renames
assistant, job, executor, task-session and continuation tables; adds model/effort
columns; and introduces caller-bound assistant-management receipts. Existing Codex
node keys, owner secrets, assistant revisions, native threads, immutable requests,
results and uncertain-cleanup fences remain intact. Old jobs are not rewritten or
replayed. Antigravity uses a separate root and the same schema; opening a root with
the wrong runtime is rejected.

`tasks/0004_management_scopes` registers independent workspace scope IDs for
existing owner paths/modes and converts saved management results to those scopes
and the receiver's saved avatar.
Optional effort remains a JSON string when present; omission preserves the stored
receiver option, while an explicit empty string selects a supported default. The
migration preserves original request payloads for exact retry recovery, assistant/native
thread identities, revisions and task executor configuration. New owner-configured
scopes use the same registry; deleting an assistant does not delete its scope.

## Unified system notification delivery

`control/0004_system_notifications` adds structured notification events,
browser installations, reading leases and per-installation receipts. Push
subscription JSON/fingerprints and private VAPID material have separate tables
owned by the Push service. An activation event sequence prevents delayed consumers
from replaying history to newly enabled installations. Native preferences/claims
keep their existing private JSON format; native delivery does not use the browser
registration/receipt protocol. This migration is new after the released Control
0003 history; it does not import an earlier experimental Push schema.

Existing Control business tables and JSON records are unchanged. The native host
uses its Control database; a standalone browser service keeps a Control database
in its private service root. Lifecycle stream reconnection establishes a fresh
access lifetime without storing business-source baselines or reading history. Browser interrupted sends become uncertain receipts, never an
automatic resend. No retired Push store is imported.

The separate rebuildable notification installation database uses
`ui-notifications/0001_installation.mjs`. It stores device identity/channel/intent,
a logout epoch and one pending navigation target. It does not share authentication
storage or disposable business projection retention. Existing browser preference,
onboarding, token and native authorization records remain intact.

## MCP cutover

`control/0008_mcp_cutover` converts saved old connections to version-1 minimal
unsupported history records, including their last known state. It drops obsolete
invitation, handshake, TCP endpoint and native pairing-key configuration. It
neither imports an OAuth grant nor changes Task phases, reports, receipts or
saved artifact content. Old encrypted pairing secrets are no longer read or
converted by startup.

`tasks/0010_mcp_native_user` adds the human-operation request/result journal and
native profile/delete tombstones. It reuses the existing native backend checks.
Startup and receiver epoch changes fence unfinished requests as unknown; neither
reconnection nor a journal entry authorizes re-execution.

`tasks/0012_mcp_input_mailbox` adds a separate input-disposition receipt and
captures the phase at original mailbox admission. It updates saved claim replies
and receiver journal controls together. Previously admitted inputs are marked
consumed without executing them again; pending inputs remain pending. Original
mutation receipts, Task/run identities and retired work tombstones are retained.
