---
tags: [Agent, Session, Architecture, Research]
updated: 2026-10-02
---

# Session and task coordination in long-lived Agents

> Historical research: versions/current refer to review date; [Development plan](../development-plan.md) defines implementation.

## 0. Scope/evidence/conclusions

How long-lived assistants manage sessions/context/backgrounds. **Compare existing mechanisms/public behavior only, no Pot design/decision changes.**

[Hermes](hermes-sessions.md), [OpenClaw](openclaw-sessions.md), pinned sources/Dot Tasks-memory/Controls; additional same-snapshot Hermes Delegation/Cron for completion/steering/schedule continuity.

| System | Evidence | Cannot infer |
|---|---|---|
| Hermes |6dbb7b9ab857842617c2c35b63b49bd6ee77fc56 source/docs |No runs/later defaults |
| OpenClaw |a272cf0cd56eb9d0a1245daaaf139902ac32da01 source/docs |No runs/old JSONL-daily defaults cannot override |
| Dot |2026-10-02 saved official docs/review |No public stores/thresholds/algorithms/scheduler/recovery |

Facts from evidence, inferences labeled; task general work, **not same schemas/first-class objects**.

## 1. Long-lived is not infinite context

**Inference**: none accurately one-model-all-history-forever, evidence depth differs.

| Layer | Question | Evidence |
|---|---|---|
| Identity/knowledge |Across conversations? |Hermes files/OpenClaw memory/Dot notes-ChatGPT memory |
| Entry/history |Continue where? |Key-ID/key-lifecycle/Dot same assistant separate visible chats |
| Turn context |Actually sees? |Live summaries/snapshots/assembly-pruning/Dot selected |
| Run/turn |When execution ends? |Leases/lanes/Dot completed != goal |
| Background/triggers |Who advances without chat? |Delegation-cron/spawn-heartbeat/Dot schedules-events |

Persistent session != full inference history; memory != still-running process.[H1][H2][O1][O2][D1]

## 2. Hermes: stable session/bounded context/async results

### 2.1 Continuation

**Fact**: profile-platform-chat-thread key->ID; runtime rebuild/restart not new conversation.[H1]

Core no daily/idle/restart reset; plugin time policy separate.

Explicit new-reset new ID, same key; default compact same ID, resume rebind; stop-suspended may replace.

```text
Same entry key
  -> Persistent S1
      -> User turn
      -> Compact:S1/smaller live context
      -> Restart:S1/rebuilt runtime
  -> Explicit reset:S2
```

### 2.2 Growing history

**Fact**: in-place live rewrite/soft archives/search.[H2]

Head/structured summary-anchors-pointers/tail, not full transcript; goals-constraints-progress-decisions-files-next/tools paired.


**Memory differs from summary**: start-frozen MEMORY-USER, writes not replace current block; search fragments only reenter.[H1][H3]

### 2.3 Delegation context

**Fact**: delegate fresh child, no automatic parent history/tools; explicit goal-context.[H4]

Not wholly blank: workspace project rules may prompt, not transcript copy.

Inherited enabled tools minus memory writes/user clarification/cross-messages/cron, etc; leaf/orchestrator delegation differ.

**Inference**: briefings/results connect contexts, not repeated full tool logs; live transcripts inspectable.

### 2.4 Chat while working

**Fact**: detached-capable top-level async handle, parent continues; default batch waits all/results between turns.[H4]

Optional independent_completions/groups change delivery grain, **not dependencies/order**; execution parallel.

Orchestrator waits its workers; synchronous fallback one-shot/cron, not universally immediate.

### 2.5 Guidance/completion/cancel

**Fact**: spawn-tree list-steer-stop by ID, follow-up doesn't auto-cancel.[H4]

Next iteration boundary, not active tool interruption; queued != read, pending-missed retained, post-end steer rejected.

Completion state.db/fresh-turn queue; adapter ACK admission not model/reply complete; API-server may timeline-only/client next turn.

Undelivered retry at-least-once after restart; **running child not auto-restored**, owner loss unknown. Stop/reset/close affects owned children, not Dot Pause.

Child-owned background processes terminate unless explicit handoff; PID alone not transfer.

### 2.6 Cron not waiting in main

**Fact**: persistent prompt/schedule/skills/destination, fresh sessions/no full previous chat.[H5]

Continuity mechanisms:

