# Agent layer validation

Date: 2026-10-04. Pi/Runtime foundational increment for #2, not complete multi-driver acceptance.

## Validated

-56 nonignored Runtime/1 protocol pass.
- Original6 real_pi pass; seventh real Pi with two local mock
  providers validates migration/model/handoff/old native retention/provider attribution.
- Runtime/protocol locked check/Rust formatting/diff pass.
- No paid models/host Codex auth/session reuse.

Covers old metadata/pinned restart snapshots/corrupt native sessions/duplicate-late-post-terminal filtering,
every durable migration-stage restart/candidate failures/settings wait/new inheritance-old pinning,
legacy import idempotency/transparent unknown protocol/nonzero unknown usage/MCP summary/conflicts.

Reproduce via development mise test/real Pi commands; real Pi only connects to test-launched
local mocks. Production additional providers require allowlisted HTTPS, not test loopback HTTP.

## Codex probe (not accepted)

Temporary candidates `@agentclientprotocol/codex-acp`2.1.1 / `@openai/codex`0.159.1.
Empty isolated Codex config/no API-key env, ACP initialize/
session-new only, no prompt/login/history.

- initialize advertises HTTP MCP/load-resume.
- session/new `-32000 Authentication required`.
- Claims do not establish cancel/cleanup/thread isolation/safe recovery.
- Whole group stopped, candidates not in image.

