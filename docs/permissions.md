# Agent permissions

pot-agent owns permission policy and grant matching. Runtime supplies trusted
Agent identity, owner approval/storage and execution routing. Agent process
placement on Host does not grant Host execution authority.

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

Approval cards identify the Agent, environment and operation. Built-in execution
tools use localized questions and labeled arguments. Plugins may provide declarative
`approval_display` text; without it, cards show raw arguments. Display text does not
change execution or authorization, and plugin code never runs to prepare a prompt.
Cards show the bound plugin ID (or built-in tool label) and the environment's display
name. Internal tool names, routing IDs and versions stay out of the card. A supplied
summary replaces code/argument display; missing summaries fall back to raw arguments.
Runtime verifies plugin
identity (including bundle digest) against the Thread configuration; package IDs are not
publisher authentication.
The request's outer `environment` identifies the target; `args` excludes this routing field.
Actions are **Always allow**, **Allow once**, **Deny** (永久允许、允许一次、拒绝).
`proposed_grant` is optional and normally omitted. Always allow derives the exact
scope from the reviewed tool and arguments unless an explicit scope is supplied;
there is no extra directory-grant action.
Only authenticated owners can approve or revoke grants; Agents cannot self-approve.

Requests separate the execution target, actual operation and presentation:

```json
{
  "owner": "main",
  "environment": "host",
  "generation": 3,
  "turn": "turn-id",
  "call_id": "call-id",
  "tool": "cua_repl",
  "plugin": {"id": "computer-use", "version": "0.4.0", "digest": "bundle-sha256"},
  "args": {"code": "let tab = await cua.open('https://example.com');"},
  "display": {
    "summary": {"en": "Allow browser use?", "zh-CN": "是否允许使用浏览器？"},
    "labels": {"code": {"en": "Operation code", "zh-CN": "操作代码"}}
  }
}
```

`display` comes from the pinned plugin's `approval_display` metadata and only
changes presentation. An explicit `proposed_grant` uses the grant scope format
(for example `{"kind":"exact","tool":"cua_repl","args":{...}}`) and must cover
the reviewed operation. Current requests omit this proposal; the UI only shows it
when supplied. It is not a second copy of the default exact grant.

One-time approval belongs to the concrete call and is consumed before execution.
Permanent operation grants match the complete normalized tool arguments, including
command and working directory. They survive restart and configuration changes for the same
Thread. They do not permit arbitrary commands under an approved directory.
Revocation affects future checks and does not undo effects or stop an authorized
session. Ended turns, changed execution generations and shutdown invalidate pending approvals.
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

Owner API: `GET/POST /api/v1/permissions`. Sources: `crates/agent/src/permissions.rs`,
`crates/runtime/src/permissions.rs`. Dated checks and untested scope:
[validation](validation/agent-permissions-20261008.md).
