<p align="center">
  <img src="resources/app/assets/Pot384.png" width="96" height="96" alt="Pot">
</p>

# Pot

**Your AI assistant, connected to other assistants, ready to get things done.**

Pot turns conversations into completed work. Ask your assistant to research,
work with files or automate recurring tasks. **Agent Link** connects Pot,
Codex and compatible agents to exchange messages and delegate work across computers.

[Documentation](docs/README.md) · [Codex setup](integrations/codex/README.md) · [Releases](https://github.com/jjyr/pot/releases)

## Main features

- **Personal assistants** — chat, share files and images, and work in persistent workspaces.
- **Agent Link** — collaborate across assistants, each with its own workspace and permissions.
- **Background tasks** — delegate work, follow progress and receive results. Codex tasks have independent persistent threads and can run in parallel.
- **Automation** — schedule recurring work and receive notifications.
- **Models and extensions** — choose providers and add plugins, MCP servers and Skills.

## Install

Start from source today; packaged distribution is pending.

| Platform | Installation |
|---|---|
| macOS desktop | Apple Silicon, macOS 26+. [Build from source](docs/development.md#toolchains-and-workflow); Homebrew distribution is pending. |
| Linux server / Web UI | [Start from source](#linux-server--web-ui) on a glibc-based amd64 or arm64 host. |
| Docker | Image not yet published. See [server deployment](docs/deployment.md). |
| Linux desktop | No published desktop package yet. Use the server's Web UI in your browser. |
| Windows desktop | No published desktop package yet. Connect to a server's Web UI in your browser. |

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
