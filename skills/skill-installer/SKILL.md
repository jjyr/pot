---
name: skill-installer
description: Install skills from a local directory or GitHub, inspect installed skills, or update or remove an installed user skill when requested.
---

# Install and manage user skills

Global skills are installed at `~/.pot/agent/skills/<name>`. Control owns global skill installation and updates for connected execution.
Task Agents receive fixed read-only bundles and cannot install/update global skills.
Independent App local mode uses its own global catalog. Use `--dest <project>/.agents/skills` when the user
requests a project-specific installation.

List installed bundles by reading SKILL.md metadata in these directories.
Project bundles take priority over user global bundles. When listing remote
skills, use the user's specified repository or catalog. There is no default
curated catalog.

## Install

Resolve helper paths from the injected catalog, relative to the relevant SKILL.md.
Global commands must run on Control (or the independent local owner); on execution
nodes use only explicit project `--dest` paths. Do not write into the supplied cache.

Use the helper through exec_command. It validates the complete bundle, preserves
resources and executable script permissions, prepares a sibling staging directory,
and publishes only after validation succeeds.

```sh
python3 <injected-skill-installer-path>/scripts/install_skill.py --local /data/workspace/downloads/example-skill
python3 <injected-skill-installer-path>/scripts/install_skill.py --repo owner/repo --path path/to/example-skill --ref main
python3 <injected-skill-installer-path>/scripts/install_skill.py --url https://github.com/owner/repo/tree/main/path/to/example-skill
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
python3 <injected-skill-creator-path>/scripts/quick_validate.py ~/.pot/agent/skills/example-skill
```

It checks structure and metadata, not instruction quality or script behavior.
For writing or revising instructions, use skill-creator.

To remove a skill, inspect and remove only the requested bundle at its actual
location using exec_command. Check whether another same-name bundle becomes
visible through discovery priority. Report installed, updated or removed names,
locations and validation results. Changes are discovered on the next turn.
