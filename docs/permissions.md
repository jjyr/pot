# Machine execution authorization

Control owns execution authorization for each enrolled Runtime in its Space.
Main tools execute on the owning Control; Tasks execute on their fixed Runtime.
Execution uses the Runtime service account and its OS privileges. A workspace
is a working directory, not a security boundary. OS privacy and administrative
permissions remain independent.

## Decisions and scope

New machines default to `execution_access: "ask"`. Every new command, patch or
plugin invocation requires an exact owner decision. Approval identifies the
assistant, Task/turn, generation, call ID, Runtime, enrollment epoch, tool and
complete arguments. Composed tools pass through the same authorization boundary.

| Decision | Effect |
|---|---|
| `allow_once` | Approves only the displayed call and arguments |
| `allow_always` | Persists `execution_access: "allow"` for this machine in this Space |
| `deny` | Rejects this call; future calls retain the current machine policy |

Persistent authorization covers all assistants, Tasks and scheduled executions
on that machine in this Space, survives restart and never authorizes another
machine or Space. The approval card names this scope. It also releases ordinary
pending calls on the same machine. Owner settings can restore `ask`; pending
approvals are invalidated and later operations must request authorization again.
Revocation does not undo effects or stop running commands; use cancellation.
Removing/re-enrolling a machine clears its saved grant and temporary decisions.

Instruction edits always require independent exact review, including when the
machine has persistent execution authorization. They offer allow-once or deny;
`allow_always` is rejected for instruction edits.

An allow-once shell decision approves the command, not a promise about the files
it can affect. Arbitrary programs cannot be confined by parsing command text.
Under `ask`, model TTY allocation and additional session input require persistent
authorization. Output polling and cancellation remain available with executor
session ownership checks. The user-operated native terminal is a separate owner
interface. Unattended Tasks can wait for approval; scheduling grants no authority.

## Remote execution and recovery

Remote requests return to authenticated Control services. Runner validates the
signed approved operation against the original run, fixed Runtime, tool, arguments
and shell settings before executing. Model-provider credentials remain in Control.
Operation receipts prevent duplicate effects; unknown results retain their fences
and are inspected by original identity, never replayed on another machine.
Temporary decisions expire with the work context or process restart.

## Persistence and owner API

`permissions.json` version 5 stores `machines` and enrollment `epochs`. Each machine
policy has exactly `execution_access` (`ask`, `allow` or owner-configured `deny`).
Machine-level `deny` blocks execution; it is distinct from denying one approval.
Unsupported formats fail
without rewriting files; previous Host/Sandbox policies are not implicitly adopted.

`GET /api/v1/permissions` returns machine policy and pending requests for the
selected conversation owner. `POST` accepts `{id, action}` for the decisions above.
Owner settings use:

```json
{
  "action": "configure",
  "machine_id": "runtime-id",
  "machine_policy": {"execution_access": "ask"}
}
```

Sources: `internal/control/permissions.go` and `internal/control/dispatch.go`.