Pinned source [ca1d9717](https://github.com/agentclientprotocol/codex-acp/tree/ca1d97173ad37b471d5a4e5847725a4657d34e29):
createSessionConfig passes MCP into threads; cancel requests
interrupt/waits turn-completed. No cross-lane negative test or source-only
proof of background cleanup, so CodexDriver then disabled/unimplemented.

## Untested scope

No new image/real Codex models/auth refresh/musl/MCP
lane isolation/subagent-disable/background cleanup-resume/full profile
GUI. Profiles via API, old form retained; fake coverage
then event gate only, not full lifecycle/cancel. No provider
budgets/profile-provider concurrency then; development plan owns progress.

## 2026-10-05 updated requirements

-65 Runtime/1 protocol/7 real Pi mocks pass, 0 paid.
- Tests version-neutral new hashes/old checksum tamper/argv conflicts/PTY Unicode/dedicated login dedupe/session caps/close.
- Real loopback WebSocket missing bearer/non-native Origin/duplicate/resize/detach-replay, no user credentials.
- App locked check including Swift/Linux ARM64-musl zigbuild/lint pass.
- Isolated WKWebView bundled HTML/CSP/xterm ready, 88x31/renderer/Unicode no JS error.
- No real VM terminal UI/screenshots/login/callback/persistence/full tree isolation/new Driver claim.
- No full two-page Setup matrix; remaining work in plan.

## 2026-10-05 routing increment

-69 Runtime/1 protocol/7 Pi mocks pass, 0 paid.
- Allowlist/policy auth-revision/pinning-restart/capability mismatch/dedupe/schedule resolution/global-profile-provider concurrency/cross-provider queues.
- Cancellation retains slot; unconfirmed stop retains leases until restart, deterministic regression.
- mise check formatting/scripts/workspace/Swift passes; no routing GUI/real cross-driver Codex then.

## 2026-10-05 Codex transport comparison (host, no credentials/paid calls)

`scripts/tests/codex-contract.py` isolated HOME/CODEX_HOME/minimal env/
loopback Responses-MCP only, no user config/auth/history.
Whole group stopped; version diagnostic, not gate.

Codex0.159.1 vs codex-acp2.1.1:

- Two parallel sessions same MCP name pot/different HTTP identities. Both calls/
 outputs correct lane; official approval allows only generated side-effect-free identity probes.
- Native App Server: two turns/two requests each, resumed turn, cancelled request;
6 total/no other model. Cumulative50->75, last25; meter deltas,
not summed cumulative.
- ACP additionally temp threads/fixed gpt-5.6-luna for two titles, ignoring
selected main model. Turn usage25 vs actual50.
Cannot satisfy model pinning/full metering, so choose native
App Server (#2 permitted fallback), no silent transport switch.
- Continuation same IDs; cancellation waits terminal/HTTP disconnect,
**not proof all shell/background resources stop**, dedicated guest negatives needed.
- No native spawn/send/wait Agent tools in this probe schema only.
- App10 pass.

Reproduce with installed executables, no installs/upgrades:

```sh
mise exec -- python scripts/tests/codex-contract.py --codex /path/to/codex
mise exec -- python scripts/tests/codex-contract.py --codex /path/to/codex --acp /path/to/codex-acp
```

Transport decision evidence, **not delivered CodexDriver/real login/guest acceptance**.


## 2026-10-05 native Codex Runtime / ARM64 guest

Persistent App Server host shared by launch spec, independent threads by lane.
Main/workers/summaries/cancel use Driver events; settings select without pi.json.
Open native candidate/inject_items initialization before binding commit.
Actual CLI empty threads not persisted, so thread/start ID alone insufficient.

`scripts/tests/codex-runtime.py` real Runtime/Codex0.159.1/local Responses,
isolated HOME/CODEX_HOME, 0 host credentials/paid calls. Pass:

- No-Pi config/restart before first message/turns/native restart continuation.
- Migration/new epoch/dedupe no model/restart/missing files reject silent replacement.
- Three main MCP turn identities; two workers/two manual schedule runs/main distinct thread IDs.
- Cumulative delta/atomic baseline, missing baseline/rollback/incomplete remain unknown.
- Stream cancel waits terminal; real sleep60 background stops before finish, process checked gone.
- No subagents in schema; summary role prompt/no shell-apply_patch; unrelated inherited MCP disabled.

Found MCP reuse: thread/resume retained previous-turn identity.
After cleanup unsubscribe before next resume to reinitialize turn identity.

Same tests pass offline ARM64-musl VM with image official CLI/cross Runtime.
Evidence guest_acceptance: false means **real account/model/subscription unvalidated**,
not absence of guest execution; VM has no user login.

70 Runtime/1 protocol/10 App/7 Pi mocks pass;
mise check pass. npm-lock build baseline in image, no runtime version lock.
Issue#2 remains: Claude/AGY/codecs/full settings-onboarding/account matrix incomplete then.

## 2026-10-05 native Claude CLI contract

Original Claude2.1.289/temp HOME-CLAUDE_CONFIG_DIR/local Anthropic SSE;
no host credentials/paid models. claude-runtime.py:

- No-Pi select/turns/restart same UUID.
- No-model idempotent migration/new epoch UUID/missing JSONL broken.
- Two workers/two schedules independent; read-only summary no Bash-Write-Edit.
- Explicit built-ins exclude Agent/Task/SendMessage/Cron; Pot MCP works.
- Native background sleep60 stops before finish; cancelled model/process exit confirmed.
-23 mock requests/no other models; turn cumulative usage/cache input/no double count.

Old host build had auxiliary model/last-only usage/live background,
not accepted. Help/init lifecycle compatibility,
no fixed version; upgrades meet contract. Unconfirmed cancel/error keeps stop unconfirmed.
Codex contract regression23 mocks passes; Claude real auth/expiry/rate limit/upgrades pending.

### Official integration terms

Read [Claude legal/compliance](https://code.claude.com/docs/en/legal-and-compliance)2026-10-05.
Page permits original CLI preload/run under Commercial Terms, no binary modification/auth restriction;
users authenticate/provider bills directly, no resale/subsidy/usage mediation. Distinguishes original subscription
login from third-party credential collection. Pot original binary/official login/no token extraction;
not Anthropic authorization/endorsement or substitute for distributor accepting terms.

Linux ARM64-musl original2.1.289/latest Runtime nonroot same
contract `.local/d1/claude-guest-20261005/data/claude-runtime.json`, 23 mocks.
TCP reset/EOF both confirm cancel disconnect; summary allowlist/MCP authority,
not plan mode which rejected read-only MCP. Every mock tool must succeed, final prose cannot mask rejection.
Guest mounted latest Runtime; final bundled Runtime needs separate check.

## 2026-10-05 native API codecs

Responses/Messages/Gemini output caps/usage observers; Pi uses
native APIs without translation. Installed Pi three mocks full turns validate
paths/models/real-key injection/caps/cache/Gemini thinking accounting.
message_start retains output reserve; message_delta cumulative replaces.
All8 real_pi pass: DeepSeek/migration/cancel/budget/compact/workers-schedules.
Real providers/compaction-checkpoint matrices pending; safe_checkpoint_resume false for new formats.

Final Claude image `pot.local/sandbox:cd5858f99ca0649d32c6` bundled guest passes:
`.local/d1/claude-guest-bundled-20261005/data/claude-runtime.json`, 23 mocks.
Kernel logs identify npm termination as build container1GiB cgroup;
explicit3GiB within4GiB VM fixes install/bundle, no user resource change.

## 2026-10-05 API-only settings

Shared API form/private save. Offline no-pi initial setup/
auth/UUID dedupe/conflict-stale revision/blank same-endpoint key retention/mode0600/
no public key/old binding endpoint stable.
Three new codecs pass cold no-Pi/no-budget configuration.
76 Runtime/1 protocol/8 real Pi offline pass; GUI compiles, interaction pending.

## 2026-10-05 two-page onboarding / API-only GUI

Isolated dev.pot.setup-check/setup-ui-check-20261005, running user Pot untouched.
UI none-selected cannot continue/all-selected second page/same PTY;
logged-out Codex blocked/API-only example.invalid/fake key saves to empty chat.
API messages/requests0; Quit/restart completion/model persisted, still0.

Initial main selection metadata only, session first real message; existing migration preflight retained.
Codex/Claude host regressions23 mocks each pass.
Preparation detects/reuses image tools; missing repair in Terminal or deselect/later.
No real login/download-interruption/full themes.

## 2026-10-05 settings/routing/lifecycle addendum

- Profile edit/copy preserves main/worker fallback, explicit worker choice not overwritten.
 Auth/unknown/revision/user-choice precedence regressions.
- Fake session concurrency/filter/cancel cleanup failure/missing terminal.
 Missing terminal stop unconfirmed, capacity retained.
- Provider shared scope/isolation/global-local limits/manual resume/restart reservations/
 rollback/wait-route cleanup; 8 real Pi mocks pass.
- Codex/Claude wrapper argv23 mocks each pass,
 managed conflicts rejected, no shell expansion.

### AGY unresolved requirements

Read official [headless](https://antigravity.google/docs/cli/headless/),
[CLI reference](https://antigravity.google/docs/cli/reference/), 
[subagents](https://antigravity.google/docs/subagents/), [MCP](https://antigravity.google/docs/mcp/)
[Additional Terms](https://antigravity.google/terms), 2026-10-05.
Structured events/usage/subagent tools documented, but help lacks
accepted per-lane delegation disable/MCP/cancel cleanup combination; then disabled.
Terms restrict third-party products; enterprise differs.
Insufficient to establish permitted hosted original CLI use; distributor/provider confirmation needed.
Terminal availability only, not technical/terms acceptance.

82 Runtime/1 protocol/10 App/8 Pi mocks pass.
mise check/build/bundle; image `pot.local/sandbox:4247db1d941e1045cb89`
Codex/Claude23 mocks each including wrappers pass.
Evidence `.local/d1/codex-guest-final-20261005/data` and
`.local/d1/claude-guest-final-20261005/data`.

Isolated final API profile-copy dedupe/binding stable/routing-provider saves/
stale revisions rejected, 0 models. New routing/budget/task Change Agent compiles;
Mac locked during interaction, unlock requested, not GUI accepted.
Old onboarding evidence not new-page acceptance. Test Sandbox stopped normally via Helper.


## 2026-10-05 clock triggers, auth failures, routing interaction

Codex/Claude one-minute schedules wait for actual Runtime clock,
no manual run; native session independent of main/two workers/two manual runs.
Both host CLIs pass; original Codex bundled image passes updated contract
(`.local/d1/codex-guest-clock-20261005/data/codex-runtime.json`).

Mock401 then success validates visible failure,
ready/native_incomplete/active unchanged native ID;
next turn resumes without real auth access/change. Codex host/guest pass.
Claude api_retry/authentication_failed previously retried minutes;
Runtime verified SIGINT/waits terminal-exit before reauthentication message.
Host auth recovery/nonretry400/redaction and continuation/cancel/background
cleanup/workers/schedules pass, loopback only/0 paid.

Unlocked isolated UI profile copy/background default/allowlist,
concurrency2/provider4 saved; weekly15000/auto-resume saved.
API persisted values/0 models. Truncated long permission explanation fixed/compiled;
layout/task migration not retested to avoid interrupting user's login Terminal.
Real auth/calls/full upgrades/themes pending, AGY unresolved then.


## 2026-10-05 separate Terminal / real Codex account

Updated#2 two sidebar buttons. Remove auth PTYs/login actions-commands/
old Helper login RPC; reject old terminal request, legacy catalog login
ignored on deserialize, never executed/returned. Official checks read-only.
Preparation -> normal Assistant settings; empty tool selection allowed/no auto main.

Isolated dev.pot.setup-check navigation/read-only Agent/empty selection/
Set up later Terminal. Fresh independent-cold-ui-20261005 profiles/bindings
empty, logged-out CLIs unselectable, usage0, Terminal available.
Busy shell read survives Terminal-Sandbox navigation/status refresh,
`POT_BUSY_NAV:KEPT_SHELL_INPUT`, no injected commands, focus restored.
App NeedsAuth blocks main/guidance. Final UI/long explanation
Mac locked, compilation/tests not GUI acceptance.

Real user-authenticated guest Codex, gpt-6-luna:
main pot_list_tasks MCP succeeds, two independent concurrent threads;
read-only summary/metering, normal restart authenticated.
Image `pot.local/sandbox:ff8c27149694187b0495` restart preserves epoch2/native
`01a10962-c6ac-7331-8720-54ca5e80af90`, MCP continuation.
Cancel active foreground sleep60 -> cancelled/turn cleared/ready;
next same-thread turn succeeds, slot released.

Summary omitted feature overrides failed to restore shell/files; explicitly
set each turn/declare main permissions; host-guest regressions added.
Earlier attempt before shell started retained as failure, not pass.
Cancel incomplete unknown, not0; 9 native turn records,
input255894/output1060/cache210432/unknown1, not zero-paid claim.
Subscription native post-execution records not HTTP request counts.

`setup-ui-check-20261005/workspace/product/issue2-account-{main,workers,resume,cancel,after-cancel}.json`.
`independent-cold-ui-20261005/workspace/product/issue2-cold-ui.json`.
Only test copy root switched; after Quit CUA auto-reopened default root,
stopped without model/chat, then fixed LSEnvironment.
Daily Pot untouched, no auth credentials/live SQLite reads.

Final ff8c Codex35 loopback; d2bc Claude
32 including clock/401-stop-resume/400-redaction/cancel.
0 paid offline. Claude real login awaited then; AGY
terms/isolation unconfirmed/disabled. Upgrade compatibility/download/theme/
permission matrices incomplete, #2 not closable.


Final snapshots read persisted driver/model before first turn, not old defaults;
regression passes. Discovery reads versioned invocation, missing wrapper cannot borrow
system CLI status; wrapper args unknown without arbitrary status execution.
84 Runtime/1 protocol/11 App/mise check/build pass.
8 Pi mocks retain earlier evidence, not claimed rerun.


Final package `pot.local/sandbox:21841888feb8f142ea98`.
Isolated copy restart displays Codex/gpt-6-luna before message;
authenticated/same epoch-thread/usage9,
0 new package-check models; Claude required/AGY unknown.
`setup-ui-check-20261005/workspace/product/issue2-final-package.json`.
No full final native/account rerun; ff8c/d2bc evidence scoped to those versions.


## 2026-10-05 skip Claude account / compatibility / Terminal cleanup

User has no Claude account; integrate/skip real testing. Driver/settings/
read-only official status/guest original CLI retained, no login waiting/mock-as-real
claim or auto-select logged-out Claude.

Cache successful probes by launch config/executable metadata.
Unchanged avoids per-turn help; replacement/reinstall rechecks, bounded cache/output/time,
failed group stops. Missing/incompatible sets actionable needs_rebind before models;
binding/session preserved, compatible restore/explicit retry same native ID.

Host original2.1.289/mock extended36 requests,
0 paid: two turns no repeat probe/restart once/wrapper replacement compatible-upgrade,
missing flag/program no calls/restore same session; 401-400/
workers/schedules/clock/read-only/cancel/cleanup pass.
Simulated replacement is not all-vendor-version guarantee.

terminal-runtime.py offline guest real foreground sleep/separate background
groups/HUP-TERM-resistant shells/grandchildren stop after PTY close/normal
Runtime exit; new boot/empty PTYs/usage0.
terminal-tree-guest-20261005/data/terminal-runtime.json.
Deliberate setsid daemon detachment outside, VM/D4 scope.

Authorized real Codex one-minute schedule in existing isolated account root,
actual clock only. Complete structured report/independent native thread,
main epoch2/ID stable; immediately paused.
Run input44197/output169/cache28160/unknown0; total
10 turns/input300091/output1229/cache238592/unknown1.
Real service, not0 paid; evidence
setup-ui-check-20261005/workspace/product/issue2-account-clock.json, image218418.

Mac locked, final send gate/long text/task migration UI untested.
Skipped Claude account no longer blocker; AGY permission/isolation unresolved then.


Final `pot.local/sandbox:5240639ea6342c173067` Claude guest
36 loopback/77seconds import-unpack, paid0/account_acceptance false.
claude-upgrade-guest-20261005/data/claude-runtime.json; same Terminal
cleanup7seconds import-unpack.84 Runtime/1 protocol/
mise check/build/bundle pass; App unchanged/11 not rerun.


Isolated copy524063 restart read-only authenticated Codex/
same epoch-thread/Claude required/paused schedules/10
turns, no new models. Evidence
setup-ui-check-20261005/workspace/product/issue2-claude-package.json.


### 2026-10-05 AGY original CLI Driver (limited main chat)

User requested original CLI/AgentDriver like Claude: agy_json profile,
CLI-created binding/--conversation/final usage/SIGINT/
Assistant settings; old run_agy removed. CLI own HOME/permissions, no credential reads;
no validated official auth status, unknown.

Host/image `pot.local/sandbox:fb8d9bcb06dacd38b387` fake contract pass:
initial ID/continuation/restart/command missing-restore/error-invalid redaction/
ID mismatch/post-result invalid output prevents success/usage once/cancel-resume.
Then routing rejects AGY before launch; Driver rejects worker/read-only summary.
Fake transport, not original model/tool acceptance.

Fresh offline guest original smoke help controls/empty HOME/
default model/no-TTY headless failure, 0 credentials/models.
`.local/d1/agy-driver-auth2-guest-20261005/data/{agy-cli-smoke,agy-runtime}.json`;
7seconds import-unpack; 88 Runtime/1 protocol/11 App,
check/build/bundle pass; UI locked/untested.

Unaccepted: real AGY account/model/original tool events/full cleanup/vendor terms.
Custom agents/MCP not proven global-config isolated, missing strict Claude MCP/source
flags; no task-MCP/subagent-disable/read-only claims, background disabled then. Selection
or commands preserve worker default. Not full #2 AGY acceptance.


### 2026-10-05 AGY roles under native CLI capabilities

User accepted native tools/role-isolation differences, strict controls not prerequisite.
Enable backgrounds/schedules/summaries with per-turn stdin roles, no enforced read-only claim.
No Pot MCP/task-schedule tools; remove inherited
POT_TASK_API/POT_TASK_TOKEN; CLI manages native config.
Select/edit same native main-worker defaults;
existing tasks stay bound, explicit AGY migration allowed.

Different Drivers adapt Codex config/features,
Claude tools-settings-MCP flags, AGY JSON/conversation/per-turn
roles; shared session/event/usage/cancel, not uniform permissions.
Strict isolation/MCP/native subagent disable false; routing only accepts tools under
user policy, not exact_budget/live_steering/desktop; tests pass.

Host/image `pot.local/sandbox:20ba6069212d23d28f93` fake regression:
two workers distinct IDs/main stable/completion-summary/manual
schedule independent worker/report plus continuation/restart/recovery/cancel/usage/redaction.
Fixture verifies no Pot MCP argv/task API env.
Same guest original help/empty-HOME failure smoke,
8seconds import-unpack, model_calls0.
`.local/d1/agy-roles-guest-20261005/data/{agy-runtime,agy-cli-smoke}.json`.
88 Runtime/1 protocol/11 App/check-build-bundle pass.
Real account/original background tools/UI unaccepted; fake not substitute.
Updates prior disabled policy while retaining historical scope.


### 2026-10-05 Agent/model attribution/categories

request_routes agent/profile_id atomically with actual
binding provider/epoch/hash at reservation/native turn. Group Agent+model,
same model across Agents separate/same pair profiles-providers combined, detailed identity retained.
Main/workers/schedules/compaction same ledger, cache/allowances/history unchanged.

Idempotent schema upgrade; backfill only session/provider/epoch/hash/model match
validated immutable bindings, otherwise unknown.
Overview window/session lifetime, bounded20-item categories.
App cards/pages, scope-range-zone reset/stale response rejection.

92 Runtime/1 protocol/11 App/check-build-bundle pass.
Cross-Agent same-model/window-scope/unknown-incomplete/dedupe/
old upgrade-reopen/exact backfill/wrong session-epoch-hash/page totals tested.
Host AGY extended fake passes, same
`pot.local/sandbox:f3c82a6bfc6d743f28e8` offline guest8seconds import-unpack:
main/two workers/schedule/summary agy+agy-mock, input-output-cache/
requests-unknown match totals, profile_id agy-test.
Original help/empty-HOME failure smoke, model_calls0.
`.local/d1/agent-model-usage-guest-20261005/data/{agy-runtime,agy-cli-smoke}.json`.
No real accounts; locked Mac prevented categories/pagination UI review.
