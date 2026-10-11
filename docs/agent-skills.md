# Agent skills

Updated: 2026-10-11. Skills are filesystem bundles. The approved Control-connected
design in [Composable runtimes](runtime-composition.md) makes Control authoritative
for global user/builtin catalogs and task-bound versions. Execution nodes receive
selected bundles and may cache them; they must not discover globals from their
own user or builtin roots. Project skill discovery remains at the actual execution
workspace. This distribution change is pending in #39.

The implementation details below describe delivered standalone/local behavior,
including the destination-global discovery that must be replaced for connected
tasks. They are not the target remote resource-management design. Standalone App
local mode retains its independent catalog. In the remote design, global creation,
installation and updates belong to Control; task views are read-only and updates
take effect for later launches. Project installations retain workspace permissions.

## Directories and discovery

```text
skills/<name>/                    # Packaged builtin bundles in this repo
~/.pot/agent/skills/<name>/        # User global bundles
PROJECT/.agents/skills/<name>/    # Project-specific bundles
```

There are no shared/main/task categories. Standalone Main and task agents discover
their owning Runtime's global user and builtin skills. Connected tasks instead
use the global versions selected by Control. Every bundle contains UTF-8 SKILL.md
with YAML frontmatter and optional scripts, references and assets. Only metadata
is advertised during discovery; full instructions are read on demand.

Runtime supplies ordered roots. Project directories are discovered from the assigned workspace upwards, stopping at a repository root or the Sandbox workspace boundary. The nearest project wins, then global user skills, then builtins. Names discovered in the packaged builtin catalog are reserved: user/project attempts to shadow them are diagnosed and ignored. Bundle names match their directory names in authored and packaged skills.

Malformed bundles produce diagnostics and are omitted. Symlink directories and SKILL.md symlinks are skipped. Supporting assets inside a bundle are not recursively discovered as additional skills. Catalog ordering and conflict resolution are deterministic.

## Unified execution view and permissions

Both user and builtin global bundles appear at the execution environment's `~/.pot/agent/skills/<name>`. Runtime injects resolved absolute paths, so models do not have to expand `~`. In the Linux provider, the executor sets HOME to its assigned execution workspace; the scope mount view is installed beneath that home. A main scope uses `/data/workspace`, and a task scope uses `/data/workspace/tasks/<task-id>`. Different providers must implement the same home-relative convention.

| Resource | Main scope | Task scope |
|---|---|---|
| Global user bundles | Writable | Read-only |
| Builtin bundles | Read-only | Read-only |
| Project bundles | Existing workspace permissions | Existing workspace permissions |

Helper shares the Host user directory and packaged builtin root into root-only guest source directories. The root broker creates a private mount namespace, binds the global user root at the home-relative skill path, and binds each builtin bundle read-only over its corresponding entry. Task scopes also remount the user root read-only. Empty reserved mountpoint directories can exist in the user backing directory; builtin files are never copied there. Symlink sources/mountpoints and failed mount preparation fail the operation without an unrestricted fallback.

Main/task runners remain separate unprivileged Unix users; task scopes cannot access raw writable shares or another main scope's writable view through `/proc`. Tool children drop effective capabilities and set no_new_privs. Shared workspace group permissions retain desktop access to task files, but do not provide mutual isolation between tasks in one Sandbox. Skills grant no extra tools or Sandbox lifecycle authority.

Configuration overrides are POT_AGENT_SKILLS_DIR for the Host user directory and POT_BUILTIN_SKILLS_DIR for packaged builtin files. Helper supplies the latter to Server. Standalone development defaults to this repo's flat skills directory. Changing the configured source root requires restarting the Sandbox; directory contents remain live through the share. No compatibility layer for the previous role-specific directory layout is provided.

## Injection and task selection

ExecutionContext.skill_resources carries ordered SkillRoot entries, reserved names and optional selections. pot-agent discovers supplied filesystem roots without interpreting roles, Sandbox identity or mounts. AgentExtensions::skills(context) is the shared override for main/task callers.

Task creation accepts optional `skills: ["name", ...]`: omitted discovers all skills, a list narrows discovery, and an empty list disables injection. The normal task request record preserves selected names across restart, not skill contents or versions. Unavailable selected skills fail visibly when preparing a turn.

Catalogs reload each turn and append to the system prompt without entering persistent history. Summary turns omit the catalog, and pot-agent does not advertise skills if the final tools lack exec_command. Explicit `/skill:name` invocation is not implemented. Main project discovery reflects the default Sandbox captured at turn preparation; task environment bindings remain fixed.

## Creation, installation and validation

Builtin skill-creator describes format, concise instruction design, supporting resources and validation. Builtin skill-installer handles local/GitHub installation, updates, listing and removal. Their instructions expose only the unified skill path, project paths and usable commands; they do not describe source storage or mounts. Tasks can read these same instructions, but readonly global mounts prevent global mutations.

The creator's scripts/quick_validate.py validates YAML metadata and scaffolds, and checks ordinary resource files and inline local Markdown links. Authored bundles require explicit names and matching directory names. Standard license/metadata/allowed-tools fields may be preserved but do not grant tool permissions. Shared format fixtures exercise Rust discovery and Python validation. Validation does not execute scripts or establish behavioral quality.

The installer uses the adjacent creator validator. It accepts a local bundle, GitHub repo/path/ref or tree URL. Downloads use only credentials available in the current execution environment. Archive extraction rejects traversal and special files. Installation preserves resource contents and executable permissions, normalizes readable permissions, validates before publishing, and refuses replacement without --replace. Ordinary publication failures restore the previous directory; crash recovery and concurrent installers are not transactional.

apply_patch remains workspace-scoped. Prepare global edits in the workspace and publish them through the installer with exec_command. Installation defaults to the current user's `~/.pot/agent/skills`; project installations use --dest. Changes are discoverable next turn.

## Verification

The guest image pins py3-yaml 6.0.2; local helper tests use a repository-local venv with the same PyYAML version. Run `mise run test`, `mise run test:skill-authoring`, `mise run test:skills` and `mise run clippy:backend`. CI runs both Python suites.

Runtime tests verify common catalogs, absolute home-relative locations, builtin name protection, project precedence, fixed task bindings and selection restoration. Portable mount tests inspect bind/remount arguments and setup failures. Real dual-VM acceptance checks the resolved home path, main user writes, builtin EROFS, task user/builtin EROFS, task directory-creation rejection and packaged validator/installer execution. Acceptance evidence is recorded separately in the dated validation report.
