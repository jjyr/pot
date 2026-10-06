# Native Pot Agent acceptance

Date: 2026-10-06. This replaces Pi execution in source. Old Pi acceptance reports remain historical; they do not validate this implementation.

## Implemented scope

- Rust `pot-agent` handles direct OpenAI Chat Completions / Responses, Anthropic Messages, and Gemini model/tool loops.
- Codex App Server and Grok ACP remain native drivers with their own agent loops.
- Built-in tools use Codex-style `apply_patch` and bounded `exec_command`; main task/schedule tools reuse the role-scoped MCP dispatcher.
- Storage persists complete provider-native assistant replay, tool calls/results/receipts and checkpoints. Compaction and model requests share Runtime's reservation/usage policy.
- Pi RPC, extensions, npm dependencies, credential import scripts, executable discovery and smoke-agent fallback are removed. New `pot-v2.sqlite` / `usage-v2.sqlite` files intentionally do not import old development sessions.

Design references were the locally checked-out Pi commit `9ad083102aa9afd483dbb5e0da53e69f2902c6a6` (agent loop, protocol adapters and replay transformations) and Codex commit `822e58cc3d666166c7446c5b1ea2e52f5d09594c` (turn state, tool execution/cancellation and context management). The implementation is Rust code in this repository; neither upstream runtime is embedded for API execution.

## Verification

| Check | Result | Scope |
|---|---|---|
| `mise run test` | Pass: 111 backend tests | Core, Storage, Agent, Driver, Gateway, Runtime, Server, Protocol |
| `mise run test:app` | Pass: 14 tests | Frontend state, streams, navigation, usage and artifact paths |
| `cargo check --workspace --locked -j 4` through mise | Pass | macOS workspace including App / Swift bridge |
| `mise run check:swift` | Pass | Helper, image builder, image checker compile; no VM boot |
| `cargo zigbuild --locked -p pot-server --target aarch64-unknown-linux-musl -j 4` through mise | Pass | Linux ARM64 backend including Pot Agent |
| `mise run lint` / `git diff --check` | Pass | Source formatting, scripts, whitespace |
| Native npm lock dependency closure | Pass | Codex/Grok required packages retained; Pi packages absent |

Local Agent integration uses real HTTP streams and real filesystem/shell operations against deterministic providers. The eight integration tests cover:

- Four native wire formats, tool continuation, exact usage aggregation, and restart continuation without repeated file/shell effects.
- Responses encrypted reasoning, Anthropic signatures, Gemini thought signatures and Chat reasoning content replay; upstream secrets are absent from saved context.
- Shell cancellation and timeout, process cleanup, failed-tool reporting and a usable later turn.
- Allowance pause after a completed tool, reopening Storage, and continuing without tool replay.
- Metered/persisted context compaction and compaction usage tagging.
- Unknown interrupted tool effects become explicit context results and are not executed again.
- Same-ID tool calls reuse receipts; conflicting arguments cannot execute a second side effect.
- Pot task creation through the existing authorization/receipt dispatcher.

Additional tool/codec tests cover patch add/update/move/delete, preflight failure without earlier writes, traversal/symlink rejection, invalid Unicode patch prefixes, fragmented tool arguments, truncated/unterminated response rejection, and byte-fragmented multiline/CRLF SSE. Standalone Server tests verify the new compaction route, removed Pi route, authentication/restart/SSE behavior, and shutdown cleanup of simultaneously active main and worker shells.

## Limits of this acceptance

No paid inference, real-provider account acceptance, new Sandbox image build/boot, or interactive GUI configuration acceptance was performed. Existing image size and VM reports refer to prior builds. Rebuild the bundled image before using the new backend in App.

Commands are synchronous/bounded; persistent interactive Agent shell sessions, live steering and parallel tool dispatch are not implemented. Context estimation is conservative UTF-8 bytes with a 32,768 window; oversized first-turn context can fail explicitly. Individual patch replacements are atomic, but multi-file commits and external shell effects are not a transaction. Host isolation continues to rely on the Sandbox; guest-user filesystem isolation remains separate work.

See the [current runtime contract](../../runtime/README.md) for the supported configuration and recovery semantics.

## Follow-up: configurable loop and execution control (2026-10-06)

This follow-up supersedes the initial synchronous-command and no-live-steering limits above. Initial test counts remain the evidence for the original replacement revision.

- Profile-pinned AgentSettings controls context window, compaction threshold/enablement/reserve, replacement system prompt, built-in tools, all-tool allowlist, request timeout and shell execution limits. The regular request guard includes system/tool context and output reservation. Invalid or oversized context is reported before a model request.
- AgentExtensions installs trusted code-defined tools, request-copy context transformations and token estimation. Runtime initialization accepts an injected Agent host, shared with workers; summary roles retain their read-only boundary.
- exec_command can yield a live turn-scoped pipe session; write_stdin sends input/EOF or polls it. Product events stream bounded output. Sessions are cleaned on cancellation, timeout, normal turn end and allowance checkpoint. No cross-turn/restart process sessions or PTY support are implemented.
- Authenticated owner/turn-bound control endpoints persist guidance with idempotent receipts, inject it before the next request, support cooperative pause/resume and reject stale/settling targets. Manual pauses hold capacity and do not interrupt current work. Existing queued task guidance and allowance checkpoint behavior are unchanged.
- The latest 200 model/tool/turn metadata diagnostics persist per owner. They exclude model payloads, command arguments/output and credentials. There is no new advanced settings/control UI.

Verification: `mise run test` passes **119 backend tests**, `mise run test:app` passes **14 App tests**, `mise run check` (lint, Rust workspace and Swift Helper compilation) and Linux ARM64 musl Server build pass; `git diff --check` passes. No real-provider calls, image rebuild/boot or interactive GUI acceptance were added.

