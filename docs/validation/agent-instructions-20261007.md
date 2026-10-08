# Agent instruction validation — 2026-10-07

## Change

Runtime now discovers and refreshes AGENTS.md from the selected Sandbox workspace and `~/.pot/agent/AGENTS.md`. pot-agent exposes instruction injection and does not discover files itself. Pot does not load AGENTS.override.md or impose a file size cap. Global read failures retain the last successful value; confirmed absence or empty contents clear it.

## Verification

- `mise run test`: 228 passed, 0 failed, 1 live-provider test ignored.
- `mise run clippy:backend`: passed with warnings treated as errors.
- `mise run lint`: passed. SwiftFormat could not write its optional user cache under the filesystem sandbox; formatting checks still succeeded.
- `git diff --check`: passed.

Thirteen added tests cover nearest-root traversal with directory/worktree markers, nested repositories, cwd-only discovery without a marker, ignored override files, refresh/removal, full reads beyond 32 KiB, lossy UTF-8, relative and absolute guest symlinks, rejected Host escape links, unreadable project-file errors, global last-good caching and warning recovery, default Sandbox changes, fixed task environments, and actual Runtime context injection for both engines and roles. pot-agent mock-model requests exercise OpenAI Chat, OpenAI Responses and Anthropic Messages, custom prompts, Summary exclusion and persistent-history separation. A separate regression test confirms the Host agent cwd cannot supply an implicit project document.

Local logs: `/tmp/pot-agents-md-test.log`, `/tmp/pot-agents-md-clippy.log`, `/tmp/pot-agents-md-lint.log`.
