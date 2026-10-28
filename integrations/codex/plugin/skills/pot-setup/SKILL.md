---
name: pot-setup
description: Configure, review or remove an independent remote Pot MCP connection in Codex.
---

Use the user's deployment-provided Pot MCP URL. This optional helper contributes
no MCP server and starts no local Host. Do not use a plugin policy overlay for a
URL: Codex overlays cannot change transport settings.

1. Ask for the endpoint and desired configuration scope if they are not known.
   Require HTTP/HTTPS without credentials, query secrets or fragments. The
   deployment owner manages reachability and certificates.
2. Inspect only necessary non-secret server metadata. Check user configuration,
   applicable project configuration and effective `codex mcp list --json`, keeping
   raw output, headers, environment values and authentication material out of the
   conversation and logs. The included `scripts/pot-mcp.py` performs this check
   conservatively: `python3 PATH_TO_PLUGIN/scripts/pot-mcp.py --name NAME --url URL`.
   It requires Python 3.11+ and a Codex CLI with `mcp list --json` and `mcp add`.
3. Preserve a matching existing connection and all its policies. Explain disabled,
   duplicate alias, cross-scope, same-name/different-URL, managed/plugin and old
   stdio conflicts; let the user choose which existing definition to retain or
   explicitly change. Do not add an alias to evade a conflict. Do not blindly run
   `codex mcp add`: it replaces same-name entries and resets their policies.
4. For a new unambiguous user-scoped connection, run the helper with `--apply`
   after the user has selected the endpoint. It uses `codex mcp add NAME --url URL`.
   Run configuration commands serially. If CLI support is absent or a command
   fails, explain the limitation and use the client's documented MCP settings.
   A failed command can have effects: inspect before repeating it.
5. Complete standard client OAuth in the client's own interactive terminal/settings
   (`codex mcp login NAME` if needed; add can itself start authentication). The
   browser shows Pot login and one connection confirmation with PKCE. Never copy
   tokens, auth URLs, codes or callback queries into chat/logs or the plugin. Do
   not add roles, tool checkboxes, or per-task credentials. Return to Codex and
   check `/mcp`, discovery and one permitted tool call. Distinguish configuration,
   authentication and a successful tool call.
6. For endpoint replacement, use the client's documented settings or a reviewed,
   targeted edit of the selected independent entry's URL, preserving its tool
   allow/deny policies, timeouts, enablement and all unrelated configuration.
   The helper deliberately stops on replacement conflicts; it does not rewrite
   TOML. Log out of the old endpoint as appropriate and authenticate the new one.
   Never print whole config files or credentials to perform the edit.
7. Explain the separate lifecycles. `/mcp` and `codex mcp list` review connections;
   `codex mcp logout NAME` logs out; `codex mcp remove NAME` removes the independent
   user entry. Project entries need separate scoped removal. Server-side grant
   revocation is separate. Disabling/uninstalling this helper does not remove the
   connection, log out, stop a receiver or delete history.

A schedulable Agent requires an explicitly installed and started receiver, its
own authorization and receiver-owned login/model/workspace/permissions. Ordinary
MCP login grants no assistant/task/run identity. Do not launch a receiver during
ordinary setup. Update Pot, this helper and relevant adapters together, refresh
the client's installed plugin cache, then explicitly reconnect. Old P2P pairing
is unsupported; never migrate it or silently fall back. Preserve historical
Tasks, reports and artifacts; disconnection is not cancellation.
