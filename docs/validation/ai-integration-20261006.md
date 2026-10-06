# Simplified AI integration acceptance — 2026-10-06

Implementation for [issue #7](https://github.com/jjyr/pot/issues/7), in the separate `codex/ai-configuration` worktree. This supersedes the earlier Terminal-based configuration flow.

## Delivered behavior

Settings and first-time Chat share one compact colored provider catalog and configuration panel. Codex and Grok use official browser authorization. Claude, Gemini, DeepSeek and OpenCode Go/Zen use API presets. Custom API exposes the endpoint and four request formats. Check and Save remain separate adjacent actions. First-time Chat saves the connection and Main Agent together; ordinary additions retain the existing Main Agent. Product Terminal UI, assets and sidebar entry are removed. Agent shell capabilities and Runtime PTY facilities remain.

The explicitly approved native authorization routes start, query and cancel official login. A transient Mac loopback listener relays only the registered login callback into the VM. State, path, expiration and host validation reject foreign/stale callbacks. Receiving a callback does not establish authentication: official completion and account verification are required. The listener closes on completion, failure, cancellation, timeout, environment loss or shutdown.

## Native observations

The native bundled App used a fresh isolated validation root, leaving production configuration untouched. Nineteen actual screenshots with Chinese interaction explanations are at `/Users/jjy/Workspace/pot/.local/validation/ai-configuration-20261006/simplified/index.html`. Screenshots are ignored local artifacts.

- Empty Chat immediately guides the user to shared AI Configuration without requiring API users to log in.
- All eight catalog entries open their corresponding forms; API presets hide endpoint/protocol choices.
- Codex official browser authorization was started; waiting, reopen and cancellation controls appeared. Cancellation displayed an explicit result.
- Missing model and unsafe HTTP endpoint checks retained the draft and reported the problem.
- A fake key/model and `https://pot-validation.invalid/v1/chat/completions` were saved under the name `验证专用 · 不可调用`. Save and start chatting returned to Chat; Settings confirmed the selected Main Agent and model. No model request was sent.
- A second Gemini connection with a fake key/model was saved without checking or sending inference. The original Main Agent remained selected.
- Saved status means configuration exists; it does not imply successful account authorization or provider access.

## Verification

`mise run check`, `mise run test`, `mise run test:app` (11 passing tests), native build and bundle passed. Eight real-engine tests remain ignored. Five authorization tests cover native boundaries, stale cancellation, callback state/expiry and an actual local HTTP relay preserving encoded queries without redirects or inferred authentication. The isolated Grok ACP fixture covers sessions, streaming, fail-closed permissions and cancellation receipt. Pure Swift OpenCode tests cover the documented per-plan model routing table.

## Unverified scope and limits

### Follow-up: real Codex sign-in and callback cleanup

The user completed real Codex sign-in through the Mac-to-VM callback. Native discovery displayed authenticated status and account models. However, the authorization panel still displayed waiting and the Mac callback listener remained open. The pending attempt was cancelled to close the listener without removing the authenticated account.

The follow-up closes the Mac listener immediately after a validated callback is successfully relayed, while sending the browser its response. Further callback requests are rejected. Official account verification after a relayed callback also settles Codex authorization if the matching completion notification is unavailable; callback receipt alone remains insufficient. Verified authorization completion brings the existing Pot window to the foreground. A real local HTTP test confirmed one successful response followed by connection refusal, and all five authorization tests passed. Automatic foreground return still requires a fresh user login on the updated build to verify end to end.

The initial pass did not exercise real accounts. The follow-up above records the user's subsequent successful Codex sign-in and model discovery through the VM callback. Device-code completion, Grok account completion and inference remain untested. The user's environmental network problem is excluded as a blocker, not counted as successful acceptance. Grok currently supports main Chat; enforced task-tool policy, permission-review UI, native usage accounting and checkpoint replay are not delivered. Permission requests fail closed and incomplete usage is not reported as measured zero. Full keyboard-only, dark-theme and window-size matrices were not repeated for this version.
