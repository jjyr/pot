# Control final qualification — 2026-10-11

## Real provider

Existing DeepSeek configuration was read privately from `~/.pi/agent/models.json`.
Its credential differs from the environment variable that previously returned
HTTP 401. The configured official endpoint returned HTTP 200 for model discovery.
The supported `deepseek-flash` model completed a minimal task via the devpc
Control and macOS Host Task Agent, returning the exact destination project marker
`CROSS_MACHINE_PROJECT`. The endpoint is a complete Chat Completions URL, not a
base URL. No tools or file changes were requested by this paid test.

Two minimal tasks passed. The stricter task omitted the marker from its prompt
and still returned the project-only marker: `f42148dc-caec-4eb9-b00d-02de2f62e93d`.
The first task was `90a33064-65ce-421f-a4b1-1aecebb8943d`. Native App showed the completed
Task, fixed machine/Host, and exact reply. Execution-node JSON/SQLite files did not
contain the Provider key. Provider credentials stayed on Control; none are stored
in this report or repository. Earlier 401 and incorrect endpoint 404 attempts
failed explicitly without fallback and are not counted as successful runs.

## Pause and reboot

The independent physical Linux Control/macOS Host harness now supports
`--suspend-control`. SIGSTOP for 33 seconds exceeds the lease lifetime; SIGCONT
resumed the original run after reconciliation. Exactly one X was written and
model sampling did not advance during the pause. Execution SIGKILL still left
Interrupted until explicit guidance started a new run, without replay or leaked
Provider credentials. The harness resumes only its owned PID before cleanup.
Evidence: `/tmp/pot39-suspend-evidence.json`, `/tmp/pot39-suspend.log`.

This is process-suspension qualification, not a physical OS sleep/power-loss test.
Earlier disconnect/crash, guidance/cancel, plugins, files and remote schedule
results are in [production integration](task-agent-production-20261011.md).

## Build, persistence and limits

Server lifecycle: ten tests passed, including resource upgrades preserving
assistants/rules/Sandbox definitions, private local authority rotation and shutdown.
Runtime: 177 passed; Storage: two; Server: 32 with one existing opt-in ignored;
UI: 13; locale catalogue: 806 keys; release packaging: three; license policy: four.
Workspace/all-target Clippy passed. macOS and Linux debug CLI builds passed.

CLI packaging previously referenced a removed document; it now includes the
canonical deployment guide. The full macOS CLI release archive built successfully (optimized native build:
5m 19s; Web/WASM build also passed). Extraction verified the deployment guide,
Tailscale library and bundled Skills; isolated idle Runtime startup and shutdown
passed using the installed binary: only Runtime capability, an empty resource
list and no UI. An initial long macOS temporary root exceeded the Unix socket
path limit; repeating under a short `/tmp` root passed.

External FCM/browser OS notification delivery is untested; the relay and encrypted
payload contracts are qualified in [notifications](control-notifications-20261011.md).
Physical whole-machine sleep/power loss and remote desktop transport are not
claimed. Desktop transport remains explicitly deferred by the user.
