---
tags: [Agent, Architecture, Memory, Research]
---

# Long-term Agent identity, memory, and conversations

> Historical research: versions/current refer to review date; [Development plan](../development-plan.md) defines implementation.

Research: 2026-10-02
Status: research/recommendations, no comparative runs, not implementation spec.
Related: [Personal Agent product](../product-design.md), especially original4.4-4.5.
Environment: [Docker GUI](docker-gui.md), [Apple Containerization](apple-containerization.md).

ChatGPT Dot=OpenAI, Meta Muse=Meta, our product=Pot local personal Agent; keep names distinct.

## 1. Two core questions

### 1.1 Identity is not an ever-growing prompt

Four layers instead of all in history:

1. **Identity/contract**: stable ID/name/role/style/user rules/non-overridable external-content rules.
2. **User model/memory**: preferences/relations/projects/facts/experience/skills, correctable/deletable/sourced/scoped.
3. **Responsibilities/task state**: goals/timing/progress/external IDs/effects, state machines not prose only.
4. **Execution**: inference/tools/compact/short-term state, rebuildable/changeable, not sole identity/fact store.

Continuity durable assets, not per-chat weight changes/permanent process.

### 1.2 Long relationship/bounded context

Same main UI, selected per-call context; timeline/topics/tasks/memory lifetimes differ.

**Long main chat != infinite context != shared execution session.**

OpenClaw rolling/Hermes snapshots/Dot tasks/Muse side chats support directions.[O3][H1][D1][M2]

### 1.3 Candidate to test

**Light hybrid, not final: product identity/explicit choices/business state; native inference/sessions; gradual experience memory.**

No required extra product LLM+Codex worker; one Codex main with product persistence/scheduler/UI, role not self-built loop.

Test dynamic per-turn injection and native memory correction/disable/forget, pinned prototypes not diagram proof.

## 2. Evidence/confidence

| System | Read | Confirm | Cannot confirm |
|---|---|---|---|
| OpenClaw |Pinned workspace/memory/main/compact/provenance docs |Public files/runtime/search/maintenance |All code correctness/our measured quality |
| Hermes |Memory/personality/providers/compact |Small core/frozen/search/providers |Provider equivalence/claimed quality |
| Codex |Official memories/App Server/AGENTS |Local lifecycle/interfaces |Hosted internals/ACP parity |
| Claude Code |Official memory/execution |Rules/auto/project/session/compact |Consumer Claude parity |
| Consumer Claude |Current/legacy help |Controls/topics/project boundaries |Schemas/ranking/full algorithms |
| Dot |Tasks-memory |Notes/memory/task/background |Notes schema/recall/rolling internals |
| Muse |Personality/memory/data help |Files/main-side/edit-delete |Loop/upstream reuse/algorithms |

OpenClaw: a272cf0cd56eb9d0a1245daaaf139902ac32da01.
Hermes: 6dbb7b9ab857842617c2c35b63b49bd6ee77fc56.

Confirmed means explicit docs, not measured; recommendations labeled, pins matter, old JSONL/reset defaults not current facts.

Codex core partly open-source, not wholly closed; compare public client vs undisclosed hosted mechanisms.

## 3. Separate state concepts

| Layer | Examples | Authority | Model input |
|---|---|---|---|
| Technical identity |Agent/environment/owner/node cert |Registration/pairing |IDs as needed, no keys |
| Persona/contract |Name/tone/role/honesty |User/product |Small stable rules |
| User model |Language/zone/preferences/relations |Statements/labeled inference |Core/scoped recall |
| Semantic |PostgreSQL decision/reason |Sourced dated facts |Relevant recall |
| Episodic |Yesterday incident |Events/chats/artifacts |Summary/evidence |
| Procedural |Troubleshooting/integrations |Validated skills |On demand |
| Future duties |Weekly/event work |Scheduler/intents/goals |At wake |
| Working |Plan/tools/screen |Execution |Recent/checkpoint |
| External truth |Sent mail/PR/auth |Actual service/runtime |Current query |

Three confusions:

