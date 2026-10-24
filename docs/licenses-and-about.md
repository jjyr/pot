# About and dependency notices

Pot's own source is MIT licensed. `LICENSE` is the source of truth for its
copyright and terms. Third-party dependencies retain their own terms.

## Application information

The macOS **Pot → About Pot** menu opens the standard AppKit panel. Its name,
version and copyright come from the packaged `Info.plist` and `LICENSE`.
**Settings → General → About** shows the product version, source revision and Go
version. Copyright notices stay hidden until the user opens **Copyright notices**;
a scrollable modal contains the font license link and expandable plain-text
dependency notices. The modal closes with **Close** or Escape. Browser clients use
the same read-only `/api/v1/about` projection for their connected Control.

`scripts/build-pot-app` and `scripts/build-pot-cli` set
`internal/buildinfo.Version` and `Revision` through Go linker flags. The App
version is `POT_APP_VERSION` (default `0.1.0`); its revision is the Git commit,
with a dirty suffix when tracked source changes exist. `POT_BUILD_REVISION` can
supply explicit build provenance. Unpackaged Go builds identify themselves as
development builds. `pot --version` uses the same metadata.

The default macOS bundle is independently identified as
`.local/pot/PotPreview.app` / `dev.pot.go-preview` (retained for OS/WebView preference compatibility). A local release build uses
`.local/pot/Pot.app` / `dev.pot.app`. Building a bundle does not open or replace an
installed App. The release task creates local ZIP/checksum/cask artifacts;
publication is a separate workflow.

## Packaged host notices

App resources and portable CLI archives include these original plain-text files:

| File | Coverage |
|---|---|
| `LICENSE.txt` | Pot's own source |
| `GoDependencyLicenses.txt` | Go modules linked by the packaged entry points and the Go runtime |
| `FrontendFontLicense.txt` | IBM Plex Sans under the SIL Open Font License |
| `FrontendIconLicense.txt` | Retained Lucide/Feather and GitHub Octicons under ISC/MIT terms |
| `FrontendDiagramLicense.txt` | Pele and its bundled Lucide zoom icon under MIT/ISC terms |
| `FrontendVirtualizerLicense.txt` | TanStack Virtual Core under MIT terms |
| `ModelDirectoryLicense.txt` | The models.dev catalog seed and Lobe Icons provider artwork |
| `node/LICENSE` | The pinned Node.js distribution and its bundled dependencies |

`scripts/go-notices` walks the actual Go package dependency graph using
`go list -mod=readonly -deps`, collects module-root license/notice files, and
includes the Go runtime license. A dependency without a notice fails packaging.
Cross-compiled archives collect this graph with their target GOOS/GOARCH and
CGO settings, rather than inheriting the packaging host's dependency selection.
The App inventory includes Wails; portable headless CLI inventories omit GUI
packages.

Run `mise run licenses` to generate the CLI/runtime inventory locally. App
packaging regenerates it for the native entry point as well. These are notice
inventories; distributed non-Go files have separate notices and are not covered
by the Go dependency graph. `scripts/go-notices` also appends the embedded
MCP JavaScript runtime notices from `pkg/mcp/assets/NOTICE.txt`, generated from
the pinned bundle inputs.

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
and all supported platforms include the same runtime notices.

## Asset notices

Node tool plugins, built-in Skills and frontend assets retain their own notices.
Brand icons derive from `resources/app/PotIcon.swift`; tray atlases use the same
artwork.

SRT and its npm dependencies are pinned by `sandbox/package-lock.json`. Packaging includes their notices under `sandbox/THIRD-PARTY-NOTICES.txt`, exposed by About alongside Node and Go notices. The checked SDK extension is maintained in `sandbox/patch-sdk.mjs`.
