# Builtin Agent skills

Each direct child bundle contains SKILL.md. Main and task agents discover the
same builtin and user skills. Runtime maps builtins read-only into the execution
home's `.pot/agent/skills` directory alongside user bundles and reserves builtin
names against user/project overrides. Only main agents can modify user bundles.