- **Remember account != authorization**; cookies/tokens credentials, not memory.
- **Past action != present state**; files/tokens/PRs change.
- **Summary != facts DB**; not send-dedupe/transactions/control authority.

## 4. OpenClaw layers

### 4.1 Files

Official distinctions: [O1][O2][O4]

| File | Responsibility |
|---|---|
| IDENTITY.md |Name/vibe/emoji |
| SOUL.md |Persona/tone/bounds |
| AGENTS.md |Work/memory rules |
| USER.md |Compact preferences/style/relations/projects |
| MEMORY.md |Curated facts/decisions/summaries |
| memory/date.md |Detailed daily experience/search |
| DREAMS.md |Optional reviews |
| skills |Reusable procedures/on demand |

MEMORY not full log; private main only, recent journals at new/reset, details searched.

USER personal4000-character budget; budget principle, not copy numeric default.

Supersede changed directives, not contradictory append; shared/personal profiles by authenticated identity, not display names.[O4]

### 4.2 Beyond files

Workspace separates:

- Human-readable identity/memory;
- Agent SQLite runtime/sessions/transcripts/indexes;
- Native codex-home;
- Legacy migration/sidecars.[O1]

Not few Markdown+sessions.json; cwd not hard sandbox.

### 4.3 Persistent main/bounded model

DMs main/Home, groups/threads/coding separate.[O3][O5]

Default**no auto reset**, compact rolling; optional time. Docs differ source: ordinary reset retains transcript ID/boundary/revision, ACP differs; see [OpenClaw4](openclaw-sessions.md).

Not old daily4AM guarantee; product/main/native IDs distinct.

Origin results/heartbeat/group notices; **main coordinates bounded reports**, not all worker logs.

### 4.4 Write/search/review

Public: [O2][O6]

- Agent writes files;
- Precompact private flush;
- FTS5/vector/hybrid relevance-time-diversity;
- Dreaming candidates/promotions;
- memory_search/get and separate session tools;
- Pluggable memory/context.[O8]

**Recall/promotion/compaction differ**; storage/index/eligibility separate.

### 4.5 Compact/prune/forget

Summary/tail/raw retained, safeguard pending asks/exactIDs/failure truthful.[O7]

Pruning model tool projection, not raw deletion; restart markers vs compact-page in-memory shorthand inconsistent; reliable conclusion**raw/view separate**, not uniform persistence.[O9]

forget lineage/tombstones prevents recapture, **not all transcripts/free-text/external copies**.[O10]

Deleting one sentence insufficient, one command not universal erasure.

### 4.6 Value/cost

Borrow identity/main-task/raw-view/maintenance/provenance.

Rich runtime, not file-copy equivalence; single-user sharing not multiuser, multimachine authority explicit.

## 5. Hermes core/search/frozen snapshots

### 5.1 Instance identity

HERMES_HOME SOUL, default~/.hermes, projectAGENTS, personality session overlay.[H2]

**Project switch not persona switch**, prompt not permission system/no universal injection protection.

### 5.2 Small core

Official: [H1]

- MEMORY2200characters environment/conventions/experience;
- USER1375profile;
- add/replace/remove tool;
- Overflow explicit merge/remove, not silent truncation;
- state.db/FTS5/session_search.

Configurable core not total capacity.**Always-needed core/occasional details search**; CJK chars not tokens.

### 5.3 Frozen tradeoff

Start-frozen prompt for prefix cache; writes immediate/tool response current, initial block unchanged.[H1]

Gateway weeks across process restarts; restart not session boundary; never-new higher compaction/less refreshed-memory benefit. Natural task/topic/day new recommended.

Permanent UI cannot require users knowing /new; internal epochs/current correction deltas possible recommendations.

### 5.4 Reviews/providers

Post-turn memory/skills/helper model/budgets/off/write-notify controls.[H1]

Providers context/prefetch/sync/end extraction/mirrored writes; Honcho user/session model.[H3]

Default coexist, not plugin replacement; never-replace overview vs explicit disable built-ins differs, use concrete config.

Questions: copies/correction/offline/cloud disclosure?

### 5.5 Compression

