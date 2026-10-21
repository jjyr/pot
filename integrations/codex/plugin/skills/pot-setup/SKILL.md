---
name: pot-setup
description: Set up the Pot plugin connection to another Pot or Codex node, or reopen its local connection page.
---

Open Pot's connection page inside Codex:

1. Call `pot_open_connections` to obtain a temporary local page URL.
2. Pass that URL directly to `open_in_codex` with `target: {type: "browser", url: ...}`. If only the Browser tool is available, open it in the Codex in-app browser. Keep the page available for the user.
3. Explain that the page lists discovered Tailscale, public, private-network and local addresses in that order. The user selects Get connection address for an address the other node can reach. The page immediately shows and selects the full invitation for manual copying; its clipboard button is an optional shortcut. Paste the full invitation into the other node's Add Agent page. Alternatively, paste the other node's invitation into this page.

The page creates invitations only when the user clicks Get connection address.
This activates the selected peer listener. Tailscale addresses can use direct
listeners; an existing TCP forwarding mapping can also be reused. The connection
flow does not change Tailscale configuration. Closing the address list clears
the displayed invitation. The initial Codex assistant is read-only;
project workspace permissions are a separate local owner choice.

The returned URL grants temporary access to connection management. Pass it only
to the local browser opener; do not print it, publish it or include it in messages.
Do not read the clipboard or extract connection invitations into the conversation.
If the page session expires, call `pot_open_connections` again. If neither Codex
browser opener is available, explain the limitation and offer the local
`pot-codex-agent connections --root ...` command for the user's configured host;
do not silently launch an external browser.
