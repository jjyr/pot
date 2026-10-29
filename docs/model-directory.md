# Model connections and metadata

The node owns node-wide model connections and metadata. Builtin providers,
Custom API and optional providers such as Magpie all register through the same
[`contributes.model_providers` contract](plugins.md#model-connection-contributions).
A package contributes connection types; owners create named connections with
credentials, protocol, endpoint, available models and a default model.

The bundled [standard-model-providers](../plugins/standard-model-providers/README.md)
package expands supported models.dev entries and contributes Custom API. The same
validated manifest is embedded for hosts without a packaged plugin directory;
installed copies take precedence and both obey Extensions enablement. Standard
readers reuse Pot's protocol implementations. Optional Node adapters read provider
metadata on node; all inference goes directly through Pot's Gateway.

## Provider contributions

`GET /api/v1/providers` returns registered `provider_types`, configured connections,
their typed cached catalogs, and public model facts. Save, discovery and inference-test requests identify the connection type with
`provider_type_id`. Directory entries supply model facts and artwork, rather than
an independent list of connection implementations. UI features and request formats
follow registered capabilities and supported protocols.

Gateway routes retain the namespaced type and package identity separately from
public-directory identity. Migration 0016 assigns existing routes to
Custom API while preserving endpoint, protocol, allowed hosts, credential
references, route IDs and directory bindings. Package identity records provenance;
a package update can supply the current adapter for a future metadata read.

Public facts come from the bundled `resources/model-directory.json` snapshot of
`https://models.dev/api.json?type=all` and the last valid
`models-dev/catalog.json` under node's data directory. Source/update instructions
and licenses are retained with the assets. Bundled `resources/provider-logos/`
artwork takes precedence over directory logos. Plugin types use their package icon;
missing artwork uses the shared fallback.

The standard package maps supported directory SDK declarations to Pot's Chat
Completions, Responses, Anthropic Messages and Gemini codecs. Per-model protocol
and endpoint declarations take precedence over directory provider defaults.
Unsupported SDKs, missing endpoints, deprecated models and models without text
input/output or tool calling are excluded from ready directory choices. No upstream
SDK is loaded. The models.dev catalog used for these facts is separate from
`core.ModelCatalog`, which configures model descriptors and prompt/tool overrides.

## Catalog refresh and discovery

App and Web consume node's local projection; they do not fetch models.dev or
keep an independent upstream cache. Ordinary snapshots, selection and inference
perform no upstream metadata requests.

The node refreshes public models.dev data every 24 hours, recording the attempt in
`models-dev/check.json` before fetching. Restarts preserve that interval. Fetches
use ETag, bounded responses and timeouts; failed updates retain the last valid
catalog and logos. No credentials or connection details are sent to models.dev.

Enabled plugin catalog connections refresh at startup, after a connection save,
and every five minutes, through at most four supervised exchanges at once. This
lifecycle is independent of quota monitoring and waits for its children on
shutdown. Refreshing catalogs never invokes inference. Settings and model pickers
read cached results: current catalog IDs replace old discovered IDs, while the
saved default remains visible as owner intent if it disappears upstream.

The connection editor discovers models after a 400 ms pause when its address,
protocol and credential/public default are available. Discovery preserves opaque
model and routing-group IDs, supplements local directory choices, and never
changes the current selection automatically. Edits cancel stale requests; leaving
the editor disposes background work. Failure permits manual entry and retry.
Directory facts and discovery success do not prove inference access.
`POST /api/v1/providers/check` returns `supported` and an optional typed `catalog`
containing `read_at` and model records. An unsupported reader leaves manual model
entry available.

Catalog and allowance caches are independent, keyed by immutable route, method
and current package digest. Concurrent reads of the same saved connection share
one bounded exchange. Changed connection revisions, replaced adapters and disabled
packages fence results before publication. Discovery uses the actual scoped
credential; a response started before credential rotation cannot become facts for
the replacement route. Authorization or permission failures invalidate the affected
saved snapshot. A quota permission failure does not disable inference.

Assistant creation and settings use one editable model input with native cached
suggestions and a full-model picker that remains unfiltered by the current input.
The default Task model uses the same controls.
The connection type's directory binding selects the public catalog,
falling back to the profile's explicit directory binding. Unbound Custom API
connections can suggest IDs across the public directory. Cached connection facts
take precedence for duplicate IDs; known non-conversation models are omitted,
while saved defaults and current selections remain available. Typing an arbitrary
model ID is always allowed and browsing suggestions performs no discovery or
inference request.

## Connection ownership and editing

Connections belong to the local node and are available to its assistants. Saving one
does not create an assistant. The first saved connection becomes the node default;
changing that default initializes newly created assistants without altering existing
assistants, connection order or Task snapshots. An assistant's Add Provider flow
retains its settings draft, selects the newly saved connection when returning, and
restores the prior selection on cancellation. One-click assistant creation uses the
local node default connection and its concrete default model; missing configuration
opens connection setup.

First-use Chat offers the shared Add assistant page and never bootstraps an
assistant while reading its directory. Selecting API creation uses the default
connection or opens Provider setup, then explicitly creates one assistant with a
random two-word slug ID such as `amber-otter` and a default name such as `Amber Otter`,
using the selected UI locale for its welcome message. Nested setup in an assistant draft
returns to that draft. The explicit bootstrap API retains its one-time identity
receipt for callers that intentionally request it; ordinary page loads do not call it.

Provider setup uses the shared chooser and editor. Names and marks come from the
registered types. The editor exposes name, API URL, credential, default model,
request format, context/output limits and reasoning effort. It does not edit
assistant instructions, execution policy or tool selection. Capability-based quota
controls remain within the connection; their contract is described in
[Plugins](plugins.md#connection-allowance-monitoring).

API URLs accept HTTP/HTTPS origins, recognized version bases such as `/proxy/v1`,
or complete inference endpoints. The node shares one resolver across save,
discovery and tests. Origins default to `/v1` (`/v1beta` for Gemini); recognized
bases receive the selected protocol's suffix. Complete custom paths are retained.
A recognized endpoint for a different protocol is rejected, and the editor offers
an explicit correction. Gemini binds `{model}` at request time. URLs cannot embed
credentials or fragments; discovery also rejects query parameters.

A saved credential can be reused for discovery or tests only when resolved
endpoint, protocol and type still match the saved route. A changed provider type
requires an explicit credential. Blank fields retain a saved private key; a public
application credential declared by the type applies when there is no saved or
explicit key. Private credentials stay in node's encrypted store.

Test connection sends one short streaming inference request with the unsaved
settings. Success requires a complete decoded text reply. It does not save the
connection or write conversation history. The request has a 30-second deadline and
256 KiB response bound, follows no redirects and never retries inference.

## Model facts and parameter intent

Assistants save their own connection revision, concrete model and parameter
selection. Changing an assistant does not mutate another assistant or its source
connection. Empty parameters inherit the connection default model's settings; a
different model derives defaults from its own metadata. Explicit overrides remain
editable intent in `Profile.model_settings`, rather than replacing shared facts.

Exact cached connection facts take precedence over public-directory facts. Public
lookup first matches the complete model ID in the selected directory provider.
Custom API has no fixed directory binding: official model-family providers take
precedence, followed by provider-ID order. If the complete ID is absent, public
lookup may retry the exact suffix after the final `/` using the same scope and
precedence. It never guesses similar names or changes the requested model ID,
route, protocol or credential. Unknown facts use generic defaults.

Context uses an explicit override, otherwise a positive known context limit,
otherwise 32,768 tokens. Default output uses a positive known output limit,
otherwise 8,192, bounded below context. Explicit output must be positive, below
context and within the known maximum. Exact connection limits are applied before
public suffix facts can reject an override. A separate known input limit bounds
input and compaction. Missing/zero metadata is unknown, not a zero-token budget.

Context/output inputs remain empty when inherited and show effective placeholders;
clearing restores inheritance. Presets use decimal K/M units and only edit the
field. Switching models in the connection editor fills context and output with
the selected model's defaults. Late discovery updates these automatic values,
but preserves any limits manually edited for that model. Opening an existing
connection preserves its saved overrides.
Reasoning effort is optional free text: saving trims it, blank clears it,
and each protocol encodes it in its own field. Pot does not infer effort choices
from models.dev or restrict the provider's vocabulary.

Known text/image/audio/PDF input capabilities are intersected with implemented
protocol encodings; image support does not imply PDF. Discovery facts are scoped
to exact IDs and the matching route. Missing/incompatible optional HTTP modality
extensions retain unknown capability rather than explicit rejection. Explicit
model-catalog descriptor overrides take precedence. See
[Chat attachments](chat-attachments.md) and [token estimation](token-usage.md#context-estimation).

Fresh assistant turns, default Task creation and first resource preparation obey
current source enablement. An unavailable adapter disables its own types while
unrelated connections remain usable. Issued Gateway requests, prepared Tasks and
paused executions keep their immutable route and resource snapshots. Fresh turns
can adopt updated facts for the same saved connection revision; explicit overrides
survive those updates. Changing a connection default affects future selections,
not existing assistant intent.

## Prompt cache lifetime

A saved connection's `model_settings.prompt_cache_minutes` estimates the model
prompt cache lifetime in minutes (blank inherits 15). The provider editor exposes
one value shared by that connection's models, from 1 to 525,600 minutes. Separate
connections retain independent estimates. This is not a provider expiry query or
a guarantee that a request will read the cache.

Cache pre-compaction becomes due at 90% of this lifetime, measured from the last
successful ordinary request's start. It requires positive reported cache reads
or writes, an unchanged route and thinking setting, and context above 30%.
Unknown/zero cache activity or an expired window skips that opportunity without
falling back to standalone summarization. Queue/start checks re-evaluate the
window; preparation also respects its captured expiry. Changes recalculate
unstarted opportunities from the same anchor. A skipped/attempted history needs a
new ordinary model response before another opportunity. Context-window protection
continues independently of this cache-maintenance policy.
