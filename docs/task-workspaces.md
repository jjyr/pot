# Task workspace selection

The receiving Pot adapter prepares and records one local workspace before
starting a Task Agent. Other adapters own their workspace rules; directing nodes
do not prepare a local workspace for linked execution. See [Executor](executor.md)
for process lifetime and authorization.

## Selection rules

Each local Pot execution has one effective workspace on its receiving node.
Apply these rules in order:

| Task intent | Workspace |
| --- | --- |
| Explicit instruction to reuse an existing worktree or modify a directory in place | The specified directory |
| Feature development in a specified Git repository | A new managed Git worktree |
| Other work in a specified project | The specified project directory |
| No specified project | A new independent directory under the destination Runtime workspace `tasks/` |

Feature development defaults to a worktree even when a source repository is
specified. An explicit in-place instruction overrides this default. Explicit
reuse of an existing feature worktree does not create a nested or replacement
worktree. A specified non-Git project uses its directory directly.

The invoking agent selects a structured workspace intent from the user's request.
Runtime validates that intent and applies these fixed path rules; it does not
extract project paths or task intent from arbitrary brief text. A repository or
directory named in reference material alone is not a selected workspace.

## Managed Git worktrees

Create managed worktrees at:

```text
<destination user home>/.pot/worktrees/<random-id>/<source-project-directory-name>

Example:
/home/jjy/.pot/worktrees/a7d6/pot
```

The destination Runtime resolves its own OS user home; this is not the invoking
node's home or a literal shell `~`. The worktree location is independent of
the Runtime data root and Agent resource directory. On Windows the same layout
is joined using native path separators beneath the destination user's profile.

The source project name is the basename of the selected source repository root,
recorded before creating the worktree. It is not derived from a remote URL, Task
title or branch name. Runtime validates it as one destination-local path component.

The random ID is four lowercase hexadecimal characters. Runtime reserves its
parent directory atomically with a newly generated ID and retries collisions;
it never reuses an occupied directory. The ID is a path grouping key, not Task
identity or an access credential. Separate Tasks receive separate reservations,
including Tasks for repositories with identical directory names.

Runtime performs an actual Git worktree creation, with a separate feature branch,
before the project's first Agent request. Before creating a new worktree, Runtime
updates the selected base branch from its remote and pins the fetched commit.
An explicitly selected base branch follows the request; otherwise use the remote
default branch, without assuming its name is `main`. An explicitly pinned commit
remains pinned rather than being replaced with a newer branch tip.

This base update is a fetch of the selected
remote branch followed by worktree creation from the resulting commit. It obtains
the latest base without running a merge or rebase in the source checkout. Runtime
preserves the source checkout, its checked-out branch and its uncommitted files.
The feature branch is created in the new worktree; branch selection is separate
from directory naming. Ambiguous remotes/default branches or a failed fetch fail
preparation rather than silently using a stale base. Existing explicitly reused
worktrees and direct-project Tasks are not automatically pulled.

Record the fetched base commit with preparation. Concurrent Task preparation must
not substitute a subsequently updated tracking ref for that pinned commit, and a
retry or resume must not fetch and change the base of an already prepared Task.
Creation failure does not silently fall back to modifying the source checkout or
copying files.

## Temporary and direct project directories

For a Task without a project, `kind=temporary` allocates a private, unique
`pot-task-<random-id>` directory under `<effective Runtime workspace>/tasks/` on
the receiving node. The API name describes an independent Runtime-created
workspace; it does not imply deletion when a process ends. The existing Runtime
workspace configuration determines this location, including `--root`,
`--workspace` and saved configuration. With default configuration it is:

```text
<destination user home>/.pot/agent-workspace/tasks/pot-task-<random-id>
```

For example, the macOS default is
`/Users/<user>/.pot/agent-workspace/tasks/pot-task-<random-id>`. A custom Runtime
workspace uses its own `tasks/` child. Remote receivers allocate from their local
configuration; callers do not supply a local allocation path. `TMPDIR` does not
affect this allocation. Tool-local temporary files and caches keep their existing
rules.

Runtime accepts configured workspace-root aliases and records canonical paths.
The `tasks` entry must be a real directory without group or other write access;
Runtime creates it and each unique Task directory with mode `0700`. Directory
handles constrain allocation to that workspace, and path/parent identities are
checked before recording the result. Preparation errors fail explicitly, including
an unavailable or unwritable root, a non-directory or symlink at `tasks`, or a
changed allocation path. There is no fallback to OS temporary storage.

For a direct project or an explicitly reused worktree, validate the specified
directory on the destination. Resolve its canonical existing path and retain the
requested project identity. Missing directories, non-directory targets and invalid
repositories fail preparation rather than becoming new empty projects or
temporary Tasks.

## One execution binding

The effective workspace determines:

- Default command and plugin cwd.
- AGENTS.md and project Skill discovery.
- `apply_patch` root and relative-path resolution.
- Protected execution workspace and its filesystem scope.
- The workspace path supplied in model context.

