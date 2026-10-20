# Magpie model connection

This optional Pot plugin contributes one Magpie connection type. It reads the
configured gateway's public HTTP API; it does not access Magpie's private files,
change its providers, or install anything in Magpie.

The connection uses OpenAI Responses by default. Chat Completions, Anthropic
Messages, and Gemini protocols are also supported by Magpie and can be selected
on the connection. Pot's Gateway handles inference directly using that protocol;
this plugin never proxies model streams.

The Control-local adapter reads `/v1/models` for the currently enabled models and
routing groups, preserving their exact IDs. It reads `/v1/magpie/quotas` separately
for subscription, plan, and balance account windows. A missing quota permission
(for example, HTTP 403 for a restricted gateway key) does not prevent discovery
or inference.

The default gateway address is `http://127.0.0.1:3425`. A configured connection
credential is supplied transiently as `MAGPIE_API_KEY`; without one, reads identify
Pot locally with `Authorization: Bearer magpie-pot`. A gateway on another computer
requires a key authorized by its owner. Both use `User-Agent: Pot/0.1.0`.

Metadata reads follow no redirects and have a 15-second deadline and a 1 MiB
response limit. Errors exclude upstream bodies and URLs. Responses contain only
the known metadata fields used by Pot.

Quota values are percentages, not fractions, and separate reset windows and
shared pools remain separate. The source's `readAt` and `asOf` values are retained;
the snapshot's `read_at` only records when Pot fetched it. Unlimited, deferred,
failed, and absent values are never converted into zero usage. A plan's `until`
with `renew: auto` is its renewal time, not an expiration claim.

The current public quota response does not document durable account/window IDs.
Rows therefore explicitly report ambiguous identities; display labels, personal
information, row order, and reset timestamps are not invented stable identities.
Pot owns the generic connection-level quota threshold and notification policy.

The original Magpie icon is distributed with its MIT license and source notice.
