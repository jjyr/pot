# Deployment

This page covers local App hosting, standalone Runtime resources, Control membership
and explicit Web hosting. Remote browser access requires HTTPS.

Pot has one backend with two hosts: `pot serve` runs it independently; the macOS
App owns it inside its own process. App requests, including the Swift settings
adapter, dispatch in memory. App does not connect to a daemon or management
socket. Closing App shuts down its local Control and Runner before the Sandbox Helper.
Provider credentials are configured on Control and encrypted in SQLite; see
[master key setup](secrets.md) for unattended deployments.

## Install Server

Server is installed from Git with Cargo; no prebuilt Server download is provided.
Control and execution-only (Runner) use the same `pot` binary on Linux or macOS.
Install Rust/Cargo through rustup (the repository pins Rust 1.98.1 in `mise.toml`), Git, Python 3
(available as `python3`), a native C/C++ build toolchain, CMake, pkg-config, Make
and Perl for native dependencies including vendored OpenSSL. The default Web UI
also requires Brotli CLI and curl. On macOS,
install Xcode Command Line Tools. Ensure Cargo's binary directory (normally
`~/.cargo/bin`) is on PATH.

```sh
cargo install --git https://github.com/jjyr/pot.git --locked --bin pot pot-server
pot --version
```

This builds from the repository's default branch. For reproducible deployments,
add `--rev COMMIT` using the same commit on Control and Runners. To upgrade,
repeat the installation with `--force`, then restart the affected services.

The default `web-ui` feature builds the frontend, compresses it with the external
Brotli CLI and embeds it in the binary. Install `brotli` and `curl` before running
the command above (for example, `brew install brotli` on macOS). Developers can
use the repository's `mise install`, which provides Brotli 1.2.0 through the
`conda:brotli-bin` backend without a separate Conda installation.

The first Web build needs network access to prepare the pinned nightly WASM
toolchain, wasm-bindgen CLI and verified noVNC sources. This takes longer than a
backend-only build. Subsequent builds reuse valid generated assets and compress
only changed or damaged files. No generated WASM, JS or `.br` files are committed
to Git; no separate static directory is required at runtime.

