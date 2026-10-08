---
tags: [OpenClaw, Agent, Session, Architecture]
---

# OpenClaw session management architecture

> Historical research: versions/current refer to review date; [Development plan](../development-plan.md) defines current implementation.

Compiled: 2026-10-02
Category: Technology / Agent runtimes
Related: [Identity/memory research](identity-memory.md)

Pinned official snapshot `a272cf0cd56eb9d0a1245daaaf139902ac32da01`; docs/reset source reviewed, no runtime/all-version claims. Diagrams mechanisms, not source dependency graphs.

## 1. Stable addresses / bounded context

**Gateway owns state**: route Agent/session, queue, read history, assemble, model-tools, persist/reply.[S1][S2]

Six concepts:

| Concept | Responsibility | Boundaries |
|---|---|---|
| agentId | Workspace/identity/config/store | Many sessions |
| sessionKey | Route/concurrency bucket | e.g.agent: main: main, shared context |
| sessionId | SQLite transcript identity | Normal reset may retain, not context generation |
| lifecycleRevision/reset | Lifecycle/history window | Fence stale writes |
| Run/turn | Execution/model-tools | Not permanent session |
| Model context | Actual input | Assembled/token-bounded |

**Permanent chat need not load full history**; durable history/summaries/recall provide continuity.

## 2. ASCII architecture

```text
  [Telegram / WhatsApp / Slack / other channels]
  [Control UI / Mobile / TUI / CLI / RPC clients]
                         |
                         v
  +-------------------- Gateway ----------------------------+
  | A. Authentication + ingress + deterministic routing     |
  |    bindings -> agentId -> dm/group/thread scope          |
  |    -> sessionKey + source/reply route                    |
  |                         |                               |
  |                         v                               |
  | B. Session lifecycle + admission checks                 |
  |    sessionId + lifecycleRevision + reset window          |
  |    reuse / reset / fork / archive / delete               |
  |                         |                               |
  |                         v                               |
  | C. Queue + execution ownership                          |
  |    per-session serial lane -> execution claim           |
  |    -> main / cron / subagent concurrency budget          |
  |                         |                               |
  |                         v                               |
  | D. Runtime + context assembly                           |
  |    history + bootstrap + recall + tools + current input  |
  |    context engine: ingest / assemble / compact / after   |
  |        |                    |                           |
  |        v                    v                           |
  | E. Bounded model view   F. Memory maintenance            |
  |    pruning / summary       private memory flush          |
  |        |                    |                           |
  |        v                    v                           |
  | G. Model/harness <-> Tools   [Workspace memory files]    |
  |    built-in / CLI             MEMORY.md, memory/*.md     |
  |        |                                                |
  |        v                                                |
  | H. Persist results + settle run + reply to source        |
  |                                                         |
  | I. Session tools -> child sessions / other sessions      |
  |    completion & coalesced notices -> requester / Home    |
  +--------------+-------------------------+----------------+
                 |                         |
                 v                         v
  +------------------------------+  +-----------------------+
  | Per-agent SQLite             |  | Archives / retention   |
  | openclaw-agent.sqlite        |  | sessions/ artifacts    |
  | - session rows / metadata    |  | age / entry / disk cap |
  | - transcript event tree      |  | archive, restore, GC   |
  | - reset / compaction markers |  +-----------------------+
  | - durable admitted input     |
  +------------------------------+

  Native harnesses may also own separate native thread/history state.
  Incognito uses process-memory storage rather than durable disk storage.
```


## 3. A: Routing / isolation

### 3.1 Agent first, session second

bindings specificity peer-thread>guild-team-account-channel; selected workspace/store, deterministic host routing/reply source.[S3]

**Shared context differs from shared destination**; phone/Slack DM may main, Slack reply remains Slack, not model guess.

### 3.2 DM scope

| dmScope | Sharing |
|---|---|
| main(default) | All DMs main |
| per-peer | Sender, cross-channel normalization |
| per-channel-peer | Channel+sender |
| per-account-channel-peer | Account+channel+sender |

identityLinks maps canonical peer, affects routes, not credentials.[S1]

Canonical agent:<agentId>: main ignores custom mainKey; Slack Assistant/View DM root-thread buckets even main scope.[S1][S2]

### 3.3 Groups/threads/automation

Default per-group separate history, main/binding override explicit sharing.

```text
main:            agent:<agentId>:main
group:           agent:<agentId>:<channel>:group:<id>
room/channel:    agent:<agentId>:<channel>:channel:<id>
thread:          <base-key>:thread:<threadId>
Telegram topic:  <group-key>:topic:<topicId>
```