Eight added tests cover configurable large/invalid limits, effective prompt/output/tool policy, code tool/context/token hooks, interactive pipe input and incremental output, turn-end process cleanup, durable/idempotent guidance and pause/resume, cancel while paused with zero model requests, and oversized-context rejection before networking. Existing four-protocol, budget/recovery, timeout/cancellation and Server shutdown checks remain passing.

At the time, isolation boundaries were proposed for discussion. The proposal has since been removed; see the current [Agent/Sandbox design](../agent-sandbox-design.md). Controller and shell still share the guest user; this follow-up does not claim controller, task or browser permission separation.

## Follow-up: context estimation and usage calibration (2026-10-06)

The reported replay issue affected context estimation and compaction decisions, not duplicate billing in the actual-usage ledger. Default estimation now projects the provider-native context, excludes duplicated display/tool-call metadata and saved usage, and counts native assistant replay once. Complete normalized provider usage calibrates an unchanged prepared-history prefix; only subsequent context growth is estimated. Calibration persists across reopen, binds model/system/tools/history through a fingerprint, and is invalidated by context edits or cleared after compaction. Missing/partial/zero usage creates no new measured baseline. Custom estimators now receive protocol/model; profiles can disable provider calibration.

Actual billing and allowance-reservation rules are unchanged. Cache is included once in normalized input; reasoning once in normalized output; repeated cumulative usage frames are not added. Context estimates remain estimates, not billed usage or an exact tokenizer guarantee.

Verification: **125 backend tests**, **14 App tests**, `mise run check`, Linux ARM64 musl Server build and `git diff --check` pass. Six added tests cover four-protocol projection/usage normalization, replay/display duplication, incomplete/zero observations, calibrated trailing input, invalidation/reopen, and a four-protocol HTTP regression that enlarges stored display text without triggering an extra compaction or changing actual ledger totals. Existing compaction and checkpoint/cancellation cases remain passing. No real-provider token-efficiency comparison or image/GUI acceptance was performed.

The [long-term evaluation framework](../token-evaluation.md) is a separate design proposal; no benchmark runner or vendor adapter is implemented by this fix.

## Follow-up: standalone unit regression matrix (2026-10-06)

This follow-up adds **29 unit tests**: 23 in pot-agent and six in pot-gateway. Pot-agent now has 28 unit tests; the existing Runtime Agent integrations and eval contract tests remain separate. The new fixtures call private Agent state transitions with in-memory feedback and injected native streams, without HTTP servers, Runtime/Storage dependencies or paid model calls. Shell tests use small temporary local processes/files, explicitly close sessions and remove their workspaces.

| Area | New regression coverage |
|---|---|
| Context estimation | Four-protocol UTF-8/system/tool/result contributions; protocol invalidation; overflowing usage cannot calibrate a prefix; calibrated growth saturates without wrapping |
| Usage | Repeated cumulative snapshots stay at the latest input/output totals; cache and reasoning subsets; Anthropic initial output remains unknown; missing/partial/zero usage; overflow rejection |
| Compaction decisions | Below/equal/above explicit threshold; minimum history; disabled policy; derived threshold with capped output reserve; absence of a recent user boundary; provider-calibration switch |
| Compaction recovery | Recent-turn suffix preserved; summary requests omit tools; successful replacement clears the anchor; stream/tool/non-reducing/persistence failures preserve pre-commit history; acknowledgement/display failures do not publish two lifecycle terminals |
| Tool protocol contract | Required parameters survive all four encodings; missing/null/misnamed/non-object schemas, malformed properties/required lists and duplicate names reject before request encoding |
| Stream faults | Four protocols crossed with failure before response, after partial text, after complete tool arguments but before terminal response, and after final usage; no incomplete tool execution or persisted pending calls; observed usage survives later stream failure |
| Recovery and effects | Started/unstarted interrupted calls are not replayed; failures before tool-start persistence and after an effect but before receipt persistence; durable receipt reuse and conflicting-argument rejection |
| Shell boundaries | Independent simultaneous sessions; session capacity; EOF/stale IDs; invalid numeric limits before spawn; bounded stdout/stderr; two-session cancellation cleanup; full-buffer draining; UTF-8 across byte fragments and pipe reads, including a literal terminal U+FFFD |

The tests also exposed and accompany these fixes:

- Validate the tool schema envelope before encoding it, instead of sending a null parameter schema when an extension uses the wrong field.
- Close the compaction lifecycle on validation/persistence failures. Persist the candidate replacement before changing in-memory history/anchor. Observe lifecycle transitions only after service acknowledgement; a later display failure does not emit a second terminal event.
- Retain incomplete trailing UTF-8 in the pipe reader across reads/polls and distinguish literal U+FFFD from an incomplete decoding placeholder.
- Preserve initial Anthropic text-block content as well as later text deltas, without duplicating replay text.

Verification: `mise run test` passes **178 backend tests**; `mise run check` passes formatting/script lint, Rust workspace checks and Swift Helper compilation. The final pot-agent-only rerun passes all 28 tests after making persistence-failure injection depend on checkpoint phase rather than save-call counts. `git diff --check` passes. No paid inference, image rebuild/boot, line-coverage percentage or exhaustive fault/fuzz coverage is claimed. Product billing remains covered separately by Runtime/Storage tests; Agent unit observations are not an authoritative settlement ledger.

## Host/Sandbox follow-up

The guest controller placement described above is historical. Current deployment and independently rerun routing/cleanup validation are recorded in [Host pot-agent acceptance](host-pot-agent-20261006.md).
