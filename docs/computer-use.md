# Computer use plugin

The default-enabled `computer-use` plugin provides a persistent `cua_repl` JavaScript session and headless Chromium in the assigned **Linux Sandbox**. The Host runs model inference and imports plugins. Main and Worker can use it under the Sandbox permission policy; Main uses the selected Sandbox and Worker uses its assignment. Host browser execution and native desktop automation are not supported.

## Configuration

Use owner-only `pot config get/set agent` (`GET/POST /api/v1/agent`). The default `params.agent.plugins` is:

```json
{"computer-use": {"enabled": true}}
```

Set `enabled:false` to disable it, or `plugins:{}` to disable all plugins. If `allowed_tools` is configured, include `cua_repl`. `builtin_tools` controls shell, stdin and patch tools only. Saving older settings removes the `browser` builtin and migrates its allowed-tool entry to `cua_repl`; re-save/rebind old profiles to pin the plugin.

Import or replace a trusted package from an absolute Host directory:

```json
{"computer-use": {"enabled": true, "source": "/absolute/path/computer-use"}}
```

Saving imports the manifest and files into an immutable bundle pinned by version and SHA-256 digest. Existing tasks keep their saved bundle. Re-import with a new profile revision to apply package changes; disabling retains the imported bundle.

### Model capabilities

Configure `params.agent.model_catalog` with exact provider/model IDs:

```json
{
  "schema_version": 1,
  "default": {"input_modalities": ["text"]},
  "models": {
    "provider-id": {
      "exact-model-id": {"input_modalities": ["text", "image"]}
    }
  }
}
```

Unmatched models use the default; the built-in default is text only. Objects merge recursively, missing/null fields inherit, and arrays replace. Explicit false, zero and empty strings are preserved; invalid overrides fail validation. Capability declarations must match the provider's actual support.

Runtime selects observations from this configuration:

- Text: accessibility roles, names, values and element IDs; coordinate actions are rejected.
- Image: accessibility data plus a 1280×800 JPEG viewport; coordinate actions are allowed.

Model code cannot change the mode. Canvas or inaccessible content may require vision. Additional descriptor fields include `supports_image_detail_original`, `supported_in_api`, `visibility`, `priority`, and literal model/tool message overrides; these do not change tool schemas or permissions.

## CUA REPL

`cua_repl` is exposed to pot-agent and scoped Pot MCP. It accepts `code`, optional `session_id`, and optional `reset`. An initial call without code returns API documentation and a session ID. Reuse the ID to preserve variables, top-level `await` and tab handles across calls and turns. Main may supply `sandbox_id` when opening a session; later calls retain its original target. Each invocation passes the same permission checks, including calls composed through `exec`. Use direct `cua_repl` calls for typed screenshot input.

```javascript
let tab = await cua.open("https://example.com");
let state = await tab.observe();
nodeRepl.write(state.title);
```

In a later call with the same session ID, use IDs from the latest observation:

```javascript
state = await tab.fill({element: "8", sequence: state.sequence, text: "3"});
state = await tab.click({element: "9", sequence: state.sequence});
```

The API provides `cua.open`, `cua.getTab`, `cua.getState`, and tab methods `navigate`, `observe`, `fill`, `click`, `press`, `scroll`, `close`. Each operation emits and returns a fresh observation. Actions require the current sequence and observed element ID. Await operations sequentially; concurrent actions are rejected. `nodeRepl.write` and `console.log` emit text.

Closing the tab preserves JS state. End the REPL with `reset:true` and its existing `session_id`; reset without an ID is rejected. Each REPL supports one tab. Popups, frame targeting, uploads/downloads and unrestricted CDP/page evaluation are not exposed. This is Pot's API; it is not compatible with Codex's CUA API.

## Plugin contract and lifecycle

Packages own their instructions, tool schemas and implementation. See the [bundled manifest](../plugins/computer-use/plugin.json). Version 1 requires `runtime:"node"`, `execution:"sandbox"`, confined package paths, unique tool names, and flat scalar input schemas. Bundles are limited to 32 files / 512 KiB. Plugins export `createPlugin({mode,emit})` with `execute({tool,input})` and `dispose()`.

Runtime checks tool admission and sends the pinned bundle and model-derived mode to the generic Guest runner. Sessions are bound to owner, Agent epoch, Sandbox and boot generation; bundle digest and observation mode stay fixed. A qualified Linux image with the declared dependencies is required.

Node and Chromium run in the existing unprivileged Guest scope/cgroup, with temporary package files and browser profiles. CDP uses inherited pipes. Chromium runs with `--no-sandbox` inside the managed Sandbox; JavaScript itself is not an isolation boundary. Model credentials and Host product files are not supplied to the Guest. Browser sessions are separate from noVNC and have no manual takeover or persistent login profile.

Ordinary JS errors preserve state and emitted observations. Timeout, cancellation, process loss, binding changes or Guest restart invalidate the session; start a new REPL after loss. Reset/shutdown clean up temporary files, and cancellation ends the scope. Inspect the page before retrying an action: cancellation is not rollback, and receipts deduplicate whole invocations, not nested actions.

Limits: 30 seconds per invocation, 32 KiB code, 2 MiB transport, 256 KiB text output, 300 accessibility elements and a 1 MiB screenshot. Only the newest screenshot per invocation is retained, as typed image content.

## Verification

Rebuild the Linux image with the current executor and broker; older published images do not gain plugin support automatically.

```sh
mise run test
mise run test:executor
mise exec -- cargo build --locked -p pot-tool-executor --bin pot-tool-executor
mise run test:browser -- --chromium /usr/bin/chromium
```

The browser suite checks both modes, persistent state, interactions, stale recovery, reset, replay and cancellation against a disposable page. For a live root Guest broker, use `scripts/tests/browser.py --broker --package /qualification/computer-use` with `POT_BROWSER_TEST_TOKEN`. These tests make no model API calls; local macOS browser runs are test fixtures only.
