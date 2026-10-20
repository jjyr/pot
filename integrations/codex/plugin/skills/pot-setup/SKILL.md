---
name: pot-setup
description: Set up the Pot plugin connection to another Pot or Codex node, or reopen its local connection page.
---

Open Pot's connection page inside Codex:

1. Call `pot_open_connections` to obtain a temporary local page URL.
2. Pass that URL directly to `open_in_codex` with `target: {type: "browser", url: ...}`. If only the Browser tool is available, open it in the Codex in-app browser. Keep the page available for the user.
3. Explain that the page lists discovered Tailscale, public, private-network and local addresses in that order. The user copies the connection address appropriate for the other node and pastes it into that node's Add Agent page. Alternatively, paste the other node's invitation into this page.

The page creates invitations only when the user clicks Copy connection address.
Copying activates the selected peer listener. A Tailscale row marked as requiring
setup also enables its private TCP forwarding when copied. Discovery itself
does not change Tailscale configuration. The initial Codex assistant is read-only;
project workspace permissions are a separate local owner choice.

The returned URL grants temporary access to connection management. Pass it only
to the local browser opener; do not print it, publish it or include it in messages.
Do not read the clipboard or extract connection invitations into the conversation.
If the page session expires, call `pot_open_connections` again. If neither Codex
browser opener is available, explain the limitation and offer the local
`pot-codex-agent connections --root ...` command for the user's configured host;
do not silently launch an external browser.
