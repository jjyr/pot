---
tags: [Agent, Architecture, POC, pot]
---
# Pot architecture research and POC validation

> Old `poc/` sources are removed; commands apply only to the linked historical commit.

> Historical record: versions, allowances, UI reflect section dates. Current scope/contracts: [Development plan](../development-plan.md), [Documentation index](../README.md).

Date: 2026-10-03. [Local personal Agent workspace design](../product-design.md) v0.11. Code/evidence: [POC reproduction](baseline-reproduction.md).

Official research, native UI/VM experiments, five real basic driver integrations, deterministic Runtime tests complete. **These support continuing the technical approach, not full Pot/five-driver execution/Dot parity acceptance.** No real models/user credential reads-writes/service accounts. Revalidation adds local fixed model fixtures with real Pi tools/session save-resume/cancel/steering.

> Historical experiments through 2026-10-03. Remaining acceptance includes future product work, not current priority; see [POC plan](../poc-plan.md). [evidence](https://github.com/jjyr/pot/tree/5ed4f40277bceb2f1a62f90c2fdf789f54f3bdb2/poc/baseline/evidence); unqualified evidence filenames are relative there.

## 1. Conclusions and architecture decisions

| Question | Conclusion | Decision |
|---|---|---|
| GPUI with tray/desktop viewer? | GPUI 0.2.2 + Swift NSStatusItem/same-window WKWebView runs; noVNC connects/sends keys | Rust GPUI + small Swift bridge; WKWebView/noVNC first |
| Avoid requiring Docker? | Built Apple Containerization 0.47.0 example; real ARM64 VM/shared-directory recreate/network pass | Local Swift Helper/library; cctl is library probe, not final Helper |
| What is agy / embedding? | User confirmed Google Antigravity; official Python SDK0.1.20 start/cancel/exit passes | Prefer SDK adapter; CLI stream-json differs from Claude controls |
| Shared five-driver protocol? | Pi RPC/OpenCode HTTP/Claude SDK/AGY differ; Codex basic App Server passes fresh Linux VM, host backfill no longer blocks | Stable product protocol, per-driver adapters/capabilities, not single ACP abstraction |
| Long-term state? | SQLite Runtime crash/event/revoke/generation tests pass, no memory-quality evaluation | Minimal C: product explicit preferences/tasks/schedules/auth facts; provider native context |
| Takeover guaranteed by flag? | Cooperative races pass, shell bypass remains; real separate UID protected files unreadable | Separate controller/desktop/Agent domains, enforce controlled entries; no Agent root/display/CDP |
| Unified login? | Claude/Codex third-party auth restrictions differ | Per-driver permitted Setup auth, separate model/service authorization |

## 2. Evidence levels and completed POCs

Real validation uses actual programs/VMs/drivers; fixture validation controlled services; source/docs are not runtime passes. Retain limitations with evidence.

### 2.1 Native UI and display path

Built GPUI 0.2.2; same-process Swift locates NSWindow/adds NSStatusItem/WKWebView. noVNC1.6.0 completes RFB3.8 fixture handshake/display/a-key down-up. Screenshot inspected; tray action reopens same hidden window.

`poc/baseline/evidence/native-ui.json`, `rfb-events.json`, `native-viewer.png`. Colored framebuffer is fixture, not Linux desktop. Tray invoked action method, not real mouse-menu acceptance.

Real Apple VM -> Xvfb -> headed Chromium -> x11vnc -> loopback WebSocket -> same GPUI WKWebView/noVNC. `native-vm-viewer.png` displays real browser/Chinese page, keys reach VNC, Ctrl+L selects address. `native-vm-ui.json`, `rfb-vm-events.json`, `linux-vnc.txt`. POC --no-sandbox/dbus-GPU warnings are not hardened image acceptance.

Default GPUI lacked offline Metal Toolchain; runtime_shaders builds. Packaging choice, not GPU/distribution acceptance. Resize/Chinese IME/clipboard/multiscreen/Accessibility untested.

### 2.2 Apple Containerization

Pinned library0.47.0 (`bc994b88df46207fad7775b0eabc51947e315881`), upstream cctl modified only temp state/init pin/explicit network flag. Ad hoc virtualization signing; no global Apple service/Docker/OrbStack.

Real passes:

1. Alpine3.22 ARM64 starts, `uname -m` aarch64.
2. `/data/proof.txt` survives new VM same host mount, `PERSISTENCE_PASS`.
3. Controller dir0700/file0600 unreadable by new `potagent` UID, `SEPARATE_UID_BOUNDARY_PASS`.
4. Vmnet eth0/custom DNS HTTPS Alpine index, `NETWORK_PASS`.
5. Xvfb/Chromium/x11vnc/Chinese fonts; normal desktop UID headed browser, native viewer/keys; `vm-results.json`.

Official Kata3.32.0 ARM64 kernel archive SHA256 `8736c054d9223974735394f822000823baef509e1c33405ec798240fa9b6e4b5`; kernel `fb2cfb79eb1ae19447a85d75682d7fa5cfec97e24beb2609a492b806e8072c8d`; init `ghcr.io/apple/containerization/vminit:0.47.0`.

Shared-directory persistence is not rootfs persistence. Removing cctl guest state loses APK installs. Ship pinned GUI image; persist workspace/browser/provider/rebuild manifests separately.

UID file proof is preliminary, not all X11/CDP/shell bypass prevention. Passwordless VNC is controlled POC only; product must authenticate broker without raw VNC exposure.

### 2.3 Driver integrations: handshakes and uncovered capabilities

| Driver / pinned version | Executed | Not executed | Recommended adapter |
|---|---|---|---|
| Codex0.159.3 | Fresh Linux initialize/reinitialize rejection/ephemeral thread start-read/idle interrupt rejection, 5/5 | Real turns/resume/steer/interrupt/compact/tool sandbox | Direct App Server, clean deployment; ACP external compatibility |
| Claude SDK0.3.288 | Native stream-json initialize control handshake | API-key turn/permission callback/active cancel/resume | Official SDK; raw protocol diagnostics |
| Pi0.87.1 | RPC management; local fixture real write/persistence/restart/active abort-steer | Real provider capabilities/quality | RPC, current `@earendil-works/pi-coding-agent` |
| OpenCode1.18.34 | HTTP health/session create-read-abort-delete, 162 OpenAPI paths | prompt/SSE/tools/active cancel/resume | HTTP Server + SSE / SDK |
| AGY SDK0.1.20 | Agent start/connection.cancel/normal close with deny-all | Valid auth chat/history-resume/tools/tasks | Python SDK sidecar, pinned SDK/runtime |

`engine-probes.json`, `agy-sdk.json`. Initial idle cancellation only; Pi fixture adds active stop, others pending. Initial Pi session not materialized without turn; fixed model tools materialize JSONL and real process restart resumes. Fixture scope, no real provider. AGY files exist but conversation_id null, not resume acceptance.

Host Codex initialization waited for existing ~/.codex backfill despite temp sqlite_home; 30-second timeout in sandbox/approved standalone. No locks/DB/user state touched. Redacted codex-backfill.txt is local blocker, not product infeasibility.

AGY stream-json uses `event`, explicitly not Claude control_request/control_response. Official ARM64 CLI1.2.15 manifest found; SDK/other handshakes on Mac. **Codex basic Linux protocol now verified; other drivers need separate product-image acceptance**.

### 2.4 Product Runtime: 22 tests

`runtime.py`/`test_runtime.py` real SQLite/crashing subprocess/thread coordination, original22/22; 4 adversarial additions fix3 defects. Original coverage:

- Crash before/after commit, restart reads/event deduplication.
- Persistent outbox/late worker epoch fencing.
- Unknown external outcomes neither success nor blind retry.
- Explicit preference correction/forgetting tombstones, binding invalidation/new epochs.
- Missed schedules coalesce/skip, responsibility survives restart.
- Account/scope/revocation/action rules.
- Close input/wait in-flight before takeover, human ownership persists restart.

Minimal deterministic implementation, not production/distributed consistency/LLM review/semantic memory/real provider worker feedback.

Minimal C: product owns authorization/schedule/task facts; summaries cannot decide sent status/permission. A/C quality/cost/false memory untested; no model-quality victory claim.

### 2.5 OAuth fixture: 3 tests

Real loopback callbacks state/PKCE/one-use-expiry/account-scope/revoke, 3/3. Local issuer; **no GitHub OAuth registration/token exchange**. Mechanism, not platform approval/real auth/revocation propagation.

## 3. Official research corrections

### 3.1 Dot: behavioral baseline researched, pixels/motion need real access

Official Getting started/Tasks and memory/Controls/Computers and apps/Messaging support main/task threads, Activity/Outputs/Scheduled/guidance/services/review/Custom Rules/view-Take over-Return control. Viewing is not takeover; main pause/delegation stop/schedule off differ.

Custom Rules are instructions Dot tries to follow, not OS isolation/scope authorization. Enforce hard limits at tools/account scopes separately.

User chose Setup then chat naming, default Pot. Dot supports personalization but does not prove this exact sequence; user requirement, not verified Dot behavior. Real logged-in access still awaiting user response; no unauthorized inspection.

### 3.2 Setup auth depends on distribution

[Claude SDK overview](https://code.claude.com/docs/en/agent-sdk/overview) says third parties need approval to offer claude.ai login/subscription limits; use Quickstart API keys. Default API key; installed CLI subscription is not automatic permitted inheritance.

[Codex Auth](https://developers.openai.com/codex/app-server): existing local/open-source authentication continues, migrate to Sign in with ChatGPT; commercial/hosted never permitted this authentication, SIWC supports those cases. Check actual distribution; commercial cannot assume App Server OAuth. Handshake is not authorization.

Pi/OpenCode follow providers; AGY SDK has api_key. Each needs low-risk actual turns. Five Setup targets do not justify Ready without required capabilities.

## 4. Remaining acceptance and blockers

| Priority | Work | Missing conditions | Pass criteria |
|---|---|---|---|
| P0 | Real Dot parity | User Dot/access authorization requested | Screenshots/flows main-task/Activity/Outputs/Scheduled/review/takeover/onboarding |
| P0 | Five real-model Linux adapters | No POC model credentials; fresh guest bypasses host Codex state | Per-driver task/resume/active stop/steer-follow-up/permission callbacks |
| P0 | Hardened desktop | UID files/cooperative broker incomplete | Agent no direct display/CDP/VNC/socket; shell-worker negative bypass tests, drain input |
| P0 | Distributable package | Ad hoc only | Developer ID/notarization/updates/entitlements/cold install-restart-uninstall |
| P1 | Tool/profile upgrades | Shared recreate only | GUI upgrade/manifests/profile/rollback/schema recovery |
| P1 | GitHub service loop | Local fixture not registration/token | Dedicated account/minimal scope/callback/revoke/uncertain-write verification |
| P1 | Chinese input/performance | Fonts not IME, no baseline | IME/clipboard/resize/cold start/download/memory/latency/takeover-cancel residue |
| P1 | A/C semantic memory | No real turns/long-term evaluation | Fixed cases/cost-accuracy/correction-forgetting-relearning; then select strategy |
| P1 | Background lifecycle | Schedule logic only | sleep-wake/exit/budget/notifications, truthful offline state |
| P2 | Remote Docker/multinode | Not first release prerequisite | Pair/revoke/bind; local library is not Docker API |

Basic feasibility has evidence; remaining work is credentials/UI/isolation/packaging/real tasks, not another general research round. More fixtures cannot mark it passed.

## 5. Independent revalidation and fixes (2026-10-03)

User-requested rerun Runtime/OAuth/four handshakes/AGY and new Apple VM persistence/UID/network pass. Host Codex backfill still times out. Local model8080 connection refused; no borrowed accounts/allowances.

New `test_adversarial.py` initially3/4 failures (`adversarial-before.txt`), fixed4/4 (`adversarial-after.txt`):

1. `return_control` previously reenabled input with old in-flight operations; reject until zero, truthful handoff state.
2. `review` previously allowed unknown/misspelled rules; explicit allowlist rejects.
3. `review` previously allowed unknown actions; reject until capabilities/rules include them.

22+4+3=**29 pass**. Counts do not prove boundaries. Old idempotent-consumer uses memory set/event IDs, not persistent consumer; implement atomic/recoverable acknowledgment-effects paths.

`pi_turn_fixture.py`: loopback OpenAI SSE fixed write call, real proof.txt/session JSONL, stop/restart same session restores5 messages; active incomplete SSE abort returns idle, steer appears in next request. No real reasoning/memory-quality claim. `pi-turn-fixture.json`.

`reverification.json`, `vm-reverification.txt`. Asked user for model credentials/Dot access; waiting is not approval.

## 6. Fresh Codex Linux VM validation (2026-10-03)

User chose fresh container instance without host backfill mutation. New Apple Alpine3.22 ARM64 VM shares only POC directory; fresh default `/root/.codex`, no host mount/HOME-CODEX_HOME changes.

Official `rust-v0.159.3/codex-aarch64-unknown-linux-musl.tar.gz` matches release SHA256 `cd5f307b3fcd6080773e684b86c3114a67d4f1c61dc447be09876b552eb4bea7`. Actual codex-cli0.159.3 reports Alpine3.22.6/aarch64.

Five passes: initialize; Already initialized; ephemeral thread/start; same idle thread/read; no active turn to interrupt. Last is error path, not active cancellation.

`poc/baseline/guest/codex_app_server_probe.py`, `codex-linux-app-server.json`, `codex-linux-release.json`. VM cleaned, no turn/start/model/auth mounts.

Clean guest basic integration works; host state not architecture blocker. Does not prove running App caused backfill or auth-free models. stderr missing PATH bubblewrap uses bundled fallback; pin/configure/test full tool sandbox separately.

## 7. Sources and pinned versions

Online docs read 2026-10-03; manifest URL/snapshot hashes. Webpages change; locks/commits/releases/snapshot hashes define versioned evidence.

- [Dot Getting started](https://learn.chatgpt.com/docs/dots/getting-started), [Tasks and memory](https://learn.chatgpt.com/docs/dots/tasks-and-memory), [Controls](https://learn.chatgpt.com/docs/dots/controls), [Computers and apps](https://learn.chatgpt.com/docs/dots/computers-and-apps), [Messaging](https://learn.chatgpt.com/docs/dots/channels).
- [Codex App Server](https://developers.openai.com/codex/app-server), [pinned source](https://github.com/openai/codex/tree/rust-v0.159.3), [Sign in with ChatGPT](https://developers.openai.com/siwc).
- [Claude SDK overview](https://code.claude.com/docs/en/agent-sdk/overview), [permissions](https://code.claude.com/docs/en/agent-sdk/permissions).
- [OpenCode server](https://opencode.ai/docs/server/), [SDK](https://opencode.ai/docs/sdk/). Pi installed0.87.1 docs/rpc.md, not old repository-name inference.
- [AGY SDK overview](https://antigravity.google/docs/sdk/overview/), [policies](https://antigravity.google/docs/sdk/policies/), [lifecycle](https://antigravity.google/docs/sdk/lifecycle/), [CLI headless](https://antigravity.google/docs/cli/headless/).
- [Containerization 0.47.0](https://github.com/apple/containerization/tree/0.47.0), [container 1.5.0](https://github.com/apple/container/releases/tag/1.5.0), [Kata 3.32.0](https://github.com/kata-containers/kata-containers/releases/tag/3.32.0).
- [GPUI 0.2.2](https://crates.io/crates/gpui/0.2.2), [noVNC v1.6.0](https://github.com/novnc/noVNC/tree/v1.6.0).

Distribution/license review: actual gpui0.2.2 Apache-2.0; noVNC MPL-2.0/Apple Apache-2.0 and forwarded resources need NOTICE/SBOM and delivered-file review. Technical POC does not settle licenses/distribution authorization.
