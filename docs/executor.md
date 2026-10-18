# Command execution

Control authorizes an operation and its execution mode; the fixed destination
Runtime executes it through `pkg/executor`. Main uses its owning Control; explicit
remote Tasks use the authenticated Runner. Tools cannot change destinations.
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

Main uses `WORKSPACE/assistants/ASSISTANT_ID`; Workers use
`WORKSPACE/tasks/TASK_ID`. Instructions and Skills come from the destination.
Control resources synchronize before first Task preparation. Project replication,
VM images and host/guest path translation are absent.

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
included only on macOS.

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

Omit `yield_time_ms` to wait; specify it to return a live session. `write_stdin`
retains the original process/mode. It cannot start a new command using session
identity or cross owner/generation/Runtime boundaries. Sandbox TTY/input is
available. Host TTY and nonempty input require explicit interactive authorization
on the original command; output polling, interrupt and termination remain usable.

`apply_patch` retains workspace/path checks. Protected patches run in a dedicated
Go worker under the same OS policy, so direct Go file writes do not bypass it.
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
