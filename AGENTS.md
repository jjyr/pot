# Working on pot

- Start with `docs/README.md`, `docs/product-design.md`, and `docs/poc-plan.md`.
- Current work targets uncertain integration: remote Docker GUI, GPUI/tray/macOS, and Swift helper/embedded runtime. Use OpenClaw/Hermes research as reference for long-term/session mechanisms; do not expand that work without a task need.
- Product name is pot; default assistant name is Pot. Engine Setup precedes chat onboarding. Initial engine targets: Codex, Claude, Pi, OpenCode, Google Antigravity. All interaction except engine Setup and sandbox settings follows Dot.
- Keep experiment code and evidence under `poc/`; record conclusions, version pins, reproduction and limitations under `docs/validation/`. Distinguish actual native/remote behavior, fixtures, and untested assumptions.
- `poc/baseline` preserves historical experiments. Some native/engine probes depend on `/tmp/pot-research-20261003`; read reproduction notes before running. Do not treat an old passed fixture as a new end-to-end result.
- Do not commit credentials, user browser profiles, engine homes, VM disks, dependencies, build outputs or large downloaded binaries. Use ignored task-local paths. Do not repurpose HOME or CODEX_HOME.
- Canonical machine inventory is `~/Workspace/Machines`. Store host connection details, environment/deployment notes and new machine-specific knowledge there rather than scattering it in this repo.
- Existing lightweight checks: from `poc/baseline`, run `python3 test_runtime.py`, `python3 test_adversarial.py`, and `python3 local_oauth_poc.py`. These cover 29 deterministic/fixture tests, not native desktop acceptance. Run checks appropriate to the changed boundary.
- Preserve original Notes sources; the import manifest records their provenance. New project work is maintained here.
