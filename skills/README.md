# Builtin Agent skills

Bundles live in `shared/`, `main/`, or `task/`. Runtime exposes shared and the
current role's directory to discovery. The product bundle distributes these
files read-only. `main/skill-manager` is reserved and cannot be overridden.
