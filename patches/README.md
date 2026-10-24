# Wails macOS tray adaptations

`wails-v3.0.0-beta.30-tray-aspect.patch` changes Wails' macOS status image sizing
to preserve the source image aspect ratio. Pot's tray artwork can contain an
unread badge and is wider than its height. Wails beta.30 forces both dimensions
to the status bar height, which distorts that artwork.

`wails-v3.0.0-beta.30-tray-accessibility.patch` maps the macOS `SetTooltip`
value to the status button accessibility label, without adding a hover tooltip.
This exposes Pot’s working/unread state to assistive technology and native UI
automation, matching the retained App behavior.

Upstream source: [Wails v3.0.0-beta.30](https://github.com/wailsapp/wails/blob/v3.0.0-beta.30/v3/pkg/application/systemtray_darwin.m).
Wails is copyright 2018–Present Lea Anthony and distributed under the MIT
License. The original notice is included with the linked Go dependency notices.

`scripts/build-pot-app` verifies the pinned version, original file hash and exact
patch context, copies the module into its disposable build directory, and uses a
temporary Go module file to compile that copy. The module cache and repository
Go module files are never changed. Packaged notices identify this modification.
All macOS App builds use this script, including the default build/release tasks.

Review and remove this patch when an upgraded Wails release preserves image
aspect ratio. A version or upstream source change deliberately fails preparation
until the patch is reviewed again.
