# Local installation updates

Pot checks for stable releases on startup and every four hours. The refresh icon
beside the version in Settings → General → About checks immediately on the
selected Control, including installations that cannot update themselves. Overlapping
checks share one network operation; checking never downloads or interrupts work. The
shared chat shell shows one highlighted update action at the bottom of the wide
navigation rail, or in the narrow titlebar and navigation menu. Its tooltip names the detected version. Opening it shows the Release link and
either an upgrade action or a specific reason automatic installation is unavailable. There is no separate update settings page.

Choosing **Update** downloads and verifies the complete package while work continues. Once
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
                         exclusive installation/data-root locks + ledger preflight
                         replace complete installation
                                       |
                         CLI hosts exec in place; worker relaunches native Apps
                         startup migration batch per DB
                                       |
                         verify target ledgers/versions/readiness -> release maintenance
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
previous package and migration journal. These may contain private metadata and must
remain owner-only. Keep the installation separate from all data roots and
workspaces. The Homebrew `pot-cli` Formula omits the CLI's update registration metadata, so
its hosts do not expose internal installation updates. Use
`brew upgrade jjyr/pot/pot-cli` with those hosts stopped. Both the install API and
the independent worker recheck installed metadata before replacing files.
Standalone CLI archives retain that metadata and their existing update behavior.

Source builds, Homebrew CLI and builds without a pinned release key still
discover public releases and show the Release link with a manual-install reason. Development App previews remain independent installations.

## Verification and failure handling

The binary pins an Ed25519 public key. Discovery uses the stable GitHub Release's
`update.json`, containing a base64 payload and signature. The payload binds the
version, storage format/fingerprint, update protocol, full ordered checksum-bound
migration lineage for every database kind, the fixed ledger DDL digest, target
platform, package kind,
download URL, byte size and SHA-256 digest. Download URLs must belong to this repository's versioned
release path; redirects must retain HTTPS. Invalid signatures, older/equal versions, size mismatches and digest mismatches
never authorize installation. Authenticity and installation eligibility are
separate: an incompatible format, absent migration path, unavailable platform or
installation permission still leaves the new version visible. If the signed feed
cannot be verified (including a missing pinned key), discovery may confirm the
public version through this repository’s GitHub Release API. That fallback grants
only a fixed `https://github.com/jjyr/pot/releases/tag/vVERSION` link, never
installation permission. A network failure retains previously discovered
information and reports that the check failed; it does not mean “up to date”.
Extraction confines files and relative symlinks to the staging directory.

Maintenance prevents new API business operations, Agent invocation and schedule
admission. Each host cancels its original work and reports full shutdown before
replacement. Unconfirmed process cleanup or an installation lock still held by a
host aborts installation. No automatic database backups, snapshot restoration or Down migrations are
performed. Workspaces, task files and machine identities are not replaced.

The signed target must use update protocol 2, the same non-SQL `StorageFormat`
marker and an exact extension of every current SQL migration lineage. A changed
fingerprint alone is not a rejection. The fingerprint includes every SQL file,
including root-level `migrations/ledger.sql`. Its separately signed `ledger_digest`
must equal the running binary’s embedded fixed ledger DDL: a product lineage
extension cannot authorize changing receipt metadata. Existing ledgers must have
the original sql-migrate column/primary-key layout and no triggers before their
receipts are trusted. Ledger format changes require a separate protocol design.

After every host closes its stores, the worker acquires both installation and data-root locks, reads each owned SQLite
history (including lazy conversation databases), rejects unknown/newer histories,
and durably writes `migration-journal.json` before replacing the package. The
journal contains paths and version receipts, not database contents. It and package
backups remain owner-only. New binary startup reuses `storage.OpenDatabase` through
`storage.MigrateRoot`; SQL comes only from the embedded centralized migration tree.
Each database’s entire pending batch and ledger commit in one transaction. There
is no cross-database/root transaction. Reentry skips committed batches and safely
retries rolled-back batches. Non-SQL format changes require a new marker and an
explicit upgrade design; the SQL lineage does not authorize arbitrary JSON changes.

Business admission remains held until every expected host reports a new boot
identity, target version and readiness and all existing database ledgers equal
the authenticated target. A stopped host reporting the same version cannot
satisfy retry readiness.
Same-schema startup/readiness failures retain existing package rollback behavior.
For a schema-changing target, the worker first confirms new-host cleanup and
reacquires root locks. Only an unchanged complete set of old histories permits
restoring the old package. A committed migration, added database or unreadable/
uncertain history keeps the new package and sets **blocked** with a diagnostic.
It never opens migrated data with an older binary, restores old receipts or
replays interrupted work. Partial multi-root commits follow the same rule.

After investigating the local error, **Retry this version** can restart the
installed target while maintenance remains held. It revalidates the signed feed,
installed metadata and target journal and requires every expected root’s host to
be present at the target version with no active work. Missing hosts must be
restarted using the installed new binary after repairing the startup prerequisite;
an unconfirmed cleanup stays blocked. Retry uses the same startup runner, keeps
committed receipts and does not select the old package. Installer crashes leave
maintenance held; a running manager detects the absent worker and reports blocked.
No unknown process is killed. Keep the package and journal until recovery is
qualified; operators can then remove obsolete package backups/stages and journals.

On-demand public release verification reuses this signature verifier with an
explicitly trusted pinned key and checks the version and downloaded bytes listed
in the signed feed. See the [verification command](releases.md#public-verification-after-publication)
for dependencies, reports and exit codes.

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
`POST /api/v1/updates` accepts typed `check`, `download`, `install` and `retry`
commands. Check carries a request ID, no version and no interruption; it forces
network discovery even when installation is unsupported. Install commands bind
the version and request ID, with explicit interruption confirmation when needed.
Status separates `current`, detected `version`, `release_url`, `discovery`,
`auto_install`, `reason`, `can_retry`, operation `phase` and current work counts.
The UI retains the Release link during failed/blocked operations, and scopes
checks and late responses to the selected Control. Both use the existing authenticated
Control/native/browser routes. Progress invalidates the `updates` topic; the UI
uses bounded reconnection checks only during an initiated restart and reloads
frontend assets after a successful version change.

Inspect `<installation>.updates/state.json` and `worker.log` after failure. A
blocked installation or installer process failure requires local operator
inspection before restarting services; do not delete execution uncertainty fences
or restore older receipts merely to make startup succeed. Previous packages and migration journals are retained for inspection and can be
removed by the operator after a successful update has been qualified.

The 0.0.1 naming refresh uses the `pot-2026-10-5` storage-format discriminator.
Earlier development binaries and roots use a different discriminator and are not
eligible for automatic conversion or same-version self-update. The updater still
rejects equal versions. Install the refreshed 0.0.1 manually with a separate unused
root; retain any previous root with its matching binary. No database migration or
root adoption is performed by this refresh.

Protocol 2 and its migration lineage are a bootstrap boundary. Released 0.0.1 and
0.0.2 clients retain their original fingerprint rejection and discovery behavior;
this implementation cannot make those running binaries show a hidden release or
automatically cross that boundary. Install the first protocol-2 package manually,
with all old hosts stopped. The same applies to an old Cocoa shutdown protocol,
a missing pinned key or a trust-key change. No historical tag or released package
is modified by this implementation.
