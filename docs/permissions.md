# Machine execution policy

Control owns machine policy and owner approval. The policy is machine-wide; there
is no assistant-to-machine allowlist, Sandbox assignment or persistent
assistant capability grant. Enrollment authorizes a machine to accept its
Control's commands. It does not make arbitrary guest input an authorized command.

## Defaults and operations

Each machine has `execution_mode` (`sandbox` or `host`) and `host_access` (`ask`,
`allow`, or `deny`). New machines default to Sandbox execution and Host approval.
Host mode with denied Host access is invalid. The UI names these choices **Run
inside a sandbox** and **Run directly on this machine (Host)**. Selecting a mode
does not silently grant persistent Host authority.

Assistants resolve their local machine policy for each turn. Tasks resolve the
selected machine's current policy at dispatch. A running execution retains its
actual context; changing policy does not move a process or its files. Host access
is checked for each subsequent operation, so tightening policy affects active
work. Revocation does not undo effects; cancellation stops running work.

Sandbox work does not require a separate assistant grant. Managed environments
are private to their execution workspace and cannot be shared by selecting an
existing Sandbox. Missing Sandbox support fails explicitly; there is no Host
fallback. Host execution uses the Runtime service account, not implicit root.
Operating-system privacy and administrative permissions remain independent.

## Exact Host approval

A Host request identifies its assistant, task/turn, generation, call ID, machine,
tool and full arguments. Temporary decisions are keyed by this complete request,
not by a work-wide Host capability. Different calls or arguments do not inherit
approval. Approval cards show the actual operation and offer allow-once or deny;
persistent access is configured only in machine settings.

For shell commands this approves the command, not a guarantee about which files
it can affect. Arbitrary shell programs are not constrained by parsing command
text. Single-operation Host access does not permit an interactive terminal or
additional input into a running session; output polling and cancellation remain
available and must pass executor session ownership checks. Persistent Host
access allows normal interactive execution.

Remote task Host requests return through authenticated Control services. Control
resolves the same-machine Host context, authorizes the operation, and dispatches
it through the existing signed transport and receipts. Guest code receives no
Control credentials. Unknown results are inspected by original identity, never
replayed as a new operation. A task's normal Sandbox context is unchanged.

Instruction edits always require their own review, independently of machine policy.
Temporary approvals are memory-only and expire with their work context/restart.
Re-enrollment clears the old machine's policy and temporary decisions.

## Persistence and owner API

`permissions.json` version 3 contains `policy.machines`, machine enrollment epochs,
and Sandbox budgets. Previous permission formats are rejected without migration.
No assistant allowlists or capability grants are imported.

`GET /api/v1/permissions` returns machine policies and pending requests for the
selected conversation owner. `POST` accepts `{id, action}` with `allow_once` or
`deny`. Machine configuration uses:

```json
{
  "action": "configure",
  "machine_id": "runtime-id",
  "machine_policy": {
    "execution_mode": "host",
    "host_access": "allow"
  }
}
```

The owner can also configure `sandbox_limits`. CPU and memory are aggregate
managed-environment reservation budgets, `max_sandboxes` bounds concurrent managed
environments, and disk sizing is provider-dependent. Resource reservations live
on the execution Runtime and remain held until destruction is confirmed. These
are not Host process quotas. Unsupported disk enforcement must not be represented
as a hard storage quota (Podman does not provide it).

Sources: `crates/control/src/permissions.rs`, `crates/agent/src/permissions.rs`,
`crates/sandbox/src/manager.rs`.