Gateway hygiene/loop compressor/plugins/native ownership.[H4]

Default in-place/same ID/soft archive/lean-tail/exact anchors/recovery; **summaries still reference original evidence**.

Provider differences/lossless name not all-history comprehension; data retention vs inference completeness.

### 5.6 Value/cost

Instance-project separation/small cost/search/skills/simple.

Frozen permanent-main conflict/multiple writers/learning guesses; official one-Agent/home.[H1]

Specific page search actual messages/noLLM vs README FTS+summarization; prefer page, no benchmark claims.

## 6. Codex sessions/identity ownership

### 6.1 Local memory exists

Official: [C1]

- Web/local separate controls;
- Local **off by default**;
- Eligible past chats, skip active/short-lived;
- Idle background, not immediate per-turn;
- Low allowance may skip;
- Memories summaries/durable/evidence in HOME;
- Generated state, manual edits not primary control;
- Per-chat use/contribute switches.

Cannot justify own system by absence; always-active main idle-driven freshness needs testing.

### 6.2 AGENTS vs auto

Explicit stable role/preferences vs generated experience.[C3]

Cannot override all built-in higher rules; test identity/task expression/conflicts, not just file read.

### 6.3 App Server

start/resume/fork/compact lifecycle.[C2]

Product timeline/native mapping, independent task threads, no full inheritance requirement.

**ACP communication != all memory controls**; pinned control/update/compact/import-export tests, no assumed native parity.

### 6.4 Different promises

**Codex-first**reuse sessions/memory, less work, only supported freshness/control/scope promises.

**Product memory/native execution**authoritative profile/duties/events, relevant injection, native experience optional, own versions/consistency/forget.

First fast desktop loop, second future drivers/nodes; not all duplicated memories naturally consistent.

## 7. Claude Code vs consumer

### 7.1 Code

Official: [A1][A2]

- CLAUDE explicit rules;
- Project auto-memory directories;
- MEMORY short index/topics on demand;
- Load first200lines/25KB whichever first;
- Repo worktrees/subdirs share, not automatic machines-cloud;
- Edit/delete/memory command;
- Main memory not ordinary subagents, fork exceptions.

Cwd sessions/resume-fork, new not full prior chat; old tools pruned/summary near limit, stable rules CLAUDE.[A2]

Root CLAUDE reload after compact, nested/path timing differs.[A1]

### 7.2 Consumer current vs legacy

Official page **both experiences**.[A3]

Current:

- Individual topics saved automatically/request;
- Settings Memory Topics edit/delete;
- Per-project space/summary;
- RAG past-chat tools/citations;
- Chat/cloudCowork sharing, not local;
- Deleting/expiring chat **not auto-delete entries**;
- Pause/reset/per-chat-off/incognito distinct.

Legacy only24h synthesis/delete-update, not current blanket behavior.

**Search/topics/project summaries distinct update-delete assets**, not automatic API user-model service.

### 7.3 API compaction

Beta on-demand/threshold server summaries, compatibility/headers.[A4]

Less summarizer work, not identity/conflicts/scopes/effects/forget; execution asset.

## 8. Dot

Official: [D1]

1. Multiple responsibilities/priority changes.
2. **Parallel backgrounds**report/main chat, visible threads/guidance.
3. Task-specific own context, **not all parent chat**.
4. Current/relevant ChatGPT memory/own notes.
5. Notes preferences/decisions/work, **not transcript/ChatGPT memory**.
6. ChatGPT controls may not alter Dot notes.
7. Cross-channels continuity/separate chats, private knowledge not disclosure permission.
8. Schedule separate, current stop != recurring cancel.

### 8.1 Product positioning correction

Earlier no-subagent claim **contradicts official docs**. Historical Potv0.5 one assistant/main/internal workers, no task conversations, project-Agent management/pins-autoarchive removed.

Historical deployment difference local/remoteLinux, not project assistants. User then simplified task UI to main guidance despite internal parallel/bounded contexts. First naming task, GPUI/tray/VNC pending then; research not automatic features.

### 8.2 Borrow/unknown

