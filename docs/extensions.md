# Extension management

**Extensions** is a root destination in the feature rail and narrow navigation
menu. Plugins, MCP servers and Skills share one searchable catalogue, grouped
by type and expanded by default. Each group can collapse independently and owns
an Add action. Source and status filters apply across all groups. Configuration
belongs to the local node; linked peers manage their own settings. Skill project context uses the selected assistant's
actual execution workspace, rather than a browser filesystem directory.

```text
Extensions (local node) -> local node catalogue
    |-- tools / Skills       -> prepared execution -> selected Runtime
    |-- background services  -> assistant notifications
    `-- model connections    -> AI Providers -> node readers / Gateway
```

Builtin and user Plugins use the same contribution and enablement rules.
Settings are stored in `control.sqlite`; an absent setting means enabled.
Disabled packages cannot supply new tools, background services or model
connections. Saved model connections retain their configuration and report
unavailable; already prepared executions retain their fixed resources.
Skill completion likewise uses the enabled catalogue.

A model-provider Plugin's details link to **AI Providers**, where users configure
connections and optional quota reminders. The bundled `standard-model-providers`
package supplies the directory brands and Custom API through this same flow;
Magpie supplies one connection with its visible models and quotas. See
[Model connections](model-directory.md#provider-contributions).
Other background-service routing remains under third-party accounts.

## Plugins and icons

The catalogue discovers packaged `PluginRoot` directories and user installations
under `AgentHome/plugins`. Package IDs are unique across both roots; `mcp-` is
reserved for managed MCP adapters. Import accepts an absolute directory on the
owning node, validates declared capabilities and copies only their files.
User installations can update from their recorded source or be removed. Removal
moves the directory into `AgentHome/extension-trash` for manual recovery. Builtin
packages update with Pot and cannot be removed individually.

A package may include **`icon.svg`** or **`icon.png`** beside `plugin.json`,
independent of its capability file lists. SVG takes precedence. Packaging and
imports retain these assets and optional `icon-NOTICE.txt` / `icon-LICENSE.txt`
attribution files. GitHub uses its Octicons brand mark; Codex uses the official
application icon already used elsewhere in Pot. Each icon is a regular,
package-local file up to 64 KiB. PNG icons must decode successfully and be at
most 512 × 512 pixels; SVG accepts only self-contained shapes and attributes. Scripts, external references,
embedded documents, processing instructions and symlinks are rejected. The
catalogue returns a validated data URL; missing or invalid icons use the shared
default extension glyph. Icons never enter transferable execution bundles or
change tool file digests. Skills and MCP rows have no icons or icon placeholders.

## Skills

Users can import a local bundle or a public GitHub
`https://github.com/owner/repo/tree/ref/path/to/skill` URL, create a Skill, inspect
its `SKILL.md`, and edit user/project Skill content. GitHub archives are bounded
and only the selected bundle is extracted; downloaded code is never executed.
The common discovery rules validate names and frontmatter before publication.
Reserved builtin names cannot be replaced. Local imports reject symlinks and
special files and preserve executable support scripts.

Editing validates the proposed metadata, retains the bundle's identity, and
checks both the saved revision and original content digest. Conflicts retain the
UI draft. Updates replace user bundles from their recorded installation source;
newly authored Skills have no external update source. Builtin content is read-only.
Only user installations can be removed from this global page. Project Skills can
be inspected, edited and disabled in their actual assistant workspace.

## Independent MCP servers

Independent MCP servers are configured separately from Plugin packages. Packages
can also declare a Control-local stdio MCP capability (for example GitHub
subscriptions); see [Control-local MCP contributions](plugins.md#control-local-mcp-contributions).
These package servers are discovered and invoked on the owning Control, including
for remote Tasks; their files and credentials do not travel to the execution machine.
The independent server transports below retain their execution-machine behavior. Supported transports
are stdio (command, argument array and optional absolute working directory) and
Streamable HTTP. The UI also imports `mcpServers` JSON entries containing either
`command`/`args` or `url`; duplicate IDs fail rather than overwrite. Entries save
sequentially, so an error retains earlier successful imports.

Saving does not start a process or contact the service. **Test connection and
refresh tools** explicitly initializes the configured server and discovers up to
128 tools, with a bounded catalogue and protocol IO. MCP names, descriptions and
schemas are external reference data. Tools may be disabled individually. Servers
with no discovered/enabled tools are excluded from fresh model executions.
Connection status records the last successful test, not a permanently running
connection. Runtime calls establish an invocation-scoped connection and close it
afterward. Failures never retry the operation automatically.

Each enabled server supplies one `mcp_<normalized-id>` tool through the existing
flat Plugin contract, with a selected tool name and a JSON object string for its
arguments. Original schemas accompany that tool's description. This supports
nested MCP inputs without changing other tool interfaces. Namespace collisions
are rejected. The adapter is digest-pinned and follows normal approval,
cancellation, operation receipts, cleanup supervision and execution-machine
policy. Stdio commands run on that execution machine, so their executable,
arguments, working directory and dependencies must exist there; connection tests
run on the owning node. There is no fallback to another machine.

Environment variables and request headers are stored in the existing encrypted
secret store. Public catalogue responses and execution snapshots contain no
credential values. Dispatch resolves an immutable credential revision immediately
before authorized execution. Prior secret revisions remain available to already
prepared snapshots. Blank credential fields retain values; explicit empty JSON
objects clear credentials for future executions.

HTTP authentication supports request headers (including Bearer tokens) and public
OAuth authorization-code clients with PKCE S256. OAuth discovery uses protected
resource/authorization-server metadata, optional dynamic client registration or
an owner-supplied client ID, and an optional scope. Browser authorization returns
to the same-origin callback page; native clients use a short-lived loopback
callback on the owning node. State is single-use, revision-bound and expires
in ten minutes. Access tokens are encrypted and never returned to the UI. Expired
or revoked access tokens require reconnecting; automatic token refresh and
confidential OAuth client secrets are not supported. Connection tests and calls
remain separate from authorization. An OAuth grant does not grant tool-execution
authority.

## API and drafts

Owner APIs are `GET/POST /api/v1/extensions`,
`GET /api/v1/extensions/skills/{id}`, `POST /api/v1/extensions/mcp`, and
`POST /api/v1/extensions/mcp/oauth/{begin,complete}`. The authenticated browser
allowlist exposes only these paths. Mutations check current revisions; package
file mutation is serialized on the owning node. Catalogue refreshes and live
locale changes preserve open drafts. Closing or navigating away from a modified
draft asks to discard it; pending writes block leaving. Errors keep inputs and
provide a localized summary with diagnostics.
