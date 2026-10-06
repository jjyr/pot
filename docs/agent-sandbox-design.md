# Local Agents and managed Sandboxes

Updated: 2026-10-06. Implemented boundaries; dated acceptance is recorded separately. This is the current target specification for Agent/Sandbox placement and permissions. See the [architecture](architecture.md) for the current deployment.

## Confirmed scope

Pot runs Agent loops and the persistent product control plane on the Host. Sandboxes are separately managed execution resources. The main Agent can control multiple Sandboxes; a task worker executes within exactly one assigned environment and cannot control Sandbox lifecycle or select another environment.

- Pi and Codex are the supported Drivers.
- Host-side Agent execution does not mean local model inference. Model connections remain a separate configuration concern.
- Model-selected shell, filesystem, browser and desktop operations execute in Sandboxes. Direct Host execution is out of scope, including fallback when a Sandbox is unavailable.
- Multiple Sandbox instances are part of the target model. Start with Linux providers; macOS and Windows providers are later additions rather than prerequisites for the first slice.
- Host Runtime, Storage, Gateway and native Agent sessions remain available when every Sandbox is stopped. Sandbox shutdown does not delete a conversation or product task record.

## Authority and execution

| Capability | Main Agent | Task worker | Runtime / provider |
|---|---|---|---|
| List Sandbox inventory and inspect lifecycle status | Yes | No management API | Authoritative state |
| Request create/start/stop/delete | Yes, through managed tools and policy | No | Validate, queue, execute and persist |
| Choose an execution Sandbox | Yes | No | Validate ownership and capabilities |
| Execute environment tools | In an explicitly selected Sandbox | Only in its assigned Sandbox | Route and enforce target scope |
| Assign a Sandbox to a task | Yes | No | Persist immutable task environment binding |
| Operate another task's environment | Only with main-authorized scope | No | Reject unauthorized operations |
| Execute tools directly on Host | Out of scope | Out of scope | No such execution target exposed |

Workers may inspect execution metadata for their own environment, such as OS, cwd and tool availability. That does not grant Sandbox inventory, provisioning or lifecycle capabilities. A worker reports a missing dependency or unavailable environment to the main Agent/Runtime instead of creating, restarting or changing a Sandbox.

This boundary must be enforced by tool loadouts, scoped credentials and Runtime routing, not only by prompts. Workers receive neither an executor/management credential nor access to Host helper sockets, hypervisor interfaces or an unrestricted execution gateway. A worker-supplied target override is rejected. Shell execution inside a Sandbox must not provide access to the management plane.

Runtime can prepare or recover the already assigned environment according to its lifecycle policy; that is infrastructure behavior, not worker lifecycle authority. Preparation must not silently create a different environment or change the task binding.

## Task bindings and multiple environments

At creation, an explicit `sandbox_id` selects that environment. If omitted, Runtime snapshots the currently selected Sandbox; the initial selection is `default`. Creation does not allocate a new VM per task or balance tasks across existing VMs. Three tasks without an explicit target therefore share the selected Sandbox. To use three environments, the main Agent creates their definitions and assigns each task a different ID.

The main Agent changes the execution default through `pot_manage_sandbox` with `action: select`; the Sandbox environment sidebar only selects the displayed environment. Prefer explicit IDs when creating tasks for several environments in one batch.

The assignment persists before dispatch. Changing the main selection affects only later task creation and main environment tools, never existing tasks or saved schedules. Each task uses `/data/workspace/tasks/TASK_ID` in its assigned environment. Agent profile routing and global/profile/provider concurrency limits do not choose a Sandbox. Runtime starts the bound environment when required; it does not retarget a task to another ready VM.

Persist the Sandbox identity separately from the Agent/profile binding. Each worker has one assigned Sandbox and workspace scope before environment execution begins. The worker cannot switch that binding across turns or after restart.

A task's single-environment constraint does not require a newly created VM for every task. Reuse and shared environments remain scheduling choices, with workspace scoping and resource arbitration. Sharing a Sandbox is not automatically task-level filesystem isolation; any stronger isolation claim requires enforcement and tests.

