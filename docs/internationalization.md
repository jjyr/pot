# Internationalization

This is the shared i18n rule for App and browser UI. Supported languages are
English (`en`) and Simplified Chinese (`zh-CN`).

## Required for new or changed UI text

- Put all product-authored, user-visible text in the shared
  [catalog](../web/src/locales/messages.mjs) and use `translate(locale, key)`.
  This includes menus, buttons, settings, permission states and instructions,
  dialogs, toasts, errors, empty states, notification titles/bodies, `aria-label`,
  `title`, `alt` and `placeholder`. Do not add inline English/Chinese pairs or
  locale conditionals; move wording you change in older components into the catalog.
- Use stable, semantic keys grouped by feature, such as `integrations.login` or
  `integrations.authorization.failed`. Reuse a key only when meaning and context
  match. Add or update **both languages in the same change**. Lookup falls back to
  English, then the key itself; fallback is diagnostic, not a completed translation.
- Translate complete phrases, not concatenated sentence fragments. The lookup
  accepts only `(locale, key)`: it has no automatic interpolation or plural API.
  When needed, store a complete template with named `{name}` markers and substitute
  explicitly at the call site; keep the same markers in the same order in both
  languages, as required by the catalog check. Use separate keys for plural forms
  and select explicitly. Format numbers/dates with the current locale.
- Read the current locale when rendering or completing asynchronous work. Shared
  components receive `locale: () => locale`; use `translate(locale(), key)` inside
  callbacks, not a translated string captured at mount or before `await`. Refresh
  mounted labels and accessibility text through the existing `pot:locale` page
  refresh or component update path, preserving drafts, focus and pending operations.

For example, inside a component that receives the existing `locale` accessor:

```js
import {translate} from '../locales/messages.mjs';

const t = key => translate(locale(), key);
button.textContent = t('integrations.login');
button.setAttribute('aria-label', t('integrations.login'));
```

## Notifications, errors and content boundaries

Carry stable event/status/error identifiers and data across backend/native
boundaries; translate product wording at the presentation boundary. For
notifications delivered through a native bridge, resolve the title/body with the
current UI locale before sending them for display. A path that produces text
without the shared frontend must explicitly receive the relevant locale and reuse
the catalog for its product wording; do not introduce another bilingual dictionary.
Treat existing backend push wording as legacy when changing that path. Operating
system permission dialogs use the system language; Pot's authorization button,
permission explanation, settings instructions and failure message use Pot's locale.

Map expected failures to semantic catalog keys. For unknown failures, show a
localized summary and keep raw diagnostics in details; do not use a raw
`error.message` as the only user-facing explanation. Developer logs, protocol IDs,
paths, code, user names/messages, model responses, tool output and provider
diagnostics remain verbatim. Product labels around them still require translation.

## Locale behavior and review

Language resolves from the host/query override, then local storage, then browser/
WebView language (`zh` selects `zh-CN`, otherwise `en`); unsupported values fall back
to English. General settings saves language per origin and applies it live. This
preference does not change prompts, stored transcripts, scheduling zones, other
applications or the operating system language.

Before a PR, run the catalog check listed in
[Development checks](development.md#checks-and-isolated-previews). Inspect the diff
for hardcoded text, including adapters and native display paths. Exercise the
changed flow in both languages, including permission/error/empty states and a
language switch while asynchronous work is pending; check visible and accessible
labels. Catalog validation alone does not detect hardcoded UI text or stale locale
captures.
