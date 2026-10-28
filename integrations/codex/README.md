# Codex MCP setup and receiver

Ordinary Codex MCP calls connect directly to the user's deployment-provided Pot
URL. The optional Pot helper contains an onboarding skill and configuration
preflight tool; it contributes no MCP server and does not build or start a local
Host. HTTPS, certificates and reachability are deployment responsibilities.

```text
Codex -- independent MCP URL + client OAuth --> Pot
  optional helper: setup instructions only

Explicit receiver Host -- outbound authorized MCP --> Pot
  durable work/run lifecycle --> Codex App Server
  execution-tools: trusted active Task/run context, no owner API
```

## Install and connect

Use a Codex client supporting independent Streamable HTTP MCP and standard OAuth
with PKCE. The existing App Server/plugin baseline is Codex CLI **0.163.0**;
configuration commands are checked against the upstream source linked below.
Installed versions must expose the required commands; absence is an explicit
limitation, not a reason to invent commands or fall back to P2P.

The plugin is optional:

```sh
python3 integrations/codex/install.py --source-only
# With a supported locally installed Codex CLI, omit --source-only to activate.
```

The installer preserves local package edits, unrelated marketplace entries and
independent MCP configuration. It never changes HOME or starts a receiver. An
explicit `--home PATH` controls the installation destination; `--codex-home PATH`
selects native activation configuration without redirecting HOME. If those are
different from the client's real home, register the source through the client's
supported marketplace UI; do not assume native marketplace discovery changes.

Run `pot-setup` or use Codex's MCP settings directly. For a new user-scoped entry:

```sh
python3 ~/.codex/plugins/pot/scripts/pot-mcp.py \
  --name pot_deployment --url https://pot.example.invalid/custom-endpoint
# Review the preflight result, then repeat with --apply for an unambiguous add.
```

The helper requires Python 3.11+ and `codex mcp list --json` / `mcp add`. It checks
user, ancestor project and effective client metadata. It keeps an existing exact
endpoint/name and stops on aliases, disabled entries, shadowing, old stdio,
managed/plugin differences and same-name conflicts. It emits semantic status
codes and never prints raw configuration, headers, auth output or credentials.
The example endpoint is synthetic; use the exact deployment URL without query
secrets, credentials or fragments. Paths and trailing slashes are not normalized.

`codex mcp add NAME --url URL` **replaces same-name configuration**, including tool
policies. Never use it blindly to update an entry. For replacement, use client
settings or review a targeted edit of the selected entry's `url`, preserving
allow/deny policies, timeouts, enablement and unrelated configuration. Run config
commands serially; external editors do not honor the helper's preflight checks.

Client add may itself start OAuth. The helper captures sensitive client output;
if add fails or times out, inspect the actual connection before repeating any
mutation, then complete authentication in the client's interactive settings or
`codex mcp login NAME`. Pot browser login presents one connection confirmation;
client OAuth owns PKCE and credentials. Do not paste authorization URLs, callback
queries, codes or tokens into conversation/logs. Return to `/mcp`, verify discovery
and an allowed tool call. Configured, authenticated and successfully called are
separate observations.

Review with `/mcp` or `codex mcp list`; log out with `codex mcp logout NAME`; remove
the independent user entry with `codex mcp remove NAME`. Project entries need
separate scoped removal. Logout, removal and server-side grant revocation are
separate actions. Plugin disable/uninstall leaves independent connections intact.
A plugin MCP policy overlay cannot override transport URLs.

## Explicit receiver installation and operation

Scheduling native execution requires a separately authorized receiver and the
client's local login/model/workspace/execution permissions. Ordinary MCP OAuth
login cannot bind an assistant, Task or run.

```sh
python3 integrations/codex/install.py --receiver --source-only
# Or pass --receiver --binary /path/to/built/pot-codex-agent (no Go needed).
~/.local/bin/pot-codex-agent receiver-connect \
  --root ~/.config/pot-codex/agent --url https://pot.example.invalid/custom-endpoint
~/.local/bin/pot-codex-agent receiver-serve \
  --root ~/.config/pot-codex/agent --connection CONNECTION_ID
# Stop communication without cancelling accepted work or deleting history:
~/.local/bin/pot-codex-agent receiver-disconnect \
  --root ~/.config/pot-codex/agent --connection CONNECTION_ID
```

