# Deploy the Pot Web UI

Run Pot Server on a host and access its embedded Web UI from a desktop or phone
browser. **HTTPS is recommended for remote access.**

## Choose access

| Mode | Use | Status |
|---|---|---|
| Tailscale HTTPS | Devices on the same tailnet | Supported; provision certificates on Host |
| Own-domain HTTPS | LAN, VPN or public access | Supported; provision certificates on Host |
| Local HTTP | Browser on Host or through an SSH local tunnel | Supported |
| Remote HTTP | Basic access with fewer browser features | Planned; not implemented |

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

Start Server and open `http://localhost:17701` on Host:

```sh
pot --root /absolute/instance serve
```

For a browser on another desktop, create a local tunnel and open the same URL:

```sh
ssh -N -L 17701:127.0.0.1:17701 USER@HOST
```

Raw remote HTTP is currently refused, including LAN and Tailscale IP addresses.
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
pot --root /absolute/instance serve
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

App Web access exclusively uses embedded Tailscale. Settings → Web access →
Connect Tailscale → complete login/approval → Enable Web access. The address is
clickable; Copy address produces a plain URL, Scan to connect includes the Token.
Enable MagicDNS and HTTPS certificates in Tailscale DNS settings. Client devices
need tailnet connectivity. No certificate files are required.

CLI optionally uses the same transport through `POT_TAILSCALE_HOSTNAME`; see
[Standalone Server](standalone-server.md#optional-embedded-tailscale). Existing
manual HTTPS procedures above remain supported independently.