- Self-contained prompt/skills.
- context_from existing upstream output, no waiting same-tick upstream/not strictDAG.
- continuity prior substantive output injected, not whole session.
- Optional attach/mirror(defaultoff) makes results continuable; new thread if supported, DM mirror.

**Delivered notice != recipient-session awareness**; continuable delivery explicitly bridges.

Job/output/ledger separate; interrupted unknown/no rerun, next schedule works. Errors may recovery context; upstream success filtering needs source review.

## 3. OpenClaw: rolling main/children/events

### 3.1 Persistent state

**Fact**: Gateway key/transcript/execution/routes, defaultresetnone, optional daily-idle.[O1]

Normal reset may retainID/newboundary-revision-window; source corrects docs, ACP branches differ.

ID alone not context continuity; address/transcript/window lifetimes separate.

### 3.2 Bounded continuity

**Fact**: window/bootstrap/tools/input/memory, not entire UI history.[O3]

- Pruning old tool projection, raw retained.
- Compaction summary/tail, pairs/pending asks/exactIDs.
- Flush independent memory opportunity, **not summary asset**.
- Other-private recall conditional, no merge/private scopes.


### 3.3 Coordination

**Fact**: spawn receipt/send/history-search-status authorized.[O4]

Nonthread isolated, thread may fork with runtime/same-Agent/capacity, not always Hermes fresh briefing.

Lane/claim serial per session, budget across/subagents-cron-heartbeat, foreground needn't hold all.[O5]

**Busy input**steer(default)/followup/collect/interrupt, safe points, no instant tool-stop guarantee.

### 3.4 Home awareness

**Fact**: ordinary root-role, not infinite all-transcripts.[O2][O4]

- Origin completions normally, quiet-collector-thread exceptions.
- Creation notice not necessarily wake.
- Coalesced group notices, not full logs.
- Heartbeat consumes accumulated events.
- History/search/send demand-based, notice no access grant.

**Inference**: coordination metadata in main, execution histories separate, not always fully read.

### 3.5 Schedules/recovery/reliability

**Fact**cron fresh execution vs heartbeat main, not rerun all main; not all payload branches reviewed.[O1][O2]

Started turn recovery recorded inputs/tools; unstarted memory queue not all replayed, save-admit-tools-deliver distinct.[O1][O5]

Bounded-memory notices vs durable signals/version, not universal queue; receipt accepted not achieved, cancel no undo.

## 4. Dot: public collaboration semantics, not algorithms

### 4.1 Continuity

**Official facts**: [D1]

1. Same dot multiple responsibilities/priorities/follow-up.
2. Current chat/relevant ChatGPT memory/own notes.
3. Notes preferences-decisions-work, not full transcript/ChatGPT saved memory.
4. Selected context per interaction may differ background.
5. Cross-channel same notes, separate visible chats/no automatic copies.

**Unknown**IDs/reset-epochs/compact/storage/live-note refresh; long-lived assistant not implementation disclosure.

### 4.2 Tasks

**Official facts**: [D1]

- Parallel backgrounds report, user chat continues.
- Separate visible threads/follow-up.
- Task instructions/context/own chat, **not all parent history**.
- Activity progress/files/results/input/direct guidance, continues away.
- Original computer ownership, no automatic move on main computer change.
- Completed run not guaranteed requested result/delivery; inspect errors.

**Inference**main/notes relationship, task contexts/results/guidance, not proof same internal structures.

### 4.3 Progress without chat/stop scope

**Official facts**: [D1][D2]

- Assigned work between chats, pause/wake/time-supported events.
- Saved schedule instructions/time-zone/conditions/destination; service connection not monitor creation.
- Proactive read/private notes/findings, cannot send/edit/control browser-computer.
- Pause main only, delegated stop/schedule cancel separate.

**Unknown**event store/heartbeat/recovery/dedupe/all-stage steering; no exactly-once/crash-lossless inference.

## 5. Comparison

