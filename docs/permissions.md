# Agent permissions

pot-agent owns permission policy and grant matching. Runtime supplies trusted
Agent identity, owner approval/storage and execution routing. Agent process
placement on Host does not grant Host execution authority. Codex integration is
deferred; its managed execution remains Sandbox-only.

## Environments and authority

Main defaults to Host; tasks default to their assigned Sandbox. Both use the same
execution tools and may explicitly target Host. Tasks cannot switch Sandboxes,
manage environments or delegate. Sandbox failure never causes Host fallback.

| Operation | Default policy |
|---|---|
| Host command, patch or plugin invocation | Ask unless an Agent-specific grant matches |
| Ordinary assigned-Sandbox execution | Allow; owner may configure ask |
| Continue an authorized command session | Allow with session ownership checks |
| Task, schedule, preference and Sandbox management | Allow within the role's authority |
| Change Host directory mappings | Owner approval or an exact approved grant, even under allow policy |

Policy belongs to the environment; permanent grants belong to one Agent (`main`
or a Task ID). Host and Sandbox grants are separate. Allow policy does not bypass
role, assignment, session or configuration protection. Ordinary guest execution
cannot modify permission policy to authorize itself. Permission administration
uses the trusted owner channel. No dedicated built-in web search backend exists.

```text
tool call -> resolve environment -> role checks -> permission policy
                                                      |
                                           allow / matching grant
                                                      |
                                           ask -> owner decision
                                                      |
                                                 execution
```

See [Agent tools](tool-composition.md) for tool visibility and composition, and
[command execution](executor.md) for shell/session behavior.

## Owner decisions

Approval cards identify the Agent, environment, tool, full arguments and scope.
Actions are **Always allow**, **Allow once**, **Deny** (永久允许、允许一次、拒绝).
Always allow saves the displayed scope; there is no extra directory-grant action.
Only authenticated owners can approve or revoke grants; Agents cannot self-approve.

One-time approval belongs to the concrete call and is consumed before execution.
Permanent operation grants match the complete normalized tool arguments, including
command and working directory. They survive restart and rebinding for the same
Agent. They do not permit arbitrary commands under an approved directory.
Revocation affects future checks and does not undo effects or stop an authorized
session. Ended turns, changed bindings and shutdown invalidate pending approvals.
Execution receipts prevent duplicate effects; uncertain calls are not replayed.

## Editable configuration

Host uses `~/.pot/agent/permissions.json`, alongside global `AGENTS.md`;
`POT_AGENT_HOME` overrides the Host Agent directory. Each Sandbox uses its own
guest execution HOME's `~/.pot/agent/permissions.json`, retained with that environment.

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

`policy.default` accepts `ask` or `allow`. Missing files/policy use Host `ask` or
Sandbox `allow`. Only current permanent grants are stored: no pending requests,
one-time decisions, denials, revoked entries or approval history. `id` identifies
a grant for revocation; `agent` does not support wildcards.

Host edits require Server restart; guest edits take effect on the next prepared
operation through trusted administration. UI writes use the same state and reject
conflicting external edits. Invalid configuration fails explicitly. Previous
SQLite grants are migrated when JSON is absent; existing JSON is authoritative.
Legacy path grants do not authorize current execution tools.

Guest execution sees read-only permission storage. Updated guest images/runners
are required; unsupported guests fail without Host fallback. Protection in live
Linux/macOS environments still requires qualification.

## Host directory mappings

`set_sandbox_mappings` replaces a stopped supported Sandbox's mapping list after
owner review. Review shows canonical Host sources, guest destinations and read-only
flags. Destinations must stay beneath `/mnt/host/` and cannot overlap or replace
system mounts. The prepared macOS guest provider does not support this operation.

Mappings change the environment and are visible to every Task sharing it.
Revoking a grant does not remove an existing mapping; removing it requires another
configuration change. An approved Task-specific Host operation provides access
without sharing the path with other Tasks.

Owner API: `GET/POST /api/v1/permissions`. Sources: `agent/src/permissions.rs`,
`runtime/src/permissions.rs`. Dated checks and untested scope:
[validation](validation/agent-permissions-20261008.md).
