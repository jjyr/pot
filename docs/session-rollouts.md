# Session rollouts

Model context history is an append-only, versioned JSONL journal. SQLite retains
conversation projections, task state, operational receipts and the committed
journal reference. UI message events are still queried from SQLite; a rollout
is the agent's recovery context, not a replacement for admission or delivery
receipts.

## Layout and records

An assistant stores `sessions/<database-name>.jsonl` beside its conversation
database. Each Task stores one `sessions/tasks/<identity-hash>.jsonl` beside
`tasks.sqlite`. The filename hashes the runtime/task tuple, never the run ID.
Continuations and retries append to this same session; external identifiers
cannot traverse paths.
These files belong to durable node storage, never a task workspace or cache.

The first record is a context checkpoint. Later records contain changed fields,
removed fields and array prefix/append edits. Growing message and tool-receipt
arrays therefore store their new entries rather than another full history.
Context compaction, pending-call changes and replacements use the same format;
replay reconstructs the exact JSON value, including provider-native fields.
Object key ordering is canonicalized. Each record has a format version, sequence,
timestamp, previous hash and content hash. Old migrated snapshot timestamps are
preserved. JSON formatting and property order are not recovery identities.

## Commit and recovery

```text
owner SQLite write transaction
  -> validate committed journal reference
  -> append journal delta and fsync
  -> commit new reference with owner state/receipts

restart
  -> read committed reference
  -> verify hash chain and replay through that reference
  -> ignore uncommitted tail; next write or compression reconciles it
  -> apply ordinary uncertain-effect recovery fences
```

Database writers serialize updates and file locks serialize journal representation
changes. A crash after a journal append but before SQL commit cannot advance
accepted state. Missing or corrupt committed records fail explicitly. Recovery
never substitutes an empty context and never treats an uncommitted tool result
as permission to retry or complete execution.

SQLite stores the Task session's latest committed sequence/hash separately from
each run's checkpoint reference. Every append uses the Task head, even if a new
run needs a fresh context because its configuration changed. A run's reference
still reconstructs its historical checkpoint without moving the session head or
truncating later records. The head and current run reference commit together;
older runs cannot append over the current run. Ordinary context reuse and
uncertain-effect recovery checks still apply across run boundaries.

Keep the SQLite databases and their `sessions/` directories together in backups.
Copying a database alone does not preserve its referenced context. Back up a
stopped node or use a coordinated snapshot of both representations.

## Compression

An assistant turn or task run reaching a durable terminal state signals one
background worker per Runtime. Signals coalesce into at most one pending pass.
There is no startup/open compression or periodic compression timer. Waiting for
approval does not trigger a pass. Migration on open remains a separate operation.

Each pass compresses at most two files, using zstd's default compression level,
one encoder worker, and reads paced to 8 MiB/s. Candidate scans examine at most
32 assistants and 32 Task sessions, use
rotating cursors, and alternate priority between assistants and tasks. Remaining
backlog waits for another completion signal. Only inactive assistant sessions
and Task sessions cold for seven days are eligible. Task scanning operates on
sessions, not individual runs. A Task with any active, waiting or recovery-required
run is excluded; publication rechecks the Task head and activity after preparation.

Preparation and verification happen outside the database write transaction.
Publication briefly serializes with owner updates, rechecks the committed
reference and inactivity, and takes a nonblocking file lock. If the session has
resumed or changed, the prepared archive is discarded. Runtime shutdown cancels
preparation and waits for the worker before closing databases. Errors are logged;
retries require a later completion signal.

Compression writes and syncs a temporary archive, verifies its uncompressed
content, atomically publishes `.jsonl.zst`, and only then removes the plain file.
Reading supports either representation. A resumed append restores the plain file
atomically before writing. If interruption leaves both forms, the plain journal
is authoritative. No TTL deletion or history pruning is performed.

## Existing data

Conversation migration `0005_session_rollouts` stages the old context-history
rows without deleting them. Store startup streams them into the journal, then
appends the current checkpoint. Only after the journal is durable does one SQL
transaction publish its reference and remove the old snapshots. A persisted
reclamation flag makes the subsequent SQLite `VACUUM` retryable after a crash.
This one-time import needs temporary free space and can take time on large roots.

Task migrations `0016_session_rollouts` and `0017_task_sessions` upgrade run records
to schema 10 and add Task-owned session heads. Startup exports existing inline
checkpoints in run order into one journal per Task before replacing them with
committed references. It preserves run identity, terminal receipts, continuation context
and uncertain-effect fences. A migration receipt also triggers one-time SQLite
space reclamation after successful export; interruption retries that step. It
cannot reconstruct older task checkpoints that
the previous format did not retain.

Do not downgrade the executable against migrated data. Restore the full backup
and its matching binary if rollback is required.
