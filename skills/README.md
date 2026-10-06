# Builtin Agent skills

Bundles live in `shared/`, `main/`, or `task/`. Runtime exposes shared and the
current role's directory to discovery. The product bundle distributes these
files read-only. `main/skill-creator` and `main/skill-installer` are reserved and cannot be overridden.
