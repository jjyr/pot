# Cross-machine App/Web acceptance — 2026-10-11

An isolated Linux Control on devpc and a macOS Host Runtime used outward HTTPS
membership over the tailnet. Native App and Chrome agent profile connected to
that Control; production user data was not modified.

- App local/Control switching isolated tasks and restored a remote draft; Control
  downtime disabled new sends without local fallback, preserving cached results.
- Native and Web Machines showed the remote Host, its details and environment
  task view. A discovered projection bug duplicated remote environments under
  Control; exact Runtime filtering now prevents it, with a regression test.
- App/Web schedules showed the pinned remote Host. Saving the existing native
  schedule preserved its target without adding a selector. Copy now explains that
  quitting a remote App does not stop the Control-owned scheduler.
- Control restarted twice. Its boot changed; saved durable client credentials
  still returned HTTP 200, and App/Web resumed displaying the same remote data.
  Only the Control HTTPS listener translates this credential into process
  authority. Local process tokens still rotate, and direct browser Origin is
  rejected. Tests cover persistence, old process-token refusal, local-listener
  isolation and browser Origin refusal.

Server unit checks: 32 passed, one existing opt-in ignored. Native/Web
localization: 806 keys passed. Workspace/all-target Clippy passed.
Screenshots were recorded under `/tmp/pot39-*-machines-final.png`.
Prior Chat download acceptance remains in its separate report.

This report establishes App/Web transport and interactions. Push-provider delivery
and physical OS sleep/power loss are not inferred from these UI checks.
