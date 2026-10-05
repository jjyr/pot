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

Gateway hygiene vs loop compressor thresholds differ, not universal50%; native Codex compact owns real thread, not local projection.

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

Legacy pass-noop/native Codex ownership, not universal plugin engine.[O3]

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

## 8. Source follow-up: outer sessions/Codex-ACP

### 8.1 Repositories/versions inspected

User-requested shallow official clones in Workspace, read-only, no installs/Agents/tests/modifications:

| Repository | Local directory | HEAD |
|---|---|---|
| Hermes | `/Users/jjy/Workspace/hermes-agent` | `b7200efef01dce2097c3c5556843ff3cd2764789` |
| OpenClaw | `/Users/jjy/Workspace/openclaw` | `4ad3766a2169740ea369afd08f65a1db11ccbfb5` |

2026-10-02 clone HEADs differ earlier snapshots; independent scope, no all-defaults reaudit claim.

Also codex-acp2.1.1 client/server/compaction docs, not third clone/all releases; official Threads.[C1]

### 8.2 Two integration modes

```text
Model API:
  Hermes/OpenClaw loop -> model service
  Outer tools/context;OAuth alone not CLI thread.

Native Codex Agent:
  Runtime -> App Server -> native thread/turn/tools
  Or ACP -> adapter -> App Server
```

Hermes loop1622 codex_app_server hands whole turn/returns, not stacked ordinary loop; fallback separate.[S-H1]

**Inference**outer routing/product/tasks/events vs execution thread, two IDs not two mainLLMs.

### 8.3 Hermes mapping/owner

Direct evidence:

1. codex_runtime502 thread ID stored in model_config.[S-H2]
2. Publish binding714 after projected messages persist/not retired; explicit relationship.
3. ensure_started279/302 resume/start, ID mismatch fails.[S-H3]
4. New thread history seed609/284, resume no seed duplication.
5. run_turn470 current turn/start, events projectUI; mirror not model authority.

```text
Hermes session H
  |-- state.db:product conversation/projections/events
  └── model_config.codex_thread_id = C
        -> resume/start C
        -> turn/start C:current input
        -> Native model/tools
        -> Events project Hermes
```

Failed resume clears binding/diagnoses/new seed, **not exact native recovery guarantee**.

Compaction boundaries:

- compression4322 calls native compact/thread-compact, not local rewrite.[S-H4][S-H3]
- native default no extra outer auto compact; record native events.
- hermes trigger outer, execution **native**, not second summary.
- off disables outer initiation, not native own compaction.
- Manual force native, wait terminal not call-return.

record_compaction219 boundary/count/usage only, tests native no extra request/no split read, not run.[S-H5]

### 8.4 OpenClaw native binding/projection/compact

Native extensions/codex/app-server, not codex-acp.

- binding-record34 owner Agent-session-ID/key or conversation binding; thread147/key358.[S-O1]
- lifecycleIO resume/start/link.[S-O2]
- Assembled projection not direct replacement native history.
- Optional thread_bootstrap epoch68/matching engine-policy-epoch-fingerprints projectfalse89, not universal default.[S-O3]
- Incompatible epoch-engine rotation501; **real source projection contract, not new Pot design**.[S-O4]

compact94 nonmanual default Codex owns automatic, allowed requests526 native; exceptions nonmanual flag/context engine, not never-auto claim.[S-O5]

**Inference**outer assembly/session vs native owner, projection rotation/compact execution coordinated separately.

### 8.5 ACP/acpx: identity layer, not extra summarizer

Different chain:

```text
OpenClaw ACP runtime
  -> Thin extensions/acpx
  -> External acpx package
  → codex-acp ACP server
  → Codex App Server
```

Source facts:

- codex-adapter1 package @agentclientprotocol/codex-acp.[S-O6]
- runtime921 ensure target/command/reuse/resume, delegate1002, not full ACP implementation.[S-O7]
- runtime561 acpSessionId/backend handle/generation check; different locators.
- client708 threadStart/724 **ACP sessionId=response.thread.id**, not extra model session.[S-C1]
- resume599 request.sessionId->threadId; load client history replay, not full-history model reinjection.

**Naming**adapter ACP ID equals thread.id here; official thread.sessionId live tree root may differ after fork.[C1]

Compaction ACP update/synthetic tool notification, no readable summary/replacement/encrypted extraction; not full context export.[S-C2]

**Scope**external acpx full store not audited, no all-reset/restart/import branch guarantee; wrapper owners and adapter mapping confirmed.

### 8.6 Accurate dual-layer answer

**Two state layers do not mean independently compacting one context**:

| Responsibility | Outer | Native/adapter |
|---|---|---|
| Entry/product/tasks/routes/events |Outer store |Not Gateway replacement |
| Execution locator |Bindings/owner-generation/recovery |Thread/ACP mapping |
| Turn input |Current/instructions/projection |Native append/execute |
| Native compact |Trigger/observe, not mirror-only |Codex executes |
| Old display/search |Projected transcript |Not all current context |

Correct scope: **outer-size summaries apply generic loops; native integration changes ownership, not generic compaction**.

Inferred risks, not reproduced defects: local compact doesn't shrink thread/full reinjection duplicates/unhandled reset keeps native/deleting projection not native deletion. Bindings/projections address distinctions, branch tests needed.

### 8.7 Source index

Pinned inspected commits/clean clones; test links assertions read, not run.

