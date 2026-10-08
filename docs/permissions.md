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
`ExecutionFeedback` methods. The same execution tools dispatch through an injected executor after permission
checks. Runtime routes the selected environment to its Host executor or the
authenticated Sandbox runner. The Agent cannot approve itself through model output or tools.

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

## Tools and execution environments

`exec_command`, `write_stdin` and `apply_patch` have one registration each. They accept `environment: "host"`
or `environment: "sandbox:ID"`. Main defaults to Host. A task defaults to its
persisted Sandbox and may explicitly request Host; it cannot switch Sandboxes.
The execution target is resolved before review and shown on the approval card.
Sandbox failure never triggers a Host fallback. Networking stays enabled.

New command/patch operations use the selected environment's default policy.
`write_stdin` continues an already authorized session without asking again, for
output polling, input, resize, interrupt and termination; session ownership, epoch
and environment checks remain enforced. Guest polling neither boots a guest nor
loads its permission config.
Host defaults to `ask`: a matching Agent grant allows execution, otherwise the
owner decides. Sandbox defaults to `allow`: ordinary execution proceeds without
a grant or per-call approval. An explicit Sandbox `ask` policy uses the same
grant/approval flow as Host. Host grants never authorize guest
operations, and grants in one Sandbox never authorize another Sandbox. Main-only
`manage_sandbox` is a role-scoped built-in and requires no per-call permission
approval, for any supported action. Task/schedule/preferences and Sandbox inventory
also require no permission approval; their role and product guards remain.
`set_sandbox_mappings` requires an exact existing owner-approved grant or owner
approval, even if the Host default policy is `allow`.
Summary turns retain task inspection only. Profile `allowed_tools` filters the
shared tools. Codex keeps its previous Sandbox-only schemas and policy; this
permission service is pot-agent-specific.

There is currently no independent built-in `web_search` or webpage-fetch backend.
A registered search extension uses the trusted built-in/extension policy and does
not require execution approval. Future dedicated public search uses this same
allow-by-default classification; shell-based network access still follows
`exec_command` policy.

## Default policy and role boundaries

Each permission config may contain `"policy": {"default": "ask"}` or
`"policy": {"default": "allow"}`. Missing files or omitted policy use the
execution environment's defaults: Host `ask`, Sandbox `allow`. Existing version-1
files with only grants remain supported. New files written by the owner service
include the effective policy, and grant changes preserve it. Unknown policy
values/fields fail validation. Host edits take effect after Server restart; guest
edits take effect on the next prepared operation.

Policy belongs to the environment; grants belong to individual Agent identities.
`allow` does not disable tool loadouts, task assignment/session ownership checks,
argument validation or protection of the permission configuration. A task cannot
switch to another Sandbox or manage Sandbox lifecycle. It can explicitly request
Host, where Host policy applies. Mapping changes remain Main-only Host operations.
Policy/config files cannot be edited from ordinary guest execution to self-authorize.
Changing Sandbox permissions remains an explicit owner action through trusted
permission administration; ordinary task or Sandbox management does not confer it.

```text
shared tool -> resolve environment -> enforce role/assignment boundaries
                                       |
                                       +-- policy allow -> execute
                                       |
                                       +-- policy ask -> matching Agent grant?
                                                          +-- yes -> execute
                                                          +-- no  -> owner approval
```

Tool visibility is assembled in `agent/src/lib.rs` using `ExecutionRole`, with
shared schemas in `core/src/environment_tools.rs` and the extra mapping schema
in `agent/src/environment_tools.rs`. Main receives product/management tools from
Runtime's MCP `tools/list`; `runtime/src/tool_bridge.rs::schemas` filters those by
role. Runtime injects the role in `agent/driver/feedback.rs::make_context` and
independently checks worker targeting in `runtime/src/permissions.rs::prepare`
and `runtime/src/sandbox.rs::tool`. Tool visibility is not the authorization boundary.

Default Main pot-agent loadout contains 21 tools: three execution tools, fifteen
product tools, one mapping tool and two composition tools (`exec`, `wait`). Worker
loadout contains the three execution and two composition tools. The managed Codex
Main loadout has the same fifteen product tools plus its three Sandbox
execution tools (18 total); workers have three. Summary has two task inspection
tools. Profile filters and trusted extensions can change these counts. See
[tool composition](tool-composition.md) for the registry and execution contract.

## One-time and persistent approval

Requests bind to the owner (`main` or Task ID), Agent binding epoch, accepted turn,
call ID, tool and concrete arguments. Paths and mutable Sandbox selection defaults
are resolved before review. Conflicting reuse of a request identity fails.

The native and Web conversation cards show the owner, execution environment, tool, complete arguments
and permission scope. Their three actions, in order, are Always allow, Allow once
and Deny. Always allow saves the displayed scope; cards do not offer an additional
parent-directory grant or a Sandbox-mapping-specific approval flow.

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

## Command and patch boundaries

Read files and list directories through `exec_command`; edit files through
`apply_patch` or an explicitly approved command. There are no independent
`read_file`, `write_file` or `list_directory` tools, in either direct calls or
JavaScript composition. Shell working directories are made canonical in the
selected environment before review; guest paths are never resolved against the
Runtime filesystem. Patch target checks remain in the shared executor.

