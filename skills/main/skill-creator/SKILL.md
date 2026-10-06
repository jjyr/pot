---
name: skill-creator
description: Create or update a skill when the user wants reusable instructions, scripts, references or assets for Pot agents.
---

# Create or update a skill

Write guidance that changes how an agent performs the requested task. Keep the
name and description specific enough for discovery. Put essential instructions
in SKILL.md and substantial conditional detail in references, loaded only when
needed. Add scripts for repeated or fragile operations and assets for generated
output; omit unused directories and scaffolds.

## Location and audience

Use these writable skill directories:

- `/mnt/pot-skills/user/shared/<name>`: main and task agents; default audience.
- `/mnt/pot-skills/user/main/<name>`: main agent only.
- `/mnt/pot-skills/user/task/<name>`: task agents only.
- `<project>/.agents/skills/<name>`: skills specific to the selected project.

Follow the user's requested audience and project. Project skills take priority
over user skills; role-specific user skills take priority over shared skills.
Inspect an existing bundle before editing it.

## Format

A bundle is an ordinary directory, named after its skill, containing UTF-8
SKILL.md and optional scripts/, references/ and assets/ directories. Use ordinary
files rather than symlinks. Keep supporting resources inside the bundle and refer
to them relative to SKILL.md.

```markdown
---
name: example-skill
description: Describe the concrete capability and when to use it.
---

Instructions for the task. Read [the schema](references/schema.md) when needed.
```

The YAML header starts and ends with a line containing `---`. `name` and
`description` are required for authored bundles. Names are 1–64 lowercase ASCII
letters, digits or hyphens, with no leading, trailing or consecutive hyphens.
Descriptions are nonempty and at most 1024 characters. SKILL.md is at most 1 MiB.
Optional `disable-model-invocation: true` hides the skill from automatic model
selection; use only when requested, and explain that explicit skill invocation
is not currently supported. Optional `license`, `metadata` and `allowed-tools`
can be preserved when importing standard bundles; they do not configure Pot tool
permissions. `agents/openai.yaml` is not used by Pot.

Only name and description are advertised during discovery. The body and resources
are read when needed. Instructions should preserve the user's scope, avoid
restating enforced permissions, and provide concrete steps only where useful.

## Prepare and validate

Use apply_patch to prepare or edit a bundle in the workspace. Validate it with:

```sh
python3 /mnt/pot-skills/builtin/main/skill-creator/scripts/quick_validate.py /data/workspace/draft/example-skill
```

The validator checks YAML, metadata, unfinished placeholders, ordinary resource
files and local Markdown resource links. It does not execute bundled code or
prove that the instructions produce good decisions. Run new or changed scripts
with representative inputs and inspect their output when appropriate.

For a new global skill, publish the validated bundle with the installer helper:

```sh
python3 /mnt/pot-skills/builtin/main/skill-installer/scripts/install_skill.py --local /data/workspace/draft/example-skill --dest /mnt/pot-skills/user/shared
```

For an update, use the same command with `--replace` after reviewing the existing
bundle. Project bundles can be edited directly with apply_patch; validate their
final directory. Report the location and checks performed. Catalog changes take
effect on the next turn, without restarting the agent.
