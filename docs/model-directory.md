# Model directory and connection defaults

Control owns one shared, credential-free model directory. App and Web obtain its
projection with `GET /api/v1/providers`; neither host fetches models.dev or keeps
its own upstream cache. Assistants and Tasks share the Control configuration
handle. The existing model catalog for prompts and tool overrides remains
separate from these public model facts.

## Sources and refresh

`resources/model-directory.json` is a reduced, bundled snapshot of
`https://models.dev/api.json?type=all`, with common provider SVG marks from
`https://models.dev/logos/{provider}.svg`. Builds do not require these endpoints.
The snapshot's source, update command and license are recorded alongside it.
Bundled color brand marks in `resources/provider-logos/` take precedence over
directory marks, including cached and refreshed marks. Their source revision
and license are retained alongside the SVGs. Other providers retain their
directory artwork, including brands whose original marks are monochrome.

Control loads the last valid `models-dev/catalog.json` under its data directory,
or the bundled snapshot if the file is missing or invalid. Ordinary settings,
save, selection and inference paths use in-memory data without upstream I/O.

An independent Control lifecycle task checks upstream every 24 hours. It records
the attempt in `models-dev/check.json` before requesting public data, including
when the request fails. Restarting Control does not bypass the interval. There
are no extra retries after failure. Conditional requests use ETag. Responses have
timeouts and byte bounds; parsed model identities and limits are validated before
an atomic file replacement and in-memory publication. A failed fetch, parse or
write leaves the existing catalog intact. Provider logo downloads are bounded
and concurrent; failed logo updates preserve previous marks. A missing mark uses
the shared UI's fallback icon. No API keys or configured connection details are
sent to models.dev. Shutdown cancels the refresh task.

The cache is an internal detail and has no product status, refresh button or
failure notification.

## Ownership and defaults

Providers are Space-owned named connections with credentials, protocol, endpoint,
available models and a concrete default model. Saving a Provider stores the
connection independently of assistant registration. On first-use Chat, the UI
explicitly calls `POST /api/v1/assistants/bootstrap` with its locale after connection setup
or when opening an empty directory with an existing Provider. Control atomically
creates one default assistant named Pot only if the Space still has no assistants
and has a default Provider. Concurrent clients return the same identity; existing
assistants and saved names are preserved. Nested Provider setup in an assistant
draft resumes that draft without bootstrapping Pot. The Space's default Provider
initializes new assistant forms; its concrete default model ID is visibly selected. Provider setup is a
separate chooser and connection editor; search and provider selection appear only
in the chooser. The editor contains name, API URL, credential, default model,
request format, context/output limits and reasoning effort in that order. It never
edits assistant instructions, execution policy or tool/plugin selection.
New connections have an editable default name: the selected provider name, or
Custom Provider with a numeric suffix when needed. API URLs accept a complete
inference endpoint or a base ending in a version segment such as `/v1`,
`/proxy/v1` or `/v1beta`. Control appends `/responses`, `/chat/completions`,
`/messages` or `/models/{model}:streamGenerateContent` for the selected protocol.
An origin without a path defaults to `/v1` (Gemini: `/v1beta`). Complete custom
paths are preserved without guessing additional prefixes. Recognized full
endpoints with a different protocol are rejected; the editor offers an explicit
format correction. The editor previews Control's resolved request URL without
rewriting the input. Save, discovery and connection tests share this resolver;
saved routes contain the resolved endpoint. Existing routes are not rewritten.
Gemini substitutes the path's `{model}` placeholder at request time so assistant
model overrides use their selected model.

The first saved Provider automatically becomes the Space default. Settings shows
that default with a filled star and a Default badge in the configured Provider
list; other rows offer an outlined star to select a new default. These controls
retain the directory's existing brand artwork and colors. Default changes save
immediately without reordering the list. While a save is pending, star controls
are disabled and the previous default remains visible until acknowledgement.
A failed save retains that default and offers an inline retry. Selecting a
default affects new assistant forms, not existing assistants or Task snapshots.

An assistant's Add Provider shortcut retains its draft while opening that same
connection flow. Saving selects the new connection and its default model in the
resumed assistant form; it does not create the assistant. Cancelling restores the
original selection. The creation form requires a configured Provider. Changing
Provider selects its default model. There is no synthetic "Default model" option
and no separate Space-level default model overriding the Provider default.

Assistants save their own Provider/model/parameter selection. Empty assistant
parameter fields inherit the selected Provider default model parameters; a
different model derives its context/output limits from that model's metadata.
Explicit assistant overrides take precedence. Changing one
assistant does not mutate the Provider or another assistant. Updating a Provider's
default model affects future selections only. Tasks snapshot their chosen
configuration. Providers and their current models are visible to all assistants
within the owning Space, while credentials stay at Control.

