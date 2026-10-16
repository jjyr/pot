# Development guide

## Toolchains and normal workflow

`mise.toml` pins the stable Go toolchain (1.27.2), Node (24.21.0), Python and source
check tools. macOS App/Helper development requires Apple Silicon, macOS 26+ and
selected Xcode/Swift 6.2+. Headless Go packages build independently on Linux and
must not import Wails or platform desktop libraries.

```sh
mise install
mise run setup
mise run prepare
mise run check
mise run test
mise run build
mise run open
```

`prepare` supplies retained Node, plugin, Skill, viewer and license assets.
`build`/`bundle` create `.local/go/PotGoPreview.app`, an independent development
bundle with ID `dev.pot.go-preview`. Release packaging uses the production bundle
identity. Product App starts real Go services; disposable fixtures are separate:

```sh
mise run preview
# macOS fixture UI without product data or model/tool calls:
mise exec -- go run ./cmd/pot-app -preview
```

The browser fixture binds loopback on port 17702 by default. It does not test
production browser authentication, provider transport or Sandbox isolation.

## Builds and packaging

| Command | Output / purpose |
|---|---|
| `mise run build:go` | Portable CLI and executor under `.local/go/bin` |
| `mise run build:linux` | CGO-free Linux amd64/arm64 entry points |
| `mise run build:cli` | Target CLI/runtime archive with retained resources |
| `mise run build` | Complete Wails macOS bundle and signed native macOS VM Swift Helper |
| `mise run build:web-ui` | Embedded shared HTML/CSS/JS frontend |
| `mise run build:web` | Localized retained viewer/Sandbox pages |
| `mise run build:sandbox-image` | Podman Linux OCI image, independent of App packaging |
| `mise run export:sandbox-resources` | Deterministic export validation against the embedded catalog |
| `mise run release -- VERSION` | Local release archive/checksum/cask; publishing is separate |

