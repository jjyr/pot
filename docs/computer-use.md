# Computer use plugin

The default-enabled `computer-use` plugin provides a persistent `cua_repl`
JavaScript session and a dedicated headless Chromium on Host or Linux Sandbox.
Control applies machine execution policy and exact Host operation approval.
Remote configuration and pinned bundles come from Control; execution-node
configuration cannot override them. Native desktop automation is not supported.

Sharing the machine Sandbox does not share browser sessions. Each REPL starts its
own Chromium and temporary profile. GUI/noVNC viewer ownership is a separate
mechanism; headless browser sessions do not acquire desktop control. Task cleanup
closes its sessions and leaves other Tasks' browsers and the GUI alive.

## Configuration

For connected tasks, Control is the source of global plugin definitions and
implementations. Launches contain selected plugin names only. Before starting the
Agent, the execution Runtime synchronizes current bundles from Control and
registers their tool definitions; it never discovers node-local global plugins.
The prepared bundles remain stable for the run; new Tasks synchronize again. See [resource synchronization](architecture.md).

Use owner-only `pot config get/set agent` (`GET/POST /api/v1/agent`). The default `params.agent.plugins` is:

```json
{"computer-use": {"enabled": true}}
```

Set `enabled:false` to disable it, or `plugins:{}` to disable all tool capabilities. If `allowed_tools` is configured, include `cua_repl`. `builtin_tools` controls core execution and composition tools. Saving older settings removes the `browser` builtin and migrates its allowed-tool entry to `cua_repl`; re-save/rebind old profiles to pin the plugin.

Import or replace a trusted package from an absolute Host directory:

```json
{"computer-use": {"enabled": true, "source": "/absolute/path/computer-use"}}
```

Saving imports the manifest and files into an immutable bundle pinned by version and SHA-256 digest. Running tasks keep their prepared bundle. Later executions synchronize the current bundle from the selected source Profile; unavailable or disabled selected plugins fail before sampling.

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

`cua_repl` is exposed to `pkg/agent` and scoped Pot MCP. It accepts `code`, optional `session_id`, and optional `reset`. An initial call without code returns API documentation and a session ID. Reuse the ID to preserve variables, top-level `await` and tab handles across calls during the active execution. Use `execution_target:"host"` or `"sandbox:ID"` to choose a target; Omitted targets follow the Agent default for new sessions and the original execution target for existing sessions. Sessions cannot switch execution targets. Each invocation passes the same permission checks, including calls composed through `exec`. Use direct `cua_repl` calls for typed screenshot input.

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

Closing requests graceful Chromium shutdown before removing its temporary
profile. Concurrent closes share one shutdown. A forced or unexpected browser
exit retains the profile and reports unconfirmed cleanup; reset/disposal must
confirm the enclosing process group or Guest cgroup has stopped before removing
private files. Browser actions are never retried as part of shutdown.

## Plugin contract and lifecycle

Packages own their instructions, tool schemas and implementation. See the [bundled manifest](../plugins/computer-use/plugin.json). The common schema-2 manifest declares `contributes.tools`, which requires `runtime:"node"`, `execution:"host_or_sandbox"` (Host and Sandbox) or `"sandbox"` (Sandbox only), confined package paths, unique tool names, and flat scalar input schemas. Only the tool declaration and files enter transferable snapshots; Provider and resource capabilities stay local. Tool bundles are limited to 32 files / 512 KiB. Tool implementations export `createPlugin({mode,execution_target,emit})` with `execute({tool,input})` and `dispose()`.

Control execution target adapters resolve the target, check permission, and send the pinned bundle and model-derived mode to the execution backend. Sessions are bound to Agent and epoch; Sandbox sessions also bind to boot generation. Bundle digest, execution target and observation mode stay fixed. Existing pinned Sandbox-only bundles must be re-imported/rebound to enable Host execution.

Host plugin calls use the same exact-operation approval as built-in execution tools. Sandbox calls use machine membership and their execution scope. Tool-specific approval metadata is not accepted.

Host needs Node and an installed Chrome/Chromium; the App supplies Node. Standard Linux and macOS browser paths are discovered automatically, or set `POT_BROWSER_EXECUTABLE`. Host uses temporary profiles, process-group cleanup and Chrome's native sandbox. Plugin children receive a restricted environment without inherited model credentials. Host approval authorizes code execution with the user's OS privileges; the JS context is not an isolation boundary.

Linux Sandbox execution uses the unprivileged Guest scope/cgroup and `--no-sandbox` inside the managed boundary. It requires a qualified image with Node/Chromium. Both paths use inherited CDP pipes, temporary files and idle expiry. Browser sessions are separate from existing user profiles and noVNC, without manual takeover or persistent logins.

Ordinary JS errors preserve state and emitted observations. Timeout, cancellation, process loss, binding changes or Guest restart invalidate the session; start a new REPL after loss. Reset/shutdown clean up temporary files, and cancellation ends the scope. Inspect the page before retrying an action: cancellation is not rollback, and receipts deduplicate whole invocations, not nested actions.

Limits: 30 seconds per invocation, 32 KiB code, 2 MiB transport, 256 KiB text output, 300 accessibility elements and a 1 MiB screenshot. Only the newest screenshot per invocation is retained, as typed image content.

## Verification

Rebuild the Linux image with the current executor and broker; older published images do not gain plugin support automatically.

```sh
mise run test
mise run test:executor
mise run build:go
mise run test:browser -- --chromium /usr/bin/chromium
```

The browser suite checks both modes, persistent state, interactions, stale recovery, reset, replay and cancellation against a disposable page. For a live root Guest broker, use `scripts/tests/browser.py --broker --package /qualification/computer-use` with `POT_BROWSER_TEST_TOKEN`. These tests make no model API calls. `main_host_computer_use_waits_for_owner_and_keeps_session_scope` exercises the Host Agent/approval/runner path; `POT_TEST_HOST_BROWSER_URL` enables Chrome interactions against a disposable fixture.
