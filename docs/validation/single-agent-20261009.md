# Single Agent engine validation

Date: 2026-10-09. Scope: removal of the native Agent integration and production
convergence on the in-process pot-agent with API model connections.

The CLI adapter crate, native authorization/discovery endpoints, process launch
contracts, native thread bindings, bundled CLI packages and native cumulative
usage accounting are removed. Runtime uses pot-agent for main chat and tasks.
API model selection, Core execution contracts, role-scoped tools, permissions,
plugins/Node, Gateway, Storage and Sandbox Manager/Provider boundaries remain.
No legacy conversation or usage schema migration/import is provided.

## Automated results

| Check | Result |
|---|---|
| Workspace compilation and Clippy, warnings denied | Passed, including the Rust/Swift App |
| Backend tests | 336 passed; paid-model and libtailscale-fixture tests ignored |
| Native App and shared UI tests | 17 and 11 passed; Swift client safety tests passed |
| Deterministic evaluation CLI contracts | Passed |
| Swift Helper build | Passed |
| Source lint and Web/localization checks | Passed; 755 translation keys |
| Release packaging, executor, skill mounts and permissions | 3 / 5 / 11 / 7 passed |
| Skill authoring/install fixtures | Passed |
| Dependency license policy and license fixtures | Passed |
| Local documentation links and diff whitespace | Passed |

The Sandbox regression harness now exercises two API-backed Pot profiles instead
of a native CLI profile. It preserves fixed-worker assignments, cancellation,
shell sessions, profile changes, Host restart, artifacts and desktop scenarios.
Main fixture execution explicitly targets its Sandbox; workers use their saved
assignments.

## Limits

No real VM/desktop regression, full distributable bundle, interactive UI session
or paid model inference was run for this change. Existing VM acceptance reports
are dated history and do not qualify the modified harness. Remaining model names
in API routing catalogs and upstream patch/skill attribution are not runtime
integrations.
