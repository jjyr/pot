---
tags: [Architecture, Agent, Hermes, Session]
updated: 2026-10-02
---

# Hermes session management architecture

> Historical research: versions/current refer to original review date. [Development plan](../development-plan.md) defines implementation, not recommendations/old state here.

Three layers: **entry routing selects conversation, persistence retains identity/history, context engine selects this turn's model input.** Agent runtime can cache/release/rebuild; its lifetime differs from session lifetime.

NousResearch Hermes Agent, pinned `6dbb7b9ab857842617c2c35b63b49bd6ee77fc56` from [earlier research](identity-memory.md), reviewed2026-10-02. Source/docs analysis, no runtime tests or all-version claims.

## 1. ASCII architecture

Responsibility view: CLI/Gateway share persistence/context; CLI bypasses Gateway chat routing.

```text
 Telegram / Discord / Slack / ...                     CLI / TUI
                 |                                       |
                 v                                       v
 +--------------------------------+        +-----------------------------+
 | A. Adapter + RoutingIdentity   |        | New / Continue / Resume      |
 | profile, platform, chat,       |        | session ID / title / cwd     |
 | thread, participant            |        +--------------+--------------+
 +---------------+----------------+                       |
                 v                                        |
 +--------------------------------+                       |
 | B. SessionStore                |                       |
 | session_key -> session_id      |                       |
 | create / recover / reset /    |                       |
 | switch; persisted route state  |                       |
 +---------------+----------------+                       |
                 v                                        |
 +--------------------------------+                       |
 | C. Gateway turn coordination   |                       |
 | per-session turn lease         |                       |
 | load -> run -> flush           |                       |
 | pre-agent session hygiene      |                       |
 +---------------+----------------+                       |
                 +--------------------+-------------------+
                                      v
                     +------------------------------------+
                     | D. Agent runtime / prompt assembly |
                     | session prompt + memory snapshot   |
                     | live messages + per-turn inputs    |
                     | model calls <-> tools              |
                     +-----------------+------------------+
                                       |
                     +-----------------v------------------+
                     | E. ContextEngine                   |
                     | built-in ContextCompressor         |
                     | summary + anchors + recent tail    |
                     | or plugin / provider-native path   |
                     +-----------------+------------------+
                                       |
                 read / append / compact / usage / events
                                       |
             +-------------------------v-------------------------+
             | F. SQLite state.db (canonical session storage)   |
             | sessions | messages | messages_fts | routing      |
             | prompt snapshots, usage, recovery/commit state    |
             | live rows + soft-archived rows                    |
             +-----------+------------------------+-------------+
                         |                        |
                         v                        v
          +---------------------------+  +---------------------------+
          | G. session_search         |  | Display / export / prune  |
          | FTS discovery / scroll /  |  | history views & retention |
          | read / browse             |  +---------------------------+
          +------------+--------------+
                       | selected evidence -> Agent tool result

 +--------------------------------------------------------------+
 | H. Cross-session knowledge                                   |
 | SOUL.md / AGENTS.md | MEMORY.md / USER.md | skills / providers |
 | snapshot/context -> D; memory tools & review -> durable writes |
 +--------------------------------------------------------------+

 Gateway compatibility mirror: sessions/sessions.json
                               (routing mirror, not full history)
```

## 2. Five distinct states

| State | Meaning | Lifetime / boundaries |
|---|---|---|
| session_key | Deterministic entry address | Usually stable, even /new |
| session_id | Persistent conversation identity | Default compaction/restart/resume same, new session changes |
| SessionEntry | Current target/routing | Activity/resume/model overrides/prompt pins |
| Agent runtime | Model/tool executor object | Cached/rebuilt, release does not end persistence |
| active context | Current model projection | Compacted, not all database history |

Telegram DM `agent:main:telegram:dm:123` createsS1, restart/compactS1 with old messages outside live context;/new same key->S2.

parent_session_id alone does not imply inheritance; legacy compaction/branches/reset share ancestry. Recovery/search also need relation/end reason.

## 3. A: Entry identity / isolation

Gateway resolves sources/build_session_key.[S2][S3]

