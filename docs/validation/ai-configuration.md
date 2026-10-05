# AI configuration acceptance — 2026-10-05

Implementation for [issue #7](https://github.com/jjyr/pot/issues/7), based on `origin/main` at `a5a579f`, in branch `codex/ai-configuration`. This report separates native UI observations from backend contract tests and real-provider acceptance.

## Verification

- `mise run test`: 105 backend tests passed; 8 existing tests ignored. Runtime includes 91 passing tests.
- `mise run test:app`: 11 App tests passed.
- `mise run check`: formatting, script checks, Rust compilation and Swift Helper compilation passed.
- `mise run build` and `mise run bundle`: native App and guest image built successfully.
- `git diff --check`: passed.

Backend regression coverage includes connection-only saves without selecting Chat, editing one API connection while another is active, retained keys, key-free public metadata, bounded model-list checks with no inference or configuration mutation, unsafe endpoint rejection, active Chat snapshot routing, legacy independent worker defaults, separate login/shell PTYs, and execution limits independent of credentials. Server lifecycle fixtures now use an atomic sequence alongside time/process IDs after parallel tests reproduced a temporary-directory collision.

## Native UI observations

Used the bundled native App in an independent local root with a fresh workspace. The custom API connection used `https://example.com/v1/chat/completions`, a fake credential and `fixture-model`; no model request was sent. Codex was installed and officially reported sign-in required. The user-operated authentication itself was not performed.

| Flow | Observed result |
| --- | --- |
| Empty Chat | Immediate setup card links to the shared AI Configuration page. |
| Unified settings | General, AI Configuration, Task Configuration and Work Environment; one Agent list. |
| Add API connection | Saved through the UI with a masked fixture key; Chat remained unconfigured until explicit main save. |
| Chat completion | Save and start chatting returned to Chat without sending a message. |
| Settings completion | Save changes stayed on AI Configuration. |
| Draft edits | Editing the model did not change the active Chat model; Chat input survived page/login round trips without being sent. |
| CLI guide | Dedicated shell PTY, visible official command, explicit Copy, read-only Check, Back; sign-in required disables Continue. |
| Terminal isolation | A harmless marker in the regular terminal remained after entering/leaving the login terminal; opening/copying did not execute login commands. |
| Login return | Agent name and model input survived checks and return from Terminal. |
| Common API preset | DeepSeek hid the prefilled endpoint/protocol under Advanced. |
| Missing API credential | Inline “provide API key” error preserved name/model; no external network request. |
| Restart | Saved connection, main binding and model restored. |
| Themes/layout | Light/dark AI pages and inline errors readable; long Add list scrollable; changing connection context resets the form scroll. |
| AX/keyboard | Fields/actions accessible in AX; text editing via select-all/paste worked. Full keyboard-only focus traversal remains unverified. |

The illustrated Chinese report and original PNGs are local acceptance artifacts at `.local/validation/ai-configuration-20261005/index.html` in the original checkout (`/Users/jjy/Workspace/pot`). They are ignored development artifacts, not committed screenshots. Early captures precede the final Agent-name and login-button corrections; the report identifies them and includes final regression captures.

## Remaining acceptance and capability limits

- Real API credentials, provider model-list responses and paid inference were not exercised. Local backend HTTP fixtures verify request/auth/model-list semantics.
- Real Codex/Claude/AGY login success, browser/device-code completion, expired-login recovery and the complete status-error matrix need account-based acceptance. Opening Terminal is never treated as authentication.
- AGY has no verified login command/status API; the guide explicitly uses `agy --help` and unknown-state confirmation rather than claiming sign-in success.
- CLI model discovery is not established; a documented manual model identifier is used.
- Supported CLIs are bundled with the image. Missing tools lead to environment preparation or manual Terminal repair. An online install-and-continue workflow is not delivered.
- The final task picker label change passed build/check but its last screenshot attempt was blocked by a locked Mac. Earlier task-page captures remain in the report.
- Task-specific migration, busy-round handoff, failed migration and preserved binding semantics have backend coverage; their complete native UI matrix was not exercised in this pass.
- Persisted saved configuration is verified across App restart. Unsaved Chat/form drafts are protected during navigation, not newly made durable across application termination.

These results do not mark all issue #7 acceptance boxes as complete. No real-provider result is inferred from fixture configuration or local tests.

## UI refinement — 2026-10-05

Native revalidation of Main Agent wording, compact integrated rows, direct CLI configuration entry, and the simplified OpenAI-compatible API form. Check connection and Save connection are separate adjacent footer buttons. The updated focused Chinese report contains eight native screenshots at `/Users/jjy/Workspace/pot/.local/validation/ai-configuration-20261005/refinement/index.html`; the previous report links to it and remains historical evidence.

- Selecting Codex directly opened its configuration with sign-in-required status. No real login was performed.
- New API configuration defaulted to OpenAI API and `https://api.openai.com/v1`; no Anthropic/Gemini new-connection choices.
- Missing-key Check showed an inline error while preserving name, base URL and model; no connection was saved.
- Saving Company API with a fake credential and `fixture-model` added a compact row while retaining UI fixture as the main Agent. No provider check or inference was triggered by Save.
- Reopening confirmed the `/v1` base URL expanded to `/v1/chat/completions`; saved credentials were absent from the visible field. Existing custom full endpoint paths remain preserved.
- Advanced interface choices were Chat Completions and Responses.
- Build, bundle, formatting/compilation checks, and all 11 App tests passed. This refinement does not establish real-provider acceptance; no additional dark-theme or keyboard-only matrix was performed.

## Brand grid and shared panel — 2026-10-06

Replaced the integration chooser with a compact three-column icon/name grid, centered over the AI Configuration page. Codex, Claude, Gemini, OpenAI API, Custom API and AGY lead into the same panel shell. Main Agent is now a compact summary; Change opens its Agent/model form. The focused Chinese screenshot report is `/Users/jjy/Workspace/pot/.local/validation/ai-configuration-20261006/index.html`. Earlier reports remain historical evidence.

Native observations in the same independent fixture root:

- Grid choices lead directly to the matching configuration; Codex shows the Terminal login guide.
- New Claude connections offer account-login/API-key modes; the API mode hides the official endpoint/protocol. Existing connections keep their saved integration type.
- Gemini displays key/model only; custom API displays its address. Advanced contains name, endpoint and Chat Completions/Responses/Messages/Gemini formats.
- Gemini's missing-key check produced an inline error and retained the model draft. Saving with a fake key added a Gemini profile with its native endpoint/protocol; the UI fixture remained the active main Agent. No real provider check/inference was performed. The restored list displayed Gemini's brand mark after restart.
- Main Change opens the explicit Agent/model confirmation panel; saving the already-active selection closes it without changing the binding.
- Escape from an API form input closes the panel. Full keyboard focus trapping, all window sizes and dark-theme matrix are not established by this pass.
- Build/bundle, static checks and 11 App regression tests passed. Backend wire formats were not changed; real Gemini/Claude/OpenAI account/model acceptance remains outstanding.

Brand SVGs are embedded from Simple Icons v14.0.0 (CC0); no external logo downloads happen at runtime. Codex uses the OpenAI mark, while AGY/Custom API use generic terminal/network icons.