The receiver installation does not install or activate the optional helper.
Receiver commands are provided by the shared Host. Serve is an explicit foreground
lifecycle; the helper never launches it. The installer refuses to overwrite a
different executable at `~/.local/bin/pot-codex-agent`; replace it explicitly while
the receiver is stopped. No login, model installation or system network changes
are performed by installation.

The native driver persists thread identity before effective input, resumes only
the saved thread and fails on resume rejection/identity mismatch. Terminal
continuation uses the same thread and a distinct real Pot run. It does not claim
live steering. Cancellation needs native terminal evidence and supervised process
cleanup; unknown cleanup remains fenced. Codex human review reaches the existing
Pot human control plane through a once-only receiver control bound to the original
caller/request and actual Task/run. Ordinary messages cannot approve. The same
receiver path carries configured assistant mail and lifecycle results. Files in
native results are path metadata, not transferred bytes.

The App Server reader binds and persists the turn ID exactly once from the
outstanding `turn/start` response before processing subsequent events. A
`turn/started` notification cannot replace that identity. Handled events arriving
before the response are held in order, bounded to 64 messages and 4 MiB total;
overflow reports `codex_turn_event_buffer_exceeded_outcome_unknown` and cleans up
the supervised session. Replayed approvals retain their existing review policy.
Turn lifecycle and item events require nonempty, unambiguous matching thread and
turn IDs; missing or foreign identities are ignored. Terminal results cannot be
changed by later events.

Failed terminal turns retain only a whitelisted `codexErrorInfo` category and
valid HTTP status from the matching terminal event. Unknown or malformed details
remain `codex_turn_failed`; free-form native messages, additional details and
stderr are not forwarded. Independent `error` notifications do not finish a turn
or supply guessed diagnostics. JSONL lines are limited to 4 MiB independently of
the final answer limit. Oversized events and read failures report
`codex_jsonl_read_failed_outcome_unknown` separately from process EOF. Unsupported
interaction diagnostics retain only known protocol methods and safe field-shape
metadata; elicitation is cancelled and never automatically approved. See the
[receiver Host](../../docs/mcp-receiver-host.md) for admission, FIFO input,
recovery and review boundaries.

Execution context replaces inherited reserved actor/job/run variables for each
supervised process while retaining native login configuration. The explicit
`execution-tools` entry connects only to an already running Host and verifies
active Task/run on every call. Direct Task workers enable only
`pot_execution_context`, preserving their no-delegation and local-owner message
restrictions. Assistant message execution additionally enables typed sender-free
reverse message and Task tools. Both local Host and service gates enforce this
role boundary. Ordinary global MCP credentials never become execution identity.
Codex thread start/resume injects the required `mcp_servers.pot_receiver` service
with exact actor/job/run environment. This per-thread override writes no global
client config and leaves unrelated servers/policies to the native client. Verify native client loading, effective tool policies and plugin lifecycle on
the supported Codex version before operating a receiver.

## Breaking upgrade

Stop the old receiver, update Pot/helper/adapters and refresh the client's installed
plugin cache so the old bundled stdio service is absent. Installation removes only
unmodified receipted obsolete package files; modified files stop the upgrade for
review. Remove obsolete independent stdio entries explicitly. Reconnect using the
new URL and separately authorize receiver participation. Old pairings are not
migrated, supported or used as fallback. Historical Tasks, reports and artifacts
remain; disconnect/revocation is not cancellation or permission to replay work.

See the [shared contract](../../docs/mcp-collaboration-contract.md) and
[Antigravity capability matrix](../antigravity/README.md#capability-boundaries).
Configuration references:
[Codex MCP](https://developers.openai.com/codex/mcp),
[onboarding](https://developers.openai.com/plugins/build/plugins#add-an-onboarding-skill),
[pinned CLI implementation](https://github.com/openai/codex/blob/6a6e2fb1f0e9122464a75929c58356cb7af9e0cc/codex-rs/cli/src/mcp_cmd.rs).
The [pinned thread override loader](https://github.com/openai/codex/blob/6a6e2fb1f0e9122464a75929c58356cb7af9e0cc/codex-rs/app-server/src/config_manager.rs)
converts request configuration to native CLI overrides for start/resume.
Source/schema and synthetic tests establish implementation boundaries; they do
not establish live client interoperability.
