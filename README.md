<p align="center">
  <img src="resources/app/assets/Pot384.png" width="96" height="96" alt="Pot">
</p>
<h1 align="center">Pot</h1>
<p align="center"><strong>Your AI assistant. From a conversation to completed work.</strong></p>

[Website](https://pot.jjylab.com) · [Getting started](#install) · [Documentation](docs/README.md) · [Releases](https://github.com/jjyr/pot/releases)

Pot helps you turn a goal into work you can use. Your assistant can run background
tasks, bring their results back to your conversation, and keep working with you.
**Agent Link** brings your other assistants into that conversation across computers.

## From a conversation to completed work

“Get this project ready to share: verify Linux installation, complete the
getting-started docs, and prepare representative product screenshots.”

Pot can split that goal into background tasks for installation, documentation
and screenshots. Results return to the same conversation: acceptance notes with
remaining checks, a documentation draft, and images you can preview. Keep talking
to refine the work: “Make the mobile screenshots larger; keep this docs version.”

[![Pot conversation with returned work and three background tasks](docs/images/conversation-tasks.png)](docs/images/conversation-tasks.png)

*Pot’s shared product UI with synthetic project work and background tasks. Click to enlarge.*

## Bring your other agents into the conversation

Connect another Pot or **Codex** through Agent Link. Talk to a connected assistant
directly, or let Pot delegate work and collect the result. Each agent retains its
own models, credentials, workspace and permissions. Pot-to-Pot connections and
the [Codex adapter](integrations/codex/README.md) are included; Claude, Grok bot,
Pi and other agents need compatible adapters of their own. Codex tasks use
independent persistent threads and can run in parallel.

[![Pot using Agent Link to request a Codex review and receive the result](docs/images/agent-link.png)](docs/images/agent-link.png)

*Agent Link messages and returned review in the actual product UI, using synthetic records. Click to enlarge.*

## Install

Give this prompt to your agent to install and configure Pot on this computer:

```text
Read https://github.com/jjyr/pot/blob/main/docs/install-with-agent.md and help me install and configure Pot on this computer.
```

The [shared installation guide](docs/install-with-agent.md) helps your agent check
existing data, choose a channel and complete first setup. To install it yourself:

| Channel | Download / install | Platform |
|---|---|---|
| macOS (Homebrew) | Download the release's `pot.rb`, then run `brew install --cask ./pot.rb` in its directory; [cask instructions](docs/releases.md#homebrew-generation) | Apple Silicon, macOS 26+; [first launch](docs/desktop-installation.md#macos) |
| Linux desktop · Experimental | [Download .deb](https://github.com/jjyr/pot/releases) — select `pot-desktop_VERSION_linux_amd64.deb` | Debian / Ubuntu amd64; [requirements](docs/desktop-installation.md#linux-experimental) |
| Web UI (Docker) | [View Docker image](https://github.com/users/jjyr/packages/container/package/pot) · `ghcr.io/jjyr/pot` | Linux amd64 / arm64; [installation and running](docs/docker.md) |
| Web UI (Server) | [Download Web UI / Server](https://github.com/jjyr/pot/releases) — select `pot-go-VERSION-linux-ARCH.tar.gz` | glibc Linux amd64 / arm64; [configuration and running](docs/deployment.md) |

During the unsigned release phase, macOS installation is offered through Homebrew
only. Homebrew may still require an App-specific Gatekeeper allowance; follow the
[first-launch guide](docs/desktop-installation.md#macos).

Linux download links open the official release selector; packages and the container
image await their first publication. Homebrew uses the release's local cask;
there is currently no Pot cask in the public tap. Once images are published,
choose an explicit version tag; no `latest` tag is published. Check release
notes and supplied checksums. If your package is unavailable, the
[agent guide](docs/install-with-agent.md#build-from-source-when-a-matching-package-is-unavailable)
covers the official source builds. GitHub-hosted files currently require
repository access.

Windows is currently unsupported.

## First run

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
