# P3 native remote desktop

Requires a running P2 container + SSH tunnel. Uses the same GPUI window and WKWebView; no engine credentials.

```sh
export POT_REMOTE_SSH='<destination from Machines>'
poc/integrated-desktop/poc prepare
poc/macos-shell/poc build
poc/integrated-desktop/poc run
poc/integrated-desktop/poc open
```

`POT_POC_ROOT` defaults to `.local/poc`. The broker binds loopback17681. Its random token stays in a mode0600 task-local URL file; do not publish the URL. The raw P2 tunnel still exists for the owner and can bypass this broker. This is **cooperative control of the supplied viewer/driver**, not arbitrary shell/CDP isolation.

The broker starts without an input owner. Click **Return control** to observe the page and enable Driver write, or **Take over** for human input. Use Clipboard → Send clipboard → remote Ctrl+V for Chinese. Return disables user input, releases held normal/QEMU keys and pointer buttons, waits for a real RFB Fence acknowledgement, reads remote state, then enables the driver. Changing ownership rejects stale queued epochs. Disconnect retains ownership; Connect / reconnect fetches authoritative state. Broker restart starts with no owner, requiring an explicit action again.

Native accessibility-generated WKWebView mousedown events were observed with `buttons=0`; Ctrl-modified keys also arrived without separate modifier transitions. The page normalizes these contradictory event fields and mirrors Ctrl transitions through noVNC's public API. It does not inject text or call remote DOM methods for human input. Actual Chinese composition remains a separate test from clipboard commit.

Close the window to retain the remote environment; reopen through the pot menu. Quit exits the GUI, not the remote container. Stop separately:

```sh
poc/macos-shell/poc stop
poc/integrated-desktop/poc stop
poc/remote-desktop/poc untunnel
poc/remote-desktop/poc stop
```

`verify_broker.py` exercises the **real** remote RFB/CDP path (authentication, raw viewer rejection, in-flight write/take ordering, held modifier release/fence, return observation). It changes only the POC test page. Run from repository root with `.local/poc/integrated-venv/bin/python poc/integrated-desktop/verify_broker.py` after run. Native UI acceptance evidence is separate. See `docs/validation/integrated-desktop.md`.

The same broker/page can run inside the P4 guest with local CDP, no host Python runtime required. P4 supplies guest-only bind/upstream/backend configuration; P3 defaults remain loopback-only.
