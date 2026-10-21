<p align="center">
  <img src="resources/app/assets/Pot384.png" width="128" height="128" alt="Pot — an ochre companion with an open tilted rim and a smile">
</p>

# Pot

Pot is an open-source implementation of the Dot experience.

Pot runs assistants and background tasks on autonomous Go nodes. Agent Link
connects Pot, Codex and compatible agents for conversation and task delegation.
Each node owns its models, credentials, workspace and authorized local execution.
Runtime manages no VMs or containers.

## Install

### macOS App

Requires Apple Silicon and macOS 26 or later.

```sh
brew install --cask jjyr/tap/pot
```

### Server node

On Linux or macOS, use a Go CLI archive or build the complete runtime from a
checkout with the pinned toolchains in `mise.toml`:

```sh
mise install
mise run build:cli
```

A [server node](docs/deployment.md#standalone-node) manages its assistants,
conversations, model credentials and local tasks. Connect agents through Agent
Link, and publish browser access with the independent Web UI service.

Go hosts the product logic. The macOS App uses Wails v3 and the system WebView;
main window, tray and the independent browser service share HTML/CSS/JavaScript.
The reusable [MCP component](docs/mcp.md) supplies SDK-based protocol support for
plugin tools, independent servers and Codex integration.

Trusted [Plugin packages](docs/plugins.md) contribute tools, background services
and model connections. The Runtime supplies Node for JavaScript adapters;
browser tools use Chrome/Chromium installed on the execution machine.

## Features

- **A personal AI assistant** — give instructions, ask questions, and follow up through chat.
- **Local execution** — tools run on their owning node; remote work uses Agent Link delegation.
- **Background tasks** — Pot binds each task to a fixed Runtime, tracks progress, and reports results.
- **Scheduled work** — ask Pot to run recurring tasks and notify you when something changes.
- **Persistent workspaces** — retain files in assistant and Task directories.

See the [documentation](docs/README.md) for details.

## Codex integration

Connect Codex through Agent Link by copying a connection segment and pasting it
into **Add Agent**. Pot discovers the Codex plugin's virtual assistants and uses
the existing chat and delegation interfaces. Each virtual assistant owns one
persistent Codex session, shared by its serialized chat and task queue. Codex
keeps its own login, model settings, workspace and permissions. See the
[installation and connection guide](integrations/codex/README.md).

From a checkout containing the integration:

```sh
mise exec -- python3 integrations/codex/install.py
```

Enable the Pot plugin in Codex, open `pot_open_connections`, then copy and paste
the connection segment between nodes. Configure additional assistants and their
local workspaces through the host's local `assistant` command.
