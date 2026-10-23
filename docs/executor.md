# Command execution

The owning node authorizes each operation and execution mode, then executes it
locally through `pkg/executor`. Agent Link peers execute their own accepted work.
Tools cannot change their fixed execution destination.
See [permissions](permissions.md).

```text
Dispatcher -> machine/default + exact command authorization
           -> destination Go Executor
                +-- sandbox -> Node SRT SDK -> Seatbelt / bwrap -> workload
                +-- host --------------------------------------> workload
                                      |
                          separate process supervisor
                          receipts / output / PTY / cleanup
```

## Workspaces, files and environment

Main uses `WORKSPACE/assistants/ASSISTANT_ID`. Workers bind their selected project,
a managed Git worktree, or an OS temporary directory before their first model
request. Commands, patches, plugins, instruction discovery and protected execution
use this same binding. [Task workspace selection](task-workspaces.md) defines
selection, base refresh, directory naming and recovery. Selected local resources are prepared before execution and remain fixed in the
run's resource cache.

Protected launches require packaged Node/SRT assets and a confined workspace.
Workspace/project files are writable. System libraries, selected installation
roots and the immutable plugin bundle are readable; unrelated user directories
and Runtime state are excluded. The launcher itself must not be inside a workload
write root. It generates a private home, temp and cache directory for each launch.
Do not interpret a writable workspace as data-loss protection.
System read roots are selected by the execution OS before checking path existence.
macOS installation roots and Linux-specific CA, font and browser configuration
paths are not granted on the other platform. Unsupported platforms fail closed.
The execution environment preserves a nonempty host `PATH`. Otherwise it uses
standard executable directories for the execution OS; `/opt/homebrew/bin` is
included only on macOS. Ordinary command shells retain their inherited or
shell-initialized PATH and project toolchain; Pot does not inject its bundled
Node directory. Plugin hosts use the configured bundled Node and prepend its
`bin` directory for child tools after applying execution environment settings.
Within plugins, bundled node/npm/npx take precedence over tools already on PATH.
The Node distribution, including npm/npx and their relative modules, is read-only
in protected launches. Project commands must install versions requested by their
own toolchain manager; Pot does not silently substitute versions.
An inferred tool installation root must not contain the writable workspace. For
a shared `bin` directory beside the workspace, only that executable directory is
granted read access, keeping sibling state private and workspace writes intact.

IP networking is unrestricted; no SRT proxy variables are injected. Unix IPC is
restricted independently; necessary macOS DNS support is narrowly allowed.
The macOS DNS configuration and certificate trust services are explicitly allowed
for native resolvers and TLS verification.
macOS system path aliases and DNS, timezone and shell-selection symlinks are
readable as links; their targets still require explicit read access. This lets
tools reach the allowed DNS configuration and CA bundle through `/etc` without
exposing other system files.
Linux preserves allowed system-file symlink names alongside their resolved DNS
and CA files. Links recreated beneath hidden directories do not expose their
targets; each target must be independently readable.
Only public CA bundle files and the certificate directory are readable beneath
`/etc/ssl`; the entire TLS configuration/private-key tree is not granted.
Docker and SSH-agent Unix sockets remain inaccessible, including sockets inside
readable workspaces and Linux abstract sockets. Access requires an authorized host
command; ambient `DOCKER_HOST` and `SSH_AUTH_SOCK` are not inherited.
Runtime/model authority is not inherited through ambient environment variables.
Service credentials are explicitly injected after authorization. Reserved startup,
loader and Runtime environment names cannot be used as credential destinations.

Host mode deliberately bypasses SRT and has the service account's privileges.
The product still starts it with a reviewed environment and supervises its process
lifetime. Host permissions include access to local services and other effects
outside the original process tree; cleanup does not reverse such effects.

## Commands, sessions and initialization

`exec_command` creates an independent command/cwd/environment. `cd` and `export`
do not mutate later calls. Shells must be installed absolute sh/bash/zsh paths.
Host access is requested with `request_unsandboxed:true` and a nonempty
`justification`. Omitted/false follows owner policy and existing grants. This is
permission intent, not a model-selected execution backend.

Canonical context is resolved on the destination before approval and checked
again against the actual request. Mode and policy revision are authenticated.

Product login initialization runs inside the selected execution mode on each
launch. It does not capture/replay an unsandboxed login snapshot before entering
a protected shell. The configured snapshot flag remains part of command identity;
standalone trusted executor hosts can use the bounded snapshot cache explicitly.

Commands wait up to `yield_time_ms` (10 seconds by default; the default maximum
is 30 seconds), then return a live session when still running. This bounds the
response wait, not the command lifetime. `timeout_ms` is not accepted. Use
`write_stdin` to collect later output or explicitly interrupt/terminate a command.
Cancellation, execution-scope shutdown and the configured session idle timeout
still stop the original process and require cleanup proof. Shell configuration uses
`default_yield_ms`, `max_yield_ms` and `session_idle_timeout_ms`; the retired
`default_timeout_ms`/`max_timeout_ms` fields are rejected. An offline upgrade of
persisted configurations must remove those fields and regenerate affected typed
specifications and their hashes before starting the new Runtime.

`write_stdin`
retains the original process/mode. It cannot start a new command using session
identity or cross owner/generation/Runtime boundaries. Sandbox TTY/input is
available. Host TTY and nonempty input require explicit interactive authorization
on the original command; output polling, interrupt and termination remain usable.
For a Host PTY, set both `tty:true` and `interactive:true` on `exec_command`.
The dependency is checked before shell initialization. Noninteractive servers and
test commands can use pipes and a live session without allocating a PTY.

