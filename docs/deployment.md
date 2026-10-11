# Deployment

This page covers local App hosting, standalone Runtime resources, Control membership
and explicit Web hosting. Remote browser access requires HTTPS.

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

Install the CLI for the Host OS/architecture using
[Standalone Server](deployment.md). Linux Sandbox execution
also requires [rootless Podman and an installed image](deployment.md).

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
