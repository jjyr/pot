# Native client reliability fixes — 2026-10-07

Extracted independently from deferred multi-Server PR #23 onto current main.
The changes prevent reading an unlaunched Helper's termination status, bind send
receipts to their original conversation, and display a backend error code when
the response has no message. No multi-Server API, pairing authority, credential
storage or connection-management changes are included.

`mise run check`, strict workspace `mise run clippy`, and `mise run test:app`
passed. App testing includes a Rust receipt-ownership regression and native
Swift tests for an unlaunched and exited Helper, identical drafts in different
conversations, edited drafts, main chat, existing tasks and new-task drafts.
Native Swift regressions run as a dependency of `test:app` and can also be run
using `mise run test:app-safety`. No model calls or production data are required.

The native tests execute the same receipt and Helper-status helpers used by the
App. This extraction was compiled and tested without repeating the earlier
multi-Server UI session; it does not claim remote-connection qualification.
