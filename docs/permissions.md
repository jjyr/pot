# pot-agent permissions

Updated: 2026-10-08. Implements the pot-agent scope of [issue #29](https://github.com/jjyr/pot/issues/29). Codex permission integration is deferred.

## Ownership and execution

Main normally chats on the Host. Agent process location does not confer Host
execution authority. Complex work can still be dispatched to tasks using assigned
Sandboxes. A task may explicitly request Host access, without changing its saved
Sandbox assignment.

Permission policy and matching live in `pot-agent`, not in the shared Driver or
Runtime dispatcher. Runtime supplies trusted identity, owner interaction,
current permanent-grant storage and Sandbox configuration operations through injected
`ExecutionFeedback` methods. Explicit Host file/command builtins execute locally
in pot-agent after its permission gate; Sandbox tools retain the injected executor. The Agent cannot approve itself through model output or tools.

```text
pot-agent
  Model tool call
        |
        v
  Permission policy + matching grants
        |
        +-- allow ----------------------------+
        |                                     |
        +-- ask --> Runtime --> Owner UI       |
        |                         |           |
        |                    owner decision   |
        |                         |           |
        +-------------------------+-----------+
                                  |
                                  v
                           Authorized execution
                                  |
                           Host / Sandbox tools
```

Agent checks occur before tool dispatch. Tool loadouts and existing Runtime /
Sandbox Manager role, target, session, lease and capability checks still apply.
Permissions do not grant a worker management authority or enable a disabled tool.
The existing explicitly registered code-extension API is trusted embedder code;
its tools retain their existing policy. Unknown model tool names are unavailable.
Standalone embeddings do not receive Host tools unless they explicitly supply
an owner permission service.

## Tools and default policy

| Operation | Main | Task worker | Approval |
|---|---|---|---|
| Chat and registered product status/task/schedule operations | Existing loadout | Existing restricted loadout | Existing policy |
| Assigned Sandbox command/process/patch tools | Available | Available | No per-call approval |
| `host_read_file` | Available | Available | Ask unless a matching grant exists |
| `host_write_file` | Available | Available | Ask unless a matching grant exists |
| `host_list_directory` | Available | Available | Ask unless a matching grant exists |
| `host_exec_command` | Available | Available | Ask unless an exact-operation grant exists |
| `pot_manage_sandbox` | Available | Unavailable | Ask unless an exact-operation grant exists |
| `host_set_sandbox_mappings` | Available | Unavailable | Ask unless an exact-operation grant exists |

Summary turns expose no Host tools and retain task inspection only. Profile
`allowed_tools` filters the new tools as well. Host names are explicit; existing
`exec_command`, `write_stdin` and `apply_patch` continue to target Sandboxes.
Sandbox failure never triggers a Host fallback. Sandbox networking stays enabled.

## One-time and persistent approval

Requests bind to the owner (`main` or Task ID), Agent binding epoch, accepted turn,
call ID, tool and concrete arguments. Paths and mutable Sandbox selection defaults
are resolved before review. Conflicting reuse of a request identity fails.

The native and Web conversation cards show the owner, tool, complete arguments
and permission scope. Their three actions, in order, are Always allow, Allow once
and Deny. Always allow saves the displayed scope; cards do not offer an additional
parent-directory grant or a Sandbox-mapping-specific approval flow. A directory-list
grant is recursive read access. A file grant is exact-path access; write includes read.

Permanent grants belong to one Agent identity (`main` or a Task ID). They survive
turns, Runtime restarts and executor rebinding for that same Agent. Binding epochs
still protect pending requests, but do not limit permanent grants. Owner clients
can inspect and revoke grants. Revocation affects subsequent checks; it does not undo effects
or terminate an already authorized command.

A shell or configuration grant matches the full tool and normalized arguments.
A path grant never authorizes a shell command. Persistent shell approval therefore
permits repeat executions of that exact command and working directory, not an
arbitrary command under a directory.

One-time approval is consumed in memory before execution and cannot be reused.
Agent receipts replay the saved result of a repeated call, without new approval
or duplicate execution. Cancellation, ended turns, changed bindings and shutdown
invalidate pending requests. On restart all pending/unconsumed approvals disappear;
permanent grants remain. Denied, cancelled and consumed requests are deleted from
memory; the permission service keeps no settled approval history. Agent context records started calls before approval and
never silently repeats an interrupted call. Consuming approval is not proof that
an effect completed; interrupted execution needs user-directed recovery.

## Host file and command boundaries

Host files use absolute paths resolved to canonical names for review. Execution
walks path components through directory file descriptors with `O_NOFOLLOW`;
symlink replacement after review fails. Symlinks encountered during initial
resolution show their actual destination, and matching grants use that destination.
Read tools return UTF-8 files up to 256 KiB; listing returns up to 2,000 names.
Writes create or replace regular files, refuse multiply linked files and sync
content. Writes are ordinary file replacement, not a multi-file transaction;
cancellation does not roll back an already completed write.

Host shell commands use `/bin/sh`, an explicit working directory and a default
60-second timeout (maximum 10 minutes). Output is bounded to 64 KiB per stream with explicit truncation flags.
They have no PTY or retained-session API. Cancellation/timeout kills the command
process group, and ordinary completion drains background members of that group.
This is not an OS sandbox: an approved command can perform all actions available
to the Server OS account, including external effects and deliberately detached
processes. The approval card states that authority. A path grant cannot contain
arbitrary shell execution.

Host paths belong to the Runtime machine, including when approval comes from a
remote browser. SSH remains a tool effect; dispatching an Agent to another machine
requires a Runtime there and is not implemented by this permission increment.

## Sandbox Host mappings

`host_set_sandbox_mappings` replaces the complete mapping list for an explicit,
stopped Linux Sandbox. It presents every canonical Host source, guest destination
and read-only flag. Removed entries disappear from the next startup configuration.
Mappings persist separately from authorization records; revoking the grant does
not remove an existing mapping. Removal requires another configuration change.

Destinations must be nonoverlapping children of `/mnt/host/`, with no traversal or
system mount replacement. Sources must remain canonical directories. At most 16
mappings are allowed. Linux Podman and macOS Apple Containerization consume these
settings on the next startup. The prepared macOS guest provider does not support
this operation. Providers revalidate source names on startup; owners should keep
mapping source directories under their control while instances start.

Every task sharing that Sandbox can access its mapped directories. A mapping is
an environment change, not a private Task grant. Task-specific Host file grants
provide access without exposing the path to other tasks. Resource updates retain
existing mappings. The ordinary Agent Sandbox management tool cannot bypass the
permission flow with the internal mapping action. Codex receives neither the new
Host tools nor permission-service integration; it retains existing Sandbox tools.

## Owner API and persistence

`GET /api/v1/permissions` returns pending requests and stored grants.
`POST /api/v1/permissions` accepts `{id, action}` where action is `allow_once`,
`allow_persistent`, `deny` or `revoke`. Persistent decisions may include an explicit
`scope`; it must cover the reviewed operation and paths must be canonical.
Only owner-authenticated clients can administer decisions; worker bearer tokens
cannot access this API. Browser access uses the existing instance Token authority.
Approval content is trusted UI data, never interpreted as model instructions.

Runtime stores only current permanent grants in `~/.pot/agent/permissions.json`,
alongside the global `AGENTS.md` (`POT_AGENT_HOME` can override this directory for
isolated deployments/tests). An absent file means no permanent grants; Server
creates it only when saving a grant or migrating the previous database.
It does not persist pending requests, one-time
decisions, denials, revoked grants or approval history. The file uses mode `0600`,
a synced temporary file and atomic replacement; a small `.permissions.lock` file
serializes writers sharing the Agent home. Existing conversation tool receipts
remain separate and prevent duplicate execution.

```json
{
  "version": 1,
  "grants": [
    {
      "id": "main-read-notes",
      "agent": "main",
      "permission": {
        "type": "path",
        "path": "/Users/jjy/notes",
        "access": "read",
        "recursive": true
      }
    },
    {
      "id": "task-edit-config",
      "agent": "task-123",
      "permission": {
        "type": "path",
        "path": "/Users/jjy/project/config.json",
        "access": "read_write",
        "recursive": false
      }
    },
    {
      "id": "main-run-command",
      "agent": "main",
      "permission": {
        "type": "operation",
        "tool": "host_exec_command",
        "args": {
          "cmd": "git status --short",
          "workdir": "/Users/jjy/project",
          "timeout_ms": 60000
        }
      }
    }
  ]
}
```

`id` is a unique grant identifier used for revocation. `agent` names one Agent;
wildcards are unsupported. Path access is `read` or `read_write`; write includes
read. `recursive` covers all descendants of a directory. Paths must be absolute
without parent traversal, and existing paths must be canonical. Operation grants
match the full tool and normalized arguments; omitted Host-command timeout means
60 seconds. An empty `grants` list means no permanent permissions.

Users can edit the file and restart Server to load the new state. Removing an
entry revokes it on restart. Invalid JSON, unknown fields/version, duplicate grant
IDs/scopes and invalid permissions fail startup rather than silently broadening
access. UI grants/revocations write the same file. External edits while Server is
running are not hot-reloaded; subsequent UI writes detect changed file contents
and ask for a Server restart instead of overwriting those edits.

Approving an existing Agent/scope replaces its current entry. Broader approved
grants also remove narrower entries they fully cover for that Agent. Different
valid scopes remain separate. Pending requests and permanent grants are each
bounded to 1,000; only active requests count toward the request bound.

When this JSON file is absent, startup imports permanent grants from the previous
`product/permissions-v1.sqlite`, deduplicates identical Agent/scopes, writes JSON
successfully and removes the old database. Pending decisions and history are not
imported. An existing JSON file is authoritative and is never merged with SQLite.

Waiting approvals do not hold an in-flight model request or block unrelated Agent
lanes. Clients poll the owner API; conversation cancellation remains available
while awaiting a decision.

## Verification

Run `mise run test` for backend regression, `mise run check` for build/source
checks, and `mise run test:i18n` for the shared approval labels and embedded assets.
Permission-specific tests use temporary Host directories, injected owner decisions
and local model fixtures, without real models. They exercise denial before effects,
once consumption, repeated-call receipts, grant identity/scope/revocation/restart/rebinding, editable JSON validation and SQLite migration,
symlink and hard-link boundaries and mapping validation. Provider VM startup and
native/Web interactive approval rendering require separate live qualification;
unit and compilation checks do not establish those results.
