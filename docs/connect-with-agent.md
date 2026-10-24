# Connect Codex to Pot with an agent

Give your setup assistant this prompt on the computer running Codex (the same
prompt appears in the [README](../README.md#codex)):

```text
Read https://github.com/jjyr/pot/blob/main/docs/connect-with-agent.md and help me connect Codex to Pot on this computer.
```

This guide helps an installation assistant set up the local Codex plugin with
the user. It does not change instructions for assistants running inside Pot.
Codex and Pot-to-Pot connections are supported; Claude, Grok and Antigravity
are coming soon. For another Pot, use the [Agent Link guide](agent-links.md).

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

## Get connection information and add it in Pot

1. Start a new Codex chat after installation and run the plugin's `pot-setup`
   entry. It opens the local connection page in Codex's in-app browser.
   If needed, use the system-browser fallback in the
   [integration guide](../integrations/codex/README.md#install-and-connect).
2. Keep the temporary management URL private: it grants connection-management
   access for 30 minutes. Pass it directly to the local browser; never print it
   in chat, publish it, or capture it in screenshots or logs. Reopen setup if it
   expires. Never ask the user to paste API keys or owner credentials into chat.
3. Choose **Copy connection address**, then **Get connection address** for an
   address reachable from Pot. Use loopback on the same computer, a LAN address
   on the same private network, or a suitable public or Tailscale address across
   computers. Discovery alone does not prove remote reachability. Review any
   firewall or network changes with the user; follow the integration guide
   instead of exposing the local owner HTTP listener.
4. Let the user copy the complete connection information from the selected,
   read-only field and paste it into Pot's **Add Agent**. A manual copy works
   when browser clipboard access is unavailable. Treat the invitation as private
   pairing information and keep it out of shared transcripts and screenshots.
   Only one side needs a reachable address; traffic then flows both ways.
5. Check that Pot shows the connected Codex node and its assistant directory.
   If it does not, inspect plugin status and the chosen address before retrying.
   Do not send a message or run a task just to demonstrate connectivity without
   the user's authorization.

The initial Codex assistant and task executor use an isolated read-only workspace.
Connecting does not grant access to the user's project. If the user wants project
work, establish the intended workspace and permission mode locally using the
integration guide's assistant and executor configuration. Do not broaden scope
as an implicit setup step. Models, credentials, workspaces and permissions remain
owned by the Codex host.