For work across Linux, macOS and Windows, the main Agent requests the environments and creates one task for each target. Each task executes only in its own assignment, and the main Agent compares the returned results. Cross-environment artifact copying is a managed control-plane operation; workers cannot use it as a route to arbitrary environments.

An unavailable environment makes the task wait or fail visibly under Runtime policy. There is no automatic Host fallback or task retargeting. Work requiring a different environment becomes a new task with an explicit assignment. Recreating a destroyed Sandbox must not appear to preserve its old working state; generation changes require reconciliation before further execution.

## Sandbox lifecycle

The main Agent expresses lifecycle intent through typed product tools. Runtime owns real lifecycle, capacity, leases and operation outcomes; the Agent does not run raw hypervisor management commands.

Recommended initial policy:

- Separate creating a persistent Sandbox definition from starting its execution instance. A requested start is complete only when the authenticated executor is ready.
- Start when the main Agent explicitly requests it, or when Runtime prepares an assigned environment for authorized execution. Coalesce simultaneous start requests.
- Pure conversation and product task/status operations do not require a Sandbox. Codex environment discovery and context loading need validation before promising startup only at the first tool call.
- Hold leases for active operations and task turns. Desktop follows explicit environment start/stop; detached tool processes are drained at the end of each operation rather than treated as persistent jobs. Idle recovery/reclamation is Runtime-owned and does not require a worker management tool.
- Ordinary stop/delete requests against an occupied environment return its dependencies and a busy result. A separately explicit destructive stop must settle or mark interrupted all affected operations; never report affected tasks as successful.
- Stopping one Sandbox leaves the Host control plane and unrelated environments available. Preserve declared workspace/artifact storage independently of disposable VM state.

Cancellation of a task targets its operations and waits for executor cleanup. It does not authorize stopping an entire shared Sandbox or deleting its files. Unknown remote execution outcomes are not replayed automatically.

## Driver integration

Pi stays on the Host with Pot-controlled extensions. Replace all seven native environment tools with Sandbox-backed implementations and account for shell shortcuts and implicit context access. Main and worker loadouts differ: management tools are main-only, and worker implementations route exclusively to the persisted assignment.

Codex App Server stays on the Host. Pot disables native environment tools, supplies an empty native environment list, and exposes the same Sandbox execution API through lane-scoped MCP. Inherited MCP servers, plugins/apps and native subagent tools are disabled. The implementation uses Pot's execution-only HTTP service rather than `codex exec-server`, keeping one environment protocol for both Drivers.

Qualification covers native tool loadouts, real CLI execution, resume, cancellation and routing restrictions. See [acceptance](validation/agent-sandbox-20261006.md) for tested scope.

## Product surfaces

Chat is the persistent main-assistant entry point, independent of VM readiness. AI settings choose Driver/model connections separately from Sandbox configuration.

The environment surface presents multiple Sandboxes, their state, resources, associated tasks, workspace/artifacts and optional desktop. The task surface shows its fixed execution environment beside Agent selection and execution status. VM preparation, resource queueing, execution failure and cleanup uncertainty are visible states rather than generic chat failures.

The UI should use one environment vocabulary for both Drivers. Pi extension routing and Codex executor transport are implementation details.

## Acceptance priorities

1. Host Pi and Codex sessions and the product backend work independently of Linux VM startup/shutdown.
2. Main Agent can manage two Linux Sandbox instances through policy-checked tools, select a target for its own operations and assign tasks to each.
3. Worker attempts to manage Sandboxes, select another target, access another target's credentials or invoke Host tools fail at the execution boundary, including after native-session resume.
4. Stopping/crashing one environment does not stop chat, corrupt native sessions or interrupt tasks in another environment. Busy-stop, task cancellation and shared-environment behavior follow the distinct contracts above.
5. Persistent artifacts remain accessible after Sandbox shutdown; unknown side effects are reconciled without silent replay.

These tests precede adding macOS/Windows providers. Direct Host tools are outside acceptance scope.