`workdir` must name an existing directory before the shell starts. Create a new
directory from an existing working directory in a separate call first. A missing
or non-directory workdir rejects the command without creating files.

`apply_patch` accepts absolute paths and workspace-relative paths. OS filesystem
permissions determine accessible targets. Exact file grants allow in-place writes;
directory grants also permit atomic replacement. Protected patches run in a dedicated
Go worker under the same OS policy, so direct Go file writes do not bypass it.
Before any mutation, the worker selects a strategy for each target from a private
executor-supplied snapshot of the workspace and configured sandbox write roots.
Only an existing regular-file content update covered by an exact file grant and
no directory write grant selects in-place writing. Creates, move destinations,
workspace/directory writes and host `ApplyPatch` calls retain atomic replacement;
deletes still remove directory entries and cannot use in-place permission.
Unknown or unresolvable grants never select in-place writing. The snapshot is
strategy information, not an OS permission override or model-selectable mode.
For patch launches, file write entries already covered by a directory grant are
omitted from the mount list; redundant Linux file binds would otherwise obstruct
atomic replacement. This does not add any writable paths.
Once selected, a strategy's errors are returned without trying another strategy,
including `EBUSY`, permission and read-only filesystem errors. There is no
temporary-file probe or mount-table inference of authorization.

On Linux, a writable file bind mount cannot be replaced by `rename` (`EBUSY`),
even when its hidden parent accepts temporary files. Updates to existing files
can instead write through the granted inode. This path pins and verifies the
parent and file identities, refuses symlinks and multiple hard links, and takes a
nonblocking advisory lock before comparing the original contents. It preserves
the inode and its metadata, subject to the OS's normal write rules (for example,
clearing set-ID bits or file capabilities). File-only grants do not
authorize creating, moving or deleting directory entries in the host parent.
The pinned Linux SDK can overlay a separately allowed read-only ancestor over
a file write mount; that combination remains read-only and fails explicitly.

In-place updates write before truncating and fsync the result. An I/O error
attempts to restore and fsync the original contents; restoration errors report
possible partial effects. These updates are not atomic for readers or crashes,
and the advisory lock only serializes cooperating in-place patch writers.
Uncooperative writers may race after the content check. Directory grants retain
atomic replacement; multi-file patches remain nontransactional. No failure
automatically retries outside the sandbox.

Persistent plugin sessions pin bundle, observation mode, execution mode, revision
and environment. Policy changes invalidate them rather than upgrading JS memory.
Plugin processes start in their configured execution workspace in both host and
sandbox modes. When a standalone plugin host has no configured workspace, it
inherits the caller's working directory.

## SDK adapter and packaging

`sandbox/package-lock.json` pins SRT 0.0.80 and its dependencies. Packaging uses
npm integrity verification with lifecycle scripts disabled and applies exact
SHA-256-checked SDK changes from `sandbox/patch-sdk.mjs`. Source drift fails the
build. The extension exposes explicit unrestricted-network configuration, skips
proxy infrastructure, and keeps macOS Unix socket restrictions independent of IP
network access. It does not enable weaker namespace or network settings.

The trusted Node helper receives JSON over private pipes, generates argv and
retains SDK mount bookkeeping until workload cleanup. It is not the workload's
Node REPL. One helper belongs to one command or plugin session; global SDK state
is never shared by unrelated work. Files/paths are literal entries, not patterns.

Linux hides unreadable directories with private mounts. Writes may succeed in
those overlays, but do not modify the original host tree. A successful syscall
there is not evidence that a host file was changed.

Linux needs working user/PID namespaces, bubblewrap, ripgrep, SDK seccomp assets
and the SDK's declared dependencies. Missing support fails explicitly. macOS
requires the native packaged build for process inspection. No unavailable sandbox
silently becomes host execution.

## Lifetime and recovery

An independent trusted supervisor closes private control descriptors before
workload execution and emits a separate cleanup receipt. Linux uses a child
subreaper to reap detached descendants. macOS uses stable original process
identities and per-sandbox policy membership probes, including detached members;
these native interfaces are qualified by the real sandbox suite. Host authority
is broader than this protection and is not an OS isolation guarantee.

Cancellation/timeout stops the original execution and drains descendants, terminal
handles, plugin/browser sessions and SDK helpers. Missing/failed cleanup receipts
are errors. Only confirmed cleanup permits temporary directories to be removed;
project files persist. This is not VM/cgroup isolation or CPU/memory/disk quotas.

Uncertain outcomes retain capacity and durable receipt fences. Restart/disconnect
never launches a replacement or moves work. Inspect the original identity and
explicitly reconcile recovery; stopping computation does not prove external
side effects were rolled back.

## Model-visible results

Execution results include `execution_mode` (`host` or `sandbox`) and
`execution_status`. Commands report `running` with a live session or `finished`
when settled; exit codes and stderr remain the evidence of shell success/failure.
A nonzero exit, including a permission error, does not identify a sandbox violation.

A known failure to prepare the OS sandbox reports `preparation_failed`; ordinary
execution errors report `failed`. Validation/authorization failures before
execution report `not_started`. Missing cleanup proof reports
`cleanup_unconfirmed` and retains the terminal uncertainty fence rather than
continuing the Agent. A selected boundary on a preparation failure is not evidence
that workload code ran. Structured diagnostics and partial results survive Agent
error conversion. Failed operations are not automatically replayed.

Known TTY-argument and working-directory startup rejections report `not_started`
with an explanation that the command did not run; ordinary shell failures retain
their exit codes and partial output.