`scripts/build-go-app`, `scripts/build-go-cli` and `scripts/go_packaging.py` own
asset layout and pinned Node checksums. Packaging includes plugin/Skill sources,
viewer resources, component notices and the correct target executor. It neither
builds a Sandbox image implicitly nor replaces an existing running service.
Image build architectures, explicit macOS connections and reviewed resource export
commands are documented below in [Sandbox image resources](#sandbox-image-resources).

The native macOS VM Helper builds through `helper/Package.swift` and retains
Virtualization.framework ownership. Linux containers use the Go Podman provider
on both hosts. JavaScript plugins and Node remain their own runtime; Python guest broker,
browser and desktop support remain Python. Go hosts product logic; tool
compositions run through a process-isolated goja worker. Heap/process and
turn-lifecycle limits belong at that worker boundary; goja itself supplies no
per-runtime hard heap limit.

Reusable libraries live in `pkg/`; product entry points and private implementation
live in `cmd/` and `internal/`. `mise run test:public-api` copies a consumer into an
independent Go module outside the checkout, builds the standalone composition
worker, and exercises only public imports. It is included in `mise run test`.
See [Go libraries](go-libraries.md) for the supported embedding boundaries.

Standalone startup and browser serving use [Deployment](deployment.md). CLI and
App default to the new Go root, not previous product data. Use explicit private
temporary roots for integration checks; never run two hosts against one root.

## Sandbox image resources

Install pinned tools and a working Podman builder. `build:sandbox-image` invokes
`scripts/build-go-container-image`. macOS requires an explicitly prepared
build connection; the script never starts/selects a default machine:

```sh
mise exec -- python scripts/build-go-container-image --arch arm64 --connection BUILD_CONNECTION
```

On native Linux, omit `--connection` and select `--arch amd64` or `arm64`.
Output is `.local/go/images/linux-ARCH/{sandbox-image,sandbox-image.json}`.
Transferable contexts use `--prepare-only --context FRESH_DIRECTORY`, followed
by `--prepared-context DIRECTORY --arch ARCH --output DIRECTORY` on the builder.

Export the reviewed OCI layout and update that architecture's catalog explicitly:

```sh
mise exec -- python scripts/export-go-sandbox-resources \
  --source .local/go/images/linux-arm64 --arch arm64 \
  --tag sandbox-go-resources-v0.3.0 --update-catalog
```

Retain exact exports under `.local/go/releases/resources`, review/commit the
catalog, then build clients. Keep image-only bundles needed by pinned instances;
unchanged digests retain their release tag. Omit `--update-catalog` to verify.
Archive export is deterministic for identical OCI inputs; a clean image rebuild
can change installation metadata/timestamps. Do not reuse filenames for new bytes.

Publication workflow `.github/workflows/sandbox-resources.yml` requires the
absolute reviewed export directory as `prepared_assets` and its committed tag.
It never rebuilds or changes the catalog. Local staging uses the same verifier:

```sh
mise exec -- python scripts/verify-go-sandbox-resources \
  --source ABSOLUTE_EXPORT_DIRECTORY --output ABSOLUTE_FRESH_DIRECTORY \
  --tag sandbox-go-resources-v0.3.0
```

The verifier copies/hashes only catalog files belonging to that tag, rejects
unsafe names/symlinks/existing output and fails on missing/changed files. The
workflow uploads that explicit list and never overwrites a release. Clients
embed no OCI layers. Release accessibility must match the download audience;
catalog publication does not make a private repository anonymously accessible.

For offline provisioning, stop the Runtime and run
`pot resources import -root ROOT -directory RELEASE_ASSETS_DIRECTORY`. Embedded
size/hash/extraction checks still apply. It adds no model-controlled import port.

## Checks

```sh
mise run lint
mise run check:go
mise run test:go
mise run test:ui
mise run test:eval
mise run test:packaging
mise run check:swift
mise run licenses
```

`check:go` runs formatting, vet and architectural import checks. `test:go` uses
the race detector. Browser/DOM tests cover shared state, real production adapters,
owner boundaries, drafts, IME, navigation and streamed Task output. Deterministic
Eval runs the production Agent with native fixtures and real local tool effects;
see [Eval CLI](../eval/README.md). Paid DeepSeek calls occur only through explicit
`live`/live-suite commands, never ordinary checks.

Run `mise run test:ui-http-integration` explicitly for real browser HTTP/SSE,
reconnect, session boundaries and binary download checks. It requires cached
`@playwright/cli` and its installed browser. It uses isolated temporary roots and
a deterministic loopback provider, installs nothing, makes no external model
calls and cleans up its own processes. It is separate from ordinary tests.

Sandbox Provider unit tests use lifecycle doubles and explicit local executors;
these do not establish VM/container isolation. Actual Provider qualification needs
the intended machine, installed image/resources and dedicated private workspace.
Report execution evidence and limitations in the owning issue/task response,
without repository validation reports or development checklists.

`hk.pkl` defines repository hooks. Pre-commit checks changed sources and staged
whitespace. Pre-push groups Go/product tests, macOS Helper checks, scripts and
licenses. Use pinned tools through `mise run` or `mise exec -- …`; do not bypass a
failed gate by skipping hooks. CI workflows live in `.github/workflows/` and use
separate portable, macOS and resource/release responsibilities.

## Source layout

| Directory | Ownership |
|---|---|
| `cmd/pot`, `cmd/pot-app`, `cmd/pot-tool-executor`, `cmd/pot-eval` | Product entry points |
| `internal/` | Independently testable Go components; see [boundaries](backend-components.md) |
| `web/` | Shared product HTML/CSS/JS and embedded assets |
| `helper/` | Retained Swift macOS virtualization Helpers |
| `resources/app/` | Native brand assets, vector master and locales |
| `resources/sandbox/` | Guest image recipes and retained Python/JS services |
| `resources/models-dev/`, `resources/model-directory.json` | Public model metadata/notices |
| `plugins/`, `skills/` | Existing JavaScript plugin and Skill bundles |
| `desktop/` | Retained desktop viewer resources |
| `eval/` | Scenarios, experiment scripts and Eval CLI documentation |
| `scripts/` | Build, release, checks and operational tooling |

Keep documentation focused on lasting contracts and operating instructions.
Record implementation progress and qualification results in the owning issue.
The documentation describes the current implementation and operating contracts.

## UI and retained assets

Main, tray and browser share DOM components, fonts/tokens, themes and localization;
platform adapters supply native dialogs, window operations and browser sessions.
See [shared UI](shared-ui.md) and [design language](design-language.md).

`scripts/localize-web` builds retained desktop/Sandbox HTML. Update source
templates under `desktop/` and `resources/sandbox/`, and translations under
`resources/app/locales/`; generated viewer output is not the product UI source.
`scripts/make-icons.swift` uses `resources/app/PotIcon.swift` as the vector master.
Dependency notices are generated by `scripts/go-notices` and retained runtime
notice preparation; see [licenses](licenses-and-about.md).
