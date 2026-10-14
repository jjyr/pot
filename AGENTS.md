# Documentation

Start with the [documentation index](docs/README.md) and read the pages relevant to your task.

Keep documentation focused on lasting design, behavior and operating instructions.
Do not add validation reports, test-run reports or development checklists to the
repository, including under `docs/`. Report checks, results and limitations in the
task response instead.

# Tool interfaces

Changes to model-visible tool names, additions/removals, arguments or behavior require explicit user agreement before implementation. Keep each tool focused on its existing responsibility; do not merge or split tools without that agreement.

# Development

## Backward compatibility

When implementing features or changing schemas, protocols, APIs, or configuration:
- Update affected code, callers, tests, and documentation to the new design.
- Do not preserve backward compatibility unless the user explicitly requests it. If the user requests compatibility, implement only the requested scope.