Long main/separate duties/modest notes/scheduling-channel split.

Unknown notes/ranking/consolidation/rotation, no inferred vectorDB/OpenClaw internals.

## 9. Muse: manageable identity/memory

### 9.1 File surface

Official: [M1][M2]

- Name/personality/tone/style/emoji/avatar/tagline;
- Chat/UI edits;
- Conversation/observed patterns/connectors/files;
- Memory/Soul/Identity entries;
- Memory knowledge/Soul truths-bounds/Identity name-vibe;
- Main/side chats/goals/reminders-tasks/library;
- Other-assistant ZIP import/own chats-files-Agent export.

Doc filename casing differs, no stable internal-path API claim.

### 9.2 Deletion

Source deletion may leave memory; forget best-effort **not full erasure**; reset main/files/tasks broader than side-chat delete.[M2]

UI needs layer/source/relearning clarity, not only clear-chat.

### 9.3 Lessons

**Identity/memory/goals/tasks/files visible-editable objects**, not mysterious algorithm.

Similar filenames not shared implementation/file-only proof; assembly/compact undisclosed.

## 10. Comparison

| Dimension | OpenClaw | Hermes | Codex | Claude Code | Dot | Muse |
|---|---|---|---|---|---|---|
| Identity |Files |SOUL-overlay |AGENTS bounded |CLAUDE |Product, unknown internals |GUI/files |
| Core |USER/MEMORY/journal |Small core |Generated |Index/topics |Notes/ChatGPT |Memory/files |
| Recall |Scoped search |SQLite |Evidence not generic API |Resume/files |Selected, unknown |Files, unknown |
| Main |Rolling/no time reset |Continuous/natural new |Thread |Cwd task |Main/task conversations |Main/side |
| Update |Writes/flush/dream |Writes-review/frozen |Idle eligible |Active writes |Responsibilities |Chat/UI |
| Correct/forget |Limited lineage |Tools/providers |Official generated controls |Edit/delete |Separate notes |Best-effort/reset |

No best-project-copy answer; choose ownership/freshness/delete/runtime cost.

## 11. Memory patterns

Engineering synthesis, not market statistics.

| Pattern | Benefits | Problems | Use |
|---|---|---|---|
| Small files |Readable/cheap/backup |Capacity/concurrency/provenance |Persona/preferences/index |
| Events/summaries |Trace/recover |Growth/detail loss |Timeline/tasks/evidence |
| Structured facts |Scope/version/expiry/correction |Extraction/schema |Preferences/authority |
| RAG/hybrid |Bounded large history |Misses/wrong scope/stale ranking |Archives/search |
| Skills |Reusable procedure |Wrong propagation/expiry |Integrations/browser |
| Graph/user service |Rich relations |Ops/copies/explainability |Later proven need |
| Vendor memory |Less work/native fit |Control/migration |Experience/Codex-first |

**Files/SQLite/vector index coexist**: edit surface/authority/rebuildable views, explicit facts owner.

CJK default English FTS inadequate; test segmentation/ngrams/names/mixed/repos/IDs. OpenClaw CJK trigram reference.[O6]

## 12. Recommended identity layer

### 12.1 At least three IDs

Separate:

- agent_id: assistant, stable across restart/model/name.
- environment_id: persistent environment.
- node_id: host/backend.

owner/channel separate, name not primary key; multi-node tray not same persona/shared all memory.

First one-Agent/environment; cross-node synchronization/forks later, no distributed persona prerequisite.

### 12.2 Write authority

| Layer | Writer | Advice |
|---|---|---|
| Hard boundaries |Trusted runtime/user |Not webpages/memory |
| Explicit persona |User/direct-request config |Versioned/visible/rollback |
| Explicit facts |Authorized memory |Immediate correction/supersession |
| Inferred profile |Extractor |Label inference |
| Experience |Agent/tools/results |Sources/time |
| Skills |Agent/user |Environment/version/validation |

No per-write prompts needed for schema/provenance/caps/audit.**Write rules not interactive approvals**.

