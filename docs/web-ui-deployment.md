# Deploy the Pot Web UI

Explicitly host Pot Web UI for one standalone Control. Plain `pot serve` starts
Runtime infrastructure without UI, regardless of saved listener settings. All
product requests use the configured Control; failed connections never load local
Runtime data. **HTTPS is required for remote browser access.**

## Choose access

| Mode | Use | Status |
|---|---|---|
| Tailscale HTTPS | Devices on the same tailnet | Supported; provision certificates on Host |
| Own-domain HTTPS | LAN, VPN or public access | Supported; provision certificates on Host |
| Local HTTP | Browser on Host or through an SSH local tunnel | Supported |

## Deploy with an Agent

Fill in the host, network and preferred mode:

> Deploy Pot Server and Web UI on HOST. Clients connect through NETWORK; use MODE.
> Follow docs/web-ui-deployment.md. Check support for my chosen mode, install and configure
> Server, and verify browser login and desktop access. Give me the URL, Token configuration
> instructions, and service stop/start and certificate renewal instructions.

## Install

Install the CLI for the Host OS/architecture using
[Standalone Server](standalone-server.md#install-and-run). Linux Sandbox execution
also requires [rootless Podman and an installed image](standalone-server.md#linux-host-and-podman).

Initialize a dedicated instance; replace example paths and addresses below:

```sh
pot --root /absolute/instance init
```

Expose only the Web UI listener. Keep native Runtime and guest service ports private.

## HTTP

Start an independently configured Control first (see [Control membership](standalone-server.md#control-listener-and-membership-partial-p3)). Obtain its native client credential from its private Runtime endpoint using operator access. This is distinct from the browser login Token and machine invitations. Store it in the service environment as `POT_CONTROL_TOKEN`, never in a browser URL or command-line argument. A private Control CA can be supplied with `POT_CONTROL_CA=/absolute/control-ca.pem`.

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

## HTTPS

### Tailscale certificate

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

### Own-domain certificate

Point a domain such as `pot.example.com` to Host's LAN, VPN or public address.
Use an ACME client with DNS-01 validation and a scoped DNS-provider API credential.
DNS-01 needs no public Host IP or inbound router ports. Ensure clients resolve
the domain correctly; some home routers require a private-address DNS exception.
Outside-home access additionally needs a VPN or public network path.

Provide the full certificate chain and private key on Host. Pot terminates TLS;
any TCP relay must forward TLS unchanged. See [Mobile access](mobile-access.md#start-the-owner-endpoint).

### Configure and start

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

## Connect and verify

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

## Embedded Tailscale and App

App has separate Settings → Web UI and Settings → Tailscale tabs. Both Web UI
entry points are off for new instances. Enable LAN access for HTTP on private IPv4
Host addresses, or connect Tailscale and independently enable Tailscale access.
Tailscale access supports HTTP and optional HTTPS. HTTPS requires MagicDNS and
HTTPS Certificates in tailnet DNS settings, and applies only to the tailnet listener.
Addresses are clickable; Copy address produces a plain URL, Scan to connect
includes the Token. The node connects independently of Web UI, and disabling Web
UI leaves it connected. Disconnecting the node leaves LAN access running and
retains tailnet Web UI preferences for reconnection. Existing enabled tailnet HTTPS
instances retain that mode on upgrade.

CLI localhost and manual HTTPS procedures above remain supported independently.

## Current limits

The Web host keeps Control credentials on the server and streams SSE updates.
Browser Tokens and cookies authenticate only the Web host and are not forwarded
to Control. There is no local/remote switch. Native-only APIs remain excluded by
the browser route allowlist. Remote desktop and push notifications are later #39
work; they do not use the Web host's local execution or local notification state.
