# Internationalization

Pot supports English (`en`) and Simplified Chinese (`zh-CN`). Language is a local
presentation preference, separate from assistant configuration and scheduling
zones. User names, messages, model responses, tool output and provider diagnostics
remain verbatim.

## Shared frontend preferences

The shared HTML frontend resolves language from an explicit host/query override,
then saved local storage, then the browser/WebView language (`zh` selects `zh-CN`;
other languages select English). Unsupported saved values fall back to English.
General settings exposes Language above Appearance. Its current choices are
English and 简体中文. The selected language and System/Light/Dark appearance are
saved locally and applied without restarting the Go backend or desktop.

The shared shell uses [`web/src/locales/messages.mjs`](../web/src/locales/messages.mjs)
for common text. Page components currently pair their English and Chinese labels
through the same locale accessor. New product labels must supply both languages.
The DOM owns normal input/IME behavior; preference refreshes preserve the live
editor nodes, values, ownership and pending request state. Backend payloads and
stored transcripts contain locale-independent identifiers.

Preferences are scoped to the WebView/browser origin. They are not Swift
UserDefaults or a synchronized Control setting. Language selection does not
change model prompts, execution targets, Linux application languages, stored
timestamps or scheduling time zones. Native operating-system dialogs retain the
system's language.

## Retained desktop and Sandbox pages

[`resources/app/locales`](../resources/app/locales) contains the complete retained
catalogs for the desktop viewer and Sandbox welcome page. Stable semantic keys
separate translated text from source. Each catalog supplies `language.name` and
must match English keys and placeholders.

`scripts/localize-web` validates these catalogs and builds
`desktop/index.template.html` and
`plugins/podman/image/index.template.html` into generated `index.html`,
`i18n.js` and page-specific `locales.json`. Generated files live under
`.local/web/{desktop,sandbox}`. `--check` validates without writing. App and guest
image preparation copy the generated assets; rebuild the image to distribute
changed embedded welcome pages.

The retained pages discover their locale catalogs, match exact tags before
language-base fallback and load translations before desktop initialization.
Live viewer language updates preserve noVNC and clipboard state. The Sandbox
welcome page reads the viewer preference through the broker's public
`GET /api/locale`; changing it requires the viewer token and same-origin policy.
This language endpoint neither selects an execution target nor changes guest
application languages.

Adding a language requires a complete retained catalog and translations in the
shared frontend; adding only a catalog does not extend the current product
picker automatically. Keep complete sentence templates, identical placeholders
and standard spellings for brands, protocols and units. Existing generic Web Push
notification text supports English and Chinese.

See [Development](development.md) for localization and UI verification commands.
