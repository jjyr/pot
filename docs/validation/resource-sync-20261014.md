# Pre-execution resource synchronization — 2026-10-14

## Contract

Task launches carry selected Skill/plugin names and model/execution settings.
They contain neither resource files nor embedded plugin bundles. The destination
Runtime queries Control by name before constructing the Agent's tools or sampling
the model. No requested resource version or model-visible loading tool exists.

Global Skills resolve from Control's current directories; selected plugins resolve
from the owning assistant's current source Profile. The Runtime materializes one
active bundle set and binds its digest to the execution lease. A run stays stable;
explicit continuation synchronizes current contents again. Local tasks use the
same pre-execution materialization without a network hop. Project Skills and
AGENTS.md remain independently discovered in the actual workspace.

Control no longer writes or reads per-task control-global-resources.json.
Resource reads use authenticated run-bound queries, not effect receipts containing
bundle history. Execution-workspace caches replace prior materializations.
API revision is 3 and destination Task Agent journal schema is 2; old launch
schemas are rejected rather than migrated. Deploy matching client/Control/Runtime
revisions together.

## Validation

- Control/Core/Runner/Runtime suites: 169 / 18 / 1 / 12 tests passed before the
  additional plugin refresh test; the final resource-focused suite passed all
  four tests, including that new test.
- Server suite with an isolated temporary secret key: 63 tests passed, 3 ignored.
  The initial run without that key failed at provider setup; the documented
  temporary-key setup resolved it.
- Strengthened two-node TLS/Host integration: 5 passed, 2 Podman cases ignored.
  Checks assert that accepted launches have no resource payload or embedded plugin
  configuration, while cat reads the synchronized builtin Skill through an
  ordinary local path. Project Skills remain visible and unselected global Skills
  remain absent. Plugin review/session isolation and lease recovery still pass.
- Runtime regressions cover synchronization before sampling, one synchronization
  per execution, new contents on continuation, no resynchronization on duplicate
  completed delivery, and no model request when Control resources are unavailable.
- Resource regressions cover selected/empty/omitted global Skills, project
  independence, cache integrity, replacement of old cache directories, current
  plugin definitions and implementation from the owner Profile, and refusal of a
  removed selected plugin.
- Final focused Host integration passed with an assertion on the actual cat tool
  result containing the synchronized Skill name.
- All-target Clippy for Core/Control/Runner/Runtime/Server passed with warnings
  denied. Formatting, diff checks and six architecture boundary tests passed.

Commands used mise exec with cargo test / cargo clippy, --locked,
--no-default-features for the server, and mise run test:architecture.
Server integration used the temporary POT_SECRET_KEY_FILE setup documented in
the repository test task.

Logs: /tmp/pot-resource-tests.log, /tmp/pot-resource-server-tests.log,
/tmp/pot-resource-focused-tests.log, /tmp/pot-resource-remote-tests.log,
/tmp/pot-resource-final-clippy.log, /tmp/pot-resource-cat-test.log.

## Limits

Tests use local two-node TLS fixtures and real Host execution, not independently
deployed physical machines. Linux Podman and real macOS VM synchronization were
not exercised. Web assets were excluded from backend builds. Synchronization
currently transfers the selected resource payload (up to 512 KiB / 2048 Skill
files); cache digests avoid replacing identical local contents, not downloading
the response. No large-resource chunking or conditional-download protocol is
claimed.
