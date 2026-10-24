# Releases and installation channels

CI builds and publishes each delivery channel independently after shared product
checks pass. Docker, Linux server and the experimental Linux desktop do not depend
on macOS signing. Each channel requires its own build, smoke, checksum and
applicable signing gates. Windows is currently unsupported.

The first public release is 0.0.1; the root `Casks/pot.rb` installs its App.
Release workflows propose a cask update PR after successful macOS publication;
they never push the default branch or change repository/registry visibility.

CLI builds use `pot-VERSION-PLATFORM.tar.gz`, containing `pot-PLATFORM/bin/pot`
and the complete `Resources/` directory. App ZIP, Homebrew and image names retain
their existing Pot branding.

| Channel | Versioned distribution | Publication boundary |
|---|---|---|
| Linux server, amd64/arm64 | `pot-VERSION-linux-ARCH.tar.gz` | Both native CLI builds and extracted-package smoke; GitHub Release assets |
| Linux desktop (Experimental), amd64 | `pot-desktop_VERSION_linux_amd64.deb` | Native GTK/WebKit build and real WebView smoke; GitHub Release assets; manual upgrades |
| macOS 26+, Apple Silicon | `Pot-VERSION-arm64.zip`, `pot-VERSION-darwin-arm64.tar.gz` | Native App/CLI smoke and explicit `unsigned` (ad-hoc) or `signed` (Developer ID + notarization) mode; GitHub assets, cask and signed update feed |
| Container, Linux amd64/arm64 | `pot-container-VERSION.oci.tar`, `ghcr.io/jjyr/pot:VERSION` | Native image build/smoke on both architectures, OCI integrity and metadata verification; GHCR |
| Homebrew | Root `Casks/pot.rb` in `jjyr/pot` | Published final App ZIP verified before a same-repository cask update PR; merge makes installation/upgrades available |

The experimental designation applies only to the new Linux native desktop `.deb`.
Its amd64 build and native WebView smoke have been exercised locally; native
arm64, Wayland and actual apt installation have not been fully validated. Desktop
upgrades replace the complete package manually. Linux server CLI/Web UI and Docker
are separate channels.

See [Desktop installation](desktop-installation.md) and [Docker](docker.md) for
platform requirements, installation boundaries and source-build instructions.

## Triggers and source selection