| Scenario | Key / rule | Effect |
|---|---|---|
| Default profile | agent: main:... | Legacy compatibility |
| Named profile | agent:<profile>:... | Separate profiles; named main uses main~ |
| DM | agent: main:<platform>: dm:<chat_id> | Per-DM, missing chat falls back sender |
| Group/channel | ...:<chat_id>:<user_id> | group_sessions_per_user=true |
| Thread/topic | ...:<chat_id>:<thread_id> | Shared thread by default, optional per-user |
| Slack | Workspace/scope in key | Avoid collisions |
| WhatsApp | Canonical identifier | Phone/LID normalization where mapped |

Receiving bot/profile differs from executing runtime profile; delegation must preserve correct delivery bot.

**Transcript isolation**, not memory isolation. Same-home MEMORY.md/USER.md sharing separate; design multiuser layer.[S8]

## 4. B: Creation / continuation / reset / recovery

### Lookup / create

Per-key get_or_create single-flight avoids duplicate IDs/rows; read routes/end-delete/legacy successors/recover/create-update.[S2]

touch_activity=False internal updates avoid pretending user activity, affects resume freshness/pruning.

### Explicit boundaries

```text
 no route -- first message --> S1
 S1 -- user message / gateway restart --> S1
 S1 -- default in-place compression --> S1 (smaller live context)
 S1 -- /new or /reset --> S2 (same routing key, fresh context)
 S2 -- /resume S1 --> S1 (route switches to stored conversation)
```

reset new ID/key, target old session_reset. No full history inheritance; memory/search bridge. Pre-reset memory/skill saves depend model/config/approval, not guaranteed.[S1][S2][S8]

switch ends old/reopens target/rebinds; expected_session_id CAS protects later /new-resume from old async writes.

### No core time reset

Snapshot Gateway **does not reset on idle/day/restart**. Old time policies ignored; idle/daily/both via hermes-session-reset-policy plugin.[S1][S4]

suspended means replacement on lookup; stop can break stuck resume loops, not always one-call cancel/same-ID preservation.

### Crash / restart

Routes/source/activity select persistent sessions; intentional reset boundary cannot revive older ones. Ended/reset/deleted distinguish recovery.[S1][S5]

active_turn_token/start markers clear token-matched on success, remain after crash. resume_pending same-ID differs from suspended replacement.

Default one-hour freshness limits proactive restart execution, **not history retention**; later user can load original.

## 5. C: Turn coordination / concurrency

Load-run-flush races need locks beyond individual DB operations.[S6]

| Protection | Scope | Problem solved |
|---|---|---|
| Single-flight | Gateway same-key lookup/create | Duplicate sessions |
| Turn lease | Gateway resolved session_id serial load-run-flush | Different entries sharing ID cannot interleave stale histories |
| Token/generation | Cleanup/routes/prompt pins | Late old cleanup cannot release/overwrite new |
| Compaction snapshot/fence/watermark | Summary compute/commit | Stale summary cannot erase new messages |
| SQLite WAL/transactions/retries | Persistence | Concurrent reads/serial writes/transient failures |

Lease registry **process-local/single event loop**, not distributed CLI-Gateway lock. DB has own protection; shared home not generally safe, official one-Agent/home.[S6][S8]

## 6. D: Prompt / memory snapshots / runtime caches

Session prompt snapshot + live history + explicit turn injection, not all history every turn.[S1][S8]

- SOUL.md identity, AGENTS.md project rules.
- MEMORY.md/USER.md frozen start snapshots, 2200/1375-character defaults.
- memory tool writes files/response sees change, but initial block not automatically replaced.
- prompt_pin persists last-human dynamic channel/session inputs for internal turns, stable restart/callback context.[S7]
- Ephemeral IDs/voice flags in current message, reduce prefix cache misses.

Runtime object cache/prompt freeze/provider prefix cache differ; invalidating one does not erase history.

/new natural boundary refreshes memory/rules/reduces old context; machine restart is not boundary.

## 7. E: Context compaction

### Two layers

