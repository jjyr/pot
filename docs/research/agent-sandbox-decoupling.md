# Agent and Sandbox decoupling feasibility

Research date: 2026-10-06. Status: source-level assessment and proposed design, not an implemented feature or runtime acceptance report.

Subsequent product decisions are maintained in [Local Agents and managed Sandboxes](../agent-sandbox-design.md): Pi/Codex only for this increment, main-only Sandbox management, workers fixed to one environment, and no direct Host tool execution. That specification takes precedence over exploratory alternatives below.

## Conclusion

Decoupling is feasible. Keep the Agent loop, native session state, Runtime, Storage, and model gateway on the host (or another control-plane machine). Dispatch environment operations to separately managed execution targets. A conversation can then continue without a running Sandbox, and one Agent can work across several targets.

The model inference location is a separate choice: a remote API, a local model server, or a service on another machine can supply the same Agent loop. Local inference does not require tool execution to happen locally. Pi still needs a compatible provider protocol and model capable of producing usable tool calls; local inference performance and model quality were not measured here.

The essential requirement is control over **every execution entry point**, rather than observation of tool events. Host shell/file tools, extension tools, implicit context loading, and user shell shortcuts can otherwise bypass the selected Sandbox. Running a trusted Agent application on the host with mediated tools is feasible; this is not equivalent to isolating arbitrary Agent plugins from the host.

Recommended first slice: host Runtime + host Pi + the existing Linux Sandbox, with all seven environment tools routed through a broker and the VM started only on the first environment operation. Preserve the existing Agent loop rather than implementing a new one.

## Current coupling, verified in the repository

| Boundary | Current implementation | Required change |
|---|---|---|
| App → backend | `app/src/client.rs::endpoint()` asks Helper for the guest IP and reads guest-published endpoint metadata | Discover a host backend independently of Sandbox status |
| Sandbox startup | `helper/Sources/PotHelper/main.swift::boot()` starts `/opt/pot/desktop.sh` and shares the data directory at `/data` | Separate executor/desktop startup from product backend startup; support identified target instances |
| Backend deployment | Server is standalone, but product deployment puts Server, Runtime, Storage, Gateway, and Driver in the Linux guest | Run the product control plane persistently outside disposable execution targets |
| Agent contract | `core/src/execution.rs::ExecutionContext` has one local `workspace`, local native session paths, and backend endpoints | Separate Agent state/work directory from target workspace and target identity |
| Pi environment tools | `driver/src/pi.rs` enables native `read,write,edit,bash,grep,find,ls` and sets local cwd; its prompt says Linux Sandbox | Replace all seven implementations; supply target-aware environment context |
| Codex environment tools | `driver/src/codex.rs` enables native shell and patch features and uses `danger-full-access` on ordinary turns | Do not move these settings unchanged onto the host; use mediated tools and verify native bypasses are disabled |
| Existing tool bridge | `driver/src/pot-tools.mjs` and `runtime/src/tool_bridge.rs` dispatch task/schedule operations | Add a distinct execution dispatcher and permissions; current MCP workers receive no tools |
| Tool events | `Update::ToolStarted/ToolFinished` and CLI event normalization project execution observations | These are not a request/result interface that can redirect native execution |
| Cancellation | Driver waits for native CLI/terminal cleanup | Also wait for executor operations and their processes to settle; CLI exit alone is insufficient |

The backend crate split is useful groundwork: Server can already start without App/Helper and accepts independent data/workspace paths. It does not establish remote execution or automatic Host deployment. Some architecture prose still mentions historical removed drivers; current code and `docs/development.md` take precedence.

## Upstream evidence and Driver feasibility

### Pi: direct support in the pinned version

The image pins `@earendil-works/pi-coding-agent` **0.87.1**. Its published npm archive was inspected, without installing or running it:

- `dist/core/agent-session.js::_refreshToolRegistry()` registers extension tools after built-ins in the name-indexed registry. A registered tool with the same name replaces the executable implementation.
- `dist/core/tools/{read,write,edit,bash,grep,find,ls}.d.ts` expose pluggable operation interfaces for remote execution. `BashOperations.exec` includes streaming output, cancellation signal, timeout, and exit status.
- `examples/extensions/ssh.ts` keeps Pi local and delegates read/write/edit/bash to SSH. It is an example, not a complete seven-tool isolation solution.
- `docs/containerization.md` and `examples/extensions/gondolin/index.ts` explicitly keep Pi and file-based provider credentials on the host while overriding all seven built-ins and user `!` commands to execute in a Linux micro-VM.

This is strong source evidence for the proposed split without rewriting Pi's tool loop. It does **not** establish Pot integration acceptance. The Gondolin example starts its VM at `session_start`; Pot must change that policy to start only on an authorized environment call. It also forwards environment variables and shares the host workspace, so it must not be copied as a credential-isolation design.

