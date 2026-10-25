# Antigravity Agent Link plugin

The native Antigravity plugin connects the App and CLI to Pot through MCP and
Agent Link. Pot users can create persistent Antigravity assistants, choose any
model slug advertised by the receiving `agy models`, chat with those assistants,
and exchange assistant messages across connected nodes. The receiving machine
owns the Antigravity login, native permissions and allowed workspace scopes.

```text
Pot user -> assistant management -> model / existing workspace scope
                                      |
Pot chat / assistant mail <-> authenticated Agent Link <-> shared adapter host
                                                            |
                                                  durable assistant queue
                                                            |
                                                   agy stream-json
                                                conversation_id per assistant
                                                            |
                                                   native Pot MCP tools
                                                            |
                                                Agent Link -> Pot / Codex
```

## Install and connect

Install and sign into Antigravity CLI locally. This integration uses the native
CLI protocol in `agy 1.4.7`; it does not use Gemini API keys or a model proxy.

```sh
mise exec -- python integrations/antigravity/install.py
```

The installer builds `cmd/pot-antigravity-agent`, stages its executable and native
plugin under `~/.config/pot-antigravity/plugin`, validates it with
`agy plugin validate --strict`, and installs/enables it through the official CLI.
Unrelated configuration is retained and modified managed source files are not
overwritten. `--binary /absolute/path` uses a prebuilt executable; `--home PATH
--source-only` stages an isolated package without changing Antigravity's registry.
Stop the host before upgrading its executable.

Run `pot-setup` in Antigravity. It returns a private, short-lived connection-page
URL. Open that page and copy an invitation into Pot's **Add assistant >
＋ Connect a new Agent > Antigravity** guide, or paste a Pot invitation into the
connection page. The guide shows installation and invitation steps; after
connecting, configure and create the new assistant explicitly. Pairing
allows messaging and user-only assistant management within existing local scope
choices. It grants no arbitrary filesystem path selection or credentials.

The local owner endpoint is `127.0.0.1:8794`; authenticated TLS Agent Link starts
on `127.0.0.1:8795`. The connection page supports the same address discovery and
signed listeners as the Codex adapter. One reachable peer endpoint carries both
directions. Management credentials never appear in invitations or MCP results.

After pairing, choose a model and an existing host workspace scope in Pot.
The native model list already includes reasoning levels in its slugs and labels;
Pot uses that exact selection instead of adding a conflicting effort override. The
initial scope is an isolated read-only directory under
`~/.config/pot-antigravity/agent/workspace`. New assistants have independent IDs,
avatars, model selections and persistent conversations. The Link inspector offers
**Create assistant** and each assistant's **Model settings**.

To offer another workspace, configure a template assistant on the receiving host:

```sh
~/.config/pot-antigravity/plugin/bin/pot-antigravity-agent assistant \
  --root ~/.config/pot-antigravity/agent --id project --name Project \
  --workspace /absolute/project --mode workspace-write \
  --model MODEL_SLUG
```

Remote creation selects an existing assistant's scope by ID and inherits its
workspace and permission mode. It cannot supply a new path or broaden that mode.
Changing a model requires an idle assistant and the current revision. It retains
the original conversation and applies at the next execution. Invalid or unavailable
models fail without fallback. Model discovery is metadata, not proof of plan access,
remaining quota or successful inference.

## Execution and permissions

The [shared adapter host](../../internal/agenthost) owns identity, durable queues,
mail transport, session bindings, supervision and recovery for Codex and
Antigravity. Runtime-specific protocol handlers remain separate. Every assistant
processes inputs serially; different assistants and independent tasks may run in
parallel. An `init` conversation ID is persisted before writing the first user
input. Later executions use `--conversation`, never `--continue` or a replacement
conversation after a resume failure.

Each execution requires the installed, enabled Pot MCP server and execution-scope
hook. The hook is inert in ordinary unbound Antigravity chats. In host-bound
read-only executions it denies command execution, file edits, subagents and other
non-read tools. It permits the explicitly scoped Pot messaging tools. Writable
executions retain the host's configured Antigravity permission policy; the adapter
does not pass `--dangerously-skip-permissions`.

Antigravity headless mode cannot relay interactive approvals through its stream.
No remote approval capability is advertised. Native policy may soft-deny a tool
while the model continues and explains the refusal. A completed text answer alone
does not prove that a requested tool action succeeded.

Native assistant processes inherit their exact assistant/job/root binding into the
MCP process. A task cannot impersonate a message assistant. Outgoing mail names
an explicit advertised recipient and binds the active parent when applicable.
Receipts mean durable acceptance, not model completion. Direct user replies enter
Pot's existing chat projection without waking a local Pot model.

The independent node executor snapshots its own workspace, mode and model for
each task. Creating or updating an assistant does not configure this executor. Its model and
permissions are separate local owner operations:

```sh
~/.config/pot-antigravity/plugin/bin/pot-antigravity-agent executor \
  --root ~/.config/pot-antigravity/agent --workspace /absolute/project \
  --mode workspace-write --model MODEL_SLUG
```

Cancellation and shutdown drain the trusted process supervisor before publishing
a terminal result. Unconfirmed cleanup fences new execution. Restart never replays
unfinished accepted work. Disconnect and delete retain accepted execution history.
The integration creates adapter-managed CLI conversations; it does not claim to
control arbitrary existing Antigravity GUI chats.

## Development

```sh
mise exec -- go test -race ./internal/agenthost ./pkg/agentlink ./pkg/storage
mise exec -- python integrations/antigravity/install_test.py
POT_AGY_LIVE=1 mise exec -- go test ./internal/agenthost \
  -run '^TestAntigravityLive$' -count=1 -v -timeout=9m
```

The live command requires an authenticated `agy`, the installed plugin and idle
adapter ports. It uses disposable host/Pot roots and real model calls. Failed
fixtures are retained under the printed temporary path for local diagnosis.

Protocol references: [plugins](https://antigravity.google/docs/plugins),
[MCP](https://antigravity.google/docs/mcp),
[headless streaming](https://antigravity.google/docs/cli/headless),
[hooks](https://antigravity.google/docs/hooks).
