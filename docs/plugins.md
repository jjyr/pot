# Tool plugin packages

A plugin is a trusted locally installed directory with a schema-2 `plugin.json`
manifest. `pkg/plugin` validates identity and confined file access without
starting processes. `contributes.tools` and owner-enabled `contributes.background` are supported;
removed execution Provider/resource capabilities and unknown fields fail explicitly.

```text
Resources/plugins/<package>/plugin.json + declared files
                        |
                pkg/plugin loader
                        |
              digest-pinned tool bundle
                        |
          Control machine / command authorization
                        |
        destination Go executor -> Node tool process (sandboxed on protected machines)
```

## Manifest and execution

`contributes.tools` declares `runtime: "node"`, `execution: "runtime"`,
`entrypoint`, `files`, `requirements` and `tools`. Optional `credentials` name
connected services injected into the tool process; `session_mode: "call"` closes
the process after each invocation. See [Credentials](credentials.md) and the
[bundled manifest](../plugins/computer-use/plugin.json).

Input schemas support flat scalar values and bounded string arrays. Paths are
confined relative package paths without symlinks. Tool snapshots contain only
the declared UTF-8 files, limited to 32 files / 512 KiB, and are digest-pinned.
Executors reject extra payloads and unknown capabilities. Tool installation does
not grant execution authority; each call uses [execution authorization](permissions.md). A shell command grant never authorizes a plugin. Protected plugin sessions are sandboxed; Trusted machines use host sessions.

## Discovery, synchronization and packaging

Trusted tool implementations may return a preflight failure with `isError:true`,
an `error` message, an optional `code`, and `execution_status:"not_started"` only
when no supplied operation ran. The Node host preserves these diagnostics through
the executor. Exceptions, including those thrown by evaluated JavaScript, retain
possible-effects warnings and cannot assert a preflight status. Cleanup uncertainty
remains owned by the execution supervisor.

Runtime loads packages from `PluginRoot`, defaulting to
`ResourceDirectory/plugins`. Installed tools are enabled by default; Provider
connection forms only configure models and credentials. Main turns and prepared
Tasks retain their resource snapshots. Before a Task first prepares, the original
Runner synchronizes selected current tool files and Skills through Control's
run-bound authenticated interface. Project instructions are read on the destination.

`pkg/pluginhost` owns bounded Node process sessions. Execution mode and policy revision are pinned alongside the bundle; policy changes close the old session. Tool processes follow their
execution scope and bundle digest. Cancellation, ownership checks, operation
receipts and uncertainty fences apply to plugin calls as to built-in tools.

`mise run build:plugins` packages only manifests and declared capability files.
Packages contain no VM engines, images or guest bootstrap. Stop Runtime before
replacing a complete package. Live reload, automatic installation, remote MCP
connections and a plugin marketplace remain outside this contract.

## Background services and assistant notifications

An installed schema-2 package can additionally declare `contributes.background`:

```json
{
  "runtime": "node",
  "execution": "control",
  "entrypoint": "notifications.mjs",
  "files": ["notifications.mjs"],
  "credentials": {"GH_TOKEN": {"service": "github"}}
}
```

This capability is disabled until the owner binds it to an existing local
assistant. It is independent of model tool selection. Only installed, trusted
package code runs: the service executes on the owning Control, with host access,
not on a Task's selected machine. Enabling it authorizes its background process
and declared credential reads. No background files/capabilities travel in Task
tool bundles. Stop Control before replacing installed packages.

Control uses the existing process supervisor for descendant cleanup, cancellation,
a 45-second exchange deadline, bounded protocol IO,
credential injection and durable acceptance. A fresh Node process imports the
entrypoint's `export async function poll(input)` for each exchange. Credentials
are transient environment values resolved through Integrations, never config,
argv, protocol fields or diagnostics. Processes do not survive Control shutdown.
Packages own HTTP/business behavior, polling intervals, retries, filtering,
aggregation and their opaque checkpoint. They must make repeated exchanges safe;
a failed exchange can run again with the previously committed checkpoint.

The version-1 stdin request is:

```json
{
  "version": 1,
  "now": 1767225600000,
  "accounts": {"github": "stable-account-id"},
  "config": {},
  "state": null
}
```

The host emits exactly one JSON response on stdout by serializing the returned
value (package logs must not use stdout):

```json
{
  "state": {"cursor": "next-position"},
  "delay_seconds": 90,
  "status": "ready",
  "events": [{
    "id": "account/thread/update-digest",
    "title": "Review requested",
    "body": "External text or serialized structured data",
    "url": "https://example.com/repository/pull/42",
    "occurred_at": "2026-01-01T00:00:00Z"
  }]
}
```

`events` is the standard plugin-to-assistant interface. An event contains no
sender, Control, Space, target address, system prompt or tool permissions. The
host binds its source to the installed package ID/version/file digest, and its
target to the owner's configuration. The internal `CommitBackground` adapter
accepts this trusted host binding and calls the same `inbox.EnqueueTx` primitive
used by `send_message` and task-result hooks, inside the checkpoint transaction.
It is not a model tool or a public event ingestion API. External text enters the
existing assistant conversation's notification summary lane as reference data.
Summaries have no tools and provide summaries/suggestions only; they cannot reply externally,
mark notifications read or execute instructions found in comments. A user request
arriving while a summary runs follows the existing independent Main-turn path.

Each exchange commits its checkpoint, next wake and **all** event envelopes in one
SQLite transaction with the shared `agent_messages` inbox. The next request's
checkpoint acknowledges durable acceptance, not assistant completion. IDs are
unique per Control and plugin across account/route changes; include the account
identity in IDs. Identical retries retain their original recipient and receipt;
conflicting content for the same ID rejects the whole exchange. Reconfiguring or
disabling a binding fences an in-flight response. Disabling stops future acceptance;
previously accepted events retain their original route and remain eligible for
summary. Changed account state also
fences it. Retargeting does not replay already accepted events.

