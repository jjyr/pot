# Releases and installation channels

CI builds and publishes each delivery channel independently after shared product
checks pass. Docker, Linux server and the experimental Linux desktop do not depend
on macOS signing. Each channel requires its own build, smoke, checksum and
applicable signing gates. Windows is currently unsupported.

Source builds remain the installation path until public artifacts exist. The
public `jjyr/homebrew-tap` repository exists, but does not currently contain a Pot
cask. Generating `pot.rb` does not make `brew install --cask jjyr/tap/pot` available.
These workflows never modify the tap or registry visibility.

| Channel | Versioned distribution | Publication boundary |
|---|---|---|
| Linux server, amd64/arm64 | `pot-go-VERSION-linux-ARCH.tar.gz` | Both native CLI builds and extracted-package smoke; GitHub Release assets |
| Linux desktop (Experimental), amd64 | `pot-desktop_VERSION_linux_amd64.deb` | Native GTK/WebKit build and real WebView smoke; GitHub Release assets; manual upgrades |
| macOS 26+, Apple Silicon | `Pot-VERSION-arm64.zip`, `pot-go-VERSION-darwin-arm64.tar.gz` | Native App/CLI smoke and explicit `unsigned` (ad-hoc) or `signed` (Developer ID + notarization) mode; GitHub assets, cask and signed update feed |
| Container, Linux amd64/arm64 | `pot-container-VERSION.oci.tar`, `ghcr.io/jjyr/pot:VERSION` | Native image build/smoke on both architectures, OCI integrity and metadata verification; GHCR |
| Homebrew | Generated `pot.rb` macOS release asset | Local cask installation after a release exists; public tap inclusion is a separate reviewed change |

The experimental designation applies only to the new Linux native desktop `.deb`.
Its amd64 build and native WebView smoke have been exercised locally; native
arm64, Wayland and actual apt installation have not been fully validated. Desktop
upgrades replace the complete package manually. Linux server CLI/Web UI and Docker
are separate channels.

See [Desktop installation](desktop-installation.md) and [Docker](docker.md) for
platform requirements, installation boundaries and source-build instructions.

## Triggers and source selection

Only tag pushes start CI automatically. Every tag runs checks, including Unicode
names and tags outside the release pattern. Only exact `vMAJOR.MINOR.PATCH` tags
without leading zeroes select publication. Other tags build validation artifacts
with version `0.0.0`; they do not push images or create releases. Branch pushes,
PR events and Release events do not start CI.

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
              shared Linux/macOS product checks
                         |
       +-----------------+----------------+------------------+
       |                 |                |                  |
  Linux CLI builds  Linux desktop    macOS App + CLI    native Docker builds
   + native smoke   + native smoke    + native smoke     + smoke amd64/arm64
       |                 |                |                  |
       |                 |       ad-hoc OR Developer ID  multiarch OCI export
       |                 |           + notary
       |                 |       signed feed + cask     digest/arch/version/SHA
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

Every archive has a standard `NAME.sha256` sidecar. Each channel owns a distinct
manifest: `SHA256SUMS-server`, `SHA256SUMS-desktop`, `SHA256SUMS-macos` or
`SHA256SUMS-container`. One channel cannot replace another channel's checksum
manifest. The unsigned aggregate validation bundle uses `SHA256SUMS`. Artifacts
are retained for seven days. `POT_APP_VERSION` and `POT_BUILD_REVISION` set matching
version/revision metadata across all builds. Versioned CLI filenames retain the
internal installation directory `pot-go-PLATFORM`.

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
`pot.rb`, `update.json` and `SHA256SUMS-macos`; the manifest binds the disclosure,
cask and signed feed as well as archives. Release notes direct users to this file
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

## Homebrew generation

During the unsigned release phase, the website and README offer macOS installation
through Homebrew only. The release still publishes the App ZIP, SHA256 files,
`pot.rb` and Ed25519-signed update feed; hiding a download entry does not change
packaging or publication. After a matching macOS release exists, select it in
[Releases](https://github.com/jjyr/pot/releases), read `MACOS-RELEASE.txt`, download
its `pot.rb` and run `brew install --cask ./pot.rb` in that directory. Inspect the
cask before running it; it references the versioned official App ZIP and its
SHA-256. See the [first-launch guide](desktop-installation.md#macos) for quarantine
handling. No matching published release means this installation is not yet
available.

For a future version with verified Developer ID signing and notarization, switch
the documented release mode to `signed`, then restore the macOS App link in
`website/templates/page.html` and all six catalogs, regenerate the site and add
the README download entry. Update these installation guides with that policy.
The website uses static content; it does not read release settings or secrets.
Never replace an earlier version's unsigned assets with signed bytes.

Cask generation verifies the archive's SHA-256 sidecar and embedded bundle
version, then fills the fixed versioned GitHub URL and digest. Both modes preserve
Gatekeeper quarantine handling and never writes to the remote tap.

```sh
mise exec -- python scripts/generate-cask 1.2.3 /absolute/artifacts
# After that macOS version is publicly available, also verify the URL's bytes:
mise exec -- python scripts/generate-cask 1.2.3 /absolute/artifacts --verify-url
brew install --cask /absolute/artifacts/pot.rb
```

Public tap inclusion is a separate authorized, reviewed change copying the cask
to `Casks/pot.rb`. Homebrew can install an unnotarized App, but does not guarantee
that Gatekeeper will allow its first launch; follow the
[application-specific instructions](desktop-installation.md#macos). No Homebrew tap token is consumed by these workflows.
