# Magpie model connection

This optional plugin registers one model connection through Pot's shared
[provider contribution contract](../../docs/plugins.md#model-connection-contributions).
It uses only Magpie's public gateway API, preserving Pot's other connections and
leaving Magpie's private files, configuration and plugin installation untouched.

The default endpoint is `http://127.0.0.1:3425`; OpenAI Responses is the default
protocol, with Chat Completions, Anthropic Messages and Gemini also supported.
Pot's Gateway handles inference directly. This adapter only reads metadata:

- `/v1/models`: enabled models and routing groups, preserving their exact IDs.
- `/v1/magpie/quotas`: account/plan labels and independent allowance windows.

The public application credential defaults to `magpie-pot`; an owner-supplied
private key is injected transiently as `MAGPIE_API_KEY`. A gateway on another
computer requires an authorized gateway key. HTTP 403 from the quota endpoint
reports permission failure independently of model discovery and inference.
Metadata requests identify Pot, follow no redirects, and have a 15-second deadline
and 1 MiB response limit. Diagnostics exclude upstream bodies and URLs.

Window usage is a percentage. Shared pools and separate reset windows are never
summed. Source `readAt`/`asOf` timestamps are retained; snapshot `read_at` only means
Pot fetched the response. Unknown, unlimited, failed and deferred readings do not
become zero usage. `until` with `renew: auto` identifies renewal, not expiration.

The current public quota API has no documented durable account/window IDs.
Rows report ambiguous identities; labels, personal information and row positions
are not invented stable IDs. Pot owns the generic connection-level threshold,
freshness and notification policy described in
[Plugins](../../docs/plugins.md#connection-allowance-monitoring). Catalog refresh and
model defaults follow [the shared connection behavior](../../docs/model-directory.md).

The official Magpie icon is retained with its MIT license and source notice.
