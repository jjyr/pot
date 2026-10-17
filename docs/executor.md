# Command execution

Control authorizes an exact operation, then the fixed destination Runtime runs
it through `pkg/executor`. Main uses its owning Control's local executor; explicit
remote Tasks use the authenticated Runner. Tools cannot change their destination.
See [authorization](permissions.md) and [Task coordination](task-coordination.md).

## Workspaces and context

Main uses `WORKSPACE/assistants/ASSISTANT_ID`; Workers use
`WORKSPACE/tasks/TASK_ID`. Project instructions and Skills are discovered in that
machine's filesystem. Control global resources synchronize for Tasks before first
preparation. No host/guest mapping, implicit project copying or shared Task working
directory is provided. Installed tools and OS files remain machine-local.

Relative command `workdir` resolves beneath the initial working directory; absolute
command paths use the Runtime service account's ordinary privileges. A working
directory is not filesystem isolation. `apply_patch` paths remain workspace-scoped.
External-service credentials use [run-bound injection](credentials.md).

## Commands and shells

`exec_command` creates an independent command with its own cwd and environment.
`cd` or `export` does not modify later commands. Shell paths must name an installed
supported shell; `login` defaults to the configured shell policy. Login environment
snapshots remain bounded and scoped to the executor. Timeout, output, session and
idle limits apply to every invocation.

Omit `yield_time_ms` to wait for completion; set it to return a live session after
a bounded wait. `write_stdin` polls output or interacts with that original session.
Sessions cannot cross assistant/generation/Runtime ownership. TTY allocation and
additional input under `ask` require persistent machine authorization. Empty input
polling and cancellation still check executor session ownership.

## Lifetime and recovery

Each invocation drains only its own executor processes and plugin sessions. Local
cleanup uses process groups and bounded cleanup confirmation; it does not provide
the former guest cgroup containment or VM isolation. Completion does not erase
workspace files. Cancellation cannot undo external effects.

An uncertain cleanup/result retains execution capacity and durable receipt fences.
Restart or disconnect does not launch a replacement command or move work to another
Runtime. Previous-boot operations without original cleanup proof remain fenced
until explicit owner recovery can establish the outcome. See
[Tasks](task-coordination.md) for inspection and reconciliation.

CPU/memory/disk quotas for VMs and containers are absent. Execution concurrency,
model admission and token allowances remain independently configured.
