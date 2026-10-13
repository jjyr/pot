# Pot Companion identity

## Selected direction

On 2026-10-13, the user approved the Klee-inspired companion and requested replacement
across the product. The [approved App/menu-bar concept](klee-app-menu-concept.png)
was generated with the built-in image generation tool. It is a visual reference;
production assets are an editable native vector interpretation.

Keep the ochre asymmetric body, tilted open rim, one eye, curling smile and two
short feet. The App tile is warm ivory. Compact monochrome artwork uses transparent
cutouts and a simplified nose/smile without the tight curl. There are no side handles.

## Source and exports

`crates/app/swift/PotIcon.swift` is the single character geometry master. The native
navigation and menu bar render it directly; `scripts/make-icons.swift` links it to
export the App ICNS, PNG sizes, README image, Home Screen images, menu-bar PNGs and
Sandbox wallpaper. Run `mise run icons` to regenerate everything.

Home Screen images have an opaque square background and inset artwork for OS masks.
Mac images have a rounded ivory tile and transparent outer corners. Menu-bar PNGs
are black alpha templates at 22/44 pixels, without a tile. Native AppKit templates
adapt to appearance; unread and failure overlays retain their existing semantics.
The README uses the 384-pixel image at 128 CSS pixels.

Web login, favicon, notifications, manifest and embedded navigation use these assets.
Icon URLs use `pot-klee-2` to invalidate the previous identity while preserving the
manifest app ID. Existing installed Home Screen icons may require an OS refresh.

## Motion

Opening/revealing, hover, press and successful-completion expressions keep their
existing timings. Execution adds a gentle steam indicator; waiting narrows the single
eye. Idle remains static. Reduce Motion and hidden-window behavior remain unchanged.
See the [UI specification](../../ui-ux-design.md) for exact state semantics.

## Reference generation

Built-in image generation edited the selected Klee concept into an App/menu-bar board:
retain the ochre open vessel, one eye, continuous nose/smile and two feet; use an ivory
macOS tile for the App and a simplified monochrome alpha silhouette for the menu bar;
show light/dark treatments and small-size previews. Production geometry is drawn in
Swift rather than extracted from the raster board.

## Validation (2026-10-13)

- `mise run icons`: generated all PNGs, the ICNS and wallpaper successfully.
- `cargo build --locked -p pot-app -j 4`: passed through the pinned mise toolchain.
- `cargo test --locked -p pot-app -j 4`: all 29 tests passed.
- Existing native tray tests passed: distinct idle/execution rendering, independent
  unread indicator, fixed width and `9+` cap. Light/dark output was visually inspected.
- A separately signed preview App using isolated data launched successfully; the
  actual navigation showed the new ochre companion at its native size.
- Swift formatting and Git whitespace checks passed.
- This validates source/assets and native App rendering. A full release bundle,
  installed mobile-icon refresh and a running Sandbox wallpaper were not exercised.
