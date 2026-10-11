# Sandbox command execution

The Host Agent (pot-agent) calls the same role-scoped tools. Runtime validates
arguments and routes them to a credential-free guest runner. Current Agent loops remain at the owner; remote startup is pending in #39. pot-agent also routes these same tool names to
Host after its [permission gate](permissions.md).
`environment` selects Host or a Sandbox for pot-agent. Main defaults to Host and
tasks remain bound to their fixed Host/Sandbox target. File tools share the same runner implementation.

## Commands and shells

Every `exec_command` starts a separate command with an explicit cwd. Relative
`workdir` in Linux Sandbox resolves against `/data/workspace` for main or
`/data/workspace/tasks/TASK_ID` for a worker. A previous command's `cd`, exports or
functions do not change later commands. Files written in the workspace persist. Host relative paths use Runtime’s private
Agent workspace; Host HOME remains the Server account home. Shell settings, PTY
and process-session behavior are shared. Host sessions are scoped by Agent/epoch
and drained on cancellation/shutdown, using process groups rather than guest cgroups.

Profiles default to `/bin/bash`, with login shells and snapshots enabled; the
bundled image already includes Bash. Omitted settings use the same current
defaults. Shell settings are serialized explicitly and participate in profile
checksums; profiles saved with the previous checksum format must be recreated.
An explicit `shell` must be an absolute path with basename `sh`, `bash` or `zsh`.
The shell must exist in the selected guest. `login` defaults to the profile's
`allow_login_shell` (true); explicit login execution is rejected when disabled.
Commands use `-lc` for login startup and `-c` otherwise. HOME remains the assigned
execution home, independent of a command's `workdir`.

`tty` defaults to false. Pipe mode separates stdout/stderr and retains the existing
writable stdin support. PTY mode attaches all three streams to a controlling
terminal, returns merged output in stdout, and defaults to 24 rows by 80 columns.
Terminal control bytes are returned as output; the protocol does not render a
full terminal screen. This is shared by pot-agent through Pot tools.

`write_stdin` accepts `chars` or an empty poll, `close_stdin`, `interrupt`,
`terminate`, and PTY-only `rows`/`cols` (1–1000). `interrupt` sends SIGINT to the
foreground terminal group, or the pipe process group. A single Ctrl+C character
has the same meaning. Pipe close sends EOF; PTY close writes Ctrl+D, whose meaning
is governed by the program's terminal settings. `terminate` requests hard cleanup.

## Waiting and process lifetime

Omitting `yield_time_ms` waits for command completion, using the existing default
command timeout. Supplying it returns after a bounded wait with a `session_id`
when the process is still running. For such sessions, an explicit `timeout_ms`
sets a total command timeout; otherwise they live until exit, cancellation,
termination or inactivity. `session_idle_timeout_ms` defaults to 30 minutes and
must be positive. A stdin call/poll refreshes activity; output alone does not. `write_stdin` input,
polling, resizing and stopping continue the authorized session without additional
permission approval; the same Agent/epoch/environment ownership checks still apply.

Normal turn completion retains sessions. Runtime binds them to owner, Agent
binding epoch, Sandbox and VM generation. A main default selection change does
not move existing sessions. Workers cannot retarget or access another owner's
sessions; an old epoch cannot use a new Agent's execution scope. Completed output
is retained until read, capacity reclamation or runner expiry. Session capacity
remains profile-bounded; finished processes can be reclaimed before admitting a
new command, and a full set of live processes rejects admission.

Cancellation or failed turns drain all execution scopes for that owner. Backend
shutdown drains all owners, including completed task lanes and idle main scopes.
Cancelling an already completed task closes its retained execution resources while
preserving the saved task result. Scope cancellation kills the root-owned guest cgroup, including detached setsid
descendants. Normal idle inspection drains orphan descendants when no managed
process remains, before releasing the execution lease. Active background sessions
hold leases and block ordinary Sandbox stop/delete. Idle runners retain caches
without holding a lease or unsettled-effect receipt.

The guest expires inactive runners; Runtime reconciles them every 30 seconds and
before an Agent turn. Inspection is serialized against that owner's dispatch.
Missing/changed generations invalidate handles. Unknown cleanup outcomes retain
a durable quarantine receipt and block execution until explicit recovery.
Neither process handles nor snapshots are serialized; restarting Runtime or the
executor cannot resume them or replay commands. Conversation/artifact persistence
is independent.

## Login initialization snapshots

With `shell_snapshot` enabled (default), Bash and Zsh login initialization is
captured once per cache key, without executing the user's requested command.
The cache holds exported environment, native functions, aliases, shell options
and umask. New shells restore this baseline before executing each command; a
command cannot update it. `sh` and unsupported snapshot cases use normal startup.
Capture is noninteractive login initialization; interactive-only rc files are
not implicitly sourced.

Each runner keeps at most 16 snapshots in memory. Keys include shell identity,
cwd, HOME and the metadata/contents of standard user/system startup files.
Changing these files captures a new baseline. Arbitrary files sourced by a
profile, executable behavior behind symlinks, and external service state are not
fully tracked; disabling `shell_snapshot` uses fresh login initialization.
Caches survive normal turns while the idle runner exists and are discarded on
Agent epoch change, runner expiry, cancellation or restart.

Capture has a 10-second timeout and a 512 KiB output bound; individual replay
state/environment values are bounded to avoid exec argument limits. Capture
failure falls back to ordinary login startup and is cached until invalidation or
eviction, avoiding repeated failed capture attempts. No snapshot is written to
the workspace or product storage.

## Verification

`mise run test` includes real JSON-lines runner tests for cwd/export independence,
login initialization, snapshot reuse/invalidation/fallback, PTY input/resize,
interrupt, explicit timeout/termination and idle expiry. Runtime HTTP/Helper
doubles exercise cross-turn routing, original-environment binding, epoch and
generation invalidation, parking/reactivation, restart quarantine and cleanup.
`mise run test:executor` covers broker receipts, failed cleanup, idle expiry and
orphan drainage using isolated SQLite/cgroup doubles. See the [dated acceptance
report](validation/executor-sessions-20261007.md) for what was actually run; these are not substitutes for real Linux VM
cgroup/desktop acceptance.
