# Agent isolation boundaries (proposal)

Status: design discussion, 2026-10-06. No new isolation boundary is implemented by the configurable Agent/command-session changes. See the [runtime contract](../runtime/README.md) and [Sandbox image](sandbox-image.md) for delivered behavior.

## Existing boundary

The Linux Sandbox protects the host outside its explicitly mounted data. Within the guest, `image/desktop.sh` starts Server, desktop and browser as `potdesktop`; shell tools inherit that user. Product databases, providers, native CLI HOME and task workspaces are owned by that same user. File mode 0600 protects against other users, not code running as the owner. Tool allowlists and lane-scoped HTTP authorization therefore are not filesystem or process isolation. A workspace path check on apply_patch does not restrict an arbitrary shell command.

## Separate goals

| Boundary | Intended protection | Recommended approach |
|---|---|---|
| Controller vs tool execution | Provider keys, control tokens, databases, accounting and execution state cannot be read/rewritten by shell/file tools | Separate trusted controller identity and a constrained execution broker; protect controller directories; give tools only workspace access and the explicitly required capabilities |
| One task vs another | Private files/processes cannot be read, killed or modified across tasks | Optional per-lane identities or filesystem/process namespaces; explicit shared artifact directory |
| Desktop/browser vs background work | Background code cannot bypass human takeover or read another browser profile | Separate desktop identity and brokered browser/desktop capabilities; filesystem isolation alone does not control CDP/X11 sockets |
| Guest vs host | Guest code cannot reach unmounted host files/services | Preserve the VM boundary and audit configured mounts/network exposure |

## Suggested first direction

Start with controller separation while preserving intentional shared workspaces. Both API tools and native Codex/Grok subprocesses must use the execution boundary; moving only exec_command leaves apply_patch or native shell execution with controller permissions. Controller state and credentials must be unavailable through filesystem paths, inherited descriptors/environment, same-user process inspection and control APIs. Tools should receive a task/turn-scoped capability rather than the App's root token. The model gateway and tool dispatcher remain in the trusted domain.

A small privileged launcher/broker can create execution processes under an unprivileged identity, assign ownership and kill a complete execution group. Avoid giving that identity sudo or controller privileges. The broker protocol should identify the owner, authorized workspace, resource limits and lifecycle; tools should not accept arbitrary UID/mount requests from the model. Native CLI credentials need an explicit home/identity policy and must not become generally readable by other tasks.

Add per-task isolation only when its product semantics are chosen: whether tasks intentionally share source trees, whether common caches are writable, how artifact transfer works, and who owns browser/desktop sessions. Guest namespaces/cgroups can give cheaper per-task files/processes/resource boundaries. A separate VM per task provides a stronger independent boundary but has higher startup, memory and desktop coordination costs. A directory per task alone is organizational separation.

Keep this execution policy independent of token/context/tool configuration. An embedder should be able to replace the execution backend without changing the model loop or profile semantics.

## Acceptance before claiming isolation

Run negative tests from both API shell tools and native driver tools: read/modify provider keys, control tokens and databases; inspect controller processes/descriptors; invoke root Control API; affect other task processes/files; escape the workspace through symlinks; detach child processes; bypass browser takeover. Also verify explicitly authorized sharing, normal tool execution, native login, cancellation, shutdown and artifact delivery. Current tests cover owned process-group cleanup and patch path checks, not these proposed boundaries.