| Layer | When | Trigger | Responsibility |
|---|---|---|---|
| Gateway hygiene | Before Agent | 85%/hard message guard | Avoid oversized history overflow |
| Agent compressor | Model/tools | Usage+new-message estimates/config | Routine management |

Default threshold0.50 but <512000 windows floor0.75, CodexOAuth85% cases/absolute caps/helper feasibility.**Resolved model/window/config determines actual threshold, not uniform50%**; Gateway85% distinct.[S9][S10]

Usage anchor provider prompt-completion+later estimates, persistent/validated. Without reliable anchor some prechecks wait usage; Gateway coarse fallback/overflow bounded retries.

### Default in-place

```text
 Before, session S1:
   [head] [long middle: user / assistant / tools] [recent tail]

 After, same session S1:
   LIVE:     [head] [summary + anchors + recovery pointers] [tail]
   ARCHIVED: pre-compaction rows, retained for history/search
```

compression.in_place=true rewrites same-ID live messages, soft-archives searchable history; **smaller input, not deletion**.[S9]

False legacy rotation creates new ID/parent relation/latest route; old overview child-session statements not default current implementation.

### Summary / protected tail

Prune old tool output, summary area/protected tail, structured goals/constraints/progress/decisions/files/next steps; tool call-result boundaries avoid orphan results.

lean tail ~2.5% main window, 10K-25K clamp/small-window20% cap, not final total hard limit. Exact anchors/user quotes/retrieval pointers maintain continuity; update old summaries rather than stack endlessly.

Cooldown/bounded retries/commit guards; failure keeps history/aborts or deterministic fallback depending type/config. No perfect summaries or all-stored-content comprehension guarantee.

### Plugins / native ownership

ContextEngine judges/runs compaction/token counts, explicit context.engine plugins; native paths also exist: [S9]

- Codex App Server owns real thread, request native compact; local mirror edits insufficient.
- Supported Responses opt-in server compaction retains opaque replay checkpoints.

Identify actual context owner first.

## 8. F: Persistence / views / cleanup

