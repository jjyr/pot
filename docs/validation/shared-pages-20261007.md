# Shared Tasks/Sandbox/Usage validation — 2026-10-07

## Baseline and delivered scope

Baseline: commit `eb60b9f151d342a7b0b95ab03ad6a147b0ca5f00`, after shared chat
extraction. Its App binary was built and saved before this page migration.
Both builds used the same isolated `com.jjyr.pot.ui-check` bundle/root, window,
language, appearance, synthetic transcript and API data. No production profile,
credential, model call or VM was used.

`pot-ui` now owns the native-derived Tasks list, Sandbox environment page and
complete Usage presentation (including native conversation statistics, tool
output and allowance form). App and WASM call these renderers. Shared page
primitives also serve native Settings/Schedules; those whole pages, navigation,
Swift composer, desktop WebView and authenticated transports remain adapters.
Browser Sandbox and Usage keep the existing read-only route policy; shared
components do not authorize desktop control or allowance writes.

## Native appearance and interaction

Raw screenshots are 2160 × 1586 RGB pixels. Pillow compared the same-sized
unmodified images without alignment, scaling, retouching or masking. “Body” is
`(112, 176, 2160, 1586)`, excluding native navigation and header chrome.

| Scene | Full changed pixels | Body changed pixels | Body difference bounds |
| --- | ---: | ---: | --- |
| Tasks default | 1355 | 0 | None |
| Tasks next/long title | 1557 | 1246 | `(1840, 1488, 1888, 1528)` — cursor over Next |
| Usage statistics/chart | 2303 | 0 | None |
| Allowance form | 1478 | 793 | `(1792, 248, 1840, 280)` — cursor/click effect from opening limits |
| Sandbox default | 2067 | 0 | None |
| Sandbox other environment | 1692 | 1039 | `(320, 536, 360, 577)` — cursor/click effect over environment row |

All changed regions were inspected. Native Chat/Tasks/Sandbox/Usage controls,
labels, geometry and styles remained consistent in these fixtures. Task paging
retained the long-title row and truncation. Selecting another Sandbox changed
viewing selection without changing the execution-default label. This is
representative fixture parity, not a guarantee for every streaming state, window
size, locale, OS version or physical display.

