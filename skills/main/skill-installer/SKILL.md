---
name: skill-installer
description: Install skills from a local directory or GitHub, inspect installed skills, or update or remove an installed user skill when requested.
---

# Install and manage user skills

Choose the destination based on the user's requested audience:

- `/mnt/pot-skills/user/shared`: main and task agents; default.
- `/mnt/pot-skills/user/main`: main agent only.
- `/mnt/pot-skills/user/task`: task agents only.
- `<project>/.agents/skills`: the selected project's skills.

List installed bundles by reading SKILL.md metadata in these directories.
Project bundles take priority over user bundles; role-specific user bundles take
priority over shared bundles. When listing remote skills, use the user's specified
repository or catalog. There is no default curated catalog.

## Install

Use the helper through exec_command. It validates the complete bundle, preserves
resources and executable script permissions, prepares a sibling staging directory,
and publishes only after validation succeeds.

```sh
python3 /mnt/pot-skills/builtin/main/skill-installer/scripts/install_skill.py --local /data/workspace/downloads/example-skill --dest /mnt/pot-skills/user/shared
python3 /mnt/pot-skills/builtin/main/skill-installer/scripts/install_skill.py --repo owner/repo --path path/to/example-skill --ref main --dest /mnt/pot-skills/user/task
python3 /mnt/pot-skills/builtin/main/skill-installer/scripts/install_skill.py --url https://github.com/owner/repo/tree/main/path/to/example-skill --dest /mnt/pot-skills/user/shared
```

For branch names containing slashes, prefer `--repo`, `--path` and `--ref`.
GitHub download uses credentials available in the current execution environment
through GH_TOKEN or GITHUB_TOKEN if present. Report authentication/download errors;
do not assume credentials are available or install extra dependencies to retry.

An existing destination fails without replacing it. For a requested update,
inspect the existing bundle and use `--replace`; failed publication restores the
previous bundle. Review imported instructions before using them. Installation
never executes imported scripts. Test scripts separately when relevant to the
user's task.

## Validate, update and remove

The shared format validator is available at:

```sh
python3 /mnt/pot-skills/builtin/main/skill-creator/scripts/quick_validate.py /mnt/pot-skills/user/shared/example-skill
```

It checks structure and metadata, not instruction quality or script behavior.
For writing or revising instructions, use skill-creator.

To remove a skill, inspect and remove only the requested bundle at its actual
location using exec_command. Check whether another same-name bundle becomes
visible through discovery priority. Report installed, updated or removed names,
locations and validation results. Changes are discovered on the next turn.
