<p align="center">
  <img src="resources/app/assets/Pot384.png" width="96" height="96" alt="Pot">
</p>

# Pot

**Your AI assistant, connected to other assistants, ready to get things done.**

Pot turns conversations into completed work. Ask your assistant to research,
work with files or automate recurring tasks. **Agent Link** connects Pot,
Codex and other agents through compatible adapters to exchange messages and
delegate work across computers.

[Documentation](docs/README.md) · [Codex setup](integrations/codex/README.md) · [Releases](https://github.com/jjyr/pot/releases)

## Main features

### Assistants

Give an assistant an ongoing goal: research a topic, follow a project or keep
track of changes. It can break work into background tasks, dispatch them, receive
progress and results, and follow up in your conversation. Files and workspaces
persist between tasks, so research can build on earlier work.

Assistants can also respond to events from enabled plugins, including your
[GitHub personal notifications](docs/plugins.md#github-notifications), and
receive task updates automatically. Ask for [scheduled research](docs/scheduled-tasks.md)
or recurring checks to keep following a topic while Pot is running. Choose your
model providers and extend assistants with plugins, MCP servers and Skills.

### Agent Link

Work with the agents you already use—**Claude, Codex, Grok bot, Pi** and
others—through Agent Link adapters. Connected assistants can exchange messages
and delegate tasks across computers, while each agent keeps its own models,
credentials, workspace and permissions.

Pot-to-Pot connections and a [Codex adapter](integrations/codex/README.md) are
included. Claude, Grok bot and Pi require their own compatible adapters; this
repository does not bundle them. Explicit Codex tasks each have an independent
persistent thread and can run in parallel. See [Agent Link](docs/agent-links.md)
for the collaboration protocol and supported capabilities.

## Install

Start from source today; packaged distribution is pending.

| Platform | Install / download | Availability |
|---|---|---|
| macOS desktop / Homebrew | [Build guide](docs/development.md#toolchains-and-workflow) · [Downloads](https://github.com/jjyr/pot/releases) | Apple Silicon, macOS 26+. Homebrew distribution is pending. |
| Linux server / Web UI | [Quick start](#linux-server--web-ui) · [Deployment guide](docs/deployment.md) | Available from source on a glibc-based amd64 or arm64 host. |
| Docker | [Server deployment guide](docs/deployment.md) · [Releases](https://github.com/jjyr/pot/releases) | Image not yet published. |
| Linux desktop (Experimental) | [Releases](https://github.com/jjyr/pot/releases) | Native `.deb` packaging is experimental; no desktop package is published yet. |

Windows is currently unsupported.

## Quick start

### Desktop

Open Pot, choose **Add assistant** and connect an API model provider, another
Pot, or Codex. Try: “Research this project and suggest three improvements.”

For Agent Link, copy connection information from one side into **Add Agent** on
the other. See [Agent Link](docs/agent-links.md) or [Codex setup](integrations/codex/README.md).

### Linux server / Web UI

With Git and [mise](https://mise.jdx.dev/getting-started.html) installed:

```sh
git clone https://github.com/jjyr/pot.git
cd pot
mise install
mise run prepare
mise run build:go
.local/go/bin/pot serve -root "$HOME/.pot-node" -ui 127.0.0.1:17703
```

Leave it running. In another terminal in the checkout, get your login token:

```sh
.local/go/bin/pot access -root "$HOME/.pot-node" token
```

Open <http://127.0.0.1:17703>, sign in with the token and add an assistant as above.
The first run creates the private data root and token. For a remote server,
forward the port from your computer first:

```sh
ssh -L 17703:127.0.0.1:17703 user@your-server
```

`pot serve` runs the Runtime; `-ui` adds its local browser interface.
`pot web-ui-server` is an independent browser service with a separate data root.
See [deployment](docs/deployment.md) for that setup, TLS and services.

Licensed under the [MIT License](LICENSE).
