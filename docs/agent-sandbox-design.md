# Local Agents and managed Sandboxes

Updated: 2026-10-08. Implemented boundaries; dated acceptance is recorded separately. This is the current target specification for Agent/Sandbox placement and permissions. See the [architecture](architecture.md) for the current deployment and [Sandbox provider redesign](sandbox-providers.md) for the Linux extraction and target macOS provider contracts.

## Confirmed scope

Pot runs Agent loops and the persistent product control plane on the Host. Sandboxes are separately managed execution resources. The main Agent can control multiple Sandboxes; a task worker executes Sandbox tools within one assigned environment and cannot control Sandbox lifecycle or select another Sandbox. pot-agent workers may separately request owner-authorized Host access.

- pot-agent is the single supported Agent engine.
- Host-side Agent execution does not mean local model inference. Model connections remain a separate configuration concern.
- pot-agent uses shared execution tools with an explicit environment target through its [Agent-owned permission layer](permissions.md), with environment-local policy (Host defaults to ask; Sandbox defaults to allow). No execution falls back to Host when a Sandbox is unavailable.
- Multiple Sandbox instances are part of the target model. Linux is implemented. The minimum provider redesign targets Linux default/isolated tasks and macOS development; Android, iOS and Windows remain later extensions. Providers expose qualified capabilities rather than assuming every environment has a shell or POSIX workspace.
- Host Runtime, Storage, Gateway and Thread contexts remain available when every Sandbox is stopped. Sandbox shutdown does not delete a conversation or product task record.

## Authority and execution

| Capability | Main Agent | Task worker | Runtime / Sandbox Manager / provider |
|---|---|---|---|
| List Sandbox inventory and inspect lifecycle status | Yes | No management API | Authoritative state |
| Request create/start/stop/delete | Yes, through managed tools and policy | No | Validate, queue, execute and persist |
| Choose an execution Sandbox | Yes | No | Validate ownership and capabilities |
| Execute environment tools | In an explicitly selected Sandbox | Only in its assigned Sandbox | Route and enforce target scope |
| Assign a Sandbox to a task | Yes | No | Persist immutable task environment binding |
| Operate another task's environment | Only with main-authorized scope | No | Reject unauthorized operations |
| Execute tools targeting Host | pot-agent, with owner authorization | pot-agent, with owner authorization | Owner services |

Workers may inspect execution metadata for their own environment, such as OS, cwd and tool availability. That does not grant Sandbox inventory, provisioning or lifecycle capabilities. A worker reports a missing dependency or unavailable environment to the main Agent/Runtime instead of creating, restarting or changing a Sandbox.

This boundary must be enforced by tool loadouts, scoped credentials, Runtime grants and Sandbox Manager routing, not only by prompts. Workers receive neither an executor/management credential nor access to Host helper sockets, hypervisor interfaces or an unrestricted execution gateway. A worker cannot select another Sandbox; pot-agent may explicitly request owner-authorized Host execution. Shell execution inside a Sandbox must not provide access to the management plane.

Runtime can prepare or recover the already assigned environment according to its lifecycle policy; that is infrastructure behavior, not worker lifecycle authority. Preparation must not silently create a different environment or change the task binding.

