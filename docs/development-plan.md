# Development plan and current scope

Updated: 2026-10-05. Prioritize the vertical skeleton. Track delivered capabilities separately from full scenario acceptance rather than keeping a turn-by-turn development log.

## Current capabilities

| Capability | Status and evidence |
|---|---|
| Pi + DeepSeek main chat | Real streaming replies, tools, cancellation, and continuation after Quit/reopen implemented; [Pi MVP](validation/pi-mvp.md) |
| GPUI / Sandbox / settings | Unified Kit UI, native input, noVNC desktop, resource configuration, tray; [UI specification](ui-ux-design.md), [acceptance](validation/ui-ux.md) |
| Multitask and main Agent routing | Independent sessions, concurrency, persistent guidance, cancellation, summaries, status search/archive; [multitask acceptance](validation/multitask.md) |
| Token usage and local allowances | Overall trends, task totals, tool statistics; automatic compaction and actual usage; daily/weekly allowances, waiting, checkpoint resume; [contract](token-usage.md), [acceptance](validation/ui-ux.md) |
| Scheduled tasks | Main Agent creation; calendar/interval rules, time zones, deduplication, catch-up/skip, independent execution, change notifications; [contract and validation](scheduled-tasks.md) |

The current UI creates tasks and schedules only through the main Agent; task pages are for viewing and management. Full product/public release acceptance has not been established.

## Remaining work

D1-D6 remain scope identifiers. Later capabilities have already been implemented where useful; development need not repeat the sequence.

| Phase | Current state | Still to deliver or validate |
|---|---|---|
| D1 Vertical skeleton | Pi MVP complete | Maintain for new issues only; AGY/Codex do not block current use |
| D2 Setup / onboarding | Environment/tool preparation, normal assistant selection, API-only mode, and persistent completion implemented | Cold install, download resume/failure recovery, auth validation, chat naming, onboarding progress persistence |
| D3 Report scenario S1 | Task execution and automatic orchestration available | Full two-attachment -> deployment-cost guidance -> report/sources -> restart follow-up acceptance; structured artifacts, explicit preferences |
| D4 Web takeover S2 | Desktop viewing and cooperative takeover available | Browser Agent tools, desktop resource queues, separate controller permission domain, real Pi shell/CDP/X11 bypass negative tests |
| D5 Persistent schedule S3 | Scheduling and local mock end-to-end implemented | Real source changes/evidence and full sleep/wake missed-trigger/repeated-wake acceptance |
| D6 Multiple drivers and release | Incomplete | Native Codex App Server real main chat/MCP, concurrent workers, restart continuation, and cancellation accepted; ClaudeDriver native CLI offline contract complete; real Claude account testing skipped by user decision; AgyDriver/other real account matrices pending; memory correction/forgetting, service authorization/review, upgrade recovery, signing/notarization, cold install |