There is no second private Task workspace for project-bound Tasks. Command-local
`workdir` and `cd` do not change the Task binding. Global selected Skills are materialized under the destination Runtime resource
cache, outside the project. Plugin bundles remain Runtime-managed resources.
Protected tools receive narrowly scoped access to the selected resources and
Git administrative/common directories. They are not installed into user projects.

Linked Git worktrees use a `.git` file and may reference Git metadata outside the
working tree. Protected execution must account for necessary Git metadata and
selected resource access explicitly while retaining Runtime-state protection.
Workspace selection does not replace machine or command authorization.

## Worker-visible workspace information

The Worker receives only the effective current working directory as workspace
context, for example:

```text
cwd: /home/jjy/.pot/worktrees/a7d6/pot
```

Selection mode, source checkout, base branch/commit, feature branch, random path
allocation and managed-directory ownership remain Runtime metadata. They are not
automatically repeated as workspace context or directory-selection instructions
in the Worker brief. Runtime completes workspace preparation before the Worker's
first model request; the Worker starts its assigned work in the supplied cwd.

Applicable project AGENTS.md, project Skills, tool contracts and execution
permission guidance still apply. Limiting workspace metadata does not remove
those instructions. The Worker may inspect Git state through authorized tools
when its task needs that information.

## Persistence and lifetime

Record the selection mode, source project, resolved destination workspace and
managed-directory ownership with the original accepted execution. Persist a
preparation result before using the workspace so preparation retries cannot
allocate another temporary directory, worktree or branch. Model context and tools
must use that same result.

Allowance resume, same-Task owner continuation and restart recovery retain the
original workspace. Automatic failure replacement remains a separate new Task
with a new workspace. Resuming the same Task does not generate another random
ID or reinterpret the brief. If the original
directory is missing, report its loss rather than silently recreating an empty
workspace and resuming as if files survived. Existing Tasks keep their recorded
paths and directory identities, including older OS temporary directories; Runtime
does not move, recreate or migrate them to the new allocation root.

Continuation verifies the canonical path and the recorded directory identity. A
managed worktree must still resolve to its original Git administrative and common
directories, with its administrative back-reference pointing to that worktree.
Missing or replaced directories, redirected paths and changed Git associations
fail preparation; Runtime does not adopt or recreate a replacement workspace or
grant access to new Git metadata.

The recorded branch and commit describe the initial feature branch and starting
commit. They are not a mirror of the current branch or HEAD and are not updated
on continuation. Renaming a branch in place, checking out another branch,
advancing or rebasing HEAD, and detaching HEAD do not by themselves invalidate
the workspace. The Agent inspects current Git state when developing, committing
or preparing a pull request. Initial branch validation, separate branch creation
and starting-commit selection still apply when preparing a new managed worktree.

Task state, checkpoints and operation receipts remain in their owning component
stores. Downloads must resolve through recorded original file locations or
explicitly retained delivered files, rather than assuming `WORKSPACE/tasks/ID`.
Preserve delivered artifacts independently before reclaiming their temporary
source directory.

Completion, failure, cancellation and archiving retain the original workspace.
No automatic reclamation, TTL, Task deletion, cold archive or compression/restore
is implemented. Any future reclamation must confirm process/session cleanup and
retain required deliverables and continuation files; managed worktree cleanup must
also respect ownership and uncommitted changes.

## API and preparation records

`create_task` and `POST /api/v1/tasks` accept `workspace` with `kind`
(`temporary`, `directory`, `worktree`), `project_dir`, and worktree-only optional
`remote`, `base`, `branch` and `commit`. Omission selects an independent
Runtime-created workspace unless a `project_dir` is supplied, in which case it
selects direct directory use.
The invoking model selects `worktree` for feature development in Git repositories.
An omitted feature branch becomes `codex/task-TASK_ID`.

Run schema 7 persists the preparation fence and resolved workspace alongside
the original launch in `tasks.sqlite`. Task schema 12 includes the structured
intent in launch and creation receipts. Admission creates both records in one
transaction. Preparation interrupted before a ready receipt is not automatically
replayed; inspect its original effects. Supported upgrades run through the
[database migrations](database-migrations.md).

Git preparation is supervised and follows the destination machine policy.
Protected Git preparation uses SRT, including its normal host prerequisites.
Repository checkout hooks and fsmonitor are disabled during Runtime Git commands.
For GitHub HTTPS fetches, an available connected GitHub credential is injected
transiently through the normal account service and never stored in workspace
records. Credentials remain local to the executing node. Other Git authentication
follows that node's account configuration and selected execution boundary.

Runtime-created workspaces are retained for file delivery and recovery. External
storage removal can still lose their files, especially for older Tasks in OS
temporary storage. A missing original workspace fails explicitly. The new path
rule neither clears old temporary storage nor prevents tools from using it.