For an execution-only binary, add `--no-default-features` to the install command.
This skips all Web asset generation/compression and needs neither Brotli nor the
WASM tools. To explicitly restore it, use `--features web-ui`. There is no separate
compression feature. Web hosting still requires [explicit Web configuration](#explicit-web-ui);
plain `pot serve` does not expose a browser endpoint.

Cargo does not install embedded Tailscale libraries, builtin skill directories
or macOS Sandbox helpers. Execution dependencies such as Podman, Sandbox images
and tools used by Host tasks must be installed separately.

Choose one role per data directory:

| Role | Configuration | Client / connection |
|---|---|---|
| Control | Enable the Control listener; create assistants and configure models | macOS App or explicitly enabled Web UI |
| Execution-only (Runner) | Create execution resources; join one Control; no assistants | Outward HTTPS to Control |

Plain `pot serve` starts neutral infrastructure; it does not select a role or
create resources. A joined Runner cannot also enable Control or create assistants.
A Runtime with assistants cannot join as a Runner. Use separate roots when both
roles run on one machine, and give their listeners different ports.

### Control Server

Initialize a dedicated root and start the foreground process:

```sh
pot --root "$HOME/.pot-control" init
pot --root "$HOME/.pot-control" serve
```

Keep it running and use a second terminal for the following operator commands.
Provision a TLS certificate and private key for the Control hostname, readable
by the service account. Save this as `enable-control.json`, replacing the paths:

```json
{"action":"enable_control","config":{"address":"0.0.0.0:19443","certificate":"/absolute/control.crt","private_key":"/absolute/control.key"}}
```

```sh
pot --root "$HOME/.pot-control" api /api/v1/runtime/control "$PWD/enable-control.json"
```

Allow clients and Runners to reach the HTTPS listener. Certificate identity and
membership details are in [Control listener and membership](#control-listener-and-membership).
Create an assistant by saving this as `assistant.json`:

```json
{"request_id":"assistant-1","resource":{"kind":"assistant","name":"Assistant"}}
```

```sh
pot --root "$HOME/.pot-control" api /api/v1/runtime/resources "$PWD/assistant.json"
pot --root "$HOME/.pot-control" api /api/v1/runtime/control
```

Connect the macOS App to this remote Control using its HTTPS URL and the durable
client credential in `.pot-control/product/control-client-credential.json` under
your home directory. This is a client credential, not a machine invitation.
Configure the model connection on Control through the App. For unattended
operation, configure and retain the [master key](secrets.md#master-key).
Control does not require a local execution resource when tasks run on Runners.

To enroll a Runner, save `{"action":"create_invitation"}` as `invitation.json`
and run:

```sh
pot --root "$HOME/.pot-control" api /api/v1/runtime/control "$PWD/invitation.json"
```

Use the returned invitation in the Runner procedure below.

### Execution-only Server (Runner)

On the execution machine, initialize its root and keep the foreground process running:

```sh
pot --root "$HOME/.pot-runner" init
pot --root "$HOME/.pot-runner" serve
```

In another terminal, explicitly create the resources this machine will offer.
For execution directly on the Host, save the following as `host.json`:

```json
{"request_id":"host-1","resource":{"kind":"host"}}
```

```sh
pot --root "$HOME/.pot-runner" api /api/v1/runtime/resources "$PWD/host.json"
```

Host execution runs on this machine under the service account. For isolated
execution, configure a [Sandbox resource](#runtime-resources) instead, with its
[Provider dependencies](sandbox-providers.md) and image installed. Creating a
Sandbox definition alone does not make it ready or start it.

Save a Control identity preview request as `preview-control.json`:

```json
{"action":"preview","url":"https://control.example:19443","ca_pem":null}
```

```sh
pot --root "$HOME/.pot-runner" api /api/v1/runtime/control "$PWD/preview-control.json"
```

Verify the returned identity and fingerprint against the intended Control.
For a private CA, set `ca_pem` to the trusted CA's PEM contents in both preview
and join requests. Save the join request as `join-control.json`, replacing all
placeholders and using a fresh UUID for `request_id`:

```json
{"action":"join","request":{"request_id":"UUID","url":"https://control.example:19443","invitation":"INVITATION","expected_runtime_id":"CONTROL-ID","expected_fingerprint":"CONFIRMED-FINGERPRINT","ca_pem":null}}
```

```sh
pot --root "$HOME/.pot-runner" api /api/v1/runtime/control "$PWD/join-control.json"
pot --root "$HOME/.pot-runner" api /api/v1/runtime/control
pot --root "$HOME/.pot-runner" api /api/v1/runtime/resources
```

Check that the machine appears connected in the Control client's Machines view
and that its intended execution resources are present. Joining authorizes Control
to use all configured resources on this Runtime, subject to Agent approvals.
The Runner reconnects outward after restart; it needs no inbound execution port,
assistant resource or model-provider key. Keep its data directory to retain membership.

### Process and instance management

Both examples run in the foreground. For unattended operation, arrange for your
OS service manager to run the same command with the same service account, root
and environment. Shut down cleanly before upgrading and restarting the binary.

`pot serve` runs in the foreground without Web UI, even when listener settings
exist. Enable it with `pot serve --ui --control https://CONTROL:19443` and
`POT_CONTROL_TOKEN`; optionally supply a private CA file through `POT_CONTROL_CA`.
The destination must authenticate as a standalone Control. Web requests always
use this Control and never fall back to the hosting Runtime. The separately
configured `POT_ACCESS_ORIGIN` controls the browser listener. Once explicitly
started, run `pot open` for automatic browser configuration,
or `pot access token` to configure the browser manually. `pot access qr` prints
a connection QR containing the Token; do not share it.
Localhost HTTP is a local secure-context
convenience, not remote encryption: remote access requires owner-terminated
HTTPS. HTTP listeners cannot bind outside loopback.

Use `pot --root /absolute/instance/path ...` for a separate instance. Defaults
are `~/Library/Application Support/pot` on macOS and `$XDG_DATA_HOME/pot` (or
`~/.local/share/pot`) on Linux. Data paths do not depend on the current directory.
App and CLI cannot open the same product directory concurrently: an OS lock
fails startup instead of silently sharing SQLite or attaching to another host.
Different instances also need different listener ports.

Standalone management uses `product/control.sock`, mode 0600 inside a private
product directory. CLI injects no user-entered credentials and never prints the
internal bearer. OS access to this socket is full owner authority. Requests
carrying browser Origin headers are rejected. The App has no such socket.
There is still an authenticated loopback integration listener for external
tool/model clients. In App mode its routes are restricted to these
integrations; the App UI cannot use it to access snapshots/settings.

## Runtime resources

`pot serve` starts Runtime infrastructure. Fresh roots have no assistant, Host
execution resource or Sandbox definition. Startup accepts no `--composition`
option and does not use `POT_RUNTIME_COMPOSITION` to select services. Services are assembled from the durable resource manifest.

Native callers list/create resources at `/api/v1/runtime/resources`. CLI `api`
uses the private management socket; callers do not need to handle its bearer.
Create requests use a stable request ID and exactly one typed resource:

```json
{"request_id":"assistant-1","resource":{"kind":"assistant","name":"Research"}}
```

```json
{"request_id":"host-1","resource":{"kind":"host"}}
```

```json
{"request_id":"sandbox-1","resource":{"kind":"sandbox","sandbox_id":"build","name":"Build","provider":"linux.podman"}}
```

Use an installed Provider ID from the deployment's Provider documentation.
Save a request to a JSON file and call:

```sh
pot api /api/v1/runtime/resources
pot api /api/v1/runtime/resources /absolute/path/create-resource.json
```

Creating an assistant does not call a model or enable Host execution. Creating
a Sandbox defines it without starting a VM and does not select it as the default.
Existing Sandbox management APIs configure/select/start explicit instances.
Resource creation is serialized and recorded durably before assembly. Identical
request retries return the recorded result; conflicting retries fail. Restart
restores definitions without replaying shell/model/VM-start effects. Current
product definitions are adopted during versioned resource upgrade; this does
not import assistant instruction files or saved routing preferences.

App creates its default assistant through the same resource API. New resource
creation UI is deferred by user choice. Native management authority and
Control membership remains separate; this endpoint is not a member execution
authorization mechanism. Remote Task Agents start on the selected execution
Runtime; Control retains model credentials, budgets and approvals. See
[architecture](architecture.md) for launch, receipt and recovery rules.

Web listener configuration remains separately explicit. Resource creation does
not automatically start Web access. Lazy macOS Helper lifecycle and real VM
qualification remain part of the outstanding environment deployment work.

## Control listener and membership

Use native operator `pot api /api/v1/runtime/control` for Control identity and
membership. POST exact JSON commands with `pot api PATH /absolute/path/request.json`.

Enable Control on a standalone Runtime (never the embedded App Runtime):

```json
{"action":"enable_control","config":{"address":"0.0.0.0:19443","certificate":"/absolute/control.crt","private_key":"/absolute/control.key"}}
```

Control exposes `/control/v1/identity`, `/control/v1/join`, and
`/control/v1/poll`, plus authenticated product APIs. There is no inbound execution
RPC on members. HTTPS certificates must match the Control hostname and permit server authentication.
Private fixture certificates also require `extendedKeyUsage=serverAuth`; a bare
self-signed certificate may be rejected by the TLS client.

Create an invitation on Control with `{"action":"create_invitation"}`. Preview
its identity from the joining machine with `{"action":"preview","url":"https://control.example:19443","ca_pem":null}`.
Then submit the confirmed identity and invitation on the execution machine:

```json
{"action":"join","request":{"request_id":"UUID","url":"https://control.example:19443","invitation":"INVITATION","expected_runtime_id":"CONTROL-ID","expected_fingerprint":"CONFIRMED-FINGERPRINT","ca_pem":null}}
```

The machine opens an outward authenticated connection and restores it on restart.
Use the original request for an uncertain join retry. A machine binds one Control.
On Control, `{"action":"remove_member","runtime_id":"MEMBER-ID"}` invalidates
membership; on a member, `{"action":"leave"}` fences leases and stops polling.
Joining gives Control all configured resources, with no machine grants. Agent
behavior approvals remain enforced. Do not distribute member tokens as UI tokens.

`{"action":"disable_control"}` stops the listener; restart with its explicit
configuration to reconnect members. Native diagnostic `action:call` uses `runtime_id`
and a protocol `body`; it is excluded from browser-facing machine commands.

Upgrade Control and execution binaries together. Protocol signatures and digests
use canonical nested JSON ordering across App/macOS and standalone/Linux builds;
there is no alternate legacy signature path. Persistent signing identities,
membership credentials, local operation paths and user data are retained. Active
execution-node runs follow the reboot interruption rule, not automatic replay.

See [Control architecture](architecture.md) and #39 for App mode switching,
explicit fixed-Control Web startup, and remaining remote transports.


## Explicit Web UI

Explicitly host Pot Web UI for one standalone Control. Plain `pot serve` starts
Runtime infrastructure without UI, regardless of saved listener settings. All
product requests use the configured Control; failed connections never load local
Runtime data. **HTTPS is required for remote browser access.**

### Choose access

| Mode | Use | Status |
|---|---|---|
| Tailscale HTTPS | Devices on the same tailnet | Supported; provision certificates on Host |
| Own-domain HTTPS | LAN, VPN or public access | Supported; provision certificates on Host |
| Local HTTP | Browser on Host or through an SSH local tunnel | Supported |

### Install

The default [Cargo installation](#install-server) includes the Web UI. The frontend build and compression run automatically during Cargo installation.
Linux Sandbox execution also requires rootless Podman and an installed image;
see [Sandbox providers](sandbox-providers.md).

Initialize a dedicated instance; replace example paths and addresses below:

```sh
pot --root /absolute/instance init
```

Expose only the Web UI listener. Keep native Runtime and guest service ports private.

### HTTP

Start an independently configured Control first (see [Control membership](deployment.md)). Obtain its durable client credential from `product/control-client-credential.json` using operator access after enabling the Control listener. The private file is mode 0600 and survives Control restarts. It is accepted only by the Control HTTPS listener; local Runtime process tokens still rotate on every boot. This is distinct from the browser login Token and machine invitations. Store it in the service environment as `POT_CONTROL_TOKEN`, never in a browser URL or command-line argument. A private Control CA can be supplied with `POT_CONTROL_CA=/absolute/control-ca.pem`.

Start the explicit Web host and open `http://localhost:17701`:

```sh
POT_ACCESS_ORIGIN=http://localhost:17701 pot --root /absolute/instance serve --ui --control https://CONTROL:19443
```

For a browser on another desktop, create a local tunnel and open the same URL:

```sh
ssh -N -L 17701:127.0.0.1:17701 USER@HOST
```

Manual CLI HTTP configuration is localhost-only. The App is a native client and
its embedded Runtime does not host Web UI.
The localhost tunnel is not a general iPhone access method.

### HTTPS

#### Tailscale certificate

Join Host and clients to the same tailnet. Enable MagicDNS and HTTPS Certificates
in [Tailscale DNS settings](https://login.tailscale.com/admin/dns). Get Host's
actual DNS name and Tailscale IP from `tailscale status`.

Create a private certificate directory, then issue on Host:

```sh
tailscale cert --cert-file=/private/tls/server.crt --key-file=/private/tls/server.key HOST.TAILNET.ts.net
```

Use administrator authority if required and give the Pot service account ownership
of the files. Configure the origin below as `https://HOST.TAILNET.ts.net:17701`
and bind as `TAILSCALE_IP:17701`. No router port forwarding is required.

#### Own-domain certificate

Point a domain such as `pot.example.com` to Host's LAN, VPN or public address.
Use an ACME client with DNS-01 validation and a scoped DNS-provider API credential.
DNS-01 needs no public Host IP or inbound router ports. Ensure clients resolve
the domain correctly; some home routers require a private-address DNS exception.
Outside-home access additionally needs a VPN or public network path.

Provide the full certificate chain and private key on Host. Pot terminates TLS;
any TCP relay must forward TLS unchanged. See [Mobile access](deployment.md).

#### Configure and start

Make both certificate files readable by the Pot service account with mode `0600`.
Save the following in `/private/listener-settings.json` using your actual values:

```json
{
  "POT_ACCESS_ORIGIN": "https://pot.example.com:17701",
  "POT_ACCESS_BIND": "192.168.1.10:17701",
  "POT_ACCESS_CERT": "/private/tls/server.crt",
  "POT_ACCESS_KEY": "/private/tls/server.key"
}
```

```sh
pot --root /absolute/instance config server /private/listener-settings.json
pot --root /absolute/instance serve --ui --control https://CONTROL:19443
```

Environment overrides saved settings. Arrange certificate renewal through
Tailscale or the ACME client, then restart Pot to load renewed files. Pot currently
automates neither issuance nor certificate reload.

### Connect and verify

While Server is running:

```sh
pot --root /absolute/instance open
pot --root /absolute/instance access token
pot --root /absolute/instance access qr
```

Connection QR codes contain credentials. Do not share them. Browsers save the
Token locally and automatically connect later. Verify chat/reconnect and Sandbox
desktop on intended devices. `pot access regenerate-token` invalidates all old
browser connections.

### Embedded Tailscale

A standalone Web host may use embedded Tailscale with explicit listener settings.
The native App hosts no Web endpoint. Browser configuration cannot change the
fixed Control destination. Manual localhost and HTTPS procedures above remain
available independently.

### Current limits

The Web host keeps Control credentials on the server and streams SSE updates.
Browser Tokens and cookies authenticate only the Web host and are not forwarded
to Control. There is no local/remote switch. Native-only APIs remain excluded by
the browser route allowlist. Remote desktop is deferred in #39.

Push notifications use the Web host's VAPID configuration and server-held Control
credential. One subscription covers
all assistants; per-owner cursors are durable and scoped to the Control identity.
Only finalized assistant replies notify; Task results wait for the assistant's actual
report. Clicking a notification opens its assistant. Control disconnects retain the
cursors and do not fall back to local messages.


## Browser authentication and Home Screen

The browser Token authenticates the Web host, separately from Control credentials.
It is saved in origin-scoped IndexedDB; URL fragments are removed after configuration.
Signed HttpOnly cookies support navigation; Token rotation invalidates browser
connections. Native APIs are excluded by a public allowlist. No offline transcript
cache is provided.

## Add Pot to the Home Screen

After configuring the access Token, browser **More** provides **Add to Home Screen**.
The Token configuration page has no installation action. Installation does not request notification permission.
The manifest uses a stable origin-scoped app ID, standalone display and dedicated
192/512 px icons. iOS also receives Apple Home Screen metadata and its touch icon.

Home Screen, browser/notification and login branding use the selected terracotta
Pot Companion. `scripts/make-icons.swift` generates both the rounded Mac icon
(`crates/app/assets/Pot.png` / `Pot.icns`) and an opaque, square cream-background master
(`PotHomeScreen.png`) so mobile operating systems can apply their own masks.
`scripts/build-mobile` produces the touch icon and 192/512 px `any maskable` icons
from that master. Versioned icon URLs distinguish this design from previously
installed icons without changing the manifest's stable application ID. Mobile's
GPUI header embeds the matching Mac image. README uses the same application icon.

On Android Chrome/Edge or another supporting browser, the button opens the
browser's installation prompt when available. If it is unavailable or dismissed,
use the browser menu's Install app / Add to Home Screen entry. Embedded QR
scanner browsers may require opening the URL in the system browser first.

On iPhone/iPad, the button explains Safari → Share → Add to Home Screen. Enable
Open as Web App when that option is shown. Launch the Pot icon to use a standalone
window. The app detects standalone mode and browser installation events rather
than treating acceptance of a prompt as completed installation.

A browser-trusted HTTPS certificate is required for remote Service Worker and push support. A Service Worker
registers even when VAPID/push is not configured, but uses network-only fetches:
there is no offline transcript or credential cache. Installation does not grant
access, change authorization or request notification permission. A newly installed
Home Screen app may use a separate storage context and require its own Token configuration. Notifications remain a separate explicit opt-in.


Remote Control notification relay is implemented; see its [validation report](validation/control-notifications-20261011.md)
for checked behavior and the untested external push-service delivery. Remote desktop
streaming remains deferred pending a connection design.

## Machine labels

Control owns routing labels; joined Runtimes report their OS at enrollment.
`GET /api/v1/machines` includes `tags` per machine. The authenticated
`POST /api/v1/machine-connections` operator accepts
`{"action":"set_tags","runtime_id":"ID","tags":["linux","task-runner"]}`.
This replaces the complete label set, including allowing an empty set. It does
not enable resources or verify capabilities. Runner-side edits are rejected.
See [routing semantics](agent-context.md#message-references-and-machine-labels).

## Runtime Agent execution

Each Runtime has one execution manager shared by assistants and Tasks. Local
memory calls and remote launches use the same destination-local admission path.
To set machine concurrency, create `runtime-execution.json` in that Runtime's data
directory before startup:

```json
{"max_active_agents":8,"max_queued_agents":256}
```

These are the defaults when the file is absent; both values must be positive.
The active limit must fit Tokio's semaphore limit. Unknown fields are rejected.
Settings apply on restart. Queue overflow fails the invocation; it does not select
another machine or model. Control's existing `execution-limits.json` governs
business task admission and model budgets, not physical Agent slots on other machines.

`runtime-executions.jsonl` records execution ID, Runtime/boot, owner, request/run,
generation, role and lifecycle/event metadata. It excludes prompts, response text,
tool arguments/output and credentials. Files are created with mode 0600, rotate
at 8 MiB and retain one previous file (`runtime-executions.jsonl.1`). They are local
diagnostics; conversations and Task journals remain authoritative for recovery.
No resource-management UI or new model-visible tool is introduced.