Same-container root can bypass protected files; trusted identity/pairing/host controls separate domains/outside as needed. Full guest admin means honest best-effort limits, not SOUL isolation promise.

### 12.3 Small/stable/explainable persona

Name-role/style/uncertainty/collaboration only, no full experience/tasks/keys.

No permanent emotion inference/task tone drift; explicit change history, not silent SOUL rewrite.

## 13. Memory writes/reads

### 13.1 Classify before placement

```text
Human/tool/task/external
  -> Source/owner/scope/time/trust
  -> Preference/fact/inference/experience/skill/transient
  -> Candidates/deduplication
  -> Conflict/expiry/sensitive/authority
  -> Durable versioned commit
  -> Index/summary/UI
```

Three cadences:

1. **Explicit remember-change-forget**: same turn, ack after durable result.
2. **Task experience**: checkpoint/completion, sourced artifacts/results.
3. **Background consolidation**: idle dedupe/expiry, budget/pause/retry, no foreground block.

Not wait permanent-session end or use compaction as sole memory write.

### 13.2 Minimal fact model

Recommendation, not competitor schema:

```yaml
memory_id: mem_example
subject_id: owner_example
kind: preference
claim: Prefer Chinese communication
scope: owner_private
source_kind: explicit_user
source_refs: [event_example]
observed_at: 2026-10-02T13:00:00+08:00
valid_from: 2026-10-02T13:00:00+08:00
valid_until: null
status: active
supersedes: null
confidence_class: explicit
version: 1
```

LLM0.97 not objective confidence; explicit/tool-verified/inferred classes, optional scopes/sensitivity/generation.

Relative times retain quote/resolved zone/expiry; conflicts recency+scope+authority, web cannot override user just newer.

### 13.3 Scope before ranking

```text
Request/topic-task/audience/node
  -> Permission/scope
  -> Core/active facts
  -> Keyword/semantic candidates
  -> Dedupe/conflict/freshness
  -> Budgeted excerpts/sources
  -> Turn context
```

Not global vector search trusting model secrecy; filter private/other-node/project before model.

Minimal injection + on-demand search, avoid missed search/irrelevant autoprompt, separate budgets.

### 13.4 Correction vs forgetting

**Correction**active supersedes old/history, current delta.

**Forget**explicit layers: core/journals/summaries/index/cache/providers/loaded workers/exports-backups, delete vs stop use.

Recommendations:

- Source refs/derived tracking;
- Tombstones prevent relearning retained chats;
- Invalidate loaded memory/epoch, not disk only;
- Report undeletable remote copies;
- Audit/backup retention cannot promise infinite logs+instant all erase;
- Use/contribute/transcript-save/delete separate switches.

Competitors expose imperfect boundaries, not all solved.[O10][A3][M2]

## 14. Timeline vs execution

### 14.1 Object recommendation

```text
Agent(long identity)
  |-- Main timeline(permanent UI)
  |-- Internal topics/tasks(no new visible conversation)
  |-- Responsibilities
  |-- Tasks(progress/results)
  |     +-- Execution/epoch(bounded native context)
  +-- Memory/skills/artifacts
```

Timeline UX/history, topic understanding, task state, execution implementation; many tasks/epochs.Historical Pot single visible timeline/multiple internal sessions.

No visible chat per intent; main progress/input/results/guidance; side-chat research not then product object.

### 14.2 Same execution session

Good: continuous task/recent tools/healthy context/cache.

Bad: unrelated desktop logs/identity-scope change/forget/new driver/no-progress compact.

**Natural boundaries**, completion/checkpoint/idle/topics, not arbitrary counts; active effects/tools cannot discard.

### 14.3 Epoch carryover

At least:

- Current goals/unanswered;
- Decisions/reasons/constraints;
- Task/external IDs/artifact paths;
- Completed/pending/uncertain actions;
- Next/blockers;
- Evidence pointers;
- Identity/profile versions/control flag, strong leases later.

Reload newest stable layer, not old summary ignoring correction; simple UI maintenance, no required provider commands.

### 14.4 Example

Morning repo taskID/main progress, no full logs.

Noon Chinese preference immediate, new email task with scoped contacts/content/grants.

