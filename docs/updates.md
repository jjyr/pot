# Local installation updates

Pot checks the signed stable release feed on startup and every four hours. The
shared chat shell shows one highlighted update action at the bottom of the wide
navigation rail, or in the narrow titlebar and navigation menu. Its tooltip names the target
version. There is no separate update settings page.

Clicking downloads and verifies the complete package while work continues. Once
prepared, the backend continues installation without requiring an open browser.
If work is active, installation remains prepared until the initiating UI confirms
**Interrupt and upgrade**. Cancellation retains the package and leaves work
running. Confirmation covers the installation's local executions, replies and terminal
sessions. Linked Tasks keep their receiver-owned state and are not cancelled by
updating the directing node. Interrupted local work is never automatically replayed.

## Installation boundary

An update replaces one complete installation: executable(s), shared frontend,
Node, sandbox assets, plugins and built-in Skills. Every running App, standalone
Runtime and browser service launched from that installation registers a private
local update endpoint. Processes launched from another installation are outside
the group. Each node updates its own installation.
The browser UI requests the update of its selected Control, never its own browser
machine. Operators must coordinate remote deployments separately.

```text
UI -> selected Control update port -> signed package download/staging
                                  -> independent installer process
                                       |
                         prepare each registered local host
                         cancel work, close servers/stores, confirm cleanup
                                       |
                         exclusive installation lock + data snapshots
                         replace complete installation
                                       |
                         CLI hosts exec in place; worker relaunches native Apps
                         verify versions/readiness -> release maintenance
```

CLI hosts retain their supervisor's PID when restarting. Native macOS Apps exit
and are relaunched by the installer with a new PID. Both preserve their original
arguments, environment and working directory. For macOS Apps the installer is
`Contents/MacOS/pot-app _update-worker JOB` in
an independent process. This dispatch runs before flag parsing and Wails/Cocoa
application initialization; the App does not bundle the complete `pot` CLI. The
running worker keeps executing while the old bundle is moved to the backup path.
Both Homebrew cask and direct installations use this same signed update path;
there is no installation-channel marker or Homebrew subprocess. The cask keeps
`auto_updates true`.

The installer does not invoke arbitrary shell commands, create new
user services, or start previously stopped instances. Closing the browser does
not stop a download or installation; a pending interruption confirmation still
requires a user. Starting another host during installation is serialized by the
installation lock.

The resume endpoint flushes its acknowledgement before waking the host to exec.
A failed response write or flush leaves that host stopped. This prevents a
successful restart from cutting off the acknowledgement and appearing to the
installer as an uncertain EOF that requires rollback.

Native Apps use separate preparation and exit requests. Preparation stops
business admission and closes executions, stores and business sockets while the
update socket and Cocoa process remain alive. Only successful preparation allows
the exit endpoint to flush its acknowledgement and ask Cocoa to quit. The worker
arms an OS process-exit watch before that request and waits for the actual exit;
EOF alone never proves successful cleanup. It then acquires the installation
lock, replaces the package and starts the exact original App executable. Restart
commands are exchanged only over the private live socket and are never written
to registration files or jobs. Readiness rediscovery binds the new PID to the
same data root rather than reusing the old PID's socket. Failed launch/readiness
restores and relaunches the previous package only after new-process exit or
cleanup is confirmed. Uncertain native cleanup leaves maintenance blocked.

The installed directory must be replaceable by its service account. Its private
sibling `<installation>.updates` holds the state, staged package, worker log,
previous package and data snapshots. These may contain private metadata and must
remain owner-only. Keep the installation separate from all data roots and
workspaces. The Homebrew `pot-cli` Formula omits the CLI's update registration metadata, so
its hosts do not expose internal installation updates. Use
`brew upgrade jjyr/pot/pot-cli` with those hosts stopped. Both the install API and
the independent worker recheck installed metadata before replacing files.
Standalone CLI archives retain that metadata and their existing update behavior.

Source builds and builds without a pinned release key do not advertise
an update action. Development App previews remain independent installations.

## Verification and failure handling

