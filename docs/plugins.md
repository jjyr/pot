# Plugin packages

A plugin is a trusted locally installed directory with a schema-2 `plugin.json`
manifest. `pkg/plugin` validates identity and confined file access without
starting processes. Packages can contribute tools, owner-enabled background
services and Control-local model connection types. Unknown manifest fields fail explicitly.

```text
Package manifest + declared files
           |
     validated loader
           |
           +-- tools ----------> digest-pinned Task bundle -> executor
           +-- background -----> Control service -> assistant inbox
           `-- model_providers -> Control connection registry / metadata reader
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
`ResourceDirectory/plugins`. Installed tools are enabled by default and can be disabled in [Extensions](extensions.md); Provider
connection forms only configure models and credentials. Main turns and prepared
Tasks retain their resource snapshots. Before a Task first prepares, the original
Runner synchronizes selected current tool files and Skills through Control's
run-bound authenticated interface. Project instructions are read on the destination.

`pkg/pluginhost` owns bounded Node process sessions. Execution mode and policy revision are pinned alongside the bundle; policy changes close the old session. Tool processes follow their
execution scope and bundle digest. Cancellation, ownership checks, operation
receipts and uncertainty fences apply to plugin calls as to built-in tools.

`mise run build:plugins` packages manifests, declared capability files and optional `icon.svg` / `icon.png` presentation assets and their attribution files.
Packages contain no VM engines, images or guest bootstrap. Stop Runtime before
replacing a complete package. The Extensions page imports and updates user packages separately from packaged resources. Managed independent MCP servers use the same tool execution boundary; see [Extensions](extensions.md). A plugin marketplace and automatic package updates remain outside this contract. The
[Codex Bridge](../integrations/codex/README.md) package owns its paired remote MCP
connection and durable SSE consumer.

## Model connection contributions

`contributes.model_providers.providers` declares connection types in the manifest.
A package may declare several types. Types are static; adapters have no discovery
or `describe` RPC. [Model directory](model-directory.md) defines connection
configuration, catalogue refresh and model metadata precedence. Model inference
always uses Pot's Gateway and existing HTTP/SSE codecs.

```json
{
  "providers": [{
    "id": "example",
    "name": "Example",
    "endpoint": "https://api.example.com/v1",
    "protocol": "openai_responses",
    "protocols": ["openai_responses", "openai_chat"],
    "directory_provider": "example",
    "capabilities": {"catalog": "plugin", "allowances": true}
  }],
  "adapter": {
    "runtime": "node", "execution": "control",
    "entrypoint": "provider.mjs", "files": ["provider.mjs"],
    "credential_env": "EXAMPLE_API_KEY"
  }
}
```

`protocol` selects a Pot codec; optional `protocols` lists supported choices and
must include the default. `catalog` selects `none`, `models_dev`, `http` or
`plugin`. A `models_dev` reader requires `directory_provider`; a binding can also
supply model facts alongside HTTP/plugin discovery. `directory_provider: "*"`
expands supported directory entries. Only `catalog: "plugin"` or
`allowances: true` requires an adapter. Optional `default_credential` is a public
application identity, up to 4,096 bytes without control characters. Private keys
belong in Secrets.

A Node adapter exports `async read(input)`. The only RPC methods are
`list_models` and `read_allowances`, enabled by the matching type's capabilities:

```text
Request: {version: 1, method, connection: {id, type, revision, endpoint, protocol}}
Result:  {catalog: {read_at, models}} OR {allowances: {read_at, accounts}}
Failure: {error: {code}}
```

Failure codes are `authorization`, `permission`, `rate_limited`, `unavailable`,
`overflow` and `invalid_response`; failures cannot include a result or arbitrary
message. Models contain an ID, optional display name, input/output modalities and
independent context/input/output token limits. Allowance accounts contain plan,
user, observation/renewal data and independent windows. Windows preserve unknown
`used` separately from zero, percentages above 100, `unlimited`, reset times and
shared pools. Pool percentages must not be summed. Account/window identity is
`stable` with an ID, or `ambiguous`; mutable labels are not stable IDs. Times use
RFC3339. The complete DTO and validation bounds are in
[`pkg/core/model_providers.go`](../pkg/core/model_providers.go).

