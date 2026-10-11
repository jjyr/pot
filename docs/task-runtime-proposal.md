# Task execution on disposable Runtimes

Design proposal, 2026-10-11. Related to [#39: composable Runtime and distributed Agent / Task / Environment](https://github.com/jjyr/pot/issues/39), especially P4/P5. This page records the design discussion; it does not describe delivered behavior or change model-visible tool contracts. The issue includes work that is not yet on this checkout's main branch. Existing [architecture](architecture.md), [Agent permissions](permissions.md) and [Sandbox boundaries](agent-sandbox-design.md) still describe the implementation in this checkout.

## Minimal responsibility model

Control owns persistent Tasks, scheduling, model gateway, budgets, owner approvals and durable results. A selected Runtime starts a new Task Agent and executes its tools in its configured local environment. Desktop access is an optional permission checked by the existing permission system; Runtime separately provides the desktop and coordinates its use.

```text
Control: Tasks / scheduling / gateway / approvals / results
    |
    +-- Runtime: start Task Agent and execute tools
            |
            +-- default local execution environment
            +-- optional desktop resource, subject to permission and availability
```

Keep execution IDs, bindings, leases and receipts as internal Task execution records. Do not introduce a user-visible Run object, Runner management panel or a new generic resource-management framework. Retain typed Host/Sandbox environments and the existing Sandbox Manager where managed isolation is needed.

A Runtime inside a disposable container may execute the Agent and tools directly inside that container. It does not require an additional nested Sandbox. A Runtime on a persistent machine can use its explicitly configured Host or managed Sandbox. Agent placement alone never grants access to the user's Host or desktop. A headless browser does not require a full desktop.

## Placement and lifecycle

Task intent specifies execution requirements and, optionally, a particular Runtime or existing environment. Control resolves those requirements before starting execution. Automatic placement and disposable provisioning are proposed scheduling extensions to #39; explicit placement remains supported. Unsupported model, plugin or environment configuration must fail explicitly, as specified by #39.

Temporary versus persistent execution belongs to scheduling and reclamation policy. The persistent provisioner creates the container and its Runtime; the temporary Runtime executes its assigned task. A Task Agent does not provision or destroy its own Runtime, switch to another environment, or manage another task's resources.

| Work | Execution | Completion policy |
|---|---|---|
| Ordinary collection | Disposable container Runtime with shell and/or headless browser | Commit results, then reclaim container |
| Collection requiring a GUI | Runtime with an isolated desktop | Commit results, release control, reclaim according to policy |
| Work in an existing signed-in desktop | Runtime providing that desktop, with owner-authorized access | Commit results and release control; retain desktop |
| Continuing development | Runtime with a retained project workspace | Retain declared workspace and release execution leases |

Configuration, resource versions and actual environment binding remain fixed for each execution. An existing bound environment may expose its desktop after authorization and availability checks. If a different environment is required, Control starts a new execution from explicitly saved task guidance, checkpoints and artifacts. It does not migrate a running Agent process or silently preserve shell/browser sessions across environments.

## Desktop permission within the existing system

Use the existing Agent identity, permission policy, owner approval cards and grant storage. Do not restore the machine-level Discover/Execute/Desktop grant system removed by #39. Machine membership authorizes Control to coordinate configured resources; Agent behavior remains subject to owner permission checks.

The execution path is:

```text
desktop operation
  -> resolve target and enforce task/role scope
  -> existing permission policy and matching grant, or owner approval
  -> acquire valid desktop operation lease
  -> execute through the Runtime
```

Desktop permission binds the Agent, target desktop identity and allowed operations. Observation and control are distinct scopes: permission to view must not implicitly authorize keyboard or pointer input. Viewing through App/Web is separate from Agent control permission.

| Desktop target | Proposed default |
|---|---|
| Task-created isolated desktop | Allow within the already authorized task scope, respecting any configured ask policy |
| Existing Host desktop or signed-in user session | Ask unless a matching owner grant exists |
| Desktop occupied by another task or human controller | Wait or reject until control is explicitly handed off |

Permission approval does not create a desktop, establish provider capability, or override occupancy. Runtime owns availability, exclusive control and explicit human/Agent handoff. Concurrent uncoordinated input is rejected. Existing grants cannot authorize a target outside the task's permitted resource scope.

Keep the existing Allow once / Always allow / Deny owner flow. Current permanent operation grants match complete tool arguments; they do not yet provide ongoing desktop access across different code invocations. A scoped desktop-access grant would extend the existing permission matcher and storage, rather than create a parallel authorization subsystem. Exact scope representation, approval lifetime and model-visible interface changes require a separate implementation agreement.

Temporary access is bound to the active execution and expires on completion or cancellation. Persistent owner grants are bound to stable desktop identity and Agent identity; destruction/replacement invalidates them, even if the display name is reused. Operation leases also bind the current boot/generation so stale sessions cannot act after restart. Revocation prevents future observation/input, invalidates reusable Agent control sessions and releases control after active operations settle. It does not undo effects already produced.

## Durable completion before reclamation

```text
persist launch intent and execution identity
  -> provision/select Runtime and bind configured resources
  -> start Agent with deduplicated launch acceptance
  -> execute with gateway, approvals, guidance and cancellation
  -> commit result and declared artifacts to Control-owned durable storage
  -> acknowledge durable completion
  -> settle operations and release leases
  -> reclaim temporary Runtime/resources
```

Retain result text, declared deliverables, terminal status, usage and the receipts needed to reconcile execution. Full Agent context, temporary files and desktop disks may be discarded according to retention policy; they are not required to be replicated continuously. Retaining results does not imply that a disposed execution can resume its old sessions.

If Control is unavailable, do not reclaim the only copy of an uncommitted result. Pause new actions as required by #39 and reconcile the original execution on reconnect. Lost notifications trigger a query of the original execution, not another launch. Unknown external effects remain unknown until reconciled; container loss does not justify automatic replay. Cancellation must confirm execution cleanup before normal reclamation. Destroying an exclusively held container may establish that processes ended, but does not roll back prior effects.

## Implementation scope and checks

Extend #39 P4/P5 with disposable Runtime provisioning/reclamation and durable result acknowledgment. Reuse Task records, Runtime execution, permissions, leases and operation receipts. Integrate desktop access into the permission system when desktop capability is implemented; this proposal does not implement native desktop automation or reopen the deferred remote desktop viewing channel.

Before implementation, agree any model-visible tool names, arguments or behavior changes, desktop grant representation and approval lifetime. No tool schema or runtime behavior changes are made by this document.

Acceptance should cover fresh Task Agent launch on a selected Runtime; collection without a desktop or nested Sandbox; durable results surviving reclamation; repeated launch/completion requests being deduplicated; disconnect before result acknowledgment; observation/control scope separation; occupied desktop handoff; grant revocation and desktop replacement; and cancellation or uncertain side effects without replay or Host fallback.
