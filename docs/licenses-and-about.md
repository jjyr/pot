# About and dependency notices

Pot's own source is MIT licensed. `LICENSE` is the source of truth for its
copyright and terms. Third-party dependencies retain their own terms.

## Application information

The macOS **Pot → About Pot** menu opens the standard AppKit panel. Its name,
version and copyright come from the packaged `Info.plist` and `LICENSE`.
**Settings → General → About** shows the product version, source revision, Go
version and expandable plain-text dependency notices. Browser clients use the
same read-only `/api/v1/about` projection for their connected Control.

`scripts/build-go-app` and `scripts/build-go-cli` set
`internal/buildinfo.Version` and `Revision` through Go linker flags. The App
version is `POT_APP_VERSION` (default `0.1.0`); its revision is the Git commit,
with a dirty suffix when tracked source changes exist. `POT_BUILD_REVISION` can
supply explicit build provenance. Unpackaged Go builds identify themselves as
development builds. `pot --version` uses the same metadata.

The default macOS bundle is independently identified as
`.local/go/PotGoPreview.app` / `dev.pot.go-preview`. A local release build uses
`.local/go/Pot.app` / `dev.pot.app`. Building a bundle does not open or replace an
installed App. The release task creates local ZIP/checksum/cask artifacts;
publication is a separate workflow.

## Packaged host notices

App resources and portable CLI archives include these original plain-text files:

| File | Coverage |
|---|---|
| `LICENSE.txt` | Pot's own source |
| `GoDependencyLicenses.txt` | Go modules linked by the packaged entry points and the Go runtime |
| `SwiftDependencyLicenses.txt` | Native macOS VM Swift Helper dependency notices; macOS packages only |
| `FrontendFontLicense.txt` | IBM Plex Sans under the SIL Open Font License |
| `FrontendIconLicense.txt` | Retained Lucide/Feather icons under ISC/MIT terms |
| `ModelDirectoryLicense.txt` | The models.dev catalog seed and provider artwork |
| `node/LICENSE` | The pinned Node.js distribution and its bundled dependencies |
| `novnc/LICENSE.txt` | The pinned noVNC distribution |

`scripts/go-notices` walks the actual Go package dependency graph using
`go list -mod=readonly -deps`, collects module-root license/notice files, and
includes the Go runtime license. A dependency without a notice fails packaging.
Cross-compiled archives collect this graph with their target GOOS/GOARCH and
CGO settings, rather than inheriting the packaging host's dependency selection.
The App inventory includes Wails; portable headless CLI inventories omit GUI
packages. The linked `tailscale.com/tsnet` implementation and its dependencies
are included in this Go inventory; there is no separately built libtailscale
adapter or C shared-library notice set.

Run `mise run licenses` to generate the CLI/runtime inventory locally. App
packaging regenerates it for the native entry point as well. These are notice
inventories, not a Cargo/cargo-about SPDX allowlist check or a claim that every
distributed non-Go file is covered by the Go graph.

The narrowly pinned Wails macOS tray adaptations in [patches](../patches/README.md)
preserve the tray artwork's aspect ratio and expose the status accessibility
label. Packaging applies them only to a disposable module copy using a temporary
module file. Wails' original MIT notice and a description of the local changes
are included in the App's Go dependency notices.

## About API boundary

The product information service reads only the fixed notice filenames above
from the packaged resource directory. It rejects linked notice files, invalid
UTF-8, files larger than 8 MiB and combined notice text larger than 16 MiB. The
frontend renders notice text with text nodes, without executing HTML from a
license file. `/api/v1/about` is read-only and available through the browser API
allowlist; it does not provide arbitrary file access.

Source-only previews can lack prepared notices. They report that the complete
notice set is unavailable; they do not substitute the font license for the
linked dependencies. Packaged builds include all cross-platform notice files,
and macOS packages additionally include the Swift Helper notices.

## Retained and guest assets

JS plugins, built-in Skills, Python guest brokers and the native macOS VM Swift Helper remain in
their existing runtimes. Their own source headers and bundled license files
remain with the distributed assets. Brand artwork is generated from the retained
`resources/app/PotIcon.swift` master; the Wails tray atlases use the same drawing.
`mise run icons` regenerates native, frontend and light/dark tray artwork.

Linux Sandbox images are separate resources. Their pinned recipes and package
versions live under `resources/sandbox/linux/default/`; the image's OS packages
and corresponding source obligations are separate from the host Go inventory.
A resource release must retain the upstream package notices and corresponding
source required by its pinned packages. The host About list does not claim to
inventory all software installed inside a guest image. macOS guest media and VM
disks remain owner-supplied and are not distributed by Pot.