Afternoon takeover flag false/drain/confirm, human tabs, explicit handback/reobserve, no stale coordinates.

Evening task lookup/evidence/resume-new epoch; relationship/state continuity, not whole-day prompt.

## 15. Budget/compact/recover

### 15.1 Turn contents

```text
Stable product/native rules
  + Short persona/explicit core
  + Current task/actual state
  + Relevant sourced memory
  + Bounded topic summary
  + Recent chat/necessary tools
  + Input
```

Include schemas/images/system/output reserve/reasoning, not chat count alone.

Initial available-input experiment10%stable/15%task/15%recall/50%recent/10%margin, not universal; schemas/images alter, output deducted first.

Small core/current priority/evidence retrieval, not more tokens for every relevant fact.

### 15.2 Six mechanisms

| Mechanism | Purpose | Does not solve |
|---|---|---|
| Pruning |Old tools references |Stable facts |
| Compaction |Old summary/tail |Lossless facts DB |
| Flush |Save durable facts |True task result |
| Epoch |Fresh/reload |All pending carryover |
| Retrieval |Evidence |Perfect recall |
| Cache/continuation |Cost/execution |Cross-session memory |

### 15.3 Desktop context growth

Screenshots/DOM/logs/files artifacts with time/tabIDs/refs, read as needed.

- Recent/relevant images only;
- Old descriptions not click state;
- Bounded/paginated outputs;
- Observed vs succeeded separate;
- No summary claim for unseen images.

OpenClaw text-only omitted images; artifacts preserve recoverability.[O7]

### 15.4 Validate summaries

Goal/constraints/actions-evidence/IDs/unanswered/next/sources.

Mechanically copy IDs/paths/requests; failed compact retains originals/reports degradation, bounded retry/no-progress.

Repeated summary-of-summary loses conditions; consolidation consults raw, facts not third-hand only.

## 16. Duties/workers/control

### 16.1 Bounded worker reports

Brief goal/scope/background/capabilities/artifacts/taskID/memory version.

Report completion/evidence/artifacts/externalIDs/problems/candidates; not auto success.

Workers submit candidates, central merge, CAS/transactions/single writer, not last-write preference loss.

### 16.2 Wakeup not replay whole main

Schedules/event duties/active tasks separate, wake scoped/latest/bounded.

Silence/dedupe/durable queue states-retry, memory reminder not scheduler; task/main/schedule stops differ.

### 16.3 Exact takeover state

Historicalv0.5 environment flag false/all controlled inputs check/drain/backend confirm, not prompt/GUI-only.Non-desktop unaffected, observation pause suggested privacy.

Explicit return/reobserve/verify, disconnect no reclaim recommended; flag cooperative, bypass/races test, strong owner-fencing-permissions future, not proven; restart/memory/control distinct.

### 16.4 Uncertain effects

Timeout email may sent, summary failure not retry authorization.

OperationID/objects/idempotency/unknown_outcome; query first/use supported keys/report uncertainty, no universal exactly-once.

No stepwise Sandbox approval doesn't preclude consistency; accounts/host/pairing independent.

## 17. Ownership matrix

| Option | Speed | Correction/forget | Driver switch | Main control | Work |
|---|---|---|---|---|---|
| A.Native memory |Fast |Supported limits |Coupled |Native mapping |UI/environment/tasks/control |
| B.Own all |Slow |Defined/tested |Potential high |Flexible |Extraction/search/compact/sessions/evaluation |
| C.Product core/native execution |Middle |Core controlled/experience separate |Independent core |Timeline-execution split |Identity/state/memory/adapters |

### 17.1 Compare A/minimalC first

MinimalC:

- Identity/explicit settings/key facts;
- Timeline/tasks;
- Stable injection/current delta;
- Inspect/correct/delete authority;
- Events/artifact refs;
- Native start-resume/compact-rotate.

Inference/large vectors/graphs/dreaming later; first task retention/current preferences/no duplicates.

### 17.2 One authority

Product facts/native coding experience or native memory off baseline.