| Scene | Before | After |
| --- | --- | --- |
| Tasks | [Before](https://github.com/jjyr/pot/blob/2c4ca8a16abdc2192f6da6ea7670a0460e5f46af/output/playwright/shared-pages/pot-pages-tasks-before.png) | [After](https://github.com/jjyr/pot/blob/2c4ca8a16abdc2192f6da6ea7670a0460e5f46af/output/playwright/shared-pages/pot-pages-tasks-after.png) |
| Long title | [Before](https://github.com/jjyr/pot/blob/2c4ca8a16abdc2192f6da6ea7670a0460e5f46af/output/playwright/shared-pages/pot-pages-task-long-before.png) | [After](https://github.com/jjyr/pot/blob/2c4ca8a16abdc2192f6da6ea7670a0460e5f46af/output/playwright/shared-pages/pot-pages-task-long-after.png) |
| Usage | [Before](https://github.com/jjyr/pot/blob/2c4ca8a16abdc2192f6da6ea7670a0460e5f46af/output/playwright/shared-pages/pot-pages-usage-before.png) | [After](https://github.com/jjyr/pot/blob/2c4ca8a16abdc2192f6da6ea7670a0460e5f46af/output/playwright/shared-pages/pot-pages-usage-after.png) |
| Allowance | [Before](https://github.com/jjyr/pot/blob/2c4ca8a16abdc2192f6da6ea7670a0460e5f46af/output/playwright/shared-pages/pot-pages-quota-before.png) | [After](https://github.com/jjyr/pot/blob/2c4ca8a16abdc2192f6da6ea7670a0460e5f46af/output/playwright/shared-pages/pot-pages-quota-after.png) |
| Sandbox | [Before](https://github.com/jjyr/pot/blob/2c4ca8a16abdc2192f6da6ea7670a0460e5f46af/output/playwright/shared-pages/pot-pages-sandbox-before.png) | [After](https://github.com/jjyr/pot/blob/2c4ca8a16abdc2192f6da6ea7670a0460e5f46af/output/playwright/shared-pages/pot-pages-sandbox-after.png) |
| Other environment | [Before](https://github.com/jjyr/pot/blob/2c4ca8a16abdc2192f6da6ea7670a0460e5f46af/output/playwright/shared-pages/pot-pages-sandbox-other-before.png) | [After](https://github.com/jjyr/pot/blob/2c4ca8a16abdc2192f6da6ea7670a0460e5f46af/output/playwright/shared-pages/pot-pages-sandbox-other-after.png) |

Actual native allowance input/save controls were exercised. Entering `0` and
immediately saving displayed an error and generated no policy HTTP request.
Entering `1.5M` and immediately saving produced exactly one policy request with
`limit: 1500000`. The save action reads the live editor at click time, so a value
from an earlier render cannot be submitted. Tests caught and repaired parameter
formatting, English singular/plural selection, decimal precision, relative-unit
spacing and stale render-time input capture before acceptance.

Evidence: [invalid allowance](https://github.com/jjyr/pot/blob/2c4ca8a16abdc2192f6da6ea7670a0460e5f46af/output/playwright/shared-pages/pot-pages-quota-invalid.png),
[valid allowance](https://github.com/jjyr/pot/blob/2c4ca8a16abdc2192f6da6ea7670a0460e5f46af/output/playwright/shared-pages/pot-pages-quota-valid.png).

## Browser integration

Real Chromium loaded the release GPUI WASM at 390 × 844 and 360 × 780 using
loopback synthetic data and simulated authentication. This fixture does not
qualify actual Passkey/TLS/push security.

- Actual shared Next/filter controls used the server's next offset (`1`) and
  reset offset to `0` when changing to attention tasks.
- Actual GPUI keyboard input and Search submitted the typed query, rendering the
  shared empty-result state. Clicking Clear search restored the rows and placeholder.
- Actual Sandbox navigation and environment-row clicks changed the viewing
  selection, retained the execution default, and made no browser POST requests.
- More → Usage used the shared totals/model cards and chart. The actual Last 30
  days button fetched `days=30`; scrolling revealed chart and quota statistics.
- Existing language controls, dark/reduced-motion media preferences and 360 px
  layout were exercised. Model/task names remain literal; labels are localized.
  No horizontal DOM overflow was observed. Canvas content was visually inspected
  as well; DOM width alone does not establish canvas layout correctness.
- The fixture was restarted during checks, causing expected fetch failures;
  successful recovery clears the transient connection error while preserving
  the last good page. No production endpoint was contacted.

Evidence: [Tasks](https://github.com/jjyr/pot/blob/2c4ca8a16abdc2192f6da6ea7670a0460e5f46af/output/playwright/shared-pages/pot-pages-mobile-tasks.png),
[search](https://github.com/jjyr/pot/blob/2c4ca8a16abdc2192f6da6ea7670a0460e5f46af/output/playwright/shared-pages/pot-pages-mobile-task-search.png),
[Sandbox](https://github.com/jjyr/pot/blob/2c4ca8a16abdc2192f6da6ea7670a0460e5f46af/output/playwright/shared-pages/pot-pages-mobile-sandbox.png),
[another environment](https://github.com/jjyr/pot/blob/2c4ca8a16abdc2192f6da6ea7670a0460e5f46af/output/playwright/shared-pages/pot-pages-mobile-sandbox-other.png),
[Usage](https://github.com/jjyr/pot/blob/2c4ca8a16abdc2192f6da6ea7670a0460e5f46af/output/playwright/shared-pages/pot-pages-mobile-usage.png),
[chart](https://github.com/jjyr/pot/blob/2c4ca8a16abdc2192f6da6ea7670a0460e5f46af/output/playwright/shared-pages/pot-pages-mobile-usage-chart.png),
[360 px Chinese/dark Tasks](https://github.com/jjyr/pot/blob/2c4ca8a16abdc2192f6da6ea7670a0460e5f46af/output/playwright/shared-pages/pot-pages-mobile-dark.png).

## Automated checks

- 8 shared portable tests passed: chat receipts/drafts, authoritative task-page
  transitions, Sandbox viewing/default separation, allowance bounds, independent
  locales and native-compatible parameter/plural/decimal formatting.
- 16 App regression tests passed. The previous seventeenth allowance parser test
  moved into `pot-ui`; it was retained, not removed from coverage.
- Workspace Clippy, formatting/script lint and 713 translation keys passed.
- Release WASM build and `aarch64-unknown-linux-musl` shared-layer check passed.
- Push hooks run required Rust/backend regression checks before publishing the
  commit; the Swift Helper build is verified separately. This report's UI
  acceptance is independent of model/VM execution.

## Repeatable fixture and limits

`scripts/tests/shared-ui-fixture.py --pages` supplies deterministic Tasks,
Sandbox and Usage responses on loopback. It refuses to replace a non-fixture
Runtime endpoint. Use only a disposable root and isolated App bundle:

```sh
mise exec -- python scripts/tests/shared-ui-fixture.py \
  --root .local/ui-check --port 17903 --mobile-assets crates/web-ui/dist --pages
```

For native Sandbox inventory, copy `scripts/tests/shared-ui-helper.py` as
`Contents/Helpers/PotHelper` in that isolated test bundle and make it executable.
The stub validates the fixture-owned root, refuses an existing socket, supports
only stopped status and removes its socket on exit. It never starts a VM.
The bundle wrapper must set `POT_LOCAL_ROOT` to the disposable absolute root.
Do not copy the stub into the production bundle.

Physical phone installation, IME, accessibility, performance, real vendor push,
live desktop/RFB interaction and exhaustive state/layout qualification remain
open as recorded in the mobile acceptance report. No merge or deployment is
included in these results.

The screenshot artifacts were removed from the working tree during the 2026-10-09 repository layout migration. Evidence links above point to the historical Git revision.
