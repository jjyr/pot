# Builtin Agent skills

Each direct child bundle contains SKILL.md. Main and task agents discover the
same builtin and user skills. Builtins remain in packaged `Resources/skills`;
user bundles live in `~/.pot/skills/<name>`. Builtin names are reserved against
user/project overrides. Only main agents can modify user bundles.
