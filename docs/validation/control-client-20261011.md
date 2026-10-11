# Single Control App client — 2026-10-11

P3b adds private native Control configuration, one local/remote icon, and independent
mode caches for assistants, tasks, settings, navigation and draft state. Requests
capture their originating mode and owner; Swift rejects replies across mode-generation
changes. The saved Control identity is checked before loading its Runtime directory.
Credentials stay in the native backend and the configuration file has mode 0600.

Native acceptance used a disposable Pot.app and Control on loopback. The user's real
App data was untouched. Verified missing/invalid Control configuration, local/remote
icon changes, distinct Runtime identities, restored remote chat draft after a round
trip through local mode, and failed remote requests without local fallback. The
Machines panel no longer clips under the navigation rail. Connection failures show
an error and mark cached machine status offline. The Control entry is labelled Control.
No model inference or membership creation was performed through the UI.

Automated validation: App 21 tests, shared UI 12 tests, 800 translation keys, native
build, and App/UI/Server Clippy passed. Tests cover authenticated Control-role validation,
private configuration persistence and mode/owner request capture. Remote desktop,
plugins/artifacts and cross-machine App qualification remain later phases in #39.
