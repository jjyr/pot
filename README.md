<p align="center">
  <img src="resources/app/assets/Pot384.png" width="128" height="128" alt="Pot — an ochre companion with an open tilted rim and a smile">
</p>

# Pot

Pot is an open-source implementation of the Dot experience.

Pot runs its Agent locally with managed Linux sandboxes. macOS and Linux use the
same Podman container provider; macOS runs its Linux containers in one dedicated
Pot-managed Podman machine. Pot manages machines, sandboxes and background tasks—you
simply tell it what to do and provide guidance along the way.

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
  a Control and provides explicitly configured Host or Sandbox execution resources.
  It connects outward and needs no model-provider credentials.

Go hosts the product logic. The macOS App uses Wails v3 and the system WebView;
main window, tray and the independent browser service share HTML/CSS/JavaScript.
Swift provides native macOS VM support; Node plugins and Python guest services
run in their own runtimes. See [Deployment](docs/deployment.md) for resource
setup, private data roots and browser publication.

## Features

- **A personal AI assistant** — give instructions, ask questions, and follow up through chat.
- **Managed sandboxes** — independent Linux sandboxes start on demand, run tools and preserve work across shutdown.
- **Background tasks** — Pot binds each task to a fixed Host or Sandbox, tracks progress, and reports results.
- **Scheduled work** — ask Pot to run recurring tasks and notify you when something changes.
- **A visible workspace** — inspect the sandbox desktop and take over when needed.

See the [documentation](docs/README.md) for details.
