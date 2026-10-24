---
name: pot-setup
description: Connect this Antigravity host to Pot, or reopen its local connection page.
---

Call `pot_open_connections`. Privately show the returned temporary URL to the user
as a clickable link. The URL grants connection-management access for 30 minutes;
never send it to another assistant or include it in logs or files.

The user chooses an address from the page and copies its invitation into Pot's
Add assistant > Antigravity flow, or pastes a Pot invitation into this page.
Pairing permits assistant messaging and user-only assistant creation/model edits
within workspaces that the Antigravity host's owner has already configured.
Credentials and native execution permissions stay on this host.

Do not call inference merely to connect or list models. Use `pot_connections` to
discover explicit recipients before authorized peer messages with `pot_send`.
A transport receipt means acceptance, not remote model completion.