| Question | Hermes | OpenClaw | Dot |
|---|---|---|---|
| Long-term identity |Files/entry sessions |Main/Home/channel-children |Same dot/separate channel chats |
| Daily reset default |No core |None/configurable |Unknown |
| Large history |Head-summary-tail/search |Window/prune-compact/recall |Selected/unknown algorithm |
| Compaction=forget? |No/soft archive |No/raw vs views |Notes not full/storage unknown |
| Child inheritance |Fresh explicit briefing/rules |Isolated or constrained fork |Task context/not full |
| Main unblocked |Async handle/results between turns |Children/lanes/completion |Official parallel chat |
| Guidance |Child steer/queue vs read |Send/steer safe/authorized |Follow-up/Activity |
| Completion context |Durable->delivery/parent, API exception |Owner/notices/heartbeat |Public report, internal unknown |
| Schedule continuity |Definition/fresh/output/mirror |Cron vs heartbeat |Definition/notes/context, unknown |
| Pause all? |Stop-reset owned children |Target/mode-specific |Explicitly no |
| Restart |History/pending delivery, child unknown |Active recovery/unstarted not all |Unknown |

## 6. Supported/unsupported conclusions

### 6.1 Supported inferences

1. **Entries/identity/history/relationships persist; inputs bounded/selected**. Mechanisms H/O, product evidence Dot.
2. **Main continuity needn't shared task context**, different inheritance.
3. **Dispatch/report/guidance/wake**, not infinite main compute.
4. **History/summaries/memory/schedules separate**; memory goal not executable schedule, notice not full awareness.
5. **Availability/background lifetimes separate but related**; H stop descendants, Dot Pause differs.
6. **Stages matter**; accepted-start-complete-deliver-achieve/unknown-at-least-once not exactly-once effects.

### 6.2 Unsupported

- Not all periodic new sessions; H/O defaults no time reset.
- Not permanent single context; compact/windows/selected.
- Not all tasks inherit full history.
- Not all completions automatically consumed; API/mirror/notices conditions.
- Dot notes don't disclose retrieval/rotation/scheduling.
- Pot epoch/memory/control recommendations not existing mechanisms of these systems.

## 7. Evidence index

Source locators, not tests; defaults pinned.

- [H1 Sessions](https://github.com/NousResearch/hermes-agent/blob/6dbb7b9ab857842617c2c35b63b49bd6ee77fc56/website/docs/user-guide/sessions.md): resume/reset/search, source corrections Hermes4-5.
- [H2 Compaction](https://github.com/NousResearch/hermes-agent/blob/6dbb7b9ab857842617c2c35b63b49bd6ee77fc56/website/docs/developer-guide/context-compression-and-caching.md): in-place/archive/ownership, thresholds section7.
- [H3 Memory](https://github.com/NousResearch/hermes-agent/blob/6dbb7b9ab857842617c2c35b63b49bd6ee77fc56/website/docs/user-guide/features/memory.md): frozen core/learning.
- [H4 Delegation](https://github.com/NousResearch/hermes-agent/blob/6dbb7b9ab857842617c2c35b63b49bd6ee77fc56/website/docs/user-guide/features/delegation.md): fresh/async/grouping/delivery/steer/owner-cancel, additional read.
- [H5 Cron](https://github.com/NousResearch/hermes-agent/blob/6dbb7b9ab857842617c2c35b63b49bd6ee77fc56/website/docs/user-guide/features/cron.md): fresh/context_from/continuity/mirror/unknown; success-complete wording differs, no uniform success-filter guarantee.
- [O1 Sessions](https://github.com/openclaw/openclaw/blob/a272cf0cd56eb9d0a1245daaaf139902ac32da01/docs/concepts/session.md): reset/routes/recovery, source correction OpenClaw4.
- [O2 Main](https://github.com/openclaw/openclaw/blob/a272cf0cd56eb9d0a1245daaaf139902ac32da01/docs/concepts/main-session.md): Home/notices/completion/heartbeat.
- [O3 Context](https://github.com/openclaw/openclaw/blob/a272cf0cd56eb9d0a1245daaaf139902ac32da01/docs/concepts/context-engine.md): assembly/ownership, prune-flush sources sections8-9.
- [O4 Tools](https://github.com/openclaw/openclaw/blob/a272cf0cd56eb9d0a1245daaaf139902ac32da01/docs/concepts/session-tool.md): spawn/send/inheritance/completion/access.
- [O5 Queue](https://github.com/openclaw/openclaw/blob/a272cf0cd56eb9d0a1245daaaf139902ac32da01/docs/concepts/queue.md): lanes/input/restart limits.
- [D1 Tasks-memory](https://learn.chatgpt.com/docs/dots/tasks-and-memory): work/context/notes/schedules/events/proactive.
- [D2 Controls](https://learn.chatgpt.com/docs/dots/controls): Pause/delegated-stop/schedule-cancel.