If native facts inseparable, limited forgetting or rebuild home/session; GUI delete mustn't secretly relearn old preference.

No default bidirectional auto-summary synchronization; conflict/delete authority unclear.

### 17.3 Suggested persistence

```text
agent-volume/
  identity/
    identity.json
    persona.md
  memory/
    core.md
    notes/
    skills/
  state/
    agent.sqlite
  artifacts/
  engine-homes/
    codex/
```

Concept only, core.md DB-readable view; edits same commit path, no second authority.

Separate credentials; engine homes sensitive, not shareable notes.

Schema-version future only, not migration feature; image-volume-version not automatic cloud/keychain/node/OAuth portability.

## 18. Required research experiments

Same model/tasks/pinned clients, A/minimalC/H-O configs; learning off baseline then on, meter tokens/helper calls/latency/errors/human intervention.

| Experiment | Scenario | Criteria |
|---|---|---|
| Identity |Restart/project/model/resume |StableIDs/style/no node mixing |
| Immediate memory |Remember/new epoch |Durable ack |
| Correction |Timezone/preference |No stale current/future |
| Long day |Coding/email/research/chat |Resume/bounded cost |
| Always active |No idle |Native starvation/explicit writes |
| Compaction |Repeated/exactIDs |Evidence/unanswered retained |
| Takeover |Midclick |Gate/drain/reobserve/bypass-races |
| Unknown effects |Timeout/restart |Verify/no blind duplicate |
| Forget |Keep source transcript |No relearning/residual report |
| Concurrent writes |Two workers |No lost updates |
| Poisoning |Web SOUL/exfiltration |Not user authority |
| Scope |Similar private/project/node |Pre-filter |
| Chinese |Aliases/mixed/repos |Recall/exact query |
| Offline provider |Embeddings/cloud down |Core usable/degraded/not all blocked |

Metrics precision/recall/stale-use/correction delay/reappearance/task resume/duplicates/effective cost/P50-P95; verify persistence, not model acknowledgment.

Hard safety/consistency criteria, not averages hiding one cross-scope/duplicate; quality/style/cost multisample.

## 19. Pending decisions/priorities

### P0: Before implementation

1. **Interface**ACP/AppServer/both/per-turn updates?
2. **Fact authority**product vs same native facts?
3. **Effect timing**current corrections/forced forget epoch?
4. **Timeline-task**then single chat; internal routing/references/guidance/lifecycle, no visible task/project assistants research.
5. **Trusted plane**full guest permissions vs protected identity/pair-control?
6. **Forget promise**stop use/derived-source-cache/delete residuals?

### P1: Prototype

- Rotation/compact owner/reliable events;
- Review cost/recall/foreground interference;
- CJK/artifact indexing;
- Durable wakes/idempotent recovery;
- Retention/backups;
- Scoped worker notes/grants, not all privacy inheritance.

### P2: Proven need

- Rich user services/graphs;
- Multinode same-Agent consistency;
- Agent communication/experience promotion;
- Real machine migration/credentials/upgrades.

No full distributed memory/own inference prerequisite; A-C experiments decide ownership.

## 20. Assessment

**Confirmed**layered rules/curated memories/search/history/context; no evidence permanent growing prompt sufficient.

**Advice**own stable IDs/explicit settings/timeline mapping/key tasks; all experience depends native freshness/control/quality/cost.

**Undecided**native/hybrid both test; identity ownership/main driver/workers separate.

**Historical correction**Dot backgrounds/taskthreads confirmed; Potv0.5 single chat/internal workers/no project Agents/naming first; flag/tray-VNC then untested, not frozen memory ownership. Other differences [product](../product-design.md) original0.5/4.6.

## 21. Sources

Official read materials, pinned references/live help changes; not full source audit.

### OpenClaw