See [Product design](product-design.md#product-acceptance-scenarios) for full S1-S3 criteria. No Developer ID is available; release signing/notarization is deferred and blocks only distribution. Development bundles retain necessary ad hoc signing.

## Implementation constraints

Rust Runtime + SQLite, shared `protocol` crate, and GPUI/Swift/Helper are fixed. The old `poc/` directory is removed; historical experiments remain in Git. Use Pi to advance real product slices without rewriting existing driver tool loops or starting another memory-algorithm POC. Remote multiple nodes are outside the current MVP.

Prefer deterministic tests and real Pi with local mock providers, using economical real model acceptance when needed. Historical tray/multiscreen/full IME matrices are not renewed prerequisites; regress new issues specifically. Validate signing, remote IME, and weak networks before making the corresponding product promises.


## Issue #2: Agent layer(2026-10-04)

This branch is a foundational increment and **cannot close #2 yet**.

| Phase | Current result | Remaining work |
| --- | --- | --- |
| 1 Data model | Profile/ResolvedSpec/Binding, epoch 1 import, immutable snapshots, corruption detection | Configuration edit/copy and main-session selection available; deletion and full task rebind UI pending |
| 2 Pi Driver | Main chat/workers use DriverHost/SessionHandle; event isolation and product projection extracted; original real Pi tests pass | Injectable fake session concurrency, cancellation/missing-terminal tests added; extra permission denials shown in session; real account auth/rate limits and failure UI pending |
| 3 Gateway | Shared request core, Chat/Responses/Messages/Gemini/unknown codecs, provider routing/usage attribution; three new formats pass real Pi + local mock service | Provider budget policy/UI added; offline reservation/recovery tests pass; real provider/budget recovery matrix pending |
| 4 Tool bridge | Shared schema/dispatcher, thin Pi adapter, HTTP MCP facade with lane/turn identity | Permission UI; other drivers' MCP acceptance |
| 5 Migration | Transactional handoff, new epoch, restart recovery, API settings and existing pi.json settings triggers; tasks pinned by default | Profile management/worker default UI added; task Change Agent uses the same migration API; route copy, background defaults, allowlists, concurrency, provider allowances tested interactively; task migration interaction pending; in-place switching/optional narrative disabled |
| 6 Codex main chat | Host native App Server + local mock model passes concurrency, MCP identity isolation, continuation, HTTP cancellation, cumulative usage; ACP extra title calls and turn usage gaps reproduced, so native transport chosen | Main chat/independent workers, settings, and image integrated; guest mock model contract passes. Real Codex login, main chat/MCP, original thread continuation after restart, foreground shell cancellation and subsequent continuation pass; permission UI/full failure matrix pending |
| 7 Codex workers/schedules | Independent native threads, multiple workers, read-only summary, native cumulative delta metering; Pi prerequisite removed | Independent threads validated for manual runs and real Runtime clock triggers; real account two concurrent workers, read-only summary, automatic clock trigger pass; scheduled run has an independent native thread |
| 8 Task routing | Explicit API selection, schedule profile, allowlisted tool hints, worker default; creation-time capability checks, fixed snapshots/reasons; global/profile/provider concurrency limits | Routing settings UI implemented; real cross-driver acceptance after Codex integration pending |
| 9 Terminal / discovery / commands | Runtime PTY + native bearer WebSocket bridge + bundled xterm; independent Terminal/Sandbox sidebar entries; explicit official read-only status queries; command revisions, debounced page-return refresh, PTY recovery after App reconnect; login commands/dedicated login terminals/version locks removed | Independent Terminal/Sandbox, preserved busy-shell input across navigation, guest Codex login and restart persistence tested; Linux guest foreground/separate background groups/HUP-TERM-resistant shells and grandchildren stop on close/Runtime exit; no PTY after restart; detached daemons remain D4 scope |
| 10 Claude / AGY | Native Claude CLI Driver, independent sessions/workers/schedules, read-only summary, cancellation cleanup, usage/settings; official login query; original integration terms verified | Claude bundled guest offline contract passes; prompt auth failure stop/recovery and diagnostic filtering added to host contract; real account testing skipped by user decision without waiting for Claude login; cached compatibility/command replacement/incompatible/missing/recovery host contracts pass, as does expanded 524063 guest contract; other vendor versions need separate acceptance; native AGY Driver/settings integrated; background tasks/schedules/summary enabled by user decision without Pot orchestration tools; original CLI tool isolation/real account acceptance incomplete, applicable vendor terms unconfirmed |
| 11 Onboarding / settings | General/Assistant/Agent/Sandbox navigation; Terminal before configuration; main chat rejected without setup; extra confirmation removed for initial API save | Updated #2 uses environment/tool preparation followed by normal assistant settings; forced login/automatic selection removed; independent navigation, read-only state, empty selection, no-request cold setup tested interactively; final send-disabled regression passes but UI review encountered a locked screen; cold install/partial repair/full light-dark matrix pending |

See [Agent layer validation](validation/agent-layer.md) for evidence and limitations. Codex has passed real CLI/guest offline contracts and the account scenarios above; full permission/failure matrices remain incomplete.
Claude native CLI integration/offline contract is complete; real account tests were skipped by user decision (no account), not passed. AGY can be selected for main/background profiles; the user accepted native tool permissions and enabled tasks/schedules/prompt-constrained summaries without Pot orchestration tools. Terminal availability does not establish Driver acceptance.


Token usage now includes Agent/model attribution: new requests and native turns record agent/profile/provider/epoch/spec
snapshots. Overview/session details provide combined categories and pagination, retaining historical unknown attribution without resetting ledgers or allowances.

## Issue #5: Backend crates / standalone Server

The [split design](backend-crates.md) was committed first, followed by an eight-crate implementation in an independent branch/worktree. Server starts independently; Runtime is a library; Driver holds no Service/database. Build, image, and mise entry points are updated. Database formats/native sessions remain compatible; no remote deployment product feature was added.

Regular backend regressions, 11 App tests, 8 real Pi + local mock provider tests, native Codex App Server offline contract, AGY mock transport, and independent Claude Driver fixture pass, along with workspace checks, lint, and Linux ARM64 musl Server build. Standalone Server validates auth, persistent settings/restart, port conflict, SSE closure, and active main/worker cleanup. This round's host compatibility check rejected the original Claude executable for missing required controls; it was not counted as an original CLI pass. No OCI/App bundle rebuild, remote deployment, or real account acceptance was performed. See [split validation](validation/backend-crates.md).
