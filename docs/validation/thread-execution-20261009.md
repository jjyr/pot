# Product-owned Thread execution validation

Date: 2026-10-09. Scope: direct Thread execution after native integration removal.

Runtime and eval invoke the concrete pot-agent executor. Thread configuration and
context live in the owner's SQLite Store. Engine, Binding, NativeRef, DriverHost,
SessionHandle, binding history, native identity callbacks and handoff/migration
stages are removed. Profile selection copies configuration at a safe turn
boundary without replacing context. Snapshot/event schema version 2 removes
external-engine installation, authentication methods and version fields.
No old conversation, configuration or usage compatibility is provided.

## Automated coverage

- Full backend tests, App/shared UI tests and deterministic eval CLI contracts.
- Workspace Clippy across all targets with warnings denied.
- Swift Helper compilation, Swift client safety, source lint and localization.
- Permission configuration/state fixtures and local documentation links.
- Sixteen source/target API protocol combinations preserve tool history and
  execute an external effect exactly once. Route-specific replay stays stored;
  portable calls/results are sent to a different provider/model/protocol.
- Missing/corrupt configured Thread contexts fail visibly instead of starting empty.
- Configuration changes wait for running or paused work, survive restart, commit
  once, preserve pending calls/receipts and do not create handoff files.
- Duplicate saves/selections, pending conflicts and stale execution updates are
  checked. Existing tasks retain their configuration after global profile edits.
- Provider changes invalidate usage calibration even when request text is identical.

Repository pre-commit and pre-push hooks also verify packaging, executor, skill
mount/authoring fixtures and dependency license policy.

## Limits

No paid model inference, interactive App/Web session, real VM/desktop regression
or full distributable bundle was run. The paid-model test and libtailscale FFI
fixture retain their explicit prerequisites. Earlier VM acceptance reports are
dated history, not acceptance of this Thread refactor.
