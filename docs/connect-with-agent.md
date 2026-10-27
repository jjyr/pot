# Connect Codex to Pot with an agent

Give your setup assistant this prompt on the computer running Codex (the same
prompt appears in the [README](../README.md#codex)):

```text
Read https://github.com/jjyr/pot/blob/main/docs/connect-with-agent.md and help me connect Codex to Pot on this computer.
```

This guide helps an installation assistant set up the local Codex plugin with
the user. It does not change instructions for assistants running inside Pot.
Codex, Antigravity and Pot-to-Pot connections are supported; Claude and Grok
are coming soon. For Antigravity, use the [Antigravity integration guide](../integrations/antigravity/README.md).
For another Pot, use the [Agent Link guide](agent-links.md).

The [Codex integration guide](../integrations/codex/README.md#install-and-connect)
is the authoritative reference for installation, network access, workspace
permissions and recovery. Use the guide and installer from the same revision as
the user's Pot installation.

## Check the local environment

1. Identify the computers running Pot and Codex and whether they share a machine,
   LAN or Tailscale network. Check for an existing Pot installation and plugin
   before changing either. If Pot is not installed, follow the
   [installation guide](install-with-agent.md) first.
2. Install and sign in to Codex locally through its official installation and
   login flow. Keep authentication in the user's local terminal or browser.
   The adapter targets Codex CLI 0.163.0's App Server v2 and needs plugin support;
   check the installed version and the matching integration guide rather than
   assuming a different version is compatible.
3. Obtain a Pot source checkout at the same revision as the installed Pot, with
   Git and [mise](https://mise.jdx.dev/getting-started.html) available. In that
   checkout, run `mise install` to install its declared toolchain, including Go
   and Python. The plugin installer builds the host locally; no separate
   published plugin package is required. If repository access or a prerequisite
   is unavailable, report the limitation rather than substituting a package.

Review an existing installation with the user before replacing it. The installer
preserves unrelated marketplace entries and refuses to overwrite local plugin
edits. For upgrades, stop the existing Agent Link host and back up its complete
private root before replacing it; follow the integration guide's upgrade rules.

## Install the local plugin

From the matching Pot checkout on the Codex computer:

```sh
mise exec -- python3 integrations/codex/install.py
```

The installer builds `pot-codex-agent`, installs the local Pot plugin and
registers its marketplace source. It enables Pot when the installed Codex CLI
supports the plugin command; otherwise enable the registered Pot plugin through
Codex's plugin directory. Follow the actual installer result, and do not invent
a plugin command for an unsupported CLI.

The plugin starts its local Agent Link host automatically. Enabling or connecting
the plugin does not itself start model work. No model request or sample task is
needed to finish setup.

## Connect through MCP

For ordinary tool use, configure an independent MCP server with the user's Pot
resource URL, such as `https://pot.example/mcp`. Complete the existing Pot browser
login and confirm the connection. A local Pot Host is unnecessary. The optional
setup plugin can help configure this server, but disabling or uninstalling that
plugin does not remove independent MCP configuration; remove it separately.
Never overwrite a same-named server without inspecting the conflict.

For schedulable Codex execution, install the updated adapter and explicitly run
its receiver-connect and receiver-serve commands from the
[Codex guide](../integrations/codex/README.md#install-and-connect). The receiver
initiates its connection to Pot and registers its actual native capabilities.
Pot's Add assistant page shows the connected receiver and its existing assistant
management catalogue. Model and workspace choices stay within the receiver's
preprovisioned scopes. A connection grants no new local project permissions.

For another Pot, use the MCP URL and **Connect with browser** in connection
settings. One published endpoint supports both directions of tasks and messages.

Update both ends and explicitly reconnect. TCP invitations, pairing pages and
the old local `connections` command are removed. Do not import old pairings or
fall back to them. Historical records remain readable; old unfinished work keeps
its last confirmed state. See [Pot MCP connections](pot-mcp-connections.md).
