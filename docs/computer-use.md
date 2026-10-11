# Computer use plugin

The default-enabled `computer-use` plugin provides a persistent `cua_repl` JavaScript session and a dedicated headless Chromium on **Host or Linux Sandbox**. It follows the shared [permissions](permissions.md): Main defaults to Host with owner approval; Worker defaults to its assigned Sandbox, which allows execution by default. Environment policies and Agent-specific grants apply to every invocation. Native desktop automation is not supported.

## Configuration

For Control-owned remote tasks, the approved design makes Control the sole source
of global plugin definitions and pinned bundles; execution nodes run supplied
bundles without discovering their own global plugins. That delivery/invocation
work is pending in #39. The configuration below describes delivered owner-local
plugin management and remains the source format for Control-owned configuration.

Use owner-only `pot config get/set agent` (`GET/POST /api/v1/agent`). The default `params.agent.plugins` is:

```json
{"computer-use": {"enabled": true}}
```

Set `enabled:false` to disable it, or `plugins:{}` to disable all plugins. If `allowed_tools` is configured, include `cua_repl`. `builtin_tools` controls core execution and composition tools. Saving older settings removes the `browser` builtin and migrates its allowed-tool entry to `cua_repl`; re-save/rebind old profiles to pin the plugin.

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

`cua_repl` is exposed to pot-agent and scoped Pot MCP. It accepts `code`, optional `session_id`, and optional `reset`. An initial call without code returns API documentation and a session ID. Reuse the ID to preserve variables, top-level `await` and tab handles across calls and turns. Use `environment:"host"` or `"sandbox:ID"` to choose a target; Main may also use `sandbox_id`. Omitted targets follow the Agent default for new sessions and the original environment for existing sessions. Sessions cannot switch environments. Each invocation passes the same permission checks, including calls composed through `exec`. Use direct `cua_repl` calls for typed screenshot input.

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

Closing the tab preserves JS state. End the REPL with `reset:true` and its existing `session_id`; reset without an ID is rejected. Each REPL supports one tab. Popups, frame targeting, uploads/downloads and unrestricted CDP/page evaluation are not exposed.

## Plugin contract and lifecycle

Packages own their instructions, tool schemas and implementation. See the [bundled manifest](../plugins/computer-use/plugin.json). Version 1 requires `runtime:"node"`, `execution:"environment"` (Host and Sandbox) or `"sandbox"` (Sandbox only), confined package paths, unique tool names, and flat scalar input schemas. Bundles are limited to 32 files / 512 KiB. Plugins export `createPlugin({mode,environment,emit})` with `execute({tool,input})` and `dispose()`.

Runtime resolves the environment, checks permission, and sends the pinned bundle and model-derived mode to its runner. Sessions are bound to Agent and epoch; Sandbox sessions also bind to boot generation. Bundle digest, environment and observation mode stay fixed. Existing pinned Sandbox-only bundles must be re-imported/rebound to enable Host execution.

Tools may include optional `approval_display` metadata with localized `summary` text and `labels` keyed by argument name (each uses locale-to-text maps with an English fallback). Pot shows these plain-text labels with actual arguments; tools without metadata use raw JSON. Metadata is pinned with the bundle, validated on import, and excluded from model tool schemas. Changing display text requires importing the bundle again and saving the updated Thread configuration.

Add it beside `name`, `description` and `inputSchema` in a manifest tool:

```json
"approval_display": {
  "summary": {"en": "Allow browser use?", "zh-CN": "是否允许使用浏览器？"},
  "labels": {
    "code": {"en": "Operation code", "zh-CN": "操作代码"},
    "session_id": {"en": "Session", "zh-CN": "会话"},
    "reset": {"en": "End session", "zh-CN": "结束会话"}
  }
}
```

English text is required; labels must reference declared arguments. Approval cards
show only the localized summary when supplied, and raw arguments otherwise. Actual
arguments remain in the request for authorization. This is
static plain text, without JavaScript callbacks, interpolation or inferred page
actions. Runtime checks the display against the bound package before approval.

Host needs Node and an installed Chrome/Chromium; the App supplies Node. Standard Linux and macOS browser paths are discovered automatically, or set `POT_BROWSER_EXECUTABLE`. Host uses temporary profiles, process-group cleanup and Chrome's native sandbox. Plugin children receive a restricted environment without inherited model credentials. Host approval authorizes code execution with the user's OS privileges; the JS context is not an isolation boundary.

Linux Sandbox execution uses the unprivileged Guest scope/cgroup and `--no-sandbox` inside the managed boundary. It requires a qualified image with Node/Chromium. Both paths use inherited CDP pipes, temporary files and idle expiry. Browser sessions are separate from existing user profiles and noVNC, without manual takeover or persistent logins.

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

The browser suite checks both modes, persistent state, interactions, stale recovery, reset, replay and cancellation against a disposable page. For a live root Guest broker, use `scripts/tests/browser.py --broker --package /qualification/computer-use` with `POT_BROWSER_TEST_TOKEN`. These tests make no model API calls. `main_host_computer_use_waits_for_owner_and_keeps_session_scope` exercises the Host Agent/approval/runner path; `POT_TEST_HOST_BROWSER_URL` enables Chrome interactions against a disposable fixture.
