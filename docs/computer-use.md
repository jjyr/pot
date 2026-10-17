# Computer use plugin

The default-enabled `computer-use` plugin provides a persistent `cua_repl`
JavaScript session and a dedicated headless Chromium on the fixed execution
Runtime. Control applies exact execution authorization, including remote calls. Protected machines sandbox the plugin and browser; Trusted machines execute directly.
Each REPL has its own browser and temporary profile; cleanup does not close
sibling Tasks' sessions. Native desktop automation and guest viewing are absent.

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

`cua_repl` is exposed to `pkg/agent` and scoped Pot MCP. It accepts `code`, optional `session_id`, and optional `reset`. An initial call without code returns API documentation and a session ID. Reuse the ID to preserve variables, top-level `await` and tab handles across calls during the active execution. The session remains on its original Runtime; select another machine when creating
a Task. Each invocation passes the same permission checks, including calls composed through `exec`. Use direct `cua_repl` calls for typed screenshot input.

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
confirm the enclosing execution supervisor has confirmed cleanup before removing
private files. Browser actions are never retried as part of shutdown.

## Plugin contract and lifecycle

Packages own instructions, schemas and implementation. The schema-2
`contributes.tools` contract requires `runtime: "node"`, `execution: "runtime"`,
confined package paths and unique tool names. Bundles contain only declared files
and are limited to 32 files / 512 KiB. See [Plugin packages](plugins.md).

Tool implementations export `createPlugin({mode,emit})` with `execute({tool,input})`
and `dispose()`. Executor validates the pinned bundle and observation mode.
Sessions remain bound to the assistant, generation and original Runtime. Every
call passes the same exact authorization as built-in tools; tool-specific approval
metadata is unsupported.

The machine needs Node and Chrome/Chromium; App supplies Node. Standard browser
paths are detected, or set `POT_BROWSER_EXECUTABLE`. Chromium keeps its native
sandbox enabled, uses inherited CDP pipes and a temporary profile. Plugin children
receive a restricted environment without inherited model credentials. The JS REPL is not isolation; the destination execution policy supplies the OS boundary. Unsupported Chromium/native-sandbox combinations fail rather than disabling Chromium protection or silently switching to host execution.
Browser sessions do not adopt existing user profiles or persistent logins.

The current protected backend does not support Google Chrome on macOS when its
profile singleton IPC is blocked, or Linux Chromium when its native user-namespace
sandbox conflicts with SRT's strict seccomp policy. Startup reports the bounded
browser diagnostic. Do not pass `--no-sandbox`, open host IPC, or enable weaker
nested namespace settings to hide these failures. Trusted-machine browser execution
is an explicit owner policy with the account's ordinary host privileges.

## Verification

Install current packaged Runtime assets; replacing source files does not update an existing installed package.

```sh
mise run test
mise run test:executor
mise run build:go
mise run test:browser
mise run test:sandbox
```

The browser-close fixture and Go plugin-host tests cover bounded sessions,
state, errors, ownership and process cleanup without model calls. The real sandbox
suite exercises Chromium when installed, including explicit startup rejection and
outer descendant cleanup for unsupported configurations. Successful browser/CDP
interaction requires an installed supported Chrome/Chromium combination.
