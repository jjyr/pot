# About and license policy

Pot's own source is MIT licensed; `LICENSE` and the workspace Cargo metadata are
the source of truth. Third-party dependencies retain their own terms.

## Application information

The first item in the macOS **Pot** menu is **About Pot** (Chinese: **关于 Pot**).
It opens AppKit's standard About panel with the application version, developer
name, copyright, GitHub project link, MIT license, and bundled Rust dependency
license report. Displayed developer names omit email addresses.

The build generates native metadata from Cargo's package version, authors, and
repository, and the copyright line in `LICENSE`. Bundling writes
`NSHumanReadableCopyright` to `Info.plist`, includes `LICENSE.txt`, and generates
`RustDependencyLicenses.html`. Bundle identifiers are unchanged.

## Rust dependency checks

`mise.toml` pins cargo-about 0.9.2. Install and run:

```sh
mise install github:EmbarkStudios/cargo-about
mise run licenses
mise run test:licenses
```

`about.toml` defines approved SPDX terms and targets: Apple Silicon macOS,
ARM64 Linux musl (Sandbox executor), and x86_64 Linux GNU (backend CI).
Build and transitive dependencies are included; development-only dependencies
are excluded. The order prefers MIT when dependencies offer MIT or Apache-2.0.
GPL/AGPL/LGPL-only and unknown licenses fail; dual licensing can satisfy the
policy through an approved alternative. The fail flag also rejects unresolved
license evidence. Do not lower the detection threshold or ignore errors to pass.

Three unchanged crates have explicit, narrowly scoped MPL-2.0 acceptance:

- `cbindgen` 0.28.0: build-time binding generator.
- `option-ext` 0.2.0: dependency of `dirs-sys`, used by directory discovery.
- `ece` 2.4.2: runtime dependency of `web-push` 0.11.0 for RFC 8291 message
  encryption. Reviewed during the mobile/standalone branch integration: the
  locked registry crate declares MPL-2.0 and includes its license; Pot links it
  without modifying its covered source. Preserve its MPL notice and the
  versioned source download link in the generated dependency report. This is a
  crate-specific exception, not approval of other MPL dependencies.

MPL imposes obligations on its covered files rather than requiring Pot's own
source to switch to MPL. Preserve license notices and access to the corresponding
source. The generated report includes license texts and links to versioned crate
source downloads. If these crates are modified, ship the modified covered source
under MPL. New MPL dependencies require a separate review; MPL is not globally
accepted. These name-based exceptions must be reviewed when their versions change.

## Enforcement and report

The CI license job scans the locked dependency graph and tests positive MIT,
negative GPL, and unknown-license fixtures. `scripts/pot bundle` runs the same
check before packaging, so unresolved or disallowed terms stop bundle creation.
The generated HTML is at `.local/licenses/rust-dependencies.html`; it is rebuilt
from Cargo.lock rather than checked into Git. HTML escapes dependency metadata
and license text, uses no remote scripts, and includes project/source links.

Initial cargo-about testing rejected cbindgen and option-ext; the scoped policy then
generated the report successfully without SPDX overrides or broad copyleft
acceptance. This checks declared terms against an explicit policy; it does not
prove legal compatibility of every combination or audit all distributed files.
Swift packages, bundled Node/npm, Sandbox OS packages, fonts, icons, and other
non-Cargo assets require separate inventories. The About link and report label
explicitly identify the current coverage as Rust dependencies.

## Embedded Tailscale notices

`tailscale/` vendors libtailscale at a fixed commit (BSD-3-Clause), with a pinned
Go version/module graph. `scripts/build-tailscale` collects license/notice files
for the actual linked modules and the Go runtime, failing when a module has no
notice. App and CLI archives include `TailscaleDependencyLicenses.txt`. This
inventory is separate from the Cargo-only report. Pot's adapter does not modify
the vendored upstream files; its source is in `tailscale/pot.go`.
