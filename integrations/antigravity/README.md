# Antigravity MCP setup and receiver

Ordinary Antigravity calls use an independently configured remote Pot MCP server.
The optional `pot` helper contains setup instructions only, starts no Host and
contributes no MCP server or execution hook. The native driver targets the
**agy 1.4.8** stream-json and plugin interfaces. Validate the installed client's
actual commands and schema rather
than assuming a newer SDK applies to this CLI.

```text
Antigravity -- independent remote MCP + client OAuth --> Pot
  optional pot helper: setup instructions

Explicit shared receiver Host --> agy stream-json
  required pot-receiver support: PreToolUse permission hook
  local execution-tools: exact active Task/run and role-gated callbacks
```

## Install and connect

```sh
python3 integrations/antigravity/install.py --source-only
# Omit --source-only to validate/install/enable with an existing local agy CLI.
```

Use `pot-setup` or the native MCP manager. Review existing names, exact URLs,
source/scope, enablement and policies without printing credentials or entire
configuration. Keep matching connections; resolve same-name/different-endpoint,
duplicate alias, project and old stdio conflicts before writing. The documented
remote shape is:

```json
{"mcpServers":{"pot_deployment":{"serverUrl":"https://pot.example.invalid/custom-endpoint"}}}
```

Use the field supported by the installed client (`serverUrl`, or its documented
`url` alias). Do not use legacy `httpUrl`/SSE or invent a CLI configuration flag.
Configure the exact deployment-provided HTTP/HTTPS URL without query secrets,
userinfo or fragments. Deployment owners manage certificates and reachability.
Complete the client's standard OAuth/PKCE browser flow, confirm the connection
once in Pot, then return to Antigravity and verify discovery plus a permitted tool
call. Tokens, codes, callback queries and auth URLs do not belong in chat/logs,
plugin files or endpoint queries.

Review/change/logout/remove through the independent server's native MCP manager.
Preserve its policies and unrelated settings when changing a URL. Removing or
disabling the optional helper leaves the independent connection and native login
under their own lifecycle; server grant revocation is separate. Actual lifecycle
behavior must also be verified against the installed client.

## Independent execution support

A schedulable receiver is explicitly installed/authorized/started. It retains
receiver-owned native login, model, workspace and permissions. The `pot-receiver`
support package is mandatory for native execution, independently of the optional
helper. It retains `pot-execution-scope` / `PreToolUse`; removing the helper does
not remove this hook. The driver checks enabled support, hook and local context
server before sending effective input. Missing support fails closed.

```sh
python3 integrations/antigravity/install.py --receiver --source-only
# Or --receiver --binary /path/to/built/pot-antigravity-agent to avoid building.
# Without --source-only, validate/install/enable pot-receiver using native agy.
~/.config/pot-antigravity/receiver-support/bin/pot-antigravity-agent receiver-connect \
  --root ~/.config/pot-antigravity/agent --url https://pot.example.invalid/custom-endpoint
~/.config/pot-antigravity/receiver-support/bin/pot-antigravity-agent receiver-serve \
  --root ~/.config/pot-antigravity/agent --connection CONNECTION_ID
~/.config/pot-antigravity/receiver-support/bin/pot-antigravity-agent receiver-disconnect \
  --root ~/.config/pot-antigravity/agent --connection CONNECTION_ID
```

The support package invokes `execution-tools` without a fixed root or listener.
The supervised process supplies the active job/run and absolute receiver root and
listener together; incomplete or mismatched scope fails closed. Starting the
local server does not start a Host. This also supports explicitly configured
receiver roots and listener ports without rewriting the installed package.

Installation never starts a receiver, changes HOME, logs in or changes network
settings. `--home` chooses the staging location only; native activation still uses
the current Antigravity profile. Use `--source-only` when staging an isolated
package. Source-only installation requires explicit native support activation
before execution. Stop the receiver before updating its executable/support files.
Local package modifications and symlinks are rejected; historical data is retained.

Read-only executions retain the enforced native-tool allowlist: reads/search are
allowed, while shell, writes, edits and subagents are denied. Other native
execution remains under existing policy. The hook is inert outside supervised
jobs; it does not disable users' other MCP globally. Trusted execution context
replaces inherited reserved actor/job/run variables per process. The local
`pot_receiver` service checks the current active Task/run. Task workers expose
only execution context and cannot delegate or message remote agents. Assistant
message executions additionally expose typed sender-free reverse message and
Task callbacks through the authenticated receiver gateway. The local hook and
service gate preserve the role boundary. Ordinary OAuth cannot substitute for
execution identity or supply human approval.