Pi's wrappers retain some host path processing: for example, `read` invokes `resolveReadPathAsync`, which probes host paths before using the delegated operations. Reusing wrappers is convenient for a Linux first slice, but avoiding host probes and supporting Windows path semantics may require registering full broker-backed tool implementations instead. All target path resolution must ultimately happen on the target.

### Codex: native remote executor plus client-owned tools

The image pins Codex **0.159.1**. A follow-up inspection of the same pinned source found a stronger native path than MCP mediation: `codex exec-server` provides process and filesystem execution separately from `codex app-server`. Its README documents WebSocket transport, authentication, process start/output/terminate and filesystem read/write/directory operations. App Server's experimental `environment/add` accepts an environment ID and executor URL; thread/turn environment selection connects the Agent session to that execution environment. `environment_processor.rs` implements registration through the environment manager. This is implemented upstream code, not merely a tool schema.

The preferred Codex investigation should therefore be Host `codex app-server` → target `codex exec-server`, keeping inference/session orchestration on the Host. The executor is also launched through the official Codex binary; it does not need to run an Agent/model loop. Direct authenticated executor URLs provide a local integration path without requiring the documented remote-registry/relay service. Pot currently does not use `environment/add`, target selection, or `exec-server`.

Pot already uses MCP. As another route, the matching upstream protocol declares experimental `thread/start.dynamicTools` and the client request `item/tool/call`, explicitly documented in code as executing a dynamic tool call on the client. Either MCP or dynamic tools can supply Pot-owned broker operations. Dynamic tools require handling request/result messages; Pot's current Driver declines unknown client requests rather than executing them.

Neither tool notifications nor approval requests should be treated as a universal replacement hook for built-in shell/filesystem execution. The native remote-executor path is distinct from replacing tools through Pi extensions. Exact execution routing, implicit reads, inherited integrations, thread resume, and tool result delivery need acceptance tests. Current Pot disables native shell/patch features for summaries only; that is not proof of a complete no-host-execution mode. The remote executor's lazy connection/status machinery also does not prove zero VM starts during pure chat: context loading or environment metadata may require target access before the first shell call.

The protocol's `SandboxPolicy::ExternalSandbox` names an externally enforced isolation policy. Its existence alone does not implement transport to another VM or redirect native commands there.

Until Codex remote execution passes routing and bypass tests in Pot, keep its original driver deployment in an execution environment or exclude it from the Host-with-Sandbox-tools mode. Driver support should be an explicit capability rather than a universal claim. This follow-up strengthens the source-level conclusion: Codex has a native architectural path for decoupling and deserves a parallel option in the PoC plan, without implying that Pot already implements it.

### Grok: unverified for this mode

The current Grok adapter launches `grok agent --no-leader stdio`, starts/resumes ACP sessions with a local cwd and an empty MCP server list, and rejects worker execution. Permission requests are cancelled; other client requests receive `Client capability unavailable`. Generic ACP concepts cannot prove this specific CLI delegates all execution to the client.

The exact Grok 1.0.46 npm archive was inspected. Its wrapper/bootstrap selects an official platform-native binary; npm installation does not mean the Agent is a Pi extension or hosted model SDK. The published README advertises MCP configuration and agent mode, but establishes no same-name native tool replacement, complete disablement contract, or independent remote executor. Detailed xAI documentation could not be retrieved because TLS connections failed in this pass. This investigation establishes no complete tool redirection contract for it. Treat Host-with-Sandbox-tools support as unavailable until verified; this is an evidence limit, not a claim that the upstream product cannot support it.

## Proposed boundaries

```text
Host / control-plane machine
  App → Server → Runtime + Storage + Gateway
                   │
                   ├── Driver / Agent loop / native session
                   │      ↔ remote API or local inference server
                   │      │ execution tool request
                   │      v
                   └── Tool broker / authorization / operation ledger
                              │
                         Target manager
                         ├── Linux provider → executor + optional desktop
                         ├── macOS provider → executor + optional desktop
                         └── Windows provider → executor + optional desktop
```

These are responsibility boundaries; they do not require a new process or crate for each box. A small executor inside each target needs only authorized command/file operations, output transport, cancellation, and health reporting. It does not need the model, native Agent CLI, product database, or provider credentials.

Task/schedule/list operations stay in the control plane and do not start a VM. Environment operations use the selected target. Host filesystem or shell operations, if offered as a product capability, require their own explicit policy and scope; they must not be a fallback when a Sandbox fails.

Keep the existing single-target tool interface initially by assigning one immutable target binding to each worker. Later add explicit `target_id` to execution operations, or expose target-specific tools. A single Agent can acquire Linux/macOS/Windows targets and dispatch work concurrently if its Driver supports parallel calls. Otherwise it can coordinate target-bound workers. Target resources and provider capacity remain managed by Runtime, not by arbitrary model-generated hypervisor commands.

