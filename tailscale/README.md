# Embedded Tailscale

Vendored libtailscale at commit `59d4bb82744915815178e0f0776d60026a397ee7`
(BSD-3-Clause), including its locked Go module graph. `pot.go` adds in-process
status/login discovery and tailnet-only HTTPS proxying via tsnet ListenTLS.
It does not enable Funnel. TLS keys and node state remain in the owner instance.

Build with `mise run build:tailscale`. The shared library is bundled beside Pot
and is loaded only when Tailscale is configured. Node state is persistent;
closing Pot disconnects it without deleting its identity.