- [S-H1 loop](https://github.com/NousResearch/hermes-agent/blob/b7200efef01dce2097c3c5556843ff3cd2764789/agent/conversation_loop.py#L1622): native whole turn.
- [S-H2 binding](https://github.com/NousResearch/hermes-agent/blob/b7200efef01dce2097c3c5556843ff3cd2764789/agent/codex_runtime.py#L502): IDs/seed/projection.
- [S-H3 transport](https://github.com/NousResearch/hermes-agent/blob/b7200efef01dce2097c3c5556843ff3cd2764789/agent/transports/codex_app_server_session.py#L260): resume/input/compact.
- [S-H4 dispatch](https://github.com/NousResearch/hermes-agent/blob/b7200efef01dce2097c3c5556843ff3cd2764789/agent/conversation_compression.py#L4322): native-hermes-off/no local rewrite.
- [S-H5 test](https://github.com/NousResearch/hermes-agent/blob/b7200efef01dce2097c3c5556843ff3cd2764789/tests/e2e/core/providers/test_native_codex_app_server.py#L186): no stacked compaction assertion.
- [S-O1 schema](https://github.com/openclaw/openclaw/blob/4ad3766a2169740ea369afd08f65a1db11ccbfb5/extensions/codex/src/app-server/session-binding-record.ts#L34): owner/thread/key.
- [S-O2 lifecycle](https://github.com/openclaw/openclaw/blob/4ad3766a2169740ea369afd08f65a1db11ccbfb5/extensions/codex/src/app-server/thread-lifecycle-io.ts#L66): outer/native start-resume.
- [S-O3 projection](https://github.com/openclaw/openclaw/blob/4ad3766a2169740ea369afd08f65a1db11ccbfb5/extensions/codex/src/app-server/attempt-context.ts#L68): matched epoch no repeat.
- [S-O4 rotation](https://github.com/openclaw/openclaw/blob/4ad3766a2169740ea369afd08f65a1db11ccbfb5/extensions/codex/src/app-server/thread-lifecycle-run.ts#L501): incompatible binding.
- [S-O5 compact](https://github.com/openclaw/openclaw/blob/4ad3766a2169740ea369afd08f65a1db11ccbfb5/extensions/codex/src/app-server/compact.ts#L94): automatic owner/manual.
- [S-O6 OpenClaw ACP adapter](https://github.com/openclaw/openclaw/blob/4ad3766a2169740ea369afd08f65a1db11ccbfb5/extensions/acpx/src/codex-adapter.ts#L1): adapter package.
- [S-O7 wrapper](https://github.com/openclaw/openclaw/blob/4ad3766a2169740ea369afd08f65a1db11ccbfb5/extensions/acpx/src/runtime.ts#L921): ensure/owner/delegate.
- [S-C1 mapping](https://github.com/agentclientprotocol/codex-acp/blob/v2.1.1/src/CodexAcpClient.ts#L708): ACP=thread, resume599.
- [S-C2 contract](https://github.com/agentclientprotocol/codex-acp/blob/v2.1.1/docs/session-compaction.md): notification/no summary, not all event-source audit.
- [C1 Threads](https://learn.chatgpt.com/docs/app-server#threads): IDs/resume/compact.

## 9. Direct API/OAuth source follow-up

### 9.1 OAuth not session mode

API/Responses/App Server execution vs key/OAuth auth. OAuth no persistent context/ChatGPT history/CLI proof; inspect transport/loop.

### 9.2 Hermes direct Responses

Same b7200efe:

- codex712 instructions/messages-input/storefalse, not new-only native history.
- adapter552 chat->items/tool-reasoning replay contract, not final-text only.
- codex732 session cache key, **cache key not conversation ID**, not history substitute.
- Generic loop selects/history/tools/results/re-requests, no native thread without App Server.
- Local compact alters next messages, archives retained.

Long session continuity from outer context/persistence, not OAuth token.

### 9.3 API may state/compact

Not all stateless: input-array/previous_response_id, distinct from native thread.[A1]

Optional native_compaction110 config-model-route-checkpoint gates/context_management/encrypted item/local replay sidecar; server threshold precedes local fallback, not simultaneous unconditional summaries.

Opt-in off by default, not App Server native default; capabilities not OAuth name.

Official opaque checkpoints: input-array outputs replay, previous-response new-only/no manual prune.[A2]

### 9.4 OpenClaw OAuth differs

Mode comparison not all current routes generic loop.

responses-oauth17 token-sharing/outer refresh owner, inference-routing455 private Codex Responses provider; still App Server, **OAuth-HTTP does not remove thread**.

Specific flow only; same credential/different transport different owner.

### 9.5 Additional evidence

- [Hermes request](https://github.com/NousResearch/hermes-agent/blob/b7200efef01dce2097c3c5556843ff3cd2764789/agent/transports/codex.py#L712): input/store/cache.
- [Conversion](https://github.com/NousResearch/hermes-agent/blob/b7200efef01dce2097c3c5556843ff3cd2764789/agent/codex_responses_adapter.py#L552): items/replay/checkpoints.
- [Native compact](https://github.com/NousResearch/hermes-agent/blob/b7200efef01dce2097c3c5556843ff3cd2764789/agent/native_compaction.py#L110): gates/thresholds.
- [OpenClaw OAuth](https://github.com/openclaw/openclaw/blob/4ad3766a2169740ea369afd08f65a1db11ccbfb5/extensions/codex/src/app-server/responses-oauth.ts#L17): token-sharing owner.
- [Inference](https://github.com/openclaw/openclaw/blob/4ad3766a2169740ea369afd08f65a1db11ccbfb5/extensions/codex/src/app-server/inference-routing.ts#L455): Codex projection.
- [A1 State](https://developers.openai.com/api/docs/guides/conversation-state#manually-manage-conversation-state): input-array, official body read.
- [A2 Compact](https://developers.openai.com/api/docs/guides/compaction#server-side-compaction): checkpoints/two chaining modes, official body read.

Source/contracts only, no models/new Pot design.

Related: [Identity/memory](identity-memory.md), [Product](../product-design.md).Fact comparison, not new requirements for product design.
