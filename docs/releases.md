# Releases and installation channels

CI builds and publishes each implemented delivery channel independently after
shared product checks pass. A macOS signing failure or unavailable Windows port
does not block Docker, Linux server or Linux desktop publication. A channel's own
failed build, smoke, checksum or signing gate prevents that channel from publishing.
A green validation run covers the implemented targets only; it does not establish
Windows support.

Source builds remain the installation path until public artifacts exist. The
public `jjyr/homebrew-tap` repository exists, but does not currently contain a Pot
cask. Generating `pot.rb` does not make `brew install --cask jjyr/tap/pot` available.
These workflows never modify the tap or registry visibility.

| Channel | Versioned distribution | Publication boundary |
|---|---|---|
| Linux server, amd64/arm64 | `pot-go-VERSION-linux-ARCH.tar.gz` | Both native CLI builds and extracted-package smoke; GitHub Release assets |
| Linux desktop, amd64 | `pot-desktop_VERSION_linux_amd64.deb` | Native GTK/WebKit build and real WebView smoke; GitHub Release assets; manual upgrades |
| macOS 26+, Apple Silicon | `Pot-VERSION-arm64.zip`, `pot-go-VERSION-darwin-arm64.tar.gz` | Native App/CLI smoke, Developer ID signing and notarization; GitHub assets, cask and signed update feed |
| Container, Linux amd64/arm64 | `pot-container-VERSION.oci.tar`, `ghcr.io/jjyr/pot:VERSION` | Native image build/smoke on both architectures, OCI integrity and metadata verification; GHCR |
| Homebrew | Generated `pot.rb` macOS release asset | Local cask installation after a release exists; public tap inclusion is a separate reviewed change |
| Windows desktop | Unavailable | No native runtime/package/smoke target is enabled; does not block other channels |

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
       |                 |       Developer ID + notary   multiarch OCI export
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
and Windows desktop is unavailable. The publisher also handles identical asset
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
job results before retrying. Missing signing credentials affect only macOS; a
missing Windows port is accurately documented and has no placeholder job.

## Environment, permissions and signing setup

Configure the GitHub environment `release` with required reviewers and appropriate
tag/deployment restrictions before publishing tags. Only official publication
calls can request writes. Linux/macOS publishers use `contents: write`; the GHCR
publisher has `contents: read` and `packages: write`. Checks and packaging receive
no signing secrets. GHCR must permit this repository's Actions token to publish
`ghcr.io/jjyr/pot`; an existing package may require repository access to be granted.
No extra registry secret is required for the configured GHCR path.

macOS requires its own configuration before its channel can publish:

| Configuration | Kind | Purpose |
|---|---|---|
| `POT_MACOS_RELEASE_ENABLED=true` | Release variable | Confirms macOS signing/notary setup is ready |
| `POT_UPDATE_PUBLIC_KEY` | Repository variable | Base64 Ed25519 key pinned consistently in executables and images |
| `POT_UPDATE_PRIVATE_KEY` | Release secret | Matching PKCS8 PEM for feed signing |
| `MACOS_CERTIFICATE_P12` | Release secret | Base64 Developer ID Application certificate/private key |
| `MACOS_CERTIFICATE_PASSWORD` | Release secret | Certificate import password |
| `MACOS_SIGNING_IDENTITY` | Release variable | Full `Developer ID Application: ...` identity |
| `APPLE_ID`, `APPLE_APP_PASSWORD` | Release secrets | Notary authentication |
| `APPLE_TEAM_ID` | Release variable | Apple developer team |

The public update key is useful to all builds, but the private key and Apple
credentials are used only by the macOS release workflow. Preflight rejects missing
configuration or a mismatched Ed25519 pair. A temporary keychain signs bundled
Mach-O files before the App seal. The workflow requires notarization acceptance,
validates/staples the App ticket and smoke-tests the signed packages. The cask and
feed checksums are regenerated after signing. No signature is fabricated or
silently omitted from a published macOS package.

The signed feed retains its existing JSON payload/envelope format and lists only
the macOS App and macOS/Linux CLI installations supported by the updater. It is
published when the macOS signed-update channel succeeds; Linux releases remain
manually installable if that feed is not yet available. Linux desktop and container
upgrades are explicit operations. See [Updates](updates.md) for trust bootstrap
and storage-format restrictions.

Windows needs an actual runtime port, native Windows package/smoke gate and
WebView2 qualification before a release target is enabled. The unenabled
`sign-windows-release.ps1` helper describes PFX-based Authenticode signing with
`WINDOWS_CERTIFICATE_PFX` and `WINDOWS_CERTIFICATE_PASSWORD`; it is not evidence
of a working Windows package or runner validation. Hardware-backed signing needs
a separately reviewed adapter.

Runner availability, pinned toolchains, package repositories, upstream downloads
and standard Actions minutes/billing remain execution prerequisites. A job refused
before startup does not establish a code failure. Paid model tests are not enabled.

## Homebrew generation

Cask generation verifies the archive's SHA-256 sidecar and embedded bundle
version, then fills the fixed versioned GitHub URL and digest. It preserves
Gatekeeper quarantine handling and never writes to the remote tap.

```sh
mise exec -- python scripts/generate-cask 1.2.3 /absolute/artifacts
# After that macOS version is publicly available, also verify the URL's bytes:
mise exec -- python scripts/generate-cask 1.2.3 /absolute/artifacts --verify-url
brew install --cask /absolute/artifacts/pot.rb
```

Public tap inclusion is a separate authorized, reviewed change copying the cask
to `Casks/pot.rb`. No Homebrew tap token is consumed by these workflows.
