# Internationalization

Pot supports English (`en`) and Simplified Chinese (`zh-CN`). Language is a local application preference, separate from Agent configuration, task state, and scheduling time zones.

## Preference and startup

`crates/app/swift/Localization.swift` reads the saved `language` UserDefaults key before any UI is constructed. If it is missing or invalid, it scans `Locale.preferredLanguages` in order, matching an exact supported tag first and then a supported language base. With the current catalogs, Chinese tags (including Traditional Chinese) resolve to `zh-CN`. No supported match resolves to English. The resolved choice is saved immediately; there is no persistent follow-system mode.

Settings → General starts with Language, above Appearance. Its menu is generated from the catalog files and their `language.name` values, with the current language checked. Selecting one saves it immediately and refreshes Rust views, Kit components, native menus/tooltips/accessibility names/composer labels, and the embedded desktop. Changing macOS language later does not override the saved choice.

The App stores its preference in Swift UserDefaults; Web uses browser local storage.
Web's synchronous `preferences.js` runs before the stylesheet and application
module. It reads `pot-locale` and `pot-appearance` (0 System, 1 Light, 2 Dark),
applies the resolved theme, document language and browser theme color, and tolerates
unavailable storage. The catalog loader validates the locale against discovered
languages before showing authentication text or starting the shared UI. Resolved
preferences are passed to WASM before `run`, so theme and locale are installed
before Shell and input entities are created, independently of backend data loading.
Live preference and system-appearance changes update presentation immediately;
they do not wait for API refreshes or reset editor drafts.

Each adapter exposes `settings.locale` to the same `pot-ui` shell, which updates
its presentation catalog and Kit controls through `gpui_kit::component::set_locale`.
Startup uses the saved preference before creating input entities. Language changes
update input placeholders without resetting values, focus, configuration revisions,
secrets, navigation, scroll positions, or chat drafts. They never restart the
backend or desktop.

## Translation resources

`crates/app/locales/*.json` are the source of truth. Builds discover the supported languages from these files; each catalog supplies its own `language.name` for the picker. Stable semantic keys separate text from code. English is the fallback.

- Rust static labels use `i18n::text`. All locale catalogues are cached once for controls requiring static references.
- `crates/ui/build.rs` generates checked Rust formatting macros for shared presentation from `localization-format-keys.json`; `crates/app/build.rs` does the same for native client notices. All generated language branches use Rust `format!`, so argument counts/types remain compiler checked. `localization-plurals.json` identifies count arguments and `.one` translations for English singular forms. Parameters are evaluated once.
- The App build also generates the Swift dictionary. `L.text` and `L.format` translate native services. Numbered native parameters are substituted in one pass; braces inside credentials, identifiers, diagnostics, or user text are literal. `LocalizedMessage` captures a notice's key/arguments so saved results follow live language changes.
- `scripts/localize-web` validates catalogs and builds `desktop/index.template.html` and `crates/sandbox-images/linux/default/index.template.html` into `index.html`, the shared `desktop/i18n.js`, and page-specific `locales.json`. Templates retain English fallback labels and translation keys; dictionaries contain only keys used by that page. Generated assets live in `.local/web/{desktop,sandbox}`, never in tracked HTML. `--check` validates sources without modifying them.
- Runtime's Cargo build generates and embeds its desktop resources in `OUT_DIR`, so both App and CLI receive fresh assets even with a direct Cargo build. The desktop page and its relative resource routes share ticket authorization and no-store headers. Python 3 is required by this generation step.
- App preparation, Apple VM image construction and Podman image construction copy the generated Sandbox assets. The broker serves the external resources next to the page and validates locale writes against their supported languages. Rebuild the image to distribute changes.
- Native Swift resources, shared Rust catalogs, the shared language picker and browser catalog generation all discover the same locale files. Browser matching uses exact tags before language-base fallback. Translation loading completes before desktop initialization; a native language selection made during loading is preserved. Live updates leave the noVNC connection and clipboard value intact.

To add a language, add a complete `crates/app/locales/<BCP-47-tag>.json` with the same keys/placeholders and its own `language.name`, then rebuild. Existing count templates support `.one` and the default form; languages needing additional plural categories require extending that contract. Web Push's built-in generic notification text currently remains English/Chinese.

Use complete sentence templates instead of concatenating translated fragments. Add translations for every supported catalog and keep their placeholders identical. Brand/model/protocol names and units such as GiB retain their standard spelling. Dates currently use the existing numeric year-month-day/time/zone representation; language selection never changes scheduling zones or persisted timestamps. Token entry still uses decimal K/M.

## Backend notices and content boundaries

Backend state values, request payloads, and stored transcripts remain locale independent. The existing backend has legacy prose messages; `localization-message-keys.json` maps recognized application notices at their presentation boundary. Unknown/provider diagnostics remain verbatim. This adapter does not translate arbitrary chat/model/tool content. Future backend notices should expose structured codes and parameters instead of adding new prose matching.

Task-report display translates the generated title/status wrapper only, preserving the user title and model body. Both existing Chinese task-ID markers and English markers are recognized. Activity/allowance checks continue using the backend's canonical state, not translated labels. User-supplied names, saved messages, model responses and tool outputs are not rewritten.

The Pot desktop welcome page reads the current viewer language from the broker's `/api/locale`. This public GET exposes only a language code and permits the loopback welcome-page origin. Updating language requires the viewer token and the existing same-origin policy. It does not control the execution target or change Linux/Chromium/third-party application languages. The native viewer sends the app preference after loading and on every language change; stopped execution targets receive it when their viewer loads.

## Verification

```sh
mise run test:i18n
mise run test:app
mise run lint
mise run check:rust
```

The i18n suite checks key/placeholder parity, source references, English coverage, template/catalog validation, third-language asset discovery, native matching/fallback/saved preference/live notices, literal parameter preservation, web DOM updates and broker locale authorization. Application tests cover existing state/history/input contracts plus translation fallback, singular/plural forms, report-content preservation, and HTTP rejection fallbacks for invalid error bodies while preserving server diagnostics. These checks do not boot a VM or call models. Rebuild/bundle the Sandbox image to distribute changed embedded pages.
