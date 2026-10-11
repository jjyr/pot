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
reconciles its accepted run instead of launching another Agent. These rules now have journal/coordinator and physical cross-machine
independent process-crash qualification.

## Evidence

- Core: 19 tests; Agent: 72; Runtime: 172. Includes current Assignment upgrade to
  destination Agent placement, original remote input identity after restoration,
  reboot interruption, project/global rule separation and missing-skill refusal.
- Server: 30 unit tests passed, one existing opt-in ignored. Recovery test rejects
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

## Independent processes on two physical machines

A Linux standalone Control and a macOS standalone Host Runtime ran as separate
CLI processes with separate private roots. The execution Runtime polled outward
across the tailnet over HTTPS with a one-day server-use certificate and explicit
CA. The model fixture ran outside the Control process so its counter survived
Control termination. No existing application/provider/VM service was modified.

- Control was killed after the tool wrote X, then stayed down for 33 seconds.
  The destination made no new model requests and did not replay the command.
  Restarting the same Control root restored the original accepted run, reconciled
  issued receipts, rotated the expired lease and finished with exactly one X.
- Execution Runtime was killed while its next task waited for owner approval.
  Approval settled at Control; destination restart reconciled the old run and
  left the task interrupted. No new worker model request or command occurred.
  Explicit task guidance started a new run on the new boot, preserved context
  and reported without executing the old command.
- Exactly four Worker requests were observed across these two tasks and the
  explicit continuation. Every Worker request used the selected fixture model
  and contained the destination project rule. Destination product files had no
  provider fixture secret or assistant database. Both fixture Runtime processes
  and the mock server were stopped after verification.

This test discovered cross-build JSON order differences in protocol signatures
and resolved configuration checksums. Protocol signatures/digests, newly resolved
specs and global snapshot revisions now sort nested objects before hashing. Local
receipt/session path identities retain their existing encoding. Existing resolved
specs still require a verified full-payload checksum; changed content is rejected.
An explicit launch rejection now reports its exact error instead of pretending
acceptance was lost and waiting 30 seconds. Ordering tests also run with
`serde_json/preserve_order` to cover the App dependency feature set.

The repeatable harness is `scripts/tests/control-process-recovery.py`. It requires
caller-supplied SSH host, binaries, listener IP, fresh isolated roots and remote
script path; no machine connection facts or credentials are embedded in it.
The non-secret evidence records original run identity, boots, phases, effect
contents and Worker sample count. This qualifies standalone CLI Control/Host
recovery, not native App/Web clicking, a real model, OS sleep or power loss.

All model responses above are deterministic fixtures. No paid model or new UI
acceptance is claimed. Physical cross-machine App/Web flows, supported real providers and Apple VM qualification remain tracked
in #39. Remote desktop transport remains explicitly deferred.