Cron new execution session/webhook hook-isolated; keys vary, no prefix-only permissions.[S1][S3]

global scope does not merge different Agent stores/responsibility.

## 4. B: Lifecycle/reset/fork/expiry

### 4.1 Persistent default / optional timed reset

Default reset none, compaction bounds growth; daily/idle/manual new-reset optional.[S1]

| Policy/time | Check |
|---|---|
| Daily | sessionStartedAt/local atHour(default4) |
| Idle | Actual lastInteractionAt |
| Both | First expiry |
| updatedAt | Metadata/list-maintenance, not reset authority |

Heartbeat/cron/exec metadata cannot extend idle; rollover checked next messages, not automatic model at time.[S1][S5]

### 4.2 Actual reset boundaries

**Docs inconsistent**: session/main pages change ID; schema Gateway retains but daily-idle says changes.[S1][S2][S5]

Pinned source: [C1][C2]

- Normal durable reset reuses entry.sessionId/adds boundary/new model window.
- New lifecycleRevision/start/runtime cleanup/counts; model choices may persist.
- Old history stored, not automatically in new window; search/anchors accessible.
- ACP/rebuild/new branches may change IDs; not all resets always same.

```text
sessionKey K -> sessionId S
               [older events ...]
               [reset boundary R1] -> current window starts here
               [new user / assistant / tools ...]

address K and transcript S can stay stable;
the reset window and lifecycle revision change.
```

Therefore cancellation/stale writes need lifecycle, not sessionId alone.

### 4.3 Fork history prefix

New child, parent unchanged. Auto thread/subagent active branch may isolated fallback if large; operator model-capacity overflow rejects, not silently isolates.[S5]

Operator active-parent fork rejected except last-completed excludes unfinished tail. Message fork before user message, puts message in editor.

**Fork new session; reset new window; compaction smaller representation.**

## 5. C: Queues/ownership/durable input

### 5.1 Serial per session, budgeted parallel across


maxConcurrent normal; subagents budget direct controller/maxConcurrent; cron-heartbeat-Swarm separate lanes avoid foreground blocking.

Check identity/lifecycle/owner; reset-delete-cancel fences old writes.**Cancel does not undo tools/committed compaction**.[S6][C2]

### 5.2 Busy input

| Mode | Mechanism |
|---|---|
| steer(default) | Inject safe points, else await current |
| followup | Separate later turns |
| collect | Debounced merge, preserve source routes |
| interrupt | Stop run, latest message |


Merging/delegation retains sender permission ceiling, no borrowing later stronger sender.

### 5.3 Durable input != replayable work

Existing chat.send SQLite before ACK; collect turn/source consumed atomic.[S4]

**Memory queue not auto-replayed**; unexecuted queued input Interrupted/explicit resend, channel ingress separate retry-dedupe.

Distinguish saved/admitted/tools complete/reply delivered; Accepted insufficient.

## 6. SQLite metadata/transcript

Authority on Gateway host: [S8]

```text
~/.openclaw/agents/<agentId>/agent/openclaw-agent.sqlite
    session rows:  sessionKey -> SessionEntry
    transcript:    messages / tool calls / results / boundaries / summaries

~/.openclaw/agents/<agentId>/sessions/
    legacy / archive artifacts
    sessions.json = legacy migration input
```

Entry ID/lifecycle/model-thinking/archive-pin/tokens/compact-flush; transcript append tree id-parentId, not overwrite list.[S5][S8]

| Event | Purpose |
|---|---|
| `message` | user, assistant, toolResult |
| custom_message | Model-visible extension |
| custom | State only |
| compaction | Summary/tail location |
| reset | New window/optional start |
| branch_summary | Navigation summary |

UI/model views differ; reads admission/lifecycle/authority, bounded tails/workers.[S5][S8]

Old JSON+JSONL not active scheme; stop-backup-Doctor explicit migration, no silent import/manual sessions.json edits.

## 7. D-G: Context/model execution

### 7.1 Four lifecycle methods

| Method | Responsibility |
|---|---|
| ingest | Store/index arrivals |
| assemble | Budgeted ordered messages/prompt addition |
| compact | Pressure/manual |
| afterTurn | Persist/index/maintenance |

Optional maintain/subagent boundaries.[S7]

Legacy ingest-afterTurn no-op/assemble pass-through, sanitize-validate-limit runtime, compact summarizer; not extra default DB.

### 7.2 Multiple context assets