The documented CLI workspace configuration files are `.agents/mcp_config.json`
and `.agents/hooks.json`. They supplement global configuration and do not provide
profile isolation. Do not invent a `--mcp-config` flag or a home override to load
per-run support; native execution uses the explicitly activated support package.

The hook uses `allow` to delegate to the next hook and native permission policy,
as defined by the [Antigravity hook contract](https://antigravity.google/docs/hooks).
It never emits `auto_approve` or adds permission overrides, including for the local
`pot_receiver` context server. Independent MCP servers have no legacy-name
exemption from the read-only boundary. A native permission requirement remains
in force; headless execution must not silently approve it to continue. Plugin
metadata validation confirms the required support is present, not that a real
CLI loaded the hook or enforced its decisions during execution.

The driver persists conversation identity before writing input, resumes the exact
saved conversation and rejects mismatches without opening a replacement. It has
no native turn ID; the real Pot RunID remains separate. Input means terminal
continuation, not live steering. Cancellation is confirmed only after supervised
process cleanup; it cannot roll back external effects. Unknown cleanup remains
fenced. Headless CLI interactive approvals are not implemented; success text alone
is not proof a permission-requiring action ran. No file-byte delivery is advertised.

## Capability boundaries

This matrix separates implemented Pot drivers from documented upstream interfaces.
The remote receiver extension currently wires task submit/read/cancel/terminal
input and lifecycle snapshots for Codex/Antigravity, plus configured assistant
mail and trusted role-gated callbacks. Codex native pending human requests use
once-only receiver controls; Antigravity does not advertise headless human review.
Real native CLI loading and effective configuration still require live checks.

| Agent/surface | Ordinary MCP configuration | Execution interface and current Pot boundary |
| --- | --- | --- |
| Codex CLI/Desktop/IDE | Independent HTTP server + client OAuth; optional onboarding helper | Existing App Server v2 start/exact thread resume/turn interrupt/terminal events; Task/run-bound human review and assistant callbacks connected through the receiver extension; files are metadata only; real native client loading remains unverified. |
| Antigravity CLI/App | Independent `serverUrl` + client OAuth; optional helper | Existing stream-json init/result, exact conversation resume and process supervision; separate enforced execution hook required. No headless remote approval or file bytes. |
| Claude Code | Documented `claude mcp add --transport http NAME URL`, scoped config and `/mcp` OAuth | Official Agent SDK/headless interfaces exist. No Pot driver or verified Pot session/cancel/approval mapping. |
| Grok API / Build CLI / web | API remote-MCP tool configuration; Build CLI documented MCP management; web custom connector uses reachable URL | API tool calling does not prove control of an existing Grok Agent. Build documents session/headless/resume candidates. No Pot driver, verified cancellation, run binding or approval mapping. |
| dot | Installed MCP plugins; #314 records MCP Events subscription/webhook requirements | Existing-dot identity/profile, accepted Task/run mapping, wakeup and cancellation remain unverified. No Pot driver or universal SDK claim. Ordinary notifications do not establish model wakeup. |

References: [Claude MCP](https://code.claude.com/docs/en/mcp),
[Claude Agent SDK](https://code.claude.com/docs/en/agent-sdk/overview),
[Grok remote MCP](https://docs.x.ai/developers/tools/remote-mcp.md),
[Grok Build MCP](https://docs.x.ai/build/features/mcp-servers.md),
[Grok Build sessions](https://docs.x.ai/build/features/sessions.md),
[dot investigation #314](https://github.com/jjyr/pot/issues/314),
[Antigravity MCP](https://antigravity.google/docs/mcp) and
[plugins](https://antigravity.google/docs/plugins).
These are source/documentation boundaries, not live validation of any client.
The first version does not add drivers for the other names.

## Breaking upgrade

Stop old execution, update Pot/helper/receiver, refresh installed native plugin
caches and verify the old bundled stdio MCP and helper hook are absent. Activate
`pot-receiver` separately for execution. Reconnect through the explicit new MCP
path. Old P2P pairings are unsupported, never migrated and never used as fallback.
The installer removes only unmodified receipted obsolete package assets; it does
not erase connection history, Tasks, reports or artifacts. Modified assets require
review. Disconnection/revocation does not cancel or replay accepted work.

See [Codex setup](../codex/README.md) and the
[shared contract](../../docs/mcp-collaboration-contract.md). Native login, actual
CLI plugin/schema behavior, per-run MCP configuration and end-to-end remote
callbacks need live client verification before claiming bidirectional execution.