The target architecture inserts a dedicated [Sandbox Manager](sandbox-providers.md#dedicated-sandbox-management-layer) between Runtime and Providers. pot-agent owns its permission policy; Runtime provides owner approval/storage services and owns task bindings; Manager owns environment inventory, allocation, lifecycle, scopes, receipts/recovery and artifacts. Inventory/lifecycle/scopes/receipts now live behind this boundary in `pot-sandbox`; allocation catalogs and artifacts remain target work.

## Task bindings and multiple environments

Current implementation: at creation, an explicit `sandbox_id` selects that environment. If omitted, Runtime snapshots the currently selected Sandbox; the initial selection is `default`. Creation does not allocate a new VM per task or balance tasks across existing VMs. Three tasks without an explicit target therefore share the selected Sandbox. To use three environments, the main Agent creates their definitions and assigns each task a different ID.

The main Agent changes the execution default through `manage_sandbox` with `action: select`; the Sandbox environment sidebar only selects the displayed environment. Prefer explicit IDs when creating tasks for several environments in one batch.

The assignment persists before dispatch. Changing the main selection affects only later task creation and main environment tools, never existing tasks or saved schedules. Current Linux tasks use `/data/workspace/tasks/TASK_ID`. In the provider redesign, filesystem environments declare native workspace roots; device environments may expose only artifact and app/document scopes. Agent profile routing and global/profile/provider concurrency limits do not choose a Sandbox. Runtime starts the bound environment when required; it does not retarget a task to another ready VM.

Persist the Sandbox identity separately from the Thread configuration. Each worker has one assigned Sandbox and workspace scope before environment execution begins. The worker cannot switch that binding across turns or after restart.

A task's single-environment constraint does not require a newly created VM for every task. Reuse and shared environments remain scheduling choices, with workspace scoping and resource arbitration. Sharing a Sandbox is not automatically task-level filesystem isolation; any stronger isolation claim requires enforcement and tests.

Target allocation adds explicit shared/dedicated profiles: isolated Linux work receives a fresh private instance and workspace before dispatch. This is not delivered by the current default-selection behavior. See [task placement](sandbox-providers.md#task-placement-and-environment-profiles).

For work across Linux and macOS, the main Agent requests the environments and creates one task for each target. Each task executes only in its own assignment, and the main Agent compares the returned results. Any cross-environment artifact copying is authorized by Runtime and performed by the Sandbox Manager; workers cannot use it as a route to arbitrary environments. Android device/build profiles and APK handoff are deferred.

An unavailable environment makes the task wait or fail visibly under Runtime policy. There is no automatic Host fallback or task retargeting. Work requiring a different environment becomes a new task with an explicit assignment. Recreating a destroyed Sandbox must not appear to preserve its old working state; generation changes require reconciliation before further execution.

## Sandbox lifecycle

The main Agent expresses lifecycle intent through typed product tools. Runtime authorizes lifecycle intent; the target Sandbox Manager owns real lifecycle, capacity, leases and environment operation outcomes; the Agent does not run raw hypervisor management commands.

Recommended initial policy:

- Separate creating a persistent Sandbox definition from starting its execution instance. A requested start is complete only when the authenticated executor is ready.
- Start when the main Agent explicitly requests it, or when Runtime prepares an assigned environment for authorized execution. Coalesce simultaneous start requests.
- Pure conversation and product task/status operations do not require a Sandbox.
- Hold leases for active operations and task turns. Desktop follows explicit environment start/stop; managed process sessions survive normal turns; cancellation/failure drains the owner, and idle expiry reclaims sessions. Idle runners retain initialization caches without active execution leases. Idle recovery/reclamation is Runtime-owned and does not require a worker management tool.
- Ordinary stop/delete requests against an occupied environment return its dependencies and a busy result. A separately explicit destructive stop must settle or mark interrupted all affected operations; never report affected tasks as successful.
- Stopping one Sandbox leaves the Host control plane and unrelated environments available. Preserve declared workspace/artifact storage independently of disposable VM state.

Cancellation of a task targets its operations and waits for executor cleanup. It does not authorize stopping an entire shared Sandbox or deleting its files. Unknown remote execution outcomes are not replayed automatically.

The provider redesign permits a declared VM-stop cancellation fallback only for an exclusively held instance. macOS starts with exclusive execution ownership; the main Agent, workers and manual input must queue or explicitly hand off control. A stopped VM confirms execution ended, not that prior UI/network effects were rolled back. See [ownership and cancellation](sandbox-providers.md#ownership-gui-arbitration-and-cancellation).

## Agent integration

pot-agent runs on Host with injected Runtime execution and owner services. Main
and Worker authority follows [Agent permissions](permissions.md); shared tools and
composition follow [Agent tools](tool-composition.md). Runtime and Manager enforce
assignments, scoped sessions, leases and capabilities independently of prompts.
Guest execution uses a credential-free runner behind the authenticated root broker.
Sessions remain in their original environment; see [command execution](executor.md).

The shared command/patch tools describe current Linux execution. The first provider redesign preserves Linux/macOS command tools and derives pot-agent' loadouts from assignment capabilities. Device observation/input/app operations remain future capabilities; lifecycle authority remains main-only.

Qualification covers native tool loadouts, real CLI execution, resume, cancellation and routing restrictions. See [acceptance](validation/host-pot-agent-20261006.md) for tested scope.

## Agent skills

Skills use filesystem bundles and a unified home-relative mapping; see [Agent skills](agent-skills.md). Delivered standalone execution supplies local roots and task filters. The approved Control-connected design selects global user/builtin skills and plugin versions at Control and supplies read-only task bundles to execution nodes, replacing destination-global discovery. Project rules/skills remain local to the actual workspace. Global bundle management belongs to Control; this distribution change is pending in #39.

## Product surfaces

Chat is the persistent main-assistant entry point, independent of VM readiness. AI settings choose provider/model connections separately from Sandbox configuration.

The environment surface presents multiple Sandboxes, their state, resources, associated tasks, workspace/artifacts and optional desktop. The task surface shows its fixed execution environment beside Agent selection and execution status. VM preparation, resource queueing, execution failure and cleanup uncertainty are visible states rather than generic chat failures.

The UI uses one environment vocabulary. pot-agent receives its execution backend from Runtime.

## Acceptance priorities

1. Host pot-agent sessions and the product backend work independently of Linux VM startup/shutdown.
2. Main Agent can manage two Linux Sandbox instances through policy-checked tools, select a target for its own operations and assign tasks to each.
3. Worker attempts to manage Sandboxes, select another target, access another target's credentials or invoke Host tools without owner authorization fail at the execution boundary, including after Thread resume.
4. Stopping/crashing one environment does not stop chat, corrupt Thread contexts or interrupt tasks in another environment. Busy-stop, task cancellation and shared-environment behavior follow the distinct contracts above.
5. Persistent artifacts remain accessible after Sandbox shutdown; unknown side effects are reconciled without silent replay.

These existing Linux gates remain prerequisites for provider extraction. macOS has separate qualification gates in [Sandbox providers](sandbox-providers.md#implementation-map-and-delivery-gates); they are not delivered by this specification. Host permission and mapping qualification follows the separate [permission contract](permissions.md).