Distinguish an Agent binding from an execution target binding. Changing targets should not silently replace the native conversation; a destroyed target should not silently reuse a fresh machine as though it retained the previous working state.

## On-demand lifecycle and execution contract

Use `requested → starting → ready → draining → stopped`, with explicit failed/unknown outcomes. Persist target intent and operation state on the control plane.

- Readiness means the authenticated executor is ready, not merely that the hypervisor reports a running VM.
- The first authorized environment operation acquires a target lease. Concurrent first calls share one start attempt; startup consumes target capacity, not multiple independent VM allocations.
- Pure chat, task listing, and status inspection do not acquire targets. Let the Agent explicitly request targets for cross-platform work, with automatic acquisition for the default environment.
- Idle reclamation checks active operations, terminals, desktop viewers, background jobs, and declared leases. Stopping a target while a daemon still supplies a task is not idle reclamation.
- Separate ephemeral VM state from persistent workspaces/artifacts. A stopped VM, suspended VM, and destroyed VM have different restart semantics. Preserve declared volumes; report loss of ephemeral processes/state.
- Bind calls to owner, epoch, turn, tool-call/operation ID, target ID, and target generation. Use a target-scoped capability with allowed operations, paths, network policy, deadline, and resource limits. The executor receives no root product token.
- The broker validates arguments and ownership before starting or dispatching. Return target OS/architecture, cwd, exit status, output/artifact references, and operation identity. Bound streamed output and carry truncation metadata.
- Resolve Linux/macOS paths and Windows paths on their respective targets. Do not concatenate model arguments into SSH or hypervisor command strings. Windows needs a PowerShell/exec contract rather than a promise that every target has `/bin/bash`.
- Persist operation acceptance and use executor deduplication where possible. A lost acknowledgement is an unknown outcome; do not automatically rerun a potentially completed shell command or external action. Exactly-once external side effects cannot be promised by the broker ledger alone.
- Cancellation sends an operation cancellation request and waits for target process containment to settle. Linux process groups/cgroups, Windows Job Objects, and the chosen macOS containment need provider-specific implementation. Killing the host CLI does not kill a remote command. Unknown cleanup retains occupancy/quarantine.

Reuse Runtime's existing epoch/turn filtering, queues, durable receipts, and failure reporting where applicable. Execution leases, target generations, remote cleanup, and operation reconciliation are additional contracts, not already delivered by the orchestration MCP facade.

## Linux, macOS, and Windows targets

| Target | Practical provider direction | Constraints and research status |
|---|---|---|
| Linux | Existing Apple Containerization Helper on the Mac; later Linux Docker/VM or remote provider | Best first slice. Extend the current single-instance lifecycle and add a small executor. Apple Containerization's Linux kernel/OCI flow does not start macOS or Windows guests. |
| macOS | Apple Virtualization.framework macOS VM on Apple silicon, a macOS VM manager with automation, or a remote Mac VM | Apple documents the macOS guest path separately. Needs guest image installation, provisioning, identity, lifecycle, and desktop integration. Validate applicable macOS license, platform and concurrency limits for the intended deployment. This report did not boot or provision a macOS guest. |
| Windows | An automated VM provider such as Parallels on a Mac, or Windows VM/Hyper-V/cloud provider on a remote Windows-capable node | Provider integration and Windows/image licenses are separate. On Apple silicon, Windows ARM is the natural guest; x64 emulation is not equivalent to testing an x64 Windows kernel or drivers. Parallels documents CLI automation, but Pot compatibility and guest command/cancellation behavior were not tested. |

The same Agent controlling three OS targets is feasible at the broker layer. It does not imply a universal OCI image format, one hypervisor, unlimited concurrency on a laptop, or instant cold boots. Declare OS, architecture, shell, desktop, persistence, and resource capabilities and select a compatible provider. Remote providers are a useful route to OS/architecture combinations unavailable locally.

Artifact transfer must be explicit: copy source to each target and collect outputs by target. A shared writable directory across three simultaneous builds can cause collisions and does not have portable filesystem semantics. References to outputs should remain accessible after target shutdown.

## Security consequence of moving the Agent to Host

Today guest tools share a user domain with Runtime and can potentially reach its files. Keeping Runtime, database, and provider secrets on the host while exposing narrowly scoped executor operations can improve that boundary.

However, a host Agent process is trusted application code. Native plugins, extensions, config discovery, telemetry, and implicit context reads may still execute or read on the host. Tool mediation protects against model-selected environment operations only when all such routes are covered. Use Pot-controlled extension loadouts, minimal host permissions, no broad home mounts, and environment allowlists. Credentials supplied as host environment variables must not be forwarded wholesale to targets.

