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

Remote HTTP needs a different enrollment/session implementation because current
pairing uses WebCrypto. It will lack Service Worker and Web Push; PWA installation
and system clipboard APIs may also be unavailable. Ordinary HTTP does not encrypt
sessions or content. Tailscale encrypts its network traffic, but does not make
remote HTTP a browser secure context.

## Deploy with an Agent

Fill in the host, network and preferred mode:

> Deploy Pot Server and Web UI on HOST. Clients connect through NETWORK; use MODE.
> Follow docs/web-ui-deployment.md. Check support for my chosen mode, install and configure
> Server, and verify browser login and desktop access. Give me the URL, pairing
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

## Pair and verify

While Server is running, generate a code on Host:

```sh
pot --root /absolute/instance access pair
```

Open the configured URL and enter the code and a client name. Each code is valid
for five minutes and one client. Verify login, reconnect and Sandbox → Start
desktop on the intended devices. For multiple clients, verify that revoking one
leaves the other usable:

```sh
pot --root /absolute/instance access clients
pot --root /absolute/instance access revoke CLIENT_ID
```

For background operation, use the Linux systemd or macOS LaunchAgent instructions
in [Standalone Server](standalone-server.md). Model configuration and optional
push setup are documented in [Standalone Server](standalone-server.md#configure-and-operate-without-app)
and [Mobile access](mobile-access.md#main-agent-message-notifications).
