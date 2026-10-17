# External-service credentials

Control owns configured service accounts. Saving an account makes its credential
available to every assistant and Task of that Control, including connected
execution machines. There are no additional account grants or per-plugin approval
rules. Different Controls retain independent accounts. The upstream token's
permissions determine the allowed repositories and operations.

## Account configuration

Settings → Service accounts creates, updates and deletes accounts. A service can
have several stable account IDs and one default account. The first account becomes
the default; choosing another default replaces it. Deleting the default selects
the first remaining account in account-ID order. Edits and deletes check the
account's revision to reject stale client writes. A blank token on edit keeps the
saved value; expiration is supplied explicitly and may be omitted.

The authenticated owner API exposes:

- `GET /api/v1/credentials`: `{items: [...]}` containing account metadata only.
- `POST /api/v1/credentials`: `service`, `account`, `name`, `default`,
  `expected_revision`, `token` and optional RFC 3339 `expires_at`.
- `POST /api/v1/credentials/remove`: `service`, `account`, `expected_revision`.

Metadata and immutable encrypted secret references live in `control.sqlite`.
Values use the existing [Secrets](secrets.md) encryption and master key. Owner
responses and change events never return values. An execution Runtime with
configured service accounts cannot enroll as a Runner.

## Command and plugin access

`exec_command` accepts credential references keyed by environment variable:

```json
{
  "cmd": "gh pr list --repo owner/repo",
  "credentials": {
    "GH_TOKEN": {"service": "github", "account": "personal"}
  }
}
```

Omitting `account` selects the service's default account. Missing or expired
credentials fail before starting the process. Execution destination and Host
approval still follow [machine policy](permissions.md).

A plugin declares the same `credentials` object under `contributes.tools`. The dispatcher resolves
references for the original run and injects values into the plugin's environment.
Packages, tool arguments, launch manifests and prepared-operation records contain
references, never values. A tool capability
`session_mode` of `call` closes the plugin process
after each invocation; omission retains explicit-session behavior. Plugin schemas
support scalars and bounded arrays of strings (256 items, 32 KiB per item).

Credentials are decrypted on first use and cached in Control memory by execution
owner, turn/run and generation. Concurrent misses are coalesced. The dispatcher
reuses cached values when sending commands to their execution target; the target
does not perform another credential lookup. Transient environment fields travel
through the authenticated execution path and guest pipe but are excluded from
Runner command records, Sandbox prepared inputs and guest request journals. Shell
initialization snapshots are captured before credential injection.

Near-expiry values are reread up to 30 seconds before expiration, with a one-second
minimum interval. Expired values are never used. Account changes synchronously
clear caches; subsequent commands observe the new configuration without a Runner
invalidation round trip. Execution completion, cancellation and shutdown discard
the cache. Existing processes retain their startup environment. Persistent plugin
sessions must be reset and restarted if credentials change.

After an authentication failure, request `refresh_credentials: true` on the next
command to reread the selected credential. The GitHub plugin exposes the same
option. Command failures are not interpreted as authentication failures; permission
and external-service errors must be assessed separately. Refresh never retries an
earlier command or external write.

The current account type stores supplied tokens/PATs. Rereading a static token
does not renew it; expired PATs must be replaced in Settings. OAuth refresh and
GitHub App token issuance require separate service adapters.

## GitHub

Configure a `github` account authorized for the intended repositories. Enable the
`github` plugin in the AI provider's plugin settings to expose `github_cli`. It
executes `gh` directly using `GH_TOKEN`; Control does not proxy GitHub requests.
Arguments are an argv array without the executable:

```json
{"args": ["pr", "list", "--repo", "owner/repo", "--json", "number,title"]}
```

For HTTPS Git authentication, use the same credential reference in `exec_command`
and a per-command credential helper:

```sh
git -c credential.helper= -c 'credential.https://github.com.helper=!gh auth git-credential' push origin HEAD
```

This leaves global Git configuration unchanged. SSH remotes require SSH
credentials. Branch protections remain effective. `gh` and `git` must be installed
on the execution target; the Linux image recipe includes both, and existing images
require an explicit rebuild/update.

Processes can read their injected credentials. Avoid printing them, placing them
in command arguments/files or persisting them with `gh auth login`. Pot does not
audit downstream requests or revoke tokens already read by a process. Revoke
values at the upstream provider when necessary.