Bounds are eight events per exchange, 24,000 UTF-8 bytes per body, 512 title bytes,
256 ID bytes, 2,048 URL bytes (HTTPS), 2 MiB checkpoint, 3 MiB response and
16 KiB config. Serialized event text must also fit the existing 64 KiB inbox
text bound. Wake delays are 60–86,400 seconds. At most 64 queued events per
plugin are accepted. Task results and plugin events share one receiver, in inbox
receipt order, with at most eight messages and 256 KiB of serialized envelopes
and summary payload per batch. An individually oversized task outcome is delivered
alone, preserving the existing task-result progress policy. Plugin events always
fit the single-event bound. Additional messages stay durable. Claiming a batch
has no cooldown or model charge; if a user input wins before `Report` starts,
release makes the same events immediately eligible. Existing model allowance
limits still apply. Producers own aggregation and emission pacing; Control adds
no plugin-specific delivery timer, rate table or model role. `queued` and
`attention` counts expose backlog and failed/interrupted attempts in settings.

Statuses are stable identifiers: `ready`, `baseline`, `backoff`, `rate_limited`,
`authorization`, `permission`, `overflow`; the host also reports `disabled` and
`unavailable`. Protocol/process/acceptance failures leave the old checkpoint and
schedule a five-minute retry. The plugin receives that old checkpoint again;
there is no false success receipt. Owner management uses authenticated
`GET /api/v1/plugin-notifications` and
`POST /api/v1/plugin-notifications/{plugin-id}` with
`{enabled, assistant, config, revision}`. Revisions fence stale configuration.
These are configuration ports, not public event ingestion or token-read APIs.

The event payload is the sole source of plugin title/body/URL data; its inbox
`text` field is empty. On opening an existing PR database, initialization removes
the unused persisted event `group`, clears the duplicate inbox `text` and drops
the obsolete host delivery-rate table/index. It preserves binding configuration,
revisions, opaque checkpoints (including account baselines, cursors and pending
aggregation), event IDs, original recipients and delivery states. No rebaseline
or replay is triggered by this format cleanup.

Queued events survive restart. Claiming a summary journals its membership before
model invocation; acknowledgment follows a durable reply. Started or uncertain
attempts are marked interrupted on restart and are not automatically replayed.
This provides durable local deduplication, not global exactly-once delivery across
independent Controls/devices. The owner must inspect interrupted conversations.

### GitHub notifications

In **Settings → Third-party accounts**, connect GitHub, then enable GitHub
notifications, select an assistant and save. The assistant needs an AI profile.
Optional filters are comma-separated `owner/repo` names and notification reasons.
Blank reason filters select `mention`, `team_mention`, `review_requested`,
`author`, `comment`, `subscribed`, `participating`. `subscribed` can include watched
threads; restrict reasons/repositories to reduce noise.

The plugin uses only GET requests to the GitHub REST API. It requests all
notification thread states (including read threads), follows pagination, respects
`X-Poll-Interval`, and uses `Last-Modified` / `If-Modified-Since` on non-paginated
responses plus a two-minute overlapping `since` cursor. A 304 with no accumulated
updates invokes no model. The first successful scan of recent thread state establishes a baseline without
walking the historical inbox or sending it to the model. Repository filters select
repository notification endpoints, reducing upstream reads as well as model input. Account changes establish a
fresh baseline. Offline/permission/rate-limit responses keep the old checkpoint
and back off. The Settings status distinguishes those failures.

To explicitly handle history, check **Process existing notifications on the next
check**, then save. Each checked save supplies a fresh `config.existing` request
ID (string, max 128 bytes). The plugin handles each request once after a complete
scan; the UI checkbox is not a permanent replay setting. API configuration uses
`{repositories: ["owner/repo"], reasons: ["mention"], existing: "request-id"}`.

Notifications are thread snapshots, not an event log. For changed Issue/PR
threads, the plugin expands issue comments and PR review comments/reviews,
filters using stored timestamps/content markers, and includes a reason update
when a review request/mention has no new comment. Same-thread updates coalesce
for at least one minute, with a five-minute collection threshold for continuously
updated threads. The plugin releases at most eight ready thread events per five
minutes across the connected account; the next release time is part of its opaque
checkpoint and survives process/Control restarts. Older checkpoints without that
field can release their ready events immediately. Pending threads remain in the
checkpoint until acceptance; pacing, API backoff or a stopped Control can extend
their wait. This bounds new event production, rather than delaying a claimed
summary: an existing backlog can drain in consecutive bounded batches when the
assistant is available. Context
is bounded and marked truncated when needed; use source links for full threads.
The current implementation keeps up to 2,000 thread checkpoints, 128 pending
thread groups and 20 pages per endpoint. An over-limit response leaves its cursor
unchanged with visible `overflow`; this is not a full GitHub archive or webhook
replay. Deleted, edited or transient updates absent from GitHub's notification
snapshot cannot be reconstructed. Only Issue and PullRequest subjects are expanded.

GitHub notification access must succeed independently of repository access.
The endpoint requires compatible OAuth/classic-token notification permissions
and applicable organization access; it does **not** support GitHub App tokens or
fine-grained PATs. Pot reuses the connected account and never upgrades scopes or
runs `gh auth login` automatically. A permission status requires checking the
existing authorization/organization policy; a connected label alone does not
prove this endpoint is usable.

UI windows may close while Control continues polling. Control/computer shutdown
stops polling and summaries; resume uses the stored cursor and may combine
accumulated updates. No public server, separate GitHub identity, MCP server,
automatic GitHub reply/read-state mutation or cross-device coordination is added.
