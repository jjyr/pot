# Multiple Servers in the native App

The macOS App embeds its local Server and can connect to additional independent
Servers. Servers do not discover, replicate or delegate to one another.

## Interaction

The shared toolbar contains a Server picker. It only switches existing
connections. Add, edit, pair again and remove live in Settings → Servers.
Adding takes the endpoint origin, pairing code, display name and this device's
name. Generate the one-use code with `pot access pair` on the destination
machine (or its App's pairing controls). Success returns to the connection list;
use Switch Server or the toolbar to open it. Editing an address checks that it
still identifies the paired instance. A different identity must be added as a
new Server. Removing a connection deletes this App's saved key and metadata,
without stopping the remote Server or deleting its data. The Server owner may
also revoke authorization.

Native and remote transport share the existing protocol and page components.
Each connection has its own Client, cached snapshots, navigation history,
filters, scroll handles, task/AI settings drafts and conversation input drafts.
Switching preserves background work. Inactive clients refresh less frequently.
Requests capture their destination before dispatch; late results update that
connection's state. Send retries retain the original request UUID and destination.
Receipts clear only the draft belonging to the accepted conversation.
The App starts with its local Server; saved remote connections remain available.

Language and appearance are App preferences. AI configuration, task execution
limits, usage and environments belong to the selected Server. Execution limits
are saved through `/api/v1/config/execution` on that Server and apply at host
restart. The native App restarts its local backend after saving local limits;
remote limits display a restart reminder. Remote native Codex account setup uses
the official device-code flow. No provider inference is needed to connect.

## Identity and authentication

`connections.json` in the App root stores endpoint metadata only, using atomic
0600 writes. Per-connection P-256 signing keys and client IDs live in macOS
Keychain; sessions stay in memory. Pairing uses the existing signed, one-use
challenge ceremony. Server identity is a persistent UUID stored in
`product/access-clients.json`; status advertises it and the protocol version,
and challenges bind the identity as well as origin, client and purpose.
Address changes do not confer trust on a different Server. HTTP remains a
localhost development convenience; remote endpoints require valid HTTPS.
Redirects are not followed during authentication or API calls.

Pairing grants owner access to the instance API. There are no roles or
permission tiers. The accepting listener checks Origin and session validity,
strips external cookies/authorization before internal dispatch and supplies its
own Runtime credential. Internal bearer credentials are never sent to clients.
Revocation/disable invalidate sessions and future logins. A restart prompts
transparent signed login; revoked keys show a pairing recovery message.

## Desktop and files

Sandbox lifecycle and desktop-session requests target the selected Server.
Remote WKWebView uses that Server's HTTPS desktop gateway and session cookie;
local viewing keeps its loopback gateway. Switching closes the current display
ticket, never the VM. Access-session revocation closes the associated desktop
ticket. Provider capabilities describe actual desktop availability, rather than
Client permission tiers.

Remote workspace files stay on their Server. The native Finder action is local
only; remote workspace navigation explains using Files in the Sandbox desktop.
Remote artifact download/synchronization is not implemented.
The browser/PWA still connects to its own origin; a browser multi-origin picker
is outside this increment. Real remote VM display and provider account setup
require separate qualification beyond transport/Runtime regression tests.
