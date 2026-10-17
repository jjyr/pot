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
for common text and the third-party integrations page. Components use the shared
`translate(locale, key)` lookup, which falls back to English for missing locales
or keys. Keep new integration labels in this catalog, including the Settings tab;
do not embed paired English/Chinese strings in the component. Older page components
still pair labels through the same locale accessor. New product labels must
supply both languages.
The DOM owns normal input/IME behavior; preference refreshes preserve the live
editor nodes, values, ownership and pending request state. Backend payloads and
stored transcripts contain locale-independent identifiers.

Preferences are scoped to the WebView/browser origin. They are not Swift
UserDefaults or a synchronized Control setting. Language selection does not
change model prompts, execution targets, Linux application languages, stored
timestamps or scheduling time zones. Native operating-system dialogs retain the
system's language.
