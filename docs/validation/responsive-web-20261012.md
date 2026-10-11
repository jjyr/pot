# Responsive Web UI — 2026-10-12

## Delivered

Web now switches between a 176px sidebar at widths of at least 900 CSS pixels
and bottom navigation on narrower windows. Usage and Schedules are direct sidebar
entries and remain under More on narrow screens. The active destination is marked.
Chat content and its composer share an 820px maximum column. Tasks, Machines and
Usage receive the current layout density instead of fixed mobile flags. Compact
task metadata stacks instead of clipping; task titles use the actual content width.
Task detail provides a return-to-tasks action preserving the current list query.

GPUI fully visible bounds position the shell within safe areas and the visual
viewport. The composer shrinks at short visible heights. Navigation and composer
remain outside scrolling content. Browser login retains its own safe-area padding;
the canvas does not add a second copy of GPUI's safe-area insets.

This change covers responsive presentation, not the proposed larger extraction of
shared client routing/network state or App/Web settings feature parity.

## Checks

- Release WASM build through `mise run build:mobile`.
- Portable shared UI tests: 13 passed.
- Clippy for pot-ui and pot-app, all targets, locked dependencies, warnings denied.
- Python fixture lint and staged whitespace/format checks.
- Real headed Chromium loaded the built WASM with isolated synthetic data from
  `scripts/tests/shared-ui-fixture.py`; no model, production data or credentials.
- Desktop 1200/1280px, phone 390px and narrow 320px, landscape 844x390, and the
  899/900px navigation boundary were exercised.
- Actual canvas clicks opened Tasks, task detail, returned to Tasks, opened Machines,
  More and Usage. Input typed before resizing remained after desktop/phone changes
  and navigating away and back. Dark and reduced-motion preferences were exercised.
- A simulated VisualViewport height of 420px kept input and actions within that
  visible rectangle. This was a browser geometry simulation, not a physical keyboard.
- Screenshots were inspected for canvas clipping as well as checking document width.
  A narrow task-metadata clipping issue was found and repaired during acceptance.

Local screenshots are under `output/playwright/responsive-*.png` (ignored artifacts).

## Limits

Chromium's headed DPR=1 emulation on this Retina host reported a physical canvas
box at twice the emulated CSS width, yielding half-size drawing. Acceptance used
an explicit DPR=2 browser context matching the host. This does not qualify all
browser/device pixel-ratio combinations.

No physical iPhone/Android, Safari, real mobile IME, screen-reader, notification,
or remote authentication acceptance was performed. Native App compiled through
Clippy; its window/input behavior was not visually requalified in this change.