- [O1 Workspace](https://github.com/openclaw/openclaw/blob/a272cf0cd56eb9d0a1245daaaf139902ac32da01/docs/concepts/agent-workspace.md): files/runtime/SQLite.
- [O2 Memory](https://github.com/openclaw/openclaw/blob/a272cf0cd56eb9d0a1245daaaf139902ac32da01/docs/concepts/memory.md): classes/flush/dream/search.
- [O3 Main](https://github.com/openclaw/openclaw/blob/a272cf0cd56eb9d0a1245daaaf139902ac32da01/docs/concepts/main-session.md): rolling/notices/defaults.
- [O4 User](https://github.com/openclaw/openclaw/blob/a272cf0cd56eb9d0a1245daaaf139902ac32da01/docs/concepts/user-model.md): directives/profile/authority.
- [O5 Sessions](https://github.com/openclaw/openclaw/blob/a272cf0cd56eb9d0a1245daaaf139902ac32da01/docs/concepts/session.md): routes/scope/reset.
- [O6 Builtin](https://github.com/openclaw/openclaw/blob/a272cf0cd56eb9d0a1245daaaf139902ac32da01/docs/concepts/memory-builtin.md): SQLite/FTS/hybrid/CJK.
- [O7 Compact](https://github.com/openclaw/openclaw/blob/a272cf0cd56eb9d0a1245daaaf139902ac32da01/docs/concepts/compaction.md): summaries/IDs/recovery/providers.
- [O8 Context](https://github.com/openclaw/openclaw/blob/a272cf0cd56eb9d0a1245daaaf139902ac32da01/docs/concepts/context-engine.md): assembly/plugins.
- [O9 Prune](https://github.com/openclaw/openclaw/blob/a272cf0cd56eb9d0a1245daaaf139902ac32da01/docs/concepts/session-pruning.md): tools/projections.
- [O10 Provenance](https://github.com/openclaw/openclaw/blob/a272cf0cd56eb9d0a1245daaaf139902ac32da01/docs/concepts/memory-provenance.md): lineage/admission/forget.

### Hermes Agent

- [H1 Memory](https://hermes-agent.nousresearch.com/docs/user-guide/features/memory): official core/freeze/search/review/controls.
- [H1 Snapshot](https://github.com/NousResearch/hermes-agent/blob/6dbb7b9ab857842617c2c35b63b49bd6ee77fc56/website/docs/user-guide/features/memory.md): corresponding pinned docs.
- [H2 Personality](https://github.com/NousResearch/hermes-agent/blob/6dbb7b9ab857842617c2c35b63b49bd6ee77fc56/website/docs/user-guide/features/personality.md): instance/project split.
- [H3 Providers](https://github.com/NousResearch/hermes-agent/blob/6dbb7b9ab857842617c2c35b63b49bd6ee77fc56/website/docs/user-guide/features/memory-providers.md): sync/Honcho.
- [H4 Compact](https://github.com/NousResearch/hermes-agent/blob/6dbb7b9ab857842617c2c35b63b49bd6ee77fc56/website/docs/developer-guide/context-compression-and-caching.md): dual/in-place/lean/native.

### OpenAI / ChatGPT Dot

- [C1 Memories](https://developers.openai.com/codex/customization/memories): local/web/background/controls.
- [C2 App Server](https://developers.openai.com/codex/app-server): threads/integration.
- [C3 AGENTS](https://developers.openai.com/codex/guides/agents-md): explicit guidance/earlier research.
- [D1 Dot](https://developers.openai.com/codex/dots/tasks-and-memory): background/tasks/notes/channels/schedules.

### Anthropic

- [A1 Code memory](https://code.claude.com/docs/en/memory): rules/auto/topics/budgets.
- [A2 Execution](https://code.claude.com/docs/en/how-claude-code-works): sessions/resume-fork/prune-compact.
- [A3 Consumer memory](https://support.claude.com/en/articles/11817273-use-claude-s-chat-search-and-memory-to-build-on-previous-context): topics-vs-legacy/projects/controls/delete.
- [A4 API compact](https://platform.claude.com/docs/en/build-with-claude/compaction): beta server summary/bounds.

### Meta Muse

- [M1 Muse customization](https://www.meta.com/help/artificial-intelligence/995796179982326/): persona/files/import.
- [M2 Muse data](https://www.meta.com/help/artificial-intelligence/2225571704857152/): main-side/goals/forget-reset.