| Store | Contents | Authority |
|---|---|---|
| $HERMES_HOME/state.db | Sessions/messages/FTS5/usage/prompts/state | Persistent authority |
| gateway_routing | Key->ID/origin/recovery | Routing authority |
| sessions/sessions.json | Compatibility routing mirror | Not complete list, optional writes |
| sessions/saved/*.json | User snapshots | Export, not routes |
| memories/*.md / skills | Curated knowledge/procedures | Separate transcripts |

Gateway routing home differs from owning execution-profile transcript DB; do not write all profiles through startup-fixed handle.[S11]

Canonical read failure throws TranscriptReadError, not empty-history fresh inference; distinguish absence/failure.[S12]

Docs retire normal JSONL but partial DB-failure legacy code remains; SQLite normal authority, not legacy files or complete degraded guarantee.

### Retention / cleanup

Default auto_prune=true/startup ended inactive90days; active Gateway sessions not age-deleted. Legacy lineages/pins/active turns protected.[S1]

VACUUM disk reclaim/optimize storage, not model compaction. Dead automation sessions end before retention.

Markdown-HTML display exports may include archives; JSON-JSONL live-context exports live-only rules. Check format before backup/audit.

## 9. G/H: Search / cross-session learning

session_search actual messages, no LLM summary; four modes: [S1][S13]

| Input | Behavior | Use |
|---|---|---|
| query | FTS5/lineage dedupe/match windows | Find prior discussions |
| session_id + around_message_id | Scroll around anchor | Recover surrounding evidence |
| session_id | Bounded long-session view | Review task |
| None | Recent sessions | Recent work |

Default user-assistant; add tools for debugging. Retrieved tool response alone reenters context.

Long-term learning writes memory/skills at review/boundaries; may disabled/budget-limited/pending approval. Review executed differs from persisted knowledge.[S8]

Three continuities:

1. **Same session**: task/live context.
2. **New + core memory**: small stable knowledge.
3. **Search**: specific current evidence.

## 10. CLI resume / cross-entry handoff

| Action | Mechanism |
|---|---|
| hermes -c | Terminal breadcrumb, else latest CLI |
| --resume id/title | Persistent session/latest legacy successor |
| --resume latest --in ./dir | Latest workspace/pinned directory |
| Normal resume | Restores cwd, optional --no-restore-cwd |
| /sessions | SQLite picker, not sessions.json |
| /handoff telegram | Target binds existing ID, not unrelated copy |

Configured platform/home channel, after turn; new thread/topic where supported, confirmation-summary turn. Gateway down/thread-send failure paths; multiuser nonthread restrictions differ from DM.[S1]

## 11. Personal Agent product implications

- Persistent UI timeline differs from durable sessions/model contexts/knowledge boundaries.
- Small memory/raw history/search still needs explicit current task state.
- Preserve evidence/exact IDs/recovery, prevent stale compaction overwrites.
- Coordinate resolved conversation IDs, not only entry keys.
- Permanent chat needs memory refresh/execution epoch beyond voluntary /new; epoch is recommendation, not Hermes mechanism.

## Sources

All pinned same snapshot; overview/examples differ slightly, prefer actual defaults/routes/boundaries.

- [S1 Sessions](https://github.com/NousResearch/hermes-agent/blob/6dbb7b9ab857842617c2c35b63b49bd6ee77fc56/website/docs/user-guide/sessions.md): resume/handoff/search/routes/continuity/storage/prune.
- [S2 gateway/session.py](https://github.com/NousResearch/hermes-agent/blob/6dbb7b9ab857842617c2c35b63b49bd6ee77fc56/gateway/session.py): `SessionEntry`, `build_session_key`, lookup/create/reset/switch.
- [S3 session_identity](https://github.com/NousResearch/hermes-agent/blob/6dbb7b9ab857842617c2c35b63b49bd6ee77fc56/gateway/session_identity.py): profile/transport-runtime identities.
- [S4 lifecycle](https://github.com/NousResearch/hermes-agent/blob/6dbb7b9ab857842617c2c35b63b49bd6ee77fc56/gateway/session_lifecycle.py): suspension/turn markers/freshness.
- [S5 recovery](https://github.com/NousResearch/hermes-agent/blob/6dbb7b9ab857842617c2c35b63b49bd6ee77fc56/gateway/session_recovery.py): recovery/reopen.
- [S6 lease](https://github.com/NousResearch/hermes-agent/blob/6dbb7b9ab857842617c2c35b63b49bd6ee77fc56/gateway/turn_lease.py): process-local session serialization.
- [S7 prompt pin](https://github.com/NousResearch/hermes-agent/blob/6dbb7b9ab857842617c2c35b63b49bd6ee77fc56/gateway/session_prompt_pin.py): persistent pins/fencing.
- [S8 Memory](https://github.com/NousResearch/hermes-agent/blob/6dbb7b9ab857842617c2c35b63b49bd6ee77fc56/website/docs/user-guide/features/memory.md): frozen budgets/review/writes.
- [S9 Compaction/caching](https://github.com/NousResearch/hermes-agent/blob/6dbb7b9ab857842617c2c35b63b49bd6ee77fc56/website/docs/developer-guide/context-compression-and-caching.md): dual layers/in-place/lean/native ownership.
- [S10 compressor](https://github.com/NousResearch/hermes-agent/blob/6dbb7b9ab857842617c2c35b63b49bd6ee77fc56/agent/context_compressor.py): thresholds/summaries/archive/commit guards.
- [S11 persistence](https://github.com/NousResearch/hermes-agent/blob/6dbb7b9ab857842617c2c35b63b49bd6ee77fc56/gateway/session_persistence.py): SQLite routes/JSON/profile DB.
- [S12 transcript](https://github.com/NousResearch/hermes-agent/blob/6dbb7b9ab857842617c2c35b63b49bd6ee77fc56/gateway/session_transcript.py): append/retry/load-rewind.
- [S13 search](https://github.com/NousResearch/hermes-agent/blob/6dbb7b9ab857842617c2c35b63b49bd6ee77fc56/tools/session_search_tool.py): actual messages/bounded reads.

Related: [Identity/memory research](identity-memory.md), [Personal Agent product](../product-design.md).
