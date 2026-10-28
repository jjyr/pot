---
name: pot-setup
description: Set up or review an independent remote Pot MCP connection in Antigravity.
---

This optional helper provides instructions only; it starts no Host and contributes
no MCP server, pairing page or execution hook.

Ask for the deployment-provided Pot MCP URL and the user's chosen configuration
scope. Use Antigravity's documented MCP manager to inspect existing server names,
transport, enablement and endpoint without printing raw configuration, headers,
credentials or URL queries. Preserve an existing matching connection and policies.
Resolve duplicate aliases, same-name/different-endpoint and scope conflicts with
the user before changing anything. Do not overwrite a same-name entry or invent
CLI configuration commands.

For a new entry, use the MCP manager's remote HTTP server configuration. The
native `mcp_config.json` format supports `mcpServers.NAME.serverUrl`; use the
field supported by the installed client's documentation, preserving unrelated
configuration and tool policies. Use the client's standard OAuth flow: browser
Pot login and one connection confirmation with PKCE, then return to Antigravity.
Never copy tokens, authorization URLs, callback codes/queries or secrets into the
plugin, conversation or logs. Do not add tool/target checkboxes, roles or per-task
credentials. HTTP/HTTPS, certificates and network reachability belong to the
user/deployer. Verify actual discovery and one permitted call in the client.

Review, change, log out or remove the independent server through its MCP manager.
Plugin disable/uninstall does not remove that server or revoke its login. Grant
revocation on Pot is separate. Retain other servers and policies when replacing
an endpoint. Update Pot/helper/adapters, refresh the installed plugin, and
explicitly reconnect; old P2P pairing has no migration or fallback. Preserve
historical Tasks, reports and artifacts; disconnect does not cancel work.

Schedulable execution is a separate receiver installation and authorization with
receiver-owned native login, model, workspace and permissions. Its required
`pot-receiver` execution support retains the native permission hook independently
of this optional helper. Ordinary MCP login cannot bind an assistant/task/run.
Do not start a receiver as part of ordinary MCP setup. Missing execution support
or unverified native configuration is a receiver limitation, not a reason to
broaden user permissions or remove safety hooks.
