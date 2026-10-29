# Releases and installation channels

Release preparation validates all supported channels before a formal tag exists.
Publication promotes the exact prepared bytes to GitHub Releases and GHCR without
rebuilding or signing again. Each channel retains its build, smoke, checksum and
applicable signing gates. Windows is currently unsupported.

An independent Homebrew workflow proposes a cask and Formula update PR after successful publication;
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
| Homebrew | Root `Casks/pot.rb` and `Formula/pot-cli.rb` in `jjyr/pot` | Published App ZIP and macOS CLI tar verified before a same-repository package update PR; merge makes installation/upgrades available |

The experimental designation applies only to the new Linux native desktop `.deb`.
Its amd64 build and native WebView smoke have been exercised locally; native
arm64, Wayland and actual apt installation have not been fully validated. Desktop
upgrades replace the complete package manually. Linux server CLI/Web UI and Docker
are separate channels.

See [Desktop installation](desktop-installation.md) and [Docker](docker.md) for
platform requirements, installation boundaries and source-build instructions.

## Release procedure

Use an authenticated `gh` session and the repository's pinned mise tools:

```sh
mise run release:prepare -- 0.0.9 --wait
# Optionally select a commit already merged into the remote default branch:
mise run release:prepare -- 0.0.9 --ref FULL_COMMIT_SHA --wait
mise run release:status -- PREPARE_RUN_ID
mise run release:publish -- PREPARE_RUN_ID --wait
```

Preparation defaults to remote `main`, resolves it once and pins the source SHA.
It does not upload local files or include uncommitted changes. Complete and merge
source changes before preparing a release. The workflow must also run from the
repository's default branch; a PR or arbitrary branch cannot request signing
credentials through this entry point.

`release:prepare` runs full product checks, builds all distributions, performs
native smoke checks and final macOS sealing, and generates the signed update feed.
A successful preparation saves the final packages and a release manifest in
Actions Artifacts. It creates no release tag, public GitHub Release or GHCR image.
The summary identifies the version, source SHA, preparation run/attempt and
artifact expiry. Intermediate build artifacts are retained for seven days; final
release artifacts and the manifest request 30 days, subject to repository limits.

`release:publish` selects the current successful preparation attempt's manifest artifact ID and
passes that immutable identity with the run ID to the publication workflow. It
checks trusted workflow provenance, source ancestry, completion, artifact expiry,
archive digests and every final file before creating an annotated `vVERSION` tag.
The tag binds the source and preparation identity. Publication uses the prepared
packages, signed feed and OCI image without rebuilding, repacking or resigning.

Both commands print the exact run ID and Actions URL. Run identification waits up
to 120 seconds; `--wait` then polls every ten seconds for up to two hours and
returns a nonzero status on failure or timeout. A local timeout or interruption
does not cancel the remote run. Omitting `--wait` returns after dispatch and run
identification. Each dispatch has a unique request ID, so another concurrent run
cannot be mistaken for this request. An uncertain dispatch is not automatically
repeated. Find the original run by its exact reported request marker and inspect
it with `release:status`; do not redispatch merely because the local call timed out.

`release:status` accepts preparation, publication and Homebrew run IDs. It reports
the current attempt's jobs and steps, candidate version
and source SHA, pinned artifact IDs and the earliest actual artifact expiry. For
an already completed publication, expired or missing preparation artifacts are
reported as unavailable evidence; they do not change the recorded Actions result.
A successful Homebrew proposal run does not establish that its PR was merged.
The publication status links the independent Homebrew workflow; inspect that run
and its PR separately.

Publication verifies public downloads with the trusted update key. A separate
`Release Homebrew` workflow then proposes a Homebrew update PR. Review that PR and merge it after its checks pass. Public
artifacts and Homebrew are separate completion states: a Homebrew failure must
remain visible even when downloads are already available. Report the release URL,
actual verification scope and outstanding distribution work. Publishing does not
upgrade installed machines.

`mise run release -- VERSION` remains local packaging only. The standalone
`mise run verify:release -- VERSION` checks an already public version; see below
for its trusted-key requirement.

## Workflows and source selection

`Tests` runs lightweight Linux checks and targeted macOS process-lifecycle checks
for PRs and pushes to `main`. `CI` is a
read-only manual/reusable complete product and distribution validator. Neither a
tag push nor its manual inputs authorize publication. The former `publish=true`
entry point is removed.

```sh
gh workflow run ci.yml --ref main -f pr=123
gh workflow run ci.yml --ref main -f ref=refs/heads/my-branch
```