The host runs one supervised Control-local process per exchange, with a 30-second
lifetime, 3 MiB output bound, cancellation and descendant cleanup. Adapter files
are confined UTF-8 text, up to 32 files / 512 KiB. Credentials enter only through
`credential_env`; unrelated service tokens and Node loader variables are not
inherited. Stdout is the response protocol, and stderr/exception text is discarded.
The host rejects credential echoes, unknown fields and invalid results. Connection
revisions, package digests and enablement fence acceptance. These capabilities
and files never enter Task tool bundles. Quota API failures are independent of
inference availability; host policy owns monitoring and assistant delivery.

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

A package can opt into `"mode": "stream"` on `contributes.background` for a
controlled persistent connection. The host starts a supervised process independently
of the poll scheduler, passing the same version-1 input once as a JSON line. The
entrypoint exports `stream(input, emit)` instead of `poll(input)`. Each awaited
`emit(response)` writes one bounded response line; the host validates and commits
it through the same checkpoint/inbox transaction, then sends `{"accepted":true}`.
Only that ACK lets the plugin advance its in-memory checkpoint. A rejected frame
ends the process without acknowledging it. Stream frames retain the ordinary
response/event bounds; `delay_seconds` applies after exit, not to a healthy stream.
The plugin owns heartbeats, protocol retries, reconnect backoff, aggregation and
quiet-state behavior. Control does not interpret service events or add a model
consumer. A binding revision, account change, package identity change, disabled
binding or Control shutdown cancels the process and drains descendants. Host
failures retain the last accepted checkpoint and use the ordinary retry delay.
See [Codex Bridge](../integrations/codex/README.md) for an SSE implementation.

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
Plugin events grant no tool authority and provide summaries/suggestions only. A
mixed batch may also recover eligible failed Tasks through batch-scoped tools;
plugin content cannot select or authorize recovery. These turns cannot reply externally,
mark notifications read or execute instructions found in comments. A user request
arriving while a summary runs follows the existing independent Main-turn path.

Each exchange commits its checkpoint, next wake and **all** event envelopes in one
SQLite transaction with the shared `agent_messages` inbox. The next request's
checkpoint acknowledges durable acceptance, not assistant completion. IDs are
unique per Control and background instance across account/route changes; include the account
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
background instance are accepted. Task results and plugin events share one receiver, in inbox
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
`unavailable`. The settings projection also distinguishes `pending`,
`waiting_assistant`, `assistant_missing` and `plugin_missing` from known host
configuration state. Plugin discovery and host exchange failures retain their diagnostic in
settings details. Host timeouts are identified explicitly; a successful exchange
clears the previous diagnostic. Runtime status identifiers and queue counts are also
available there. Protocol/process/acceptance failures leave the old checkpoint and
schedule a five-minute retry. The plugin receives that old checkpoint again;
there is no false success receipt. Owner management uses authenticated
`GET /api/v1/plugin-notifications` and
`POST /api/v1/plugin-notifications/{plugin-id}` with
`{enabled, assistant, config, revision}`. Revisions fence stale configuration.
These are configuration ports, not public event ingestion or token-read APIs.

The event payload is the sole source of plugin title/body/URL data; the inbox
`text` field is empty.

Queued events survive restart. Claiming a summary journals its membership before
model invocation; acknowledgment follows a durable reply. Started or uncertain
attempts are marked interrupted on restart and are not automatically replayed.
This provides durable local deduplication, not global exactly-once delivery across
independent Controls/devices. The owner must inspect interrupted conversations.

### GitHub notifications

In **Settings → Third-party accounts**, the connected GitHub row starts collapsed.
Open it once to see all notification settings and the separate sign-out action.
Notifications are enabled automatically for an unconfigured connected account,
with the first assistant in this Control's persistent creation order as recipient.
No assistant means waiting: the existing background loop binds the first one when
available. An existing binding (including disabled or a missing recipient) is never
auto-replaced. Defaults are inserted atomically into the existing configuration
store; concurrent initialization cannot overwrite an explicit save. Account changes
retain configuration, while the plugin's account identity resets its own baseline.
The assistant needs an AI profile. A missing plugin, missing recipient or unavailable
credentials is shown as a failure/waiting state rather than active polling.