The binary pins an Ed25519 public key. Discovery uses the stable GitHub Release's
`update.json`, containing a base64 payload and signature. The payload binds the
version, storage format, target platform, package kind, download URL, byte size
and SHA-256 digest. Download URLs must belong to this repository's versioned
release path; redirects must retain HTTPS. Invalid signatures, unsupported storage
formats, older/equal versions, size mismatches and digest mismatches are rejected.
Extraction confines files and relative symlinks to the staging directory.

Maintenance prevents new API business operations, Agent invocation and schedule
admission. Each host cancels its original work and reports full shutdown before
replacement. Unconfirmed process cleanup or an installation lock still held by a
host aborts installation. Closed SQLite and JSON metadata are copied privately;
workspaces, task files and machine identities are not replaced.

After replacement, hosts start with business admission held until every expected
host reports the new version and readiness. Failed readiness triggers restoration
of the previous complete package after confirming the new hosts have stopped.
If that cleanup cannot be confirmed, the installation stays blocked for operator
inspection; the installer does not kill unknown work or silently release the
maintenance gate. Execution receipts are retained during rollback, rather than
replacing them with older database copies.

Only matching `StorageFormat` markers and identical centralized migration-file
fingerprints are eligible for automatic installation. Build tooling pins the
fingerprint in each executable and signed manifest, so adding a migration
automatically blocks that automatic upgrade path. Regular manual upgrades still
use the [startup migration mechanism](database-migrations.md). Automatic upgrades
that change schemas need a separate backup/migration/restore design; package-only
rollback must never reopen a migrated database with an older binary.

## Release setup

Provision one Ed25519 release key outside the checkout and retain it securely:

```sh
mise exec -- go run ./scripts/sign-release keygen /private/path/pot-update-key.pem
```

Set the base64 public key as the GitHub Actions repository variable
`POT_UPDATE_PUBLIC_KEY`, and the private PEM as the secret
`POT_UPDATE_PRIVATE_KEY`. The CI packaging jobs pin the public key in every
executable, including container builds. After shared checks and the macOS/Linux
CLI build gates succeed, the macOS release workflow signs the manifest for the
macOS App and macOS/Linux CLI archives. It
requires the matching private key in both macOS release modes before
publication. `unsigned` packages use ad-hoc integrity signatures without Developer
ID or Apple notarization; `signed` additionally requires Apple signing and notary
configuration. The Ed25519 feed authenticates the final bytes in either case and
does not grant Gatekeeper approval. See the
[macOS first-launch guide](desktop-installation.md#macos). See [Releases](releases.md) for the environment gate, artifact naming
and channel contracts. Docker and Linux publication do not depend on macOS
signing. Unsigned local builds remain possible, but
without a pinned public key they have no self-update capability.

Install the first build containing the pinned key manually. Existing builds that
predate this updater cannot bootstrap themselves into it. Key replacement also
requires an explicit trust/bootstrap plan; the feed cannot change its own pinned
verification key. Homebrew marks the App as self-updating.

Native builds whose updater terminates Cocoa before acknowledging preparation
also require a manual installation of the repaired App. A new download cannot
repair the already running old host's shutdown protocol. Quit the old App before
replacing its complete bundle. The native upgrade simulation builds both versions
from the repaired source; it does not qualify an older released updater as a
working upgrade origin.

`GET /api/v1/updates` projects availability, phase and current work counts;
`POST /api/v1/updates` accepts only typed download/install requests with a version,
request ID and explicit interruption flag. Both use the existing authenticated
Control/native/browser routes. Progress invalidates the `updates` topic; the UI
uses bounded reconnection checks only during an initiated restart and reloads
frontend assets after a successful version change.

Inspect `<installation>.updates/state.json` and `worker.log` after failure. A
blocked installation or installer process failure requires local operator
inspection before restarting services; do not delete execution uncertainty fences
or restore older receipts merely to make startup succeed. Previous packages and
snapshots are retained for inspection and can be removed by the operator after a
successful update has been qualified.

The 0.0.1 naming refresh uses the `pot-2026-10-5` storage-format discriminator.
Earlier development binaries and roots use a different discriminator and are not
eligible for automatic conversion or same-version self-update. The updater still
rejects equal versions. Install the refreshed 0.0.1 manually with a separate unused
root; retain any previous root with its matching binary. No database migration or
root adoption is performed by this refresh.