Keep lifecycle management APIs outside the target. A guest credential should authorize only that target's permitted executor actions, not new VM creation, access to another task, or the product control API. Browser/desktop tools must use the same target identity and permission boundary; today's cooperative desktop broker does not establish this isolation.

## Suggested validation sequence

1. **Pi redirection contract:** host Pi with controlled extensions and a mock model requests all seven tools. Verify execution occurs only in a separate test target; attempts to read host sentinels, use shell shortcuts, or load unapproved tools cannot bypass mediation. Verify outputs, errors, streaming, abort and native-session resume. No paid model call is needed.
2. **Linux product slice:** host Server/Runtime with a private fresh database, separate App discovery, existing Helper as target provider, and an executor-only image. Verify opening/chatting/listing tasks starts zero VMs; the first environment call starts exactly one; concurrent calls do not duplicate startup; shutdown leaves chat and schedules usable. Do not move/open an active guest database from the host.
3. **Lifecycle/recovery:** delayed boot, failed boot, guest crash, lost acknowledgement, Host restart, cancellation of child/background processes, idle stop, target recreation and persistent artifact retrieval. No silent execution on Host and no automatic replay of unknown side effects.
4. **Driver qualification:** test Host Codex App Server with a separate authenticated `codex exec-server` first, including native shell/filesystem routing, context reads, resume, cancellation and lazy startup. Evaluate MCP/dynamic tools if Pot needs its own tool contract. Treat Grok as a separate investigation.
5. **Multiple targets:** two Linux targets first to test routing/ownership; then macOS and Windows providers. Verify OS/architecture selection, per-target workspaces, result attribution, capacity queueing, and cancellation independence. Measure cold/warm start time and idle/active resource use on each provider.

This sequence tests the architectural risk before investing in three OS backends. Scope the first implementation around host Pi and Linux; the execution protocol should allow later providers without making their acceptance a prerequisite.

## Sources and limits

Local evidence: `docs/architecture.md`, `docs/backend-crates.md`, `docs/development.md`, `runtime/README.md`, and the code paths named above. Inspected upstream material:

- [Exact Pi 0.87.1 registry metadata](https://registry.npmjs.org/@earendil-works/pi-coding-agent/0.87.1) and [published source archive](https://registry.npmjs.org/@earendil-works/pi-coding-agent/-/pi-coding-agent-0.87.1.tgz), including the files listed in the Pi section.
- [Pi repository](https://github.com/earendil-works/pi), with [containerization documentation](https://github.com/earendil-works/pi/blob/main/packages/coding-agent/docs/containerization.md) and [Gondolin example](https://github.com/earendil-works/pi/tree/main/packages/coding-agent/examples/extensions/gondolin). These main-branch links can change; findings above came from the exact npm archive.
- [Codex 0.159.1 thread protocol](https://github.com/openai/codex/blob/rust-v0.159.1/codex-rs/app-server-protocol/src/protocol/v2/thread.rs) and [sandbox/permission protocol](https://github.com/openai/codex/blob/rust-v0.159.1/codex-rs/app-server-protocol/src/protocol/v2/permissions.rs).
- [Codex 0.159.1 executor documentation](https://github.com/openai/codex/blob/rust-v0.159.1/codex-rs/exec-server/README.md), [environment protocol](https://github.com/openai/codex/blob/rust-v0.159.1/codex-rs/app-server-protocol/src/protocol/v2/environment.rs), [environment implementation](https://github.com/openai/codex/blob/rust-v0.159.1/codex-rs/app-server/src/request_processors/environment_processor.rs), and [client-owned dynamic tool request](https://github.com/openai/codex/blob/rust-v0.159.1/codex-rs/app-server-protocol/src/protocol/common.rs).
- [Exact Grok 1.0.46 metadata](https://registry.npmjs.org/@xai-official/grok/1.0.46) and [published wrapper/README archive](https://registry.npmjs.org/@xai-official/grok/-/grok-1.0.46.tgz).
- [Apple: running macOS in a VM on Apple silicon](https://developer.apple.com/documentation/virtualization/running-macos-in-a-virtual-machine-on-apple-silicon). The page identifies the official implementation direction; its dynamically rendered detailed API content was not inspected in this pass.
- [Parallels command-line interface](https://docs.parallels.com/parallels-desktop-developers-guide/command-line-interface-utility), as a provider automation entry point, not Pot acceptance evidence.
- Existing [Apple Containerization research](apple-containerization.md), as prior background about Linux VM boundaries, not new cross-platform acceptance.

No runtime code was changed, no hypervisor or dependencies were installed, no real model calls were made, and no VM was started. This report establishes source-level feasibility and specifies the experiments needed to establish delivered behavior; it claims no latency, resource, isolation or cross-platform test results.