Host and Sandbox command tools share shell, login, PTY, timeout and retained
session behavior; see [command execution](executor.md). Command defaults are
made explicit before approval. Shell grants match the entire prepared operation,
including cwd, shell, login and tty. Host execution uses the Server OS account;
an approved command can perform external effects and launch detached processes.
The Host shell approval card states that authority. A path grant does not contain
arbitrary shell execution. Host process-group cleanup does not provide the
Linux guest's root-owned cgroup containment.

Host paths belong to the Runtime machine, including when approval comes from a
remote browser. SSH remains a tool effect; dispatching an Agent to another machine
requires a Runtime there.

## Sandbox Host mappings

`set_sandbox_mappings` replaces the complete mapping list for an explicit,
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
an environment change, not a private Task grant. Task-specific approved Host commands provide access without exposing the path to other tasks. Resource updates retain
existing mappings. The ordinary Agent Sandbox management tool cannot bypass the
permission flow with the internal mapping action. Codex does not receive permission-service integration; it retains existing Sandbox tools.

## Owner API and persistence

`GET /api/v1/permissions` returns pending requests and stored grants.
`POST /api/v1/permissions` accepts `{id, action}` where action is `allow_once`,
`allow_persistent`, `deny` or `revoke`. Persistent decisions may include an explicit
`scope`; it must cover the reviewed operation and paths must be canonical.
Only owner-authenticated clients can administer decisions; worker bearer tokens
cannot access this API. Browser access uses the existing instance Token authority.
Approval content is trusted UI data, never interpreted as model instructions.

Host execution stores only current permanent grants in the Host
`~/.pot/agent/permissions.json`,
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
  "policy": {"default": "ask"},
  "grants": [
    {
      "id": "main-run-command",
      "agent": "main",
      "permission": {
        "type": "operation",
        "tool": "exec_command",
        "args": {
          "cmd": "git status --short",
          "workdir": "/Users/jjy/project",
          "timeout_ms": 60000,
          "shell": "/bin/bash",
          "login": true,
          "tty": false
        }
      }
    }
  ]
}
```

`id` is a unique grant identifier used for revocation. `agent` names one Agent;
wildcards are unsupported. Legacy path grants remain parseable for config compatibility but do not authorize
any current execution tool. They can be removed from the file. Current grants use
`type: "operation"`. Old operation grants for removed file tools must be removed;
unsupported tool names fail validation instead of granting shell access.
Operation grants match the full tool and normalized arguments. Omitted command
settings use standard defaults (`/bin/bash`, login enabled, tty disabled, 120-second
timeout unless `yield_time_ms` is present). Custom profile settings require explicit
matching values. Legacy `host_exec_command` grants load as `exec_command` with
`/bin/sh`, login disabled, tty disabled and their original timeout. An empty `grants` list means no explicit permanent grants; the default policy still applies.

Users can edit the file and restart Server to load the new state. Removing an
entry revokes it on restart. Invalid JSON, unknown fields/version, duplicate grant
IDs/scopes and invalid permissions fail startup rather than silently broadening
access. UI grants/revocations write the same file. External edits while Server is
running are not hot-reloaded; subsequent UI writes detect changed file contents
and ask for a Server restart instead of overwriting those edits.

Each Sandbox stores the same JSON format inside the guest. Execution HOME sees
it at `~/.pot/agent/permissions.json`. Linux backing storage is
`/data/.pot/agent/permissions.json`, retained with that environment's data;
prepared macOS backing storage is `/var/db/pot-permissions/.pot/agent/permissions.json`
on guest disk. All execution homes in one Sandbox share this environment's config,
with grants separated by Agent identity. Sandbox lifetime therefore controls its
permission state's lifetime. This does not move Host global `AGENTS.md` discovery.

Runtime loads guest state through the authenticated root `/permissions` control
route before each operation, and saves/revokes grants there with compare-and-swap.
Manual guest edits are reflected on the next prepared operation; conflicting UI
writes fail. Loaded guest grants appear in the owner UI; after restart a Sandbox's
grants appear after its first preparation. Ordinary tool processes see readonly
permission storage: Linux mounts both backing and HOME views readonly in each
runner namespace; prepared macOS uses a readonly policy for the backing/view
paths. Commands cannot edit these files to self-authorize. The owner can edit
through trusted Host access to retained Linux data or guest administration.
Linux guest files are readable (0644), while Host JSON is private (0600).

This control route requires a rebuilt Linux image or reprovisioned macOS guest
and a matching Rust runner. Old published images do not implement it; unsupported
guests fail explicitly without falling back to Host. Live VM mounting and macOS
policy behavior still require separate qualification.

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
`mise run test:permissions` and `mise run test:skills` verify guest config and
readonly mount construction without a VM. Permission-specific tests use temporary Host directories, injected owner decisions
and local model fixtures, without real models. They exercise denial before effects,
once consumption, repeated-call receipts, grant identity/scope/revocation/restart/rebinding, editable JSON validation and SQLite migration,
working-directory preparation, command/patch boundaries and mapping validation. Provider VM startup and
native/Web interactive approval rendering require separate live qualification;
unit and compilation checks do not establish those results.