Identity/rules/current history/summaries/tools/input/recall; bootstrap budgets/skills on demand.[S7][S10]

Private-main MEMORY.md only, not shared-group merely same Agent; interpret broad fresh-session docs with privacy scope.[S2][S10]


## 8. E: Pruning vs compaction

### 8.1 Old tool outputs

Prune toolResult, retain chat; soft head-tail then placeholders, protect recent/bootstrap.[S9]

Hidden cache-ttl markers replay projection after restart, raw result unchanged; Anthropic API-key server clearing vs eligible client route varies TTL/threshold.

Disable new pruning doesn't erase existing projection until compaction/reset; image replay cleanup not raw deletion.

### 8.2 Old summary/recent tail

Persist compaction event/tail, paired tool boundaries.[S6]

Near budget/provider overflow/manual/byte guard; include system/tools/pending/output reserve, bigger summarizer not bigger frontend window.

Safeguard new default pending asks/exactIDs/quality; failed validation keeps history/fails. Text-only summarizer marks images omitted, not screenshot understanding.


## 9. F: Flush/memory/recall

**Precompact memory flush and summary are separate assets.**

Embedded private detached view may write memory/YYYY-MM-DD.md; maintenance prompts/replies excluded, persistent files, NO_REPLY silence.[S11]

Default per-cycle flush, skip read-only/CLI/heartbeat, optional model. Failure not reset or proof facts saved.

Required preflight before inference, optional after reply/owner settlement; new input cancels/waits maintenance to avoid writers.[S6][S11]

rememberAcrossConversations relevant same-Agent private recall, **not transcript merge**; personal main default allowed, DM-isolation default off unless opt-in; groups not private sources/recipients.[S1][S2]

Routes/tool visibility/workspace memory/recall separate controls; one isolated not all.

## 10. I: Tools/subtasks/Home

### 10.1 Orchestration

| Tool | Mechanism |
|---|---|
| list/status | Visible sessions/state, not all raw history |
| history | Target/page/tool-switch/output limits |
| search | Visible transcripts/location IDs |
| sessions | Settings/reset/delete/archive/groups |
| send | Same-Gateway input/wait-notify modes |
| spawn | New child/receipt runId-childSessionKey |

Send local context address, not external recipient; channel message explicit target.[S12]

Nonthread spawn isolated, thread may fork; fork same-Agent/native restrictions, visible persistent dashboard possible, not all hidden short-lived.

Completion origin normally, collector/direct-thread/quiet owner differs. Receipt accepts, not completes.

### 10.2 Home awareness

Main/Home ordinary session/root role: [S2]

- Creation notices next turn/heartbeat, no immediate wake.
- Per-group activity coalesced conversation notices.
- Home children report Home, other origin own paths.
- Heartbeat wakes accumulated events.

Not full live transcript copy, bounded memory notices/watches grant no access.

Durable signals/stateVersion/changesSince/historyGap separate.[S12]

## 11. Permissions / Incognito

Four visibility modes: [S12]

| Mode | Scope |
|---|---|
| self | Current |
| tree | Spawn subtree, canonical main same-Agent exception |
| agent | Same-Agent |
| all(default) | Gateway, agentToAgent cross-Agent control |

Also profile/allow-deny/sandbox/identity-ACL. Unsandboxed all not privacy; watch/owner/display-name not grants.

Incognito memory-only entry/transcript/compact, expires24h/restart, no extension/archive/auto-flush; stop/clear.[S1]


## 12. Restart/archive/maintenance

### 12.1 Resume started turns

Interrupted active restart may reuse input/recorded tools; completed reply/later user closes replay eligibility.[S1]

Three failed backend starts exhaust budget; actual start resets, accepted-queued-preparing not success. History retained, new session optional.

Different from unstarted memory queues; no universal exactly-once, unsettled effects tool-specific.

### 12.2 Age/count/disk

Common defaults: [S1][S13]

| Parameter | Default | Purpose |
|---|---|---|
| mode | enforce | warn only reports |
| pruneAfter | 30d | Eligible archive, disposable automation deletion |
| archiveDashboardAfter | 7d | Idle dashboard archive |
| maxEntries | 5000 | Unarchived target |
| maxDiskBytes | 10gb | DB/WAL/artifacts budget |
| preserveRecent | false | Optional interaction protection |

Over cap normal eligible archive/synthetic delete, protect active/admitted/pins/model locks/external pointers; target not absolute.

Disk cheaper artifacts/unreferenced history/validated compressed archives before DB deletion. Cap archives may GC; manual-age-recovery protected differently, WAL counted.

