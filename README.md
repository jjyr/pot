<p align="center">
  <img src="crates/app/assets/Pot384.png" width="128" height="128" alt="Pot — an ochre companion with an open tilted rim and a smile">
</p>

# Pot

Pot is an open-source implementation of the Dot experience.

Pot runs its Agent locally with separately managed Linux sandboxes. Pot manages machines, sandboxes and background tasks—you simply tell it what to do and provide guidance along the way.

## Install

### macOS App

Requires Apple Silicon and macOS 26 or later.

```sh
brew install --cask jjyr/tap/pot
```

### Server (Control or execution-only)

On Linux or macOS, install from source with Rust/Cargo, Brotli CLI and the native build
prerequisites described in the [Server installation guide](docs/deployment.md#install-server):

```sh
cargo install --git https://github.com/jjyr/pot.git --locked --bin pot pot-server
```

Both modes use the same CLI:

- **[Control](docs/deployment.md#control-server)** manages assistants, conversations,
  model credentials, tasks and joined machines. Connect with the macOS App or Web UI.
- **[Execution-only (Runner)](docs/deployment.md#execution-only-server-runner)** joins
  a Control and provides explicitly configured Host or Sandbox execution resources.
  It connects outward and needs no model-provider credentials.

The default `web-ui` feature builds, compresses and embeds the frontend. Install
Brotli CLI first; the initial build also prepares WASM tools. Execution-only
installs can use `--no-default-features` to skip these dependencies. macOS Sandbox
helpers require separate setup.

## Features

- **A personal AI assistant** — give instructions, ask questions, and follow up through chat.
- **Managed sandboxes** — independent Linux sandboxes start on demand, run tools and preserve work across shutdown.
- **Background tasks** — Pot binds each task to a fixed Host or Sandbox, tracks progress, and reports results.
- **Scheduled work** — ask Pot to run recurring tasks and notify you when something changes.
- **A visible workspace** — inspect the sandbox desktop and take over when needed.

See the [documentation](docs/README.md) for details.
