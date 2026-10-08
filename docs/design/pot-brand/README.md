# Pot companion brand exploration

## Selected visual direction

On 2026-10-07, the user selected concept D (Pot Companion) for retention and requested exploration of subtle animation for the left navigation icon.

Reference: [D concept board](d-pot-companion-concept.png). This is a raster concept reference, not a production icon asset. The board was generated with the built-in image generation tool.

Keep the warm terracotta pot, rounded body, small side handles, two simple eyes and restrained smile. Use a dimensional ceramic treatment for the application icon and simplify the same character for the compact navigation and monochrome menu-bar icons.

## Implemented navigation motion

- Opening the window: one short settling/appearance transition, optionally with one blink.
- Hover or keyboard focus: a small expression or highlight change; pressing gives restrained tactile feedback.
- Executing work: slow steam movement, with a stable pot body. Execution follows actual main-chat or background-task state.
- Work ends: steam fades; a brief contented expression may mark completion.
- Idle: static.

Respect Reduce Motion with static expressions and execution indicators. Stop navigation animation when the window is hidden. Preserve readable state without animation. Existing error and unread accounting remain authoritative.

The user authorized implementation on 2026-10-07. The left navigation uses a native vector interpretation of the character, with the above motion. After rebasing onto `origin/main` fa3483b, the application bundle, Mobile login/header, Home Screen icons and README also use this character. The editable application/Home Screen vector master lives in `scripts/make-icons.swift`; the compact animated character lives in `app/swift/PotIcon.swift`. Home Screen assets have an opaque cream background with no pre-applied corner mask. See the [UI specification](../../ui-ux-design.md) for exact timing and state semantics.

The README displays `app/assets/Pot384.png` at 128 × 128 CSS pixels to preserve
detail on 2x/3x displays without loading the 1024 px application master. Regenerate
it with `swift scripts/make-icons.swift --readme-only`, or alongside the other
brand assets by omitting the flag.

## Validation (2026-10-07)

- `mise exec -- cargo build --locked -p pot-app -j 4`: passed.
- `mise run test:app`: all 17 tests passed.
- Existing native tray-icon checks: static execution states, independent unread count, fixed width and `9+` cap passed.
- Native CompanionButton window exercised idle, hover, cooking, successful-completion and hidden-window transitions; rendered at actual navigation size.
- A separately packaged App preview displayed the new character and successfully navigated Chat → Tasks → Chat. No Helper, Sandbox or model calls were used.
- Swift formatting and Git whitespace checks passed. The Reduce Motion branch was reviewed, but the user's system preference was not changed for this check.

After the rebase onto `origin/main` fa3483b:

- App/shared UI/mobile portable tests: 24 tests passed (16 App, 8 shared UI).
- `mise run build:mobile`: passed, including the GPUI/WASM brand image.
- App and Server build with `pot-server/embedded-web`: passed, including generated mobile assets.
- Browser fixture displayed the new icon in the running GPUI header.
- Touch/Home Screen PNGs decode at 1024/192/512 px with fully opaque background corners; the touch icon exactly matches its master. The generated Mac ICNS decodes successfully.
- Physical iOS/Android installation and existing installed-icon refresh were not tested.

Brand cleanup on 2026-10-07:

- The menu bar now renders the shared Companion geometry in monochrome, with negative-space facial features. Cooking steam, waiting expression, errors and unread badges retain their state semantics.
- The Sandbox wallpaper uses the same character as a subdued terracotta watermark.
- The unused old `PotTray.png` and its generator were removed. Packaging deletes an obsolete copy from existing bundles.
- Native tray checks passed and light/dark renderings were inspected. The wallpaper was rendered with macOS Quick Look; the editable icon generator still reproduces the existing application/Home Screen assets.

- App tests: 16 passed. Full App/Helper/Mobile build and Sandbox image rebundle passed; bundle signing verification passed. Packaged ICNS, mobile touch icon and Sandbox wallpaper match their sources; the App executable UUID matches the new build. The unused tray asset and welcome page are absent. A new interactive Sandbox session was not exercised.

## Generation prompt

Use case: logo-brand. Create a polished landscape concept board for "pot", a personal AI assistant Mac app performing background work. Warm almost-white background, generous whitespace. Display a flat symbol with lowercase pot wordmark, a macOS rounded-square app icon, and miniature idle/working menu-bar symbols. Title: "D / POT COMPANION". Design a warm little pot assistant with rounded terracotta body, tiny side handles, open rim, two dark dot eyes and a small smile; no limbs, chef hat or utensils. App icon: cream rounded square with a gently dimensional terracotta ceramic pot and soft warm light. Menu-bar version: simplified black pot with two negative-space eyes, adding small steam curves while working. Palette: terracotta #CB704E, cream #FFF5E9, charcoal #303030. Maintain the same character across applications. Exact additional text: "pot", "LOGO", "APP ICON", "MENU BAR", "IDLE", "WORKING". No marketing copy or watermarks.
