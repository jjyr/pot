# Documentation

Start with the [documentation index](docs/README.md) and read the pages relevant to your task.

Keep documentation focused on lasting design, behavior and operating instructions.
Update documentation only to correct inconsistencies with existing records.
Do not add redundant content.
Do not add validation reports, test-run reports or development checklists to the
repository, including under `docs/`. Report checks, results and limitations in the
task response instead.

# Tool interfaces

Keep each tool focused on its responsibility. Update affected callers, tests and documentation when tool interfaces change.

# Development

## UI development

When modifying UI, read and follow the [UI development rules](docs/ui-development.md).
For chat submission or delivery changes, also follow [Chat outbox](docs/chat-outbox.md).

## UI internationalization

New or changed user-visible UI text must use the shared i18n catalog and lookup,
including notification and backend/native text shown to users. Follow the
[Internationalization rules](docs/internationalization.md) for scope, locale
handling and validation.

## Database changes

Keep all schema DDL and migrations in `pkg/storage/migrations/`. Every persisted
schema or JSON format change must include a new migration and upgrade coverage
in the same change. Follow [Database migrations](docs/database-migrations.md).

## Agent prompts

Changes to Pot agent prompts require explicit user authorization.

## Releases

Before preparing or publishing a Pot release, read [Releases](docs/releases.md).
Do not add or maintain tests for release workflows.

## Backward compatibility

When implementing features or changing schemas, protocols, APIs, or configuration:
- Update affected code, callers, tests, and documentation to the new design.
- Do not preserve backward compatibility unless the user explicitly requests it. If the user requests compatibility, implement only the requested scope.

## Pull requests

- Keep PR titles and descriptions concise; explain the problem and resulting behavior.
- Illustrate architecture changes with an ASCII diagram.
