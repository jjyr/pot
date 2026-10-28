<p align="center">
  <img src="resources/app/assets/Pot384.png" width="96" height="96" alt="Pot">
</p>
<h1 align="center">Pot</h1>
<p align="center"><strong>The open-source AI assistant that coordinates your coding agents—so you don't have to micromanage them.</strong></p>

<p align="center">
  <a href="README.md">English</a> · <a href="README_zh.md">简体中文</a>
</p>

[Website](https://pot.jjylab.com) · [Why Pot?](#why-pot) · [Getting started](#install) · [Agent Link](#agent-link-your-multi-agent-hub) · [Documentation](docs/README.md) · [Releases](https://github.com/jjyr/pot/releases)

Pot turns conversational goals into completed work. Instead of constantly micromanaging one-off prompts and fragmented contexts, Pot acts as a persistent assistant: delegating background tasks, coordinating external agents across machines, and only interrupting you when high-level decisions are needed.

## Why Pot?

Coding with agents has promised 10x productivity, but many developers find themselves exhausted—spending entire afternoons micromanaging AI, re-explaining repo habits across sessions, and inspecting code line by line.

Pot is designed around a simple belief: **Human oversight should stay at the architecture level, while assistants handle the repetition.**

- **Assistants over one-off agents**: Assistants run long-lived sessions, accumulating instructions, repo habits, and personal preferences without forgetting them across compactions.
- **Goal delegation & background execution**: Give your assistant a goal. It splits the work into background tasks, tracks progress, and brings finished results back to the conversation.
- **Cross-machine hub**: Run Pot headless on a Linux server, steer it through mobile Web UI, and dispatch coding jobs to Codex on your local Mac.

👉 *Read the full design philosophy:* **[Why Coding with Agents Is Exhausting](https://jjylab.com/en/posts/why-agent-coding-is-exhausting/)**

## From a conversation to completed work

“Get this project ready to share: verify Linux installation, complete the
getting-started docs, and prepare representative product screenshots.”

Pot can split that goal into background tasks for installation, documentation
and screenshots. Results return to the same conversation: acceptance notes with
remaining checks, a documentation draft, and images you can preview. Keep talking
to refine the work: “Make the mobile screenshots larger; keep this docs version.”

[![Pot conversation with returned work and three background tasks](docs/images/conversation-tasks.png)](docs/images/conversation-tasks.png)

*Pot’s shared product UI with synthetic project work and background tasks. Click to enlarge.*

## Agent Link: Your Multi-Agent Hub

Modern AI development doesn't happen in a single tool or on a single machine. **Agent Link** turns Pot into a universal mesh connecting your preferred coding agents, specialized bots, and remote instances—while preserving each agent's native models, credentials, and sandboxed workspaces.

- **Cross-Computer Collaboration**: Connect a headless Pot on your cloud server with your local developer workstation seamlessly.
- **Independent Task Execution**: Dispatched tasks run in isolated, persistent threads without jamming your active chat.
- **Ecosystem Integration**:
  - **Codex**: **Supported.** Full bi-directional task delegation and review workflows via adapter.
  - **Claude**: *Coming soon.*
  - **Grok**: *Coming soon.*
  - **Antigravity**: *Coming soon.*

[![Pot using Agent Link to request a Codex review and receive the result](docs/images/agent-link.png)](docs/images/agent-link.png)

*Agent Link messages and returned review in the actual product UI. Click to enlarge.*

## Install

Give this prompt to your agent to install and configure Pot on this computer:

```text
Read https://github.com/jjyr/pot/blob/main/docs/install-with-agent.md and help me install and configure Pot on this computer.
```

The [shared installation guide](docs/install-with-agent.md) helps your agent check
existing data, choose a channel and complete first setup. To install it yourself:

| Channel | Download / install | Platform |
|---|---|---|
| macOS App (Homebrew) | Use the two commands below; [tap instructions](docs/releases.md#homebrew-generation) | Apple Silicon, macOS 26+; [first launch](docs/desktop-installation.md#macos) |
| macOS CLI (Homebrew) | `brew install jjyr/pot/pot-cli` after the tap below | Apple Silicon, macOS 26+ for the current archive; [CLI installation](docs/desktop-installation.md#macos-cli) |
| Linux desktop · Experimental | [Download .deb](https://github.com/jjyr/pot/releases) — select `pot-desktop_VERSION_linux_amd64.deb` | Debian / Ubuntu amd64; [requirements](docs/desktop-installation.md#linux-experimental) |
| Web UI (Docker) | [View Docker image](https://github.com/users/jjyr/packages/container/package/pot) · `ghcr.io/jjyr/pot` | Linux amd64 / arm64; [installation and running](docs/docker.md) |
| Web UI (Server) | [Download Web UI / Server](https://github.com/jjyr/pot/releases) — select `pot-VERSION-linux-ARCH.tar.gz` | glibc Linux amd64 / arm64; [configuration and running](docs/deployment.md) |

```sh
brew tap jjyr/pot https://github.com/jjyr/pot.git
brew install --cask jjyr/pot/pot
```

Install the independent headless CLI alongside the App with:

```sh
brew install jjyr/pot/pot-cli
pot-cli --version
# Upgrade the CLI through Homebrew:
brew upgrade jjyr/pot/pot-cli
```

`pot-cli` uses the existing CLI package, including Node and all runtime resources.
It has no GUI dependency. Use separate data roots when running it alongside the
App; see [CLI installation](docs/desktop-installation.md#macos-cli).

During the unsigned release phase, macOS installation is offered through Homebrew
only. Homebrew may still require an App-specific Gatekeeper allowance; follow the
[first-launch guide](docs/desktop-installation.md#macos).

Linux download links open the official release selector; packages and the container
image await their first publication. The first `brew tap`
requires the explicit Git URL above. Once images are published,
choose an explicit version tag; no `latest` tag is published. Check release
notes and supplied checksums. If your package is unavailable, the
[agent guide](docs/install-with-agent.md#build-from-source-when-a-matching-package-is-unavailable)
covers the official source builds. GitHub-hosted files currently require
repository access.

Windows is currently unsupported.

## First run

### Desktop

Open Pot, choose **Add assistant** and connect an API model provider, another
Pot, Codex, or Antigravity. Try: “Research this project and suggest three improvements.”

For Agent Link, configure the published MCP URL and complete browser
authorization. Native receivers are installed and started separately. See
[Agent Link](docs/agent-links.md) or [Codex setup](integrations/codex/README.md).

### Linux server / Web UI

With Git and [mise](https://mise.jdx.dev/getting-started.html) installed:

```sh
git clone https://github.com/jjyr/pot.git
cd pot
mise install
mise run prepare
mise run build:go
.local/pot/bin/pot serve -root "$HOME/.pot-node" -ui 127.0.0.1:17703
```

Leave it running. In another terminal in the checkout, get your login token:

```sh
.local/pot/bin/pot access -root "$HOME/.pot-node" token
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

## Connect other assistants

Agent Link uses MCP for Pot-to-Pot, Codex and Antigravity connections. To connect another
Pot, follow the [Agent Link guide](docs/agent-links.md). External assistant
support is listed below.

### Codex

**Supported.** Give this prompt to your agent on the computer running Codex:

```text
Read https://github.com/jjyr/pot/blob/main/docs/connect-with-agent.md and help me connect Codex to Pot on this computer.
```

The [assistant connection guide](docs/connect-with-agent.md) covers prerequisites,
independent MCP configuration, browser authorization and optional native receiver
setup. See the [complete Codex guide](integrations/codex/README.md).

### Claude

Coming soon.

### Grok

Coming soon.

### Antigravity

Configure an independent Pot MCP connection for ordinary tools, or install the
separate receiver support package for assistant messages and Tasks. It uses the
same MCP receiver and Agent contracts as Codex, with Antigravity stream-json as
the native execution driver. Headless human approvals are not advertised. See
the [Antigravity guide](integrations/antigravity/README.md) for setup and capability
boundaries.

## More ways to work

Keep separate assistants for different goals, retain files and workspaces between
tasks, and arrange [scheduled work](docs/scheduled-tasks.md). Choose model
providers and extend your assistants with [plugins, MCP servers and Skills](docs/plugins.md).
Optional [GitHub account setup](docs/credentials.md) and
[notification filters](docs/plugins.md#github-notifications) can bring project
updates into the conversation. GitHub is not required to run Pot.

See [installation and first configuration](docs/install-with-agent.md),
[Agent Link](docs/agent-links.md), [Docker](docs/docker.md) and the
[documentation index](docs/README.md) for operating instructions.

Licensed under the [MIT License](LICENSE).
