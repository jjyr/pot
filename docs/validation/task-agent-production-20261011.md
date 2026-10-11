# Destination Task Agent production integration — 2026-10-11

## Delivered and checked

Production remote Worker dispatch starts a new Agent at the fixed execution
Runtime. Main assistants remain at their owner. Control retains exact model
configuration, provider credentials, budgets, approvals and guidance. Execution
nodes need no assistant database and never discover their own global catalog.

Both endpoints persist effect intent/acceptance/result identities. Control records
request digests rather than copies of the live Agent input context. Model reply
bytes are saved before acknowledgment; reply loss queries the original receipt.
The destination saves context before guidance acknowledgment and retains it for
later runs of the same pinned task. Missing selected skills fail before sampling.
The destination's actual project AGENTS.md/Skills remain available.

Same-process lease recovery issues a new ID after issued tool and Control service
receipts settle and are reconciled. The original run, configuration, target,
Control session and destination boot remain fixed. Old leases stay fenced and
new execution scopes cannot reuse old sessions. Unknown results block
continuation/replacement. Terminal results wait for effect reconciliation and
confirmed execution-scope cleanup; a result alone does not release the lease.

Execution reboot never claims an old Agent again. Known receipts/scopes settle
before interruption; confirmed tool results are reconstructed into the original
pending context, while absent/unknown calls remain pending. Explicit guidance starts a new run with saved destination
context. Control restoration retains the original active input identity and
reconciles its accepted run instead of launching another Agent. These restart
rules currently have journal/coordinator tests; independent process-crash
qualification remains outstanding.

## Evidence

- Core: 18 tests; Agent: 72; Runtime: 172. Includes current Assignment upgrade to
  destination Agent placement, original remote input identity after restoration,
  reboot interruption, project/global rule separation and missing-skill refusal.
- Server: 29 unit tests passed, one existing opt-in ignored. Recovery test rejects
  unknown tool outcomes, rotates only a live same-boot run, preserves run identity,
  returns the same replacement on retry and rejects the old lease. A previous-boot
  transport test blocks on an unknown receipt, reconstructs the confirmed result,
  retains unmatched pending calls and the original request, and verifies repeated
  inspection never claims/replays the stopped Agent. CLI connection diagnostics
  now preserve the underlying error chain instead of generic rejection text.
- Host HTTPS integration: five passed; two Linux Podman tests are opt-in locally.
  Actual commands, owner approval, fixed plugin definitions, same-run 41→42 and
  rejection of cross-run sessions, project rules/Skills, follow-up context,
  pre-start disconnection and receipt recovery passed. A 32-second disconnected
  interval exceeded the lease duration; no new model sampled while disconnected,
  the original run continued, and the command's file contained exactly one X.
- Actual devpc rootless Podman: both opt-in tests passed (36.70 seconds). Two
  Runtime instances in one test process exercised new destination Agent execution,
  two task turns, project rules/Skills, owner approval, post-task Sandbox management
  and pinned plugin session isolation. Isolated test storage/image were reused.
  This qualifies actual Linux containers, not a physical cross-machine connection.
- Workspace/all-target Clippy with warnings denied passed. Formatting and staged
  checks are required before the local commit.

All model responses above are deterministic fixtures. No paid model or new UI
acceptance is claimed. Real independent-process crashes, physical cross-machine
App/Web flows, supported real providers and Apple VM qualification remain tracked
in #39. Remote desktop transport remains explicitly deferred.
