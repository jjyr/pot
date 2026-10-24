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
       embedded SQL -> transactional sql-migrate
                    |             |
                    |       SQL + version receipt
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

[`sql-migrate`](https://github.com/rubenv/sql-migrate) executes each file and its
`pot_migrations` receipt in one transaction. Receipts contain `id` and `applied_at`;
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

The frontend's rebuildable Chat cache has a separate IndexedDB sequence in
`pkg/storage/migrations/ui-cache/`. Its existing `0001_chat_cache.mjs` remains the
initial version. Browser and Wails asset hosts expose only these JavaScript
migrations, never backend SQL. Clearing browser storage does not change
authoritative history or initiate work.

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
constraints and failure/retry. A failing file rolls back its SQL and receipt;
earlier committed files remain. Database files migrate independently. Same-process
migration calls are serialized; product roots retain their exclusive process
lock. External callers must provide equivalent process exclusion.

Before future upgrades, stop all processes using the root and take a consistent
complete-root backup with the matching binary. The [self-update path](updates.md)
requires an identical migration fingerprint for package-only rollback. Restore
the complete pre-upgrade backup and binary together to roll back a schema change.
Never force an older binary to accept newer data or erase migration history.

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
