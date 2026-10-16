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

`resources/app/PotIcon.swift` is the single character geometry master.
`scripts/make-icons.swift` links it to
export the App ICNS, PNG sizes, README image, Home Screen images, menu-bar PNGs and
Sandbox wallpaper. `scripts/make-tray-atlas.swift` exports its light/dark activity
and badge frames for the Wails menu bar; shared navigation uses the exported PNG.
Run `mise run icons` to regenerate everything.

Home Screen images have an opaque square background and inset artwork for OS masks.
Mac images have a rounded ivory tile and transparent outer corners. Menu-bar PNGs
are black alpha templates at 22/44 pixels, without a tile. The native tray selects
the matching atlas appearance, preserving unread and failure semantics.
The README uses the 384-pixel image at 128 CSS pixels.

Web login, favicon, notifications, manifest and embedded navigation use these assets.
Icon URLs use `pot-klee-2` to invalidate the previous identity while preserving the
manifest app ID. Existing installed Home Screen icons may require an OS refresh.

## Motion

Opening/revealing, hover, press and successful-completion expressions keep their
existing timings. Execution adds a gentle steam indicator; waiting narrows the single
eye. Idle remains static. Reduce Motion and hidden-window behavior remain unchanged.
See the [design language](../../design-language.md#motion) for motion rules and
[activity/unread behavior](../../design-language.md#menu-bar-pot-activity-and-unread-indicator)
for state semantics.

## Reference generation

Built-in image generation edited the selected Klee concept into an App/menu-bar board:
retain the ochre open vessel, one eye, continuous nose/smile and two feet; use an ivory
macOS tile for the App and a simplified monochrome alpha silhouette for the menu bar;
show light/dark treatments and small-size previews. Production geometry is drawn in
Swift rather than extracted from the raster board.