The account row summarizes the saved configuration and runtime status. Drafts keep
the revision they started with, survive background updates and language changes,
and require an explicit save; an outdated revision is rejected without discarding
the draft. Expansion lasts for this settings visit and resets on account changes.
All fields are directly visible in the expanded account, without advanced sections.
The default scope is **All notifications**, with no reason or repository exclusions.
This includes assignments, CI activity, invitations, security alerts, and future
GitHub reason and subject types, as well as Issue/PR updates. This scope covers
notifications GitHub exposes to the connected account; repository activity that
GitHub does not put in that account's notification feed is not reconstructed.
Choose **Selected reasons** to show labeled reason checkboxes. Existing explicit
reason selections are preserved. Repository filters remain optional comma-separated
`owner/repo` names; blank selects the account-wide feed. There is no free-form
API-value field or historical-inbox import setting.

The plugin uses only GET requests to the GitHub REST API. It requests all thread
states (including read threads), respects `X-Poll-Interval`, and overlaps its
`since` cursor by two minutes. The first scan establishes a baseline without
emitting old notifications. Account changes establish a fresh baseline. Each
complete scan advances its time cursor; an incomplete scan retains its feed,
next page, remaining page rows and original start time in the opaque checkpoint.
Restart continues that scan instead of fetching its completed pages again.
Repository filters use the corresponding repository notification endpoints.
`Last-Modified` / `If-Modified-Since` apply only to non-paginated feeds.

Notification ingestion takes priority over context expansion. Each changed,
matching subject saves its title, reason, type and source link. For Issues/PRs,
comment/review expansion is best-effort, with at most eight detail requests in
an exchange. Failed, inaccessible or slow detail reads retain the basic update
with `truncated: true`; they do not roll back another notification, a completed
feed page or the scan cursor. Other subject types use a GitHub notifications link
scoped to their repository and need no Issue/PR detail APIs. Feed access failures
still report authorization, permission, rate-limit or connection status and
respect backoff; already accepted pending updates can continue draining.

Each poll has a 25-second work budget, at most 24 total requests, and a five-second
per-request deadline, within the host's 45-second exchange limit. Large lists
continue in later exchanges. The checkpoint keeps up to 2,000 thread markers
and 128 pending groups; a full pending buffer pauses page consumption until
accepted groups drain, retaining the unconsumed rows. Same-thread updates
coalesce for at least one minute, with a five-minute collection threshold for
continuously updated threads. At most eight ready groups are emitted per five
minutes; pacing survives restart. Notification snapshots are not an event log:
deleted, transient or otherwise absent updates cannot be reconstructed. Open the
source link for complete context when an update is truncated.

UI windows may close while Control continues polling. Control/computer shutdown
stops polling and summaries; resume uses the stored cursor and scan progress.
No public server, automatic GitHub reply/read-state mutation or cross-device
coordination is added.

GitHub notification access must succeed independently of repository access.
The endpoint requires compatible OAuth/classic-token notification permissions
and applicable organization access; it does **not** support GitHub App tokens or
fine-grained PATs. Pot reuses the connected account and never upgrades scopes or
runs `gh auth login` automatically. A permission status requires checking the
existing authorization/organization policy; a connected label alone does not
prove this endpoint is usable.

## Connection allowance monitoring

Connections with the `allowances` capability use an independent background
instance per connection and source package. The plugin identity, instance routing,
queue bounds and atomic checkpoint/inbox acceptance rules above apply. Connection
settings own these monitors; the plugin notification list shows background
contributions. See [Provider contributions](model-directory.md#provider-contributions)
for model connections and their shared metadata reader.

Monitoring defaults to enabled at 90 percent. An initially empty target binds to
the first local assistant when available. Explicit disablement and thresholds
survive refresh and reconnect; removing a selected assistant never silently
retargets accepted events or future warnings. Reads without an available assistant
do not consume a warning episode.

Each known fresh window is compared independently, without adding percentages
across windows or shared pools. A window at or above the threshold starts one
connection warning episode; account and plan labels remain reference data, not IDs.
Further high readings remain quiet across restarts. Recovery requires two complete,
nonempty low observations at least 60 seconds apart with distinct source timestamps. Unknown values, failed reads, stale or expired windows and empty snapshots
cannot establish recovery; explicit unlimited windows are skipped.

Both the host read and source `read_at` or `as_of` must be within 15 minutes,
allowing at most 30 seconds of clock skew. If both source timestamps are present,
both must be fresh. Reset timestamp drift does not create another episode.
Connection routes and revisions, package identities and owner binding revisions
fence acceptance as described above. Allowance permission failures affect that capability alone;
inference and Pot's own spending policies remain independent.
