# Machine permissions

Control owns assistant-to-machine authorization. Execution nodes enforce their OS
and container boundaries and cannot supply an alternative approval policy.
Machine membership and model execution authorization are separate contracts.

## Capabilities

A durable grant is `(assistant_id, machine_id, capability)`, where capability is
`host` or `sandbox`. Stable identities, not display names, are authoritative.
Host grants allow direct execution with the runtime account's OS privileges.
Remote grants are bound to an enrollment fingerprint: re-enrollment clears old
grants, temporary decisions and preallow rules before the next authorization.
Sandbox grants allow using assigned sandboxes and creating/managing owned
sandboxes within owner-configured boundaries. A grant covers future sandboxes on
that machine; sandbox IDs, commands, paths, plugins and arguments are not grant
scopes. Neither capability implicitly grants the other.

```text
Assistant + work scope
        |
        v
Resolve fixed machine + Host/Sandbox target (no execution effects)
        |
        v
Control policy / durable grant / work decision
        |
        +-- deny ----------> fail without execution
        +-- ask -----------> one shared owner request
        +-- allow
              |
              v
Check resource ownership and configuration limits
              |
              v
Prepare resources -> normalize guest arguments -> execute
```

Shell input, patches and plugins all check current access. Preparation cannot boot
or contact an execution node before authorization. Tasks are authorized before
launch, including remote Agent startup. Task targets stay fixed; a failed Sandbox
never falls back to Host. Internally `EnvironmentRef` identifies an execution
target; it is not an independently granted product resource.

## Owner decisions and work lifetime

Cards identify the assistant, machine and capability. Actions are **Always allow**,
**Allow this work**, **Deny**. The API action `allow_once` means this work, not one
command. The operation envelope remains internal for identity and receipt checks;
it does not create a command-specific permission.

Main work identity derives from the accepted conversation turn. Tasks created by
that turn inherit its work identity, which is stored with their queued jobs.
Guidance to active/queued work shares that identity. Explicitly reopening finished
or interrupted work creates a new scope; schedule occurrences are independent.
An answer from Main does not discard a child's work authorization. Temporary
decisions live only in Control memory and are lost on restart. An ended scope is
not reused by future inputs. Normal execution cleanup closes sessions; a session
handle never substitutes for current authorization.

Requests for the same assistant/machine/capability/work are joined. Cancelling one
waiter does not cancel another Task's request. Denial is remembered for the work,
so subsequent calls fail without more cards. Durable grant revocation affects
future checks, including session input; it does not undo effects or stop an
already executing operation. Interrupt/cancel remains the explicit stop action.

Changes to the assistant's own instructions remain a separate, fresh owner review.
They cannot receive a permanent machine grant. macOS privacy permissions remain
operating-system decisions.

## Machine settings

The Machines page configures `ask`, `allow` or `deny` separately for the selected
assistant's Host and Sandbox capability. Defaults are `ask` for both. Dedicated
servers may be explicitly preallowed; Linux, remote placement and the Sandbox
label alone never imply trust. `deny` takes precedence over existing grants.

Machine settings also control Sandbox resource limits and assignments. Default
limits are 16 sandboxes, 4 CPUs, 8 GiB RAM and 32 GiB disk per sandbox. Creation
checks the actual provider defaults and total machine inventory before creating
resources, and is rejected when either exceeds the configured limits. An assistant can change only resource sizing through Sandbox update;
privileged flags, host sockets, arbitrary mounts and other configuration fields
are not accepted. Provider-specific limits and busy/lease checks still apply. Podman reports
`disk_gib: 0` because it does not provide a disk quota; the disk sizing ceiling
is not a storage quota on that provider.

New assistant-created sandboxes acquire durable ownership before creation.
Existing owner-created sandboxes must be assigned in Machines. Only their owner
assistant can manage them; `shared_with` grants use without management. Creation
reservations remain visible in configuration if provisioning fails and can be
reassigned by the owner. Deleting a sandbox releases its ownership/quota entry.
Ownership checks are product access controls; Tasks sharing a sandbox still share
its OS boundary. Owner-configured Host mappings use the owner Sandbox configuration endpoint;
there is no model-visible mount-changing tool. Remote management currently
supports existing sandbox lifecycle operations; provider/transport limitations
are independent of authorization.

## Storage and owner API

Control stores version 2 `permissions.json` in its data directory, independently
of the shared Agent home. No guest permissions file,
per-environment grant store, path grant, exact-operation grant or migration exists.
Older permission formats and Task jobs without a work scope are rejected; there
is no import or migration of old authorization/Task state. Rebuild/reprovision guest brokers for the
new `/prepare` normalization endpoint; it cannot read or write authorization.

```json
{
  "version": 2,
  "policy": {
    "rules": [
      {
        "assistant_id": "assistant-id",
        "scope": {"machine_id": "server-id", "capability": "sandbox"},
        "decision": "allow"
      }
    ],
    "sandbox_limits": {
      "server-id": {"max_sandboxes": 16, "cpus": 4, "memory_gib": 8, "disk_gib": 32}
    },
    "sandboxes": [
      {"machine_id": "server-id", "sandbox_id": "dev", "owner": "assistant-id", "shared_with": []}
    ]
  },
  "grants": []
}
```

`GET /api/v1/permissions` returns the selected assistant's pending requests, grants,
identity and owner configuration. `POST` accepts `{id, action}` for decisions and
revocation. Worker tokens cannot administer permissions. IDs belonging to another
assistant are rejected.

Owner configuration uses `POST` with `action: "configure"`, `machine_id`, and:

- `capability` plus `decision` for the selected assistant's machine policy;
- `sandbox_limits` for the machine's resource bounds;
- `sandbox_access` with `machine_id`, `sandbox_id`, `owner`, `shared_with` for assignment.

Writes are atomic and detect conflicting external file edits. Manual edits require
restart. Pending requests, temporary grants, denials and history are not persisted.
Plugin approval display metadata is removed; plugins use the same machine gate.

Sources: `crates/control/src/permissions.rs`, `crates/agent/src/permissions.rs`.
