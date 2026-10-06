---
name: skill-manager
description: Install, create, update, inspect or remove user skills when the user asks to manage Agent skills.
---

# Manage user skills

User skills are filesystem bundles. The Host directory `~/.pot/agent/skills` is
mapped writable into your execution Sandbox at `/mnt/pot-skills/user`.
Use existing `exec_command` and `apply_patch` tools; no management API is needed.
Shell commands can write the mapped directory directly. `apply_patch` is scoped
to the workspace: prepare edits there, then copy the resulting bundle into the
mapped directory using `exec_command`.

Choose the requested audience:
- `shared/`: main and task Agents.
- `main/`: main Agent only.
- `task/`: task Agents only.

A bundle is a named directory containing `SKILL.md` with YAML frontmatter:

```markdown
---
name: example-skill
description: Describe what this skill does and when to use it.
---

Instructions go here. Resolve references/scripts/assets relative to this file.
```

Use lowercase letters, digits and single hyphens for names (at most 64 characters).
Descriptions must be nonempty and at most 1024 characters. Keep supporting files
inside the bundle, use ordinary files/directories rather than symlinks, and make
files readable by task Agents (`chmod -R a+rX BUNDLE`). Prepare a complete bundle
in a sibling temporary directory and rename it into place when installing.
Review existing files before updating; remove only the bundle the user requests.

Builtin skills live under `/mnt/pot-skills/builtin` and are read-only. Never
replace the reserved `skill-manager` name. Project `.agents/skills` takes priority
over user skills, which take priority over builtins. Within a source, role-specific
skills take priority over shared skills. Catalog changes take effect next turn.
Skills provide instructions and resources; they do not change tool permissions.