The optional `ref` and `pr` inputs are mutually exclusive. Ordinary validation
may select reviewed PR code, receives no signing secrets, and always runs the
full Linux/macOS normal/static and race matrix plus distribution gates. Explicit
source selections do not use shared build caches. See
[Development](development.md#ci-and-manual-pr-checks) for coverage boundaries.

`Release prepare` reuses that validation pipeline for a fixed merged source and
explicit version. Tool and credential checks happen before expensive builds;
Homebrew policy is reported as allowed, disabled or unknown. Reading repository
Actions policy can require administrative access, so unknown is not proof of
permission and does not block package preparation. The final macOS sealing job
explicitly installs its own Go/Python smoke dependencies. Final sealing
and feed signing happen only after the unprivileged validation gates succeed.
`Release publish` runs trusted workflow code, verifies the selected preparation
and consumes its data artifacts. Downloaded artifacts never supply executable
publication scripts.

```text
merged source SHA + intended version
                  |
       full product + native build/smoke gates
                  |
       final macOS sealing + signed update feed
                  |
       complete final bundle + immutable manifest
                  |  preparation successful; no tag yet
                  v
       verify preparation identity and all bytes
                  |
       create annotated vVERSION bound to preparation
                  |
       GitHub assets + GHCR (same prepared bytes)
                  |
       public verification -> Homebrew PR review/merge
```

The manifest records the version, source SHA, workflow/run/attempt identity,
macOS mode, final bundle artifact ID/digest and file names, sizes and SHA-256
values. A manifest cannot contain its own upload ID; the publishing request pins
that separate artifact ID, which is checked through GitHub's artifact metadata.

## Failure and recovery

- A failed preparation creates no tag. Fix and merge source changes, then prepare
  the same unpublished version again. Do not promote a partially successful run.
- Publish retries use the same manifest and final bundle. Missing published files
  can be added; existing bytes must match exactly. No tag is moved and no conflicting
  asset or container digest is overwritten.
- A repeated prepare run has a distinct attempt/artifact identity. It cannot
  replace the preparation already bound to a release tag, even at the same SHA.
  To recover an earlier publication after preparation was rerun, rerun the original
  publish task with its pinned inputs. Calling `release:publish` again with only
  the prepare run ID selects its current attempt, not the earlier candidate.
- Missing or expired candidate artifacts stop publication. Before any tag exists,
  prepare again. After publication starts, recover the original verified artifacts
  or release a new version; do not silently rebuild the existing version.
- GitHub and GHCR publication is not atomic. Inspect the failed stage and existing
  public state, then rerun publication with its original pinned inputs. Successful
  channels are retained and verified on retry.
- A Homebrew PR failure does not invalidate published bytes or change the
  publication workflow result. Inspect the separate `Release Homebrew` run, open
  the already generated branch's PR, or use the recovery command below.

Release transport retries only transient read/download failures, with a bounded
attempt count and delay. Authentication/permission errors and content or signature
mismatches remain failures. A download retry replaces partial bytes and must pass
the same archive size/digest checks. Errors include operation and sanitized HTTP
or transport details without credentials or signed download URLs.

Release creation submits one request and binds subsequent operations to the
returned release ID. If the response is lost, bounded reads reconcile the remote
state instead of blindly issuing another creation request. Ambiguous matching
releases, a different source, or different asset bytes stop recovery.

## Public verification after publication

Maintainers can check a published version on demand with one read-only command:

```sh
POT_UPDATE_PUBLIC_KEY=TRUSTED_BASE64_PUBLIC_KEY mise run verify:release -- \
  1.2.3 --output .local/verify-1.2.3 --markdown
```

`python scripts/verify-release` accepts the same arguments. `--public-key` can
replace `POT_UPDATE_PUBLIC_KEY`. Obtain that pinned Ed25519 public key from trusted
release configuration or an already trusted installation; a key in the feed is
never a trust root. No source SHA, macOS mode, private signing key or GitHub token
is required.

The script checks that the requested stable GitHub Release exists publicly and
its tag matches the version. It downloads `update.json`, reuses the existing
`sign-release verify` Ed25519 verifier with the supplied key, and checks the signed
payload's version. Only after the signature passes does it anonymously download
each file listed in that payload and compare its actual size and SHA-256 with the
signed values. Duplicate downloads and URLs outside this repository's selected
versioned release path are rejected. A valid signature alone does not establish
that the downloadable bytes match the feed.

Dependencies are Python 3.12+ and the project Go toolchain from `mise.toml` for the
existing signature helper, plus HTTPS access to GitHub's API/download/CDN hosts.
There is no native cross-build, C compiler, SDK, Docker or package-analysis
dependency. Package bytes are streamed and discarded; only reports are saved.
The script installs no tools, uses no login session and never extracts or executes
a downloaded package.

The scope is the public version, trusted feed signature and its listed download
bytes. It does not inspect package internals, build source/schema/resource paths,
OCI images, Homebrew pointers or other assets outside the feed. It does not
qualify Apple Developer ID signing or notarization; Pot's Ed25519 signature does
not make an ad-hoc macOS package Apple-approved. Existing release build, smoke and
signing gates remain responsible for their qualifications.

Downloads use at most three concurrent requests. The terminal reports completed
checks and files. `report.json` is saved atomically as checks/downloads complete;
`--markdown` also writes `report.md` on completion. Reports include the explicit
scope, successful download receipts, failures and pending work. Exit codes are
`0` for the selected checks passing, `1` for a signature/version/byte mismatch or
other check failure, and `2` for an unavailable public resource or exhausted
transient HTTP retries. Signature and byte conflicts are never retried. Only
explicit rate limits and transient 502/503/504 responses receive at most three
attempts, with delays capped at 30 seconds.

The publish workflow runs this verifier after uploading the prepared artifacts.
Run the command independently when needed. Normal lightweight checks use local
deterministic fixtures without downloading public release artifacts. The verifier
never publishes, replaces assets, changes a tag or proposes/merges a PR.

## Docker publication contract

Preparation builds and qualifies the production Docker image. Publication creates
the source tag and, after the `release` environment allows it, copies the prepared
OCI image to `ghcr.io/jjyr/pot:VERSION`. The only registry tag is `VERSION`: the
workflow does not move `latest` or minor-version aliases. Ordinary validation and
release preparation build and smoke-test images but never push them.

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
Release after all preparation gates. GitHub asset writers are serialized;
an interrupted publication may leave some channels publicly available. Existing assets must have the same byte size and GitHub SHA-256
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
manifest. The unsigned aggregate validation bundle uses `SHA256SUMS` and is
not the final publishable bundle. Intermediate artifacts are retained for seven
days; final preparation artifacts request 30 days. `POT_APP_VERSION` and
`POT_BUILD_REVISION` set matching version/revision metadata across all builds.
Versioned CLI filenames retain the internal installation directory `pot-PLATFORM`.

Channel publication is intentionally independent, not a transaction across GitHub
and GHCR. An environment refusal, registry error or platform failure may leave
other successful channels publicly available. Inspect actual assets and channel
job results before retrying. Missing update keys or the selected mode's signing
credentials stop release preparation before any formal tag is created.

## Environment, permissions and signing setup

Configure the GitHub environment `release` to allow the default branch `main`,
where both preparation and publication workflows execute. Replace the old `v*`
tag deployment rule with an exact `main` branch rule before using this flow;
otherwise old workflow revisions on tags can still request that environment.
A workflow cannot change the policy itself. Retain any required reviewers and
other existing environment protections. Do not allow arbitrary source branches
merely to get a candidate through the gate.

Signing jobs use the `release` environment and read-only repository access.
Unprivileged product checks and ordinary validation do not receive signing secrets.
Publication needs `contents: write` for the bound tag and GitHub assets,
`packages: write` for GHCR, and `actions: read` to verify preparation artifacts.
Homebrew needs `contents: write` and `pull-requests: write`. Repository Actions
settings must permit PR creation for automatic Homebrew proposals; otherwise use
the maintainer recovery path. No command changes repository settings or visibility.

Ordinary macOS validation exercises ad-hoc sealing and native smoke without Apple
credentials. Release preparation subsequently applies the explicitly selected
release mode and smoke-tests those final bytes. GHCR must permit this repository's
Actions token to publish `ghcr.io/jjyr/pot`; an existing package may require
repository access to be granted. No extra registry secret is required.

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
does not install this workflow; the code must be merged before preparing a release.

Runner availability, pinned toolchains, package repositories, upstream downloads
and standard Actions minutes/billing remain execution prerequisites. A job refused
before startup does not establish a code failure. Paid model tests are not enabled.

## Homebrew generation

The website and README expose macOS through Homebrew only. Release assets retain
App ZIP/CLI archives, their SHA-256 sidecars, `MACOS-RELEASE.txt`,
`SHA256SUMS-macos` and the Ed25519-signed update feed. `pot.rb` and `pot-cli.rb` are generated metadata, not uploaded assets.
Both `unsigned` and `signed` modes use the same tap path and final-byte checks.

```sh
brew tap jjyr/pot https://github.com/jjyr/pot.git
brew install --cask jjyr/pot/pot
brew install jjyr/pot/pot-cli
# App updates itself; explicitly opt into cask replacement if desired:
brew upgrade --cask --greedy jjyr/pot/pot
# Stop Formula hosts first, then let Homebrew upgrade the CLI:
brew upgrade jjyr/pot/pot-cli
```

The initial tap must include the explicit URL: the repository is `pot`, not
`homebrew-pot`. Homebrew supports an arbitrary Git URL with its two-argument tap
command; see [Homebrew Taps](https://docs.brew.sh/Taps). Casks live in root
`Casks/`; the independent CLI Formula lives in `Formula/`. No separate tap repository is used. Private repository/assets require
GitHub access; normal public installation requires the repository and artifacts
to be public. These workflows never change visibility.

`Casks/pot.rb` and `Formula/pot-cli.rb` point to published, fixed-version assets.
`scripts/sync-homebrew` downloads the final App ZIP, macOS CLI tar and checksum
sidecars using `github.token`, checks GitHub's SHA-256 digests and byte sizes,
and calls shared `generate_cask` / `generate_formula` validation. Both versions
and layouts must match. Sealing and notarization happen before publication.
The templates are `packaging/homebrew/pot.rb.in` and `pot-cli.rb.in`; neither uses
`latest` or an unchecked checksum. The Formula reuses the existing CLI archive,
with no duplicate release asset. Its current macOS 26 minimum reflects the
published CLI Mach-O, rather than a dependency on the App's GUI frameworks.

The App cask keeps `auto_updates true` for the shared signed in-app updater.
Homebrew normally skips these casks during upgrade; `--greedy` explicitly opts
in to cask replacement. The Formula is Homebrew-managed: its launcher execs
`libexec/bin/pot`, and installation removes `libexec/Resources/update.json` to
prevent internal replacement of the Cellar tree. See [CLI installation](desktop-installation.md#macos-cli).

```text
validated source SHA -> final macOS sealing/smoke -> immutable Release assets
                                                        |
                                             download + verify final ZIP + CLI tar
                                                        |
                                same-repository homebrew/pot-vVERSION branch
                                                        |
                                    reviewed PR -> Casks/pot.rb + Formula/pot-cli.rb
                                                        |
                                                brew install / upgrade
```

The independent `Release Homebrew` workflow runs after successful `Release publish`.
It also supports manual recovery on the default branch for an already published
version and its exact source SHA. Automatic invocation validates the originating
publication before deriving the version/source; it never executes code from an
untrusted event branch. It requests
`contents: write` for a new branch and `pull-requests: write` for its PR, with no
new PAT. It creates a branch from the current default branch and changes only
`Casks/pot.rb` and `Formula/pot-cli.rb`. It never moves a release tag. The publication workflow uses trusted scripts and the pinned release source SHA;
the distribution pointer commit necessarily comes later. First installation becomes available **after the Homebrew PR is merged**.

Retries reuse an identical existing branch/open PR. A different branch, closed PR,
equal/newer conflicting package or unexpected changes stop for manual review; no
existing branch is rewritten and no automatic merge/retry loop occurs. An older
release cannot downgrade the default-branch packages. Protected branch policies still
apply when reviewing/merging the PR.

A Homebrew synchronization error does not undo or relabel successful asset publication.
The independent workflow retains its failed result and recovery guidance in its
Actions summary. If repository settings prohibit Actions PR creation, the branch
may exist without a PR; a maintainer can open its PR manually. To retry after
resolving permissions or reviewing the branch/PR, use an authorized `gh` session
using the trusted default-branch tools (substitute the published version and its
exact source SHA):

```sh
mise run release:homebrew -- 1.2.3 --ref VALIDATED_40_CHARACTER_SHA --wait
# Or use an authenticated maintainer session directly:
GITHUB_REPOSITORY=jjyr/pot python3 scripts/sync-homebrew 1.2.3 VALIDATED_40_CHARACTER_SHA
```

Explicit version/SHA recovery validates the annotated release tag and published
assets without requiring unexpired preparation artifacts. The automatic path
also verifies the publication's pinned candidate while its artifacts remain available.

These recovery commands only read published assets and propose the Homebrew PR; they
never publish assets. They fail visibly if branch creation or PR permission is
missing. A maintainer must explicitly allow Actions to create PRs in repository
Actions settings for automatic PR creation, or open the generated branch's PR
manually. No workflow changes repository settings or substitutes personal
credentials for its Actions token.

For local artifact validation before publication, the existing generator remains:

```sh
mise exec -- python scripts/generate-cask 1.2.3 /absolute/artifacts
mise exec -- python scripts/generate-cask 1.2.3 /absolute/artifacts --verify-url
mise exec -- python scripts/generate-formula 1.2.3 /absolute/artifacts
```

These commands create internal `pot.rb` and `pot-cli.rb` metadata, not a Release asset or user
installation step. Homebrew preserves quarantine; an unnotarized App can still
need the [App-specific first-launch allowance](desktop-installation.md#macos).
Never disable Gatekeeper globally.