The release/build `CI` workflow starts automatically only on tag pushes. The
separate `Tests` workflow runs one Linux lightweight check on PRs and pushes to
`main`. Release/build calls and manual `Tests` runs always use the complete
Linux/macOS normal/static and race matrix, regardless of `publish`; see
[Development](development.md#ci-and-manual-pr-checks) for both coverage boundaries.
Every tag runs checks, including Unicode
names and tags outside the release pattern. Only exact `vMAJOR.MINOR.PATCH` tags
without leading zeroes select publication. Other tags build validation artifacts
with version `0.0.0`; they do not push images or create releases. Branch pushes,
PR events and Release events do not start the release/build workflow.

Ordinary manual runs never publish, including when selecting a stable version tag.
The optional `ref` and `pr` inputs are mutually exclusive. Leave both empty to
check the branch/tag selected in Actions, use a source ref, or select a PR head:

```sh
gh workflow run ci.yml --ref main -f pr=123
gh workflow run ci.yml --ref main -f ref=refs/heads/my-branch
```

The selected workflow ref must contain the new workflow and helper scripts. The
source job resolves the source once, pins its SHA and public update key, and all
jobs use those values. PR selection checks the current head, including fork
heads, rather than a merge commit. Review selected source before running it on
hosted runners. Explicit ref/PR checks skip shared mise and Go caches. Validation
has read-only repository access, no persisted checkout credentials and no release
secrets. Only source resolution has pull-request read permission.

Explicit manual publication requires `publish=true`, an existing full
`refs/tags/vMAJOR.MINOR.PATCH` source ref and no PR input. It follows the same
checks and channel gates. The release tag's SHA is checked during resolution and
again immediately before publication. Moving a tag fails the corresponding gate.
Release workflows can only be called by CI; there is no separate dispatch that
bypasses checks.

```text
resolve one source SHA + version + publication intent
                         |
              shared Linux/macOS Tests workflow
                         |
               headless/cross-platform builds
                         |
       +-----------------+----------------+------------------+
       |                 |                |                  |
  Linux CLI builds  Linux desktop    macOS App + CLI    native Docker builds
   + native smoke   + native smoke    + native smoke     + smoke amd64/arm64
       |                 |                |                  |
       |                 |       ad-hoc OR Developer ID  multiarch OCI export
       |                 |           + notary
       |                 |       signed update feed     digest/arch/version/SHA
       |                 |                |                  |
  server GH assets  desktop GH assets  macOS GH assets    GHCR :VERSION
       +-----------------+----------------+
             serialized, immutable asset additions
```

The macOS update feed also needs the Linux CLI build artifacts because it binds
the existing four supported update installations. It does not need their publish
jobs to succeed. Docker has no dependency on desktop packaging or macOS signing.
The aggregate unsigned validation bundle verifies all implemented distributions;
it is not an additional cross-channel publication gate.

## Docker publication contract

A stable tag such as `v1.2.3` automatically builds the production Dockerfile and,
after checks and the `release` environment allow it, publishes
`ghcr.io/jjyr/pot:1.2.3`. The only registry tag is `VERSION`: the workflow does not
move `latest` or minor-version aliases. Normal tags and default manual runs build
and smoke-test images but never push them.

Native `ubuntu-24.04` and `ubuntu-24.04-arm` jobs build the default complete
Dockerfile, load their native image and verify the embedded version/revision.
Each runs the real disposable container smoke. A subsequent build exports both
architectures as an OCI archive, without pushing. The verifier checks manifest
and config digests, actual Linux amd64/arm64 configs and version/revision labels.
The publisher rechecks the tag and checksum before copying that exact OCI index
and both images to GHCR; it does not rebuild after approval. A conflicting
existing version digest is never overwritten. Identical retries are allowed.

The smoke explicitly selects `--sandbox unavailable`. Standard Docker confinement
can deny the user namespaces required by SRT. That mode must recognize the known
namespace refusal and prove the protected workload did not execute, while still
checking Web UI/authentication, policy, persistence and service lifetime. It does
**not** qualify Protected execution. Use `--sandbox required` separately on a
supported host to qualify actual isolation. No privileged or unconfined CI
container is used, and a denial never falls back to host execution. Container Web
UI availability and Protected execution capability are separate properties.

QEMU is used for the combined OCI export after both native smoke jobs pass. It
does not substitute for native smoke. Registry credentials exist only in an
anonymous Linux memory file during publication, not a persistent auth store or
command arguments.

## GitHub assets and channel checksums

Linux server, Linux desktop and macOS add assets to the same version's GitHub
Release after their respective gates. GitHub asset writers are serialized;
a failure in one publication channel does not prevent another qualified channel
from running. Existing assets must have the same byte size and GitHub SHA-256
digest to be reused. Missing assets may be added; different existing bytes are
never clobbered. The initial release notes explain that availability is per channel
and the Linux desktop is experimental. The publisher also handles identical asset
creation races safely.

An unpublished draft may be absent from GitHub's release-by-tag endpoint. The
publisher checks the release list and reads a matching draft by ID before
creating anything. Multiple matches in the release list stop automatic publication.
An existing public release is reused instead of leftover drafts. All reads, uploads and
publication stay bound to the selected ID; automatically discovered drafts must
also name the validated source. A selected draft cannot be published if another
release is already public for that tag.
For a reviewed recovery, `scripts/publish-release VERSION ARTIFACTS --channel CHANNEL
--release-id ID` binds reads, uploads and publication to that existing ID; a draft
must name the validated source and tag. Use only the original artifacts from the
qualified run, wait for other asset writers to finish, and retain their hashes.
Every channel asset must be present with its expected digest and size before a
draft becomes public. Recovery never rewrites a tag or replaces different bytes.

Every archive has a standard `NAME.sha256` sidecar. Each channel owns a distinct
manifest: `SHA256SUMS-server`, `SHA256SUMS-desktop`, `SHA256SUMS-macos` or
`SHA256SUMS-container`. One channel cannot replace another channel's checksum
manifest. The unsigned aggregate validation bundle uses `SHA256SUMS`. Artifacts
are retained for seven days. `POT_APP_VERSION` and `POT_BUILD_REVISION` set matching
version/revision metadata across all builds. Versioned CLI filenames retain the
internal installation directory `pot-PLATFORM`.

Channel publication is intentionally independent, not a transaction across GitHub
and GHCR. An environment refusal, registry error or platform failure may leave
other successful channels publicly available. Inspect actual assets and channel
job results before retrying. Missing update keys or the selected mode's signing
credentials affect only macOS.

## Environment, permissions and signing setup

Configure the GitHub environment `release` with custom deployment restrictions
allowing only tags matching `v*`, and no required reviewers for automatic
publication after tag pushes. GitHub's pattern is intentionally broader than the
script's exact `vMAJOR.MINOR.PATCH` publication fence; branches are not allowed.
For manual publication, select the existing stable tag as the workflow ref as well
as the source ref, so the environment sees an allowed tag. Only official publication
calls can request writes. Linux/macOS publishers use `contents: write`; the GHCR
publisher has `contents: read` and `packages: write`. Checks and packaging receive
no signing secrets. Ordinary macOS packaging also exercises the ad-hoc sealing
path and checks final extracted signatures before native smoke, so a manual
validation run can qualify that path without publication or Apple credentials.
The official macOS workflow subsequently applies its explicitly selected release
mode to those artifacts. GHCR must permit this repository's Actions token to publish
`ghcr.io/jjyr/pot`; an existing package may require repository access to be granted.
No extra registry secret is required for the configured GHCR path.

macOS requires an explicit mode and update key pair before its channel can publish:

| Configuration | Kind | Purpose |
|---|---|---|
| `POT_MACOS_RELEASE_MODE` | Release environment variable | Required: `unsigned` or `signed`; no implicit default or fallback |
| `POT_UPDATE_PUBLIC_KEY` | Repository variable | Base64 Ed25519 key pinned consistently in executables and images |
| `POT_UPDATE_PRIVATE_KEY` | Repository or release secret | Matching PKCS8 PEM for feed signing |
| `MACOS_CERTIFICATE_P12` | Release secret | Required for `signed`: base64 Developer ID Application certificate/private key |
| `MACOS_CERTIFICATE_PASSWORD` | Release secret | Required for `signed`: certificate import password |
| `MACOS_SIGNING_IDENTITY` | Release variable | Required for `signed`: full `Developer ID Application: ...` identity |
| `APPLE_ID`, `APPLE_APP_PASSWORD` | Release secrets | Required for `signed`: notary authentication |
| `APPLE_TEAM_ID` | Release variable | Required for `signed`: Apple developer team |

`unsigned` means **no Developer ID signature and no Apple notarization**. It
requires no Apple account or certificate. On native Apple Silicon macOS, the
workflow ad-hoc signs every bundled Mach-O (including Node and CLI), seals the App,
and verifies those signatures again from the final extracted archives. Ad-hoc
signing provides integrity and executability, not developer identity or Gatekeeper
approval. Native App preview, CLI/HTTP service, Node/npm, bundle resources,
installation metadata, version and commit SHA checks remain required.

`signed` additionally requires every Apple setting above. A temporary keychain
signs bundled Mach-O files before the App seal. Both App and CLI must receive
notarization acceptance; the App ticket is stapled and validated and Gatekeeper
assessment must pass. Missing credentials or any signing/notary failure is fatal;
the workflow never falls back to `unsigned`. Preflight snapshots the chosen mode
for all subsequent jobs, so an environment variable edit cannot change it mid-run.

Both modes require the matching Ed25519 update key pair. This is Pot's update
origin authentication, independent of Apple developer identity. The feed retains
its existing JSON payload/envelope format and binds the final macOS App and
macOS/Linux CLI bytes, sizes, versions and hashes. An ad-hoc package can participate
because the existing updater verifies that pinned Pot key and the archive digest;
it does not require an Apple identity. Signature checks are never bypassed and no
unsigned feed is published. This does not confer Apple approval: read the
[macOS first-launch instructions](desktop-installation.md#macos), including for
manual upgrades. Linux desktop and container upgrades remain explicit operations.
See [Updates](updates.md) for trust bootstrap and storage-format restrictions.

After final sealing and native smoke, the workflow regenerates the cask and feed
from the exact archives. `MACOS-RELEASE.txt` records mode, version, commit and an
installation-document link. It is published alongside ZIP/TAR, SHA256 sidecars,
`update.json` and `SHA256SUMS-macos`; the manifest binds the disclosure
and signed feed as well as archives. `pot.rb` is internal generated metadata,
not a required publication file or Release asset. Release notes direct users to this file
and explain that Homebrew preserves quarantine. Archive names stay versioned in
both modes. Different bytes or mode metadata for an existing version cannot
replace published assets. Publication retries reuse identical existing assets and
add missing assets; retain the same qualified artifact set for partial retries.
A rebuild that produces different bytes must fail rather than overwrite.

For an initial release without an Apple account, set the release environment
variable `POT_MACOS_RELEASE_MODE=unsigned`. Later provision the Apple settings and
switch that one variable to `signed` **for a subsequent version**. Do not replace
an earlier version's ad-hoc downloads with newly signed bytes. The former
`POT_MACOS_RELEASE_ENABLED` flag is no longer used. Environment configuration alone
does not install this workflow; the code must be merged before tagging a release.

Runner availability, pinned toolchains, package repositories, upstream downloads
and standard Actions minutes/billing remain execution prerequisites. A job refused
before startup does not establish a code failure. Paid model tests are not enabled.

## Explicit 0.0.1 naming refresh

The `Refresh reviewed v0.0.1 naming` workflow is a separate, manual operation for
this same-version naming correction. Ordinary publishers keep all immutable-byte
checks. It accepts a successful nonpublishing CI run built with
`validation-version=0.0.1`, its exact reviewed source, the original public release
snapshot, the exact previous image index digest and `merged-source`, the reviewed
PR 280 merge SHA. The source must match the tag, the merged PR head and a parent of
that normal merge, with an identical tree. This fixed merge remains the boundary
if main later advances; later UI changes cannot enter the selected artifacts. First run it from `main` with
`lease-tag-only=true` and the exact original annotated tag object. This job has no
release environment or signing secrets: it validates the successful candidate,
merged tree and original asset snapshot, then moves `v0.0.1` with an exact
old-object lease. Its workflow token suppresses recursive tag-push CI; do not push
the replacement tag separately with a personal token. Then dispatch the same
workflow from `v0.0.1` with `lease-tag-only=false`. Signing and publication retain
the existing `release` environment's `v*` tag policy. No additional tag, environment
policy change or Actions-write permission is needed. If signing fails, the original
public assets and signed feed are still intact; inspect the saved tag objects and
partial effects before any recovery.

The workflow checks all distributions, signs the final feed with the existing
controlled release key, saves every original asset, uploads and verifies the new
assets under temporary names, and retains the replacement plan and recovery bytes
as Actions artifacts. It replaces only the specified old image digest, activates
archives before references, and updates release metadata last. GitHub cannot switch
multiple same-name assets atomically: existing App downloads can briefly require
a retry while the ZIP, sidecar and feed switch. Original assets remain available
under their preserved IDs until final verification and cask synchronization; only
then remove the exact original IDs and temporary backups. Never delete the entire
release, bypass immutable-release settings, relax normal publisher checks or
regenerate the previous signed feed for rollback. Save recovery artifacts beyond
the seven-day Actions retention if needed.

The refreshed build uses only current service/configuration/browser names and
rejects earlier root/storage discriminators. It does not convert data or migrate
running services. Equal-version self-update remains disabled; manual installation
requires a separate unused root. See [Updates](updates.md).

## Homebrew generation

The website and README expose macOS through Homebrew only. Release assets retain
App ZIP/CLI archives, their SHA-256 sidecars, `MACOS-RELEASE.txt`,
`SHA256SUMS-macos` and the Ed25519-signed update feed. `pot.rb` is not uploaded.
Both `unsigned` and `signed` modes use the same tap path and final-byte checks.

```sh
brew tap jjyr/pot https://github.com/jjyr/pot.git
brew install --cask jjyr/pot/pot
# Later, after the next cask update PR is merged:
brew upgrade --cask jjyr/pot/pot
```

The initial tap must include the explicit URL: the repository is `pot`, not
`homebrew-pot`. Homebrew supports an arbitrary Git URL with its two-argument tap
command; see [Homebrew Taps](https://docs.brew.sh/Taps). Casks live in root
`Casks/`. No separate tap repository is used. Private repository/assets require
GitHub access; normal public installation requires the repository and artifacts
to be public. These workflows never change visibility.

Before the first release, `Casks/pot.rb` is explicitly disabled and has no archive
version, URL or checksum: no installable artifact exists. It cannot silently
install a development build. After publication, `scripts/sync-homebrew-cask`
downloads the final versioned App ZIP and checksum sidecar using `github.token`,
checks GitHub's SHA-256 digest and byte size, and uses the shared
`release_contract.generate_cask` template/version/sidecar validation. Sealing and
notarization happen before these final bytes are published. There is no `latest`
URL or unchecked checksum. `packaging/homebrew/pot.rb.in` remains the single
installable cask template; repository license terms apply to generated casks.

```text
validated source SHA -> final macOS sealing/smoke -> immutable Release assets
                                                        |
                                             download + verify final ZIP
                                                        |
                                same-repository homebrew/pot-vVERSION branch
                                                        |
                                    reviewed PR -> Casks/pot.rb on main
                                                        |
                                                brew install / upgrade
```

The separate `homebrew` job runs only after macOS publication succeeds. It requests
`contents: write` for a new branch and `pull-requests: write` for its PR, with no
new PAT. It creates a branch from the current default branch and changes only
`Casks/pot.rb`. It never moves a release tag. Its source implementation remains
pinned to the validated release SHA; the distribution pointer commit necessarily
comes later. First installation becomes available **after the cask PR is merged**.

Retries reuse an identical existing branch/open PR. A different branch, closed PR,
equal/newer conflicting cask or unexpected changes stop for manual review; no
existing branch is rewritten and no automatic merge/retry loop occurs. An older
release cannot downgrade the default-branch cask. Protected branch policies still
apply when reviewing/merging the PR.

A cask synchronization error does not undo or relabel successful asset publication.
The job logs the actual error and emits a warning and Actions summary with a
recovery command. If repository settings prohibit Actions PR creation, the branch
may exist without a PR; a maintainer can open its PR manually. To retry after
resolving permissions or reviewing the branch/PR, use an authorized `gh` session
from the released source revision (substitute its exact version and SHA):

```sh
GITHUB_REPOSITORY=jjyr/pot python3 scripts/sync-homebrew-cask 1.2.3 VALIDATED_40_CHARACTER_SHA
```

This recovery command only reads published assets and proposes the cask PR; it
never publishes assets. It fails visibly if branch creation or PR permission is
missing. A maintainer must explicitly allow Actions to create PRs in repository
Actions settings for automatic PR creation, or open the generated branch's PR
manually. No workflow changes repository settings or substitutes personal
credentials for its Actions token.

For local artifact validation before publication, the existing generator remains:

```sh
mise exec -- python scripts/generate-cask 1.2.3 /absolute/artifacts
mise exec -- python scripts/generate-cask 1.2.3 /absolute/artifacts --verify-url
```

These commands create internal `pot.rb` metadata, not a Release asset or user
installation step. Homebrew preserves quarantine; an unnotarized App can still
need the [App-specific first-launch allowance](desktop-installation.md#macos).
Never disable Gatekeeper globally.
