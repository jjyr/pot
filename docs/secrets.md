# Node credentials

Each node owns its configuration. Its `control.sqlite` stores
Provider metadata in `providers` and encrypted values in `secrets`. Provider
metadata contains an immutable `secret_ref`; public API responses report whether
a credential is configured, not its value. Assistant instructions remain scoped
SQLite records in the owning assistant store. Project AGENTS.md and project
Skills are still read from the actual execution workspace.

`internal/secrets` encrypts each value with AES-256-GCM and a fresh 96-bit nonce. The
secret reference is authenticated associated data. A reference is immutable:
repeating the same save is accepted, replacing its value is rejected. Provider
configuration changes create new references, so existing Task routes remain
fixed. SQLite pages and journals only receive ciphertext for secret values.

## Master key

The master key is not stored in SQLite. On macOS, Pot uses a Keychain item
under service `pot.control.secrets` with account `go/<Runtime ID>`.
The item is created on the first secret write. A locked or unavailable Keychain
fails the operation; it never falls back to plaintext storage.

On other platforms, Pot automatically creates `~/.pot/secrets/master.key`
on the first secret write. New directories use mode `0700` and the key uses
`0600`. The complete random key is published atomically without overwriting an
existing key, including when multiple node processes initialize concurrently.
The file is reused on restart. A missing key with existing encrypted secrets is
an error, not a reason to generate a replacement (also true for Keychain).

To override either default, set `POT_SECRET_KEY_FILE` to an owner-only file
containing exactly 32 random raw bytes. Explicitly configured files must already
exist; Pot does not generate or overwrite them. For example, provision once before starting the node:

```sh
install -d -m 700 "$HOME/.config/pot"
umask 077
openssl rand -out "$HOME/.config/pot/control.key" 32
export POT_SECRET_KEY_FILE="$HOME/.config/pot/control.key"
pot serve
```

Do not regenerate this file on restart. Restoring the database requires the same
master key. Key rotation/re-encryption is not implemented. External-service account
delivery is described in [Credentials](credentials.md); model Provider keys remain
node-local. Embedders can supply a `KeySource` before configuring
Providers; all assistants share the node configuration handle.

## Execution boundary

The local execution adapter validates the fixed model and budget, then Gateway
uses the Provider credential. Keys are never included in launch manifests,
checkpoints or chat messages. Gateway and Secrets are libraries in the same
process. Agent Link peers select their own models and credentials; delegation
never transfers this node's Provider keys.

External-service credentials use separate account records and run-bound tool
environment injection. This does not expose Provider secrets or provide a bulk
secret export API. See [Credentials](credentials.md).

Provider credentials are configured on the owning node; there is no plaintext
credential-file reader or import endpoint.