## Provider and model selection

The default chooser shows common providers and Custom API. More providers expands
the remaining supported providers in place and can collapse again. Search always
queries the entire local supported directory regardless of expansion state.
Names, model options and marks come from the directory. Custom API uses the shared
settings gear icon. Pot maps known SDK types
to its Chat Completions, Responses, Anthropic Messages and Gemini adapters; this
does not load or execute upstream SDKs. Providers needing unsupported adapters,
authentication or missing endpoints are not offered as ready connections.

Per-model provider overrides take precedence over provider SDK/base URL fields.
Pot supplies known first-party endpoints when the directory omits them. Model
identity is exact within a selected provider; a same-named model from another
provider is not a fallback. Custom API connections leave catalog provider identity
empty and search the entire local directory by exact model ID. Known model-family
prefixes identify official providers, which take precedence; otherwise the first
matching provider in provider-ID order supplies metadata. Regional official
providers follow the explicit family preference order. If no provider contains the
model ID, Pot uses generic defaults. This lookup does not change the configured
endpoint, request protocol or credentials. Explicitly selected providers
retain their catalog identity independently of the generated credential-route ID.
`GET /api/v1/providers` includes a `model_defaults` map selected by Control for
Custom API placeholders, including models outside the ready-connection chooser.

The assistant selector offers supported models with text input/output and tool
calling, excluding known deprecated and specialized models. The connection editor
loads model IDs asynchronously after a 400 ms pause when address, protocol and
credential are available, including an unchanged saved credential. These IDs
supplement directory options; discovery never changes the current model value or
switches a manually entered value into a selection. Connection edits cancel stale
requests, and leaving the editor disposes background work. Failed discovery offers
inline retry and does not block manual model entry or saving. Directory metadata
and model-list access do not prove inference access.

Test connection explicitly sends one short streaming inference request using the
unsaved address, protocol, credential, model and reasoning effort. It does not save
a Provider or add conversation history. Success requires a complete decoded text
reply, not merely HTTP 200. Requests have a 30-second deadline and 256 KiB response
bound, do not follow redirects and never retry inference automatically. The editor
shows compact success/failure feedback with expandable diagnostics. Neither model
listing nor address preview performs inference. A saved key may be reused only
when the resolved endpoint and protocol still match its saved route.

User-configured endpoints accept HTTP and HTTPS, including local gateways reached
through hostnames, LAN addresses or IPv6. Check, save and inference share this
scheme policy. URLs must have a host and cannot embed credentials or fragments;
model-list checks also reject query parameters.

## Parameter overrides and execution

Every connection editor always shows its name, endpoint, request format, context
and output limits, and reasoning effort without an Advanced settings toggle.
Context and output fields are empty when inherited and show their effective
defaults as muted placeholders.
Entering a positive integer stores an override; clearing and saving restores
inheritance. Changing models updates placeholders and preserves entered values.
No automatic/custom mode selector or missing-metadata message is shown.
Context and output fields offer common-value preset buttons below their inputs.
Selecting a preset writes an editable integer override without saving or making
an API request. Preset labels use decimal K/M units (128K is 128,000 tokens).
Users can still enter any valid token count or clear the field to inherit defaults.

The connection editor also provides an optional free-text reasoning effort. Saving
trims surrounding whitespace; an empty value clears `Profile.params.reasoning_effort`
and leaves the provider's default behavior. Pot does not restrict effort strings
to a fixed set or infer them from models.dev. Protocol adapters encode the value
in the existing request-format-specific field (including Gemini's uppercase
thinking level). Providers decide which values they support. Effort participates
in the same profile revision and immutable execution-snapshot lifecycle as other
parameters.

`Profile.model_settings` stores catalog provider identity and nullable context and
output overrides. Control resolves a copy of the profile without overwriting its
editable intent. Context uses the explicit override, otherwise the exact model's
positive `limit.context`, otherwise 32,768 tokens. Default maximum output uses a
positive `limit.output`, otherwise 8,192, bounded below the context window. Explicit
output must be positive, below the chosen window and within a known model output limit.
Missing or zero metadata is unknown rather than a zero-token budget.

Resolved specs contain concrete limits. A separate positive `limit.input` bounds
input and compaction independently of total context and output reservation. Token
estimation itself continues to follow [Codex's rules](token-usage.md#context-estimation).
Public metadata does not change prompts, tool descriptions or execution authority.
Known image-input support supplies a descriptor default supported by Pot's
existing adapters; explicit model-catalog capability overrides take precedence.

Main assistants may adopt updated local model defaults at a fresh turn boundary,
only while still following the same saved profile revision. Running and paused
executions, existing Task launch manifests and other persisted spec copies remain
immutable. Manual overrides survive directory updates. Explicit configuration
changes continue through the existing pending-config and revision contracts.
