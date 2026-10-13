# Documentation

Start with the [documentation index](docs/README.md) and read the pages relevant to your task.

# Tool interfaces

Changes to model-visible tool names, additions/removals, arguments or behavior require explicit user agreement before implementation. Keep each tool focused on its existing responsibility; do not merge or split tools without that agreement.

# Development

## Backward compatibility

When implementing features or changing schemas, protocols, APIs, or configuration:
- Update affected code, callers, tests, and documentation to the new design.
- Do not preserve backward compatibility unless the user explicitly requests it. If the user requests compatibility, implement only the requested scope.
