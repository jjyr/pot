# Agent skills

Updated: 2026-10-06. Skills are filesystem resources; no skill registry or skill contents are stored in the product database.

## Resource directories

```text
skills/                         # Repo, distributed with the product
  shared/
  main/skill-manager/SKILL.md
  task/
~/.pot/agent/skills/             # Host user skills
  shared/
  main/
  task/
PROJECT/.agents/skills/          # Project bundles
```

A skill is a directory containing `SKILL.md` with YAML frontmatter (`name`, `description`, optional `disable-model-invocation`) and optional scripts, references and assets. Names follow the Agent Skills lowercase/digit/hyphen convention. The description is advertised in context; full instructions are read on demand.

Runtime provides ordered roots to the Agent. Project directories are discovered from the assigned execution workspace upwards, stopping at a repository root or the Sandbox workspace boundary. The nearest project wins, then user skills, then builtins. Within user/builtin sources, `main/` or `task/` wins over `shared/`. Project bundles use the standard flat `.agents/skills` layout. `skill-manager` is reserved: user/project attempts to override it are ignored with diagnostics.

Malformed bundles produce diagnostics and are omitted. Symlink directories and SKILL.md symlinks are skipped. Skill bundles do not recursively discover their own supporting assets as more skills. Catalog ordering and conflict resolution are deterministic.

## Runtime mapping and authority

Helper shares the Host user directory into a root-only guest source directory and shares packaged builtin files read-only. Each execution scope gets a private mount namespace:

| Source | Main scope | Task scope |
|---|---|---|
| Builtin shared + role | Read-only | Read-only |
| User skills | All role directories writable for management | Only shared + task, read-only |
| Project | Existing workspace permissions | Existing workspace permissions |

Model-visible locations are under `/mnt/pot-skills/{builtin,user}`. Task scopes cannot access raw writable shares or the main role directory. Main and task runners use distinct unprivileged Unix users, preventing tasks from accessing a main scope's writable mount through `/proc`. The root broker receives CAP_SYS_ADMIN for mount preparation; tool children drop all effective capabilities and set no_new_privs. A shared workspace group preserves desktop access to task files; this is not filesystem isolation between tasks. Root cgroup cleanup still contains each entire execution scope. Mount failures fail the operation; they never fall back to an unrestricted runner. Mapping policies live in Runtime/Helper and the guest broker, not pot-agent.

The builtin `main/skill-manager` explains management through existing Sandbox tools. Main edits files in `/mnt/pot-skills/user/{shared,main,task}`, saving directly to the Host directory. `apply_patch` remains workspace-scoped; prepare a bundle in the workspace and copy it into the user directory with `exec_command`. There is no extra management tool. Builtins are kept in the repo and packaged read-only, and can expand beyond skill-manager later.

Configuration overrides are `POT_AGENT_SKILLS_DIR` for the Host user directory and `POT_BUILTIN_SKILLS_DIR` for the packaged builtin directory. Helper supplies the latter to Server automatically. Standalone development defaults to the repo `skills` directory; a relocated standalone binary should set it explicitly. Sandboxes mount the configured directories at boot; changing the configured root requires restarting the environment. Directory contents remain live through the share.

## Shared Agent interface

`ExecutionContext.skill_resources` carries ordered `SkillRoot` entries, reserved names and an optional name selection. pot-agent discovers metadata from these supplied roots without interpreting roles, Sandbox identity or mount policy. Each root supplies the source directory and model-visible location. `AgentExtensions::skills(context)` is the common override for main/task callers; its default uses this filesystem discovery. The exported `discover_skills` and `format_skills_for_prompt` also allow other adapters to use the same catalog. Runtime injects it into Codex developer instructions.

Task creation accepts optional `skills: ["name", ...]`. Omitted discovers all eligible skills; a list narrows the discovered result, and an empty list disables skills. The normal task request record preserves this selection across restart; it stores no skill contents/index/version. An unavailable explicitly selected skill fails visibly when preparing the catalog. Editing/removing a selected skill changes later turns; this design intentionally follows current files instead of version snapshots.

Catalogs reload at each turn and append to custom/default system prompts. They participate in token estimates and compaction without entering persistent message history. Main and Worker use identical rendering. Read-only summary turns have no catalog. pot-agent does not advertise skills when the final loadout has no `exec_command` reader. Explicit `/skill:name` expansion is not implemented.

Project catalog paths refer to the default execution environment captured at turn preparation; changing the default or using another explicit Sandbox takes effect in discovery next turn. Task bindings remain fixed. Global skill paths are common to every managed Sandbox. Skills do not grant tools, Host execution or Sandbox lifecycle authority.

## Reference and tests

The metadata-first design follows Pi's `skills.ts`, `resource-loader.ts`, `system-prompt.ts`, and `examples/sdk/04-skills.ts` in `~/Workspace/pi`.

Rust tests cover role roots, project/source precedence, reserved names, YAML metadata, diagnostics, symlink exclusion, filesystem refresh, explicit task filtering/restoration, common Main/Worker model injection, custom prompts, summaries and reader gating. `mise run test:skills` checks the guest namespace policy and fail-closed syscall handling without a VM. The real Sandbox acceptance script verifies main writes reach Host, tasks read shared/task bundles but receive EROFS on writes, and builtin/main directories are hidden from tasks.
