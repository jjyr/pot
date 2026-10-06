# Pot product goals

Updated: 2026-10-06. This page maintains long-term product constraints. See the [Development plan](development-plan.md) for delivered scope and [UI specification](ui-ux-design.md) for interfaces. Unfinished goals must not be promised as existing capabilities.

## Positioning and boundaries

Pot is a personal assistant accessed from the Mac menu bar. The approved target runs Agents and persistent product state on the Host, with separately managed Sandboxes for environment operations. The main Agent controls multiple Sandboxes; each task worker executes in one fixed assigned environment and cannot manage Sandbox lifecycle. Users delegate/follow up through main chat and inspect tasks, environments and their desktops. See [Local Agents and managed Sandboxes](agent-sandbox-design.md) for the authoritative target boundary; the implemented deployment now runs Host Pi/Codex with multiple Linux execution Sandboxes.

- One long-lived main assistant, named Pot by default; task workers have no separate personas. Tasks/schedules are created only through the main Agent; UI handles viewing, guidance, and management.
- Interaction references ChatGPT Dot. Agent Setup/Sandbox configuration follow local product needs without inferring or copying closed-source internals.
- GUI, Runtime, and tools run locally; models may use cloud services. Default offline operation or data never leaving the machine is not promised.
- Agent/Sandbox decoupling initially covers Pi and native Codex App Server. Grok decoupling and direct Host tool execution are out of scope. Validate each Driver's execution routing rather than treating a handshake/host login as readiness.
- Local product bundles Containerization without requiring Docker Desktop. Remote Docker/Node Manager and multiple nodes are future directions without automatic task/identity migration.
- Persistent project assistants, inter-assistant communication, environment migration, and a complete plugin ecosystem are not current MVP prerequisites.

## User journey

Target flow: install -> environment/driver Setup and auth -> main chat naming/preferences -> delegation -> background execution -> artifacts or desktop takeover -> results -> reopen and continue.

Chat onboarding is skippable; completion progress and explicit preferences persist. Setup failure identifies the specific step while retaining valid configuration/downloads. Unvalidated drivers must not appear ready.

The main assistant answers short questions and delegates research, reports, and multistep execution to tasks with explicit goals. Main chat stays available during background work. Guidance first shows Received, then Applied after consumption; without real-time steering, explicitly queue it for the next turn.

Completed tasks enter archive; failed/interrupted tasks need attention. Archiving preserves records/native sessions; further guidance can continue the same task. The main assistant searches status/results on demand rather than loading all history every turn.

## Control and authorization

| Action | Semantics |
|---|---|
| Stop a task | Cancel only the target and wait for execution cleanup; other tasks and existing side effects remain unaffected |
| Pause/remove a schedule | Prevent future triggers without cancelling an already queued/running execution |
| View desktop | Observe without human input; viewing is not takeover |
| Take over desktop | Aim to pause Agent input/new observations and drain in-flight input; disconnect does not reclaim control automatically; observe again after handback |
| Hide window | Keep background work running; tray click reopens |
| Stop a Sandbox | Affect only that environment; ordinary stop respects active leases and leaves Host chat and unrelated environments available |
| Quit / emergency environment stop | Settle or mark interrupted the affected execution; restart does not treat interrupted work as success. See the Sandbox target design for implemented lifecycle boundaries |

Desktop takeover ultimately needs a separate controller permission domain, not just model cooperation or flags. See the development plan for current gaps.

Ordinary Sandbox tools do not prompt for approval individually. External accounts, sharing, and sending follow user authorization, rules, and platform constraints. Review displays the destination account and concrete content; approval binds to that action. Drafting does not authorize sending. Service authorization is separate from model login; revocation, task cancellation, and data deletion are different operations.

Secrets do not enter chat, logs, or ordinary memory. Host shared directories/read-write scope must be explicit; the whole home is not shared by default. Read-only sharing permits reading and does not prevent exfiltration. Actual services define account state; summaries/memory grant no permissions.

## Execution context and recovery

Runtime owns product messages, tasks, schedules, and bindings; drivers own native sessions, context, and compaction. One main driver can serve the assistant without another model forwarding each sentence.

Adapters explicitly declare auth, tools, guidance, stop, resume, and compaction capabilities. Failed native resume must not silently create a new session and claim continuation. Driver switching must not impersonate native context migration; active execution retains its binding.

Product commands are idempotent and events recoverable; files/task state survive restart. Verify external actions with uncertain results before replaying. Saved input does not imply execution success; recovery summaries point to verifiable artifacts/evidence.

Future long-term memory initially stores explicit preferences, facts, and sources with correction/forgetting. Do not mix task logs, secrets, or inferred personas into core memory. Forgetting explains the actual scope of product storage and existing native context without promising deletion of uncontrolled copies.

Runtime triggers schedules; each run uses an independent task. No overlap by default; missed triggers can coalesce into one catch-up or skip. Notification conditions are separate from frequency. The first successful observation currently counts as changed; later observations compare stable fingerprints. See [Scheduled tasks](scheduled-tasks.md). Offline local environments do not execute, and waking a powered-off computer is not promised.

## Product acceptance scenarios

All three scenarios must pass end-to-end in the real UI with real models and persistent environments; mock models prove only protocol/execution paths.

| Scenario | Flow and pass criteria |
|---|---|
| S1 Reports and guidance | Upload two sources -> request comparison -> add deployment costs during execution -> open artifact -> restart/follow up. Guidance states are truthful; report includes the addition/sources; task/session/files stay linked; reports do not duplicate. Also validate stop/interruption recovery |
| S2 Login and takeover | Web task encounters login -> view/take over -> log in manually -> hand back/continue. After takeover acknowledgment, no residual Agent input/new observations; real shell/CDP/X11 cannot bypass control. Disconnect does not reclaim control; stale control requests rejected; unrelated tasks unaffected; secrets absent from chat/logs |
| S3 Schedules and sleep | Main chat creates a Monday 09: 00 Beijing source-check schedule -> sleep misses trigger -> resume -> disable. UI shows rule/time zone/next run/missed policy/history. No overlap or duplicate creation/notification after repeated wakes; changes include evidence, unchanged runs follow silence policy; disabling does not stop the current run |

Public delivery also requires cold install/Setup failure recovery, service authorization revocation/review, memory correction/forgetting, upgrades preserving data, and signing/notarization. Cross-account access, duplicate external effects, or failed takeover block the corresponding feature release.
