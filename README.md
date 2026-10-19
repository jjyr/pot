<p align="center">
  <img src="resources/app/assets/Pot384.png" width="128" height="128" alt="Pot — an ochre companion with an open tilted rim and a smile">
</p>

# Pot

Pot is an open-source implementation of the Dot experience.

Pot runs assistants and background tasks through a Go Control with local execution.
Explicitly enrolled Runners support remote Tasks. Commands and tool plugins run
on the selected machine with owner authorization; Runtime manages no VMs or containers.

## Install

### macOS App

Requires Apple Silicon and macOS 26 or later.

```sh
brew install --cask jjyr/tap/pot
```

### Server (Control or execution-only)

On Linux or macOS, use a Go CLI archive or build the complete runtime from a
checkout with the pinned toolchains in `mise.toml`:

```sh
mise install
mise run build:cli
```

Both modes use the same CLI:

- **[Control](docs/deployment.md#standalone-control)** manages assistants, conversations,
  model credentials, tasks and joined machines. Connect with the macOS App or Web UI.
- **[Execution-only (Runner)](docs/deployment.md#runner-membership)** joins
  a Control and provides explicitly selected local execution.
  It connects outward and needs no model-provider credentials.

Go hosts the product logic. The macOS App uses Wails v3 and the system WebView;
main window, tray and the independent browser service share HTML/CSS/JavaScript.
Tools use trusted [Plugin packages](docs/plugins.md). The Runtime supplies Node;
browser tools use Chrome/Chromium installed on the execution machine.

## Features

- **A personal AI assistant** — give instructions, ask questions, and follow up through chat.
- **Local execution** — tools run on the Control by default, with explicit remote Task destinations.
- **Background tasks** — Pot binds each task to a fixed Runtime, tracks progress, and reports results.
- **Scheduled work** — ask Pot to run recurring tasks and notify you when something changes.
- **Persistent workspaces** — retain files in assistant and Task directories.

See the [documentation](docs/README.md) for details.

## Codex integration

Delegate independent tasks from Pot to your local Codex on another Tailscale
machine. An authenticated HTTPS Bridge starts separate Codex App Server threads;
SSE pushes progress and durable results back to Pot. The local Codex plugin can
also send messages/requests to a paired Pot assistant and query receipts. Your
current Codex App chat stays separate. See the [installation and pairing guide](integrations/codex/README.md).

Copy this prompt into Codex from a checkout containing the integration (use the PR
branch before merge, `main` after merge):

> Read `integrations/codex/README.md` in this Pot checkout, then run its idempotent
> installer and verify the local plugin/MCP setup using my normal permissions.
> Preserve existing checkout and configuration changes. Infer defaults; ask only
> for the Bridge/Pot endpoint, pairing route or allowed workspaces that you cannot
> determine. Keep credentials in private files, never in chat or command output.
> Report separately whether the plugin is installed/enabled and whether both
> machines are paired and connected; do not change Tailscale ACLs or enable Funnel.
