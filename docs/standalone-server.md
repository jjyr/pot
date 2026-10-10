# Standalone CLI and embedded App

For Agent-assisted Web UI deployment and HTTPS/HTTP access-mode selection, see
the [Web UI deployment guide](web-ui-deployment.md). Remote browser access requires HTTPS.

Pot has one backend with two hosts: `pot serve` runs it independently; the macOS
App owns it inside its own process. App requests, including the Swift settings
adapter, dispatch in memory. App does not connect to a daemon or management
socket. Closing App shuts down its backend before the Sandbox Helper.

## Install and run

The release CLI embeds the matching Web UI; users need neither a repository nor
Rust/Node/WASM build tools. Extract a CLI archive into a chosen prefix and add
its `bin` directory to PATH. A binary-only installation is also supported.
Building a release from source uses the pinned toolchains:

```sh
mise exec -- python scripts/build-cli
mise exec -- python scripts/install-cli .local/poc/cargo-target/release/pot --prefix ~/.local
pot init
pot serve
```

`pot serve` runs in the foreground. Web access uses the separately configured
`POT_ACCESS_ORIGIN` and listener settings; it is absent by default. With Web
access configured, run `pot open` for automatic browser configuration,
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
option and does not use `POT_RUNTIME_COMPOSITION` to select services. Internal
preset constructors remain while the resource/federation refactor is underway.

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
authorization mechanism. Peer transport is partial P3 work; local task Agents
can use remote Host/Sandbox execution, and Control membership controls are shared
by App and Web. Broader capability transports remain pending in Issue #39.

Web listener configuration remains separately explicit. Resource creation does
not automatically start Web access. Lazy macOS Helper lifecycle and real VM
qualification remain part of the outstanding environment deployment work.

## Peer listener and enrollment (partial P3)

Use native operator `pot api /api/v1/runtime/control` for Control identity and
membership. POST exact JSON commands with `pot api PATH /absolute/path/request.json`.

Enable Control on a standalone Runtime (never the embedded App Runtime):

```json
{"action":"enable_control","config":{"address":"0.0.0.0:19443","certificate":"/absolute/control.crt","private_key":"/absolute/control.key"}}
```

Control exposes `/control/v1/identity`, `/control/v1/join`, and
`/control/v1/poll`, plus authenticated product APIs. There is no inbound execution
RPC on members. HTTPS certificates must match the Control hostname.

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

See [Control architecture](runtime-composition.md) and #39 for App mode switching,
explicit fixed-Control Web startup, and remaining remote transports.