**Archive/reset/compact/prune/delete/memory-delete differ**: lists-lifecycle/window/representation/tool projection/storage/memory sources.

## 13. Clients / coding attachments

UI/mobile/TUI/Gateway CLI same state, no transcript copies; remote SQLite on remote host, local directory not authority.[S14]

resolve key-ID-label-links visibility, canonical key CLI/TUI.

attach temporary session MCP grant/revoke on exit; links identify, independent auth/ACL. Local embedded chat not Gateway attachment.

## 14. Message flow

Telegram question/Web follow-up:

1. Auth ingress/bind Agent/defaultDM main, retain Telegram reply.
2. Check existence/reset/ID-lifecycle-window.
3. Lane/budget/busy queue policy.
4. SQLite/model window/bootstrap-memory-tools/maintenance as needed.
5. Model-tools/persist/reply Telegram/settle.
6. Optional maintenance after owner, new input cancel-settle.
7. Web same key/shared state, not transcript copy.
8. Prune-compact views; reset new window, authorized old search.

**Gateway authority/stable addresses vs lifecycle/raw history vs views/foreground vs maintenance ownership.**

## 15. Evidence / sources

All same pinned snapshot, do not transfer defaults across versions.

- [S1 Session management](https://github.com/openclaw/openclaw/blob/a272cf0cd56eb9d0a1245daaaf139902ac32da01/docs/concepts/session.md): scope, reset, recovery, incognito, maintenance.
- [S2 Main session](https://github.com/openclaw/openclaw/blob/a272cf0cd56eb9d0a1245daaaf139902ac32da01/docs/concepts/main-session.md): Home, group notices, background completion.
- [S3 Routing](https://github.com/openclaw/openclaw/blob/a272cf0cd56eb9d0a1245daaaf139902ac32da01/docs/channels/channel-routing.md): Agent/key/replies.
- [S4 Queue](https://github.com/openclaw/openclaw/blob/a272cf0cd56eb9d0a1245daaaf139902ac32da01/docs/concepts/queue.md): lanes/input/restart.
- [S5 Session schema](https://github.com/openclaw/openclaw/blob/a272cf0cd56eb9d0a1245daaaf139902ac32da01/docs/reference/session-management-compaction/schema.md): ID, metadata, fork, reset/transcript events.
- [S6 Compaction](https://github.com/openclaw/openclaw/blob/a272cf0cd56eb9d0a1245daaaf139902ac32da01/docs/concepts/compaction.md): budgets/native/cancel.
- [S8 Store](https://github.com/openclaw/openclaw/blob/a272cf0cd56eb9d0a1245daaaf139902ac32da01/docs/reference/session-management-compaction/store.md): SQLite/migration/workers.
- [S9 Pruning](https://github.com/openclaw/openclaw/blob/a272cf0cd56eb9d0a1245daaaf139902ac32da01/docs/concepts/session-pruning.md): projection/server-client/images.
- [S10 Workspace](https://github.com/openclaw/openclaw/blob/a272cf0cd56eb9d0a1245daaaf139902ac32da01/docs/concepts/agent-workspace.md): bootstrap/private memory.
- [S11 Flush](https://github.com/openclaw/openclaw/blob/a272cf0cd56eb9d0a1245daaaf139902ac32da01/docs/reference/session-management-compaction/housekeeping.md): private view/NO_REPLY/conditions.
- [S12 Tools](https://github.com/openclaw/openclaw/blob/a272cf0cd56eb9d0a1245daaaf139902ac32da01/docs/concepts/session-tool.md): visibility/spawn/stateVersion.
- [S13 Maintenance](https://github.com/openclaw/openclaw/blob/a272cf0cd56eb9d0a1245daaaf139902ac32da01/docs/reference/session-management-compaction/maintenance.md): retention/caps/protections.
- [S14 Attachment](https://github.com/openclaw/openclaw/blob/a272cf0cd56eb9d0a1245daaaf139902ac32da01/docs/concepts/session-attachment.md): clients/grants.
- [C1 initSessionState](https://github.com/openclaw/openclaw/blob/a272cf0cd56eb9d0a1245daaaf139902ac32da01/src/auto-reply/reply/session.ts#L805): normal-ACP reset; boundary from line997.
- [C2 reset service](https://github.com/openclaw/openclaw/blob/a272cf0cd56eb9d0a1245daaaf139902ac32da01/src/gateway/session-reset-service.ts#L1040): boundary/same ID1101/lifecycle1122.

Next test backend reset-compact/restart unsettled effects/combinations of four multiuser controls.
