# Third-party integrations and credentials

Settings → Third-party accounts lists every supported service, including those
not connected. GitHub is currently the only adapter. Each Control can connect one
account per service. The list shows the username and logged-in or expired state;
changing accounts requires logging out first. Configured accounts are available
to every assistant and Task of that Control. Upstream permissions and branch
protections determine the allowed operations.

## Component boundary

`pkg/integrations` owns the service catalog, browser authorization sessions,
account lifecycle and credential resolution. It imports no Control, Runtime,
Task, Assistant or internal product packages. Hosts inject these ports:

- `Adapter`: service descriptor, public browser challenge and authorization result.
  Provider-specific authorization state remains private to the adapter.
- Optional `Refresher`: exchange refresh credentials when supported by a service.
- `AccountStore`: one record per service, revision checks and deletion tombstones.
- `SecretStore`: immutable encrypted writes, reads and deletion.
- `CredentialResolver`: execution callers need only scoped credential reads and
  cache cleanup; they do not depend on authorization or persistence internals.

The bundled `SQLAccounts` uses a caller-owned database and owns only its table.
Control composes it over the existing `control.sqlite` and injects the existing
[Secrets](secrets.md) component, sharing its master key. It supplies an ownership
write gate and change notifications. Integrations requires no separate database,
process or deployment. Other hosts can supply different stores and adapters.

```text
UI -> authenticated API -> Integrations -> GitHub Adapter -> GitHub OAuth
                              |   |
                        AccountStore + SecretStore
                              |
Command / plugin -> CredentialResolver -> transient process environment
```

## Browser authorization

Control embeds the public Client ID of the **Pot** GitHub OAuth App owned by
`jjyr`: `Ov23lijqmW8fsHmUPXl9`. GitHub login is available without deployment
configuration. There is no environment variable, configuration field or UI option
to replace this identity. The GitHub tool plugin consumes execution credentials;
it does not own the OAuth application or login flow.

The official OAuth App enables **Device Flow** and expiring tokens. Each user's
Control contacts GitHub directly; Pot operates no intermediary authorization
server. No Client Secret is generated, embedded or required for device login or
refresh. The Client ID identifies the application and is not a user's credential.

The GitHub adapter requests `repo read:user`, obtains a device challenge, and
opens `https://github.com/login/device` in the user's browser. Pot displays the
one-time user code. The adapter respects GitHub's polling interval and slowdown
responses, then fetches the authenticated user's stable ID and username before
saving the account. This works with local or remote Controls without a public
callback endpoint. Device codes and OAuth tokens never reach the management API.

The official app issues access tokens that expire after eight hours and refresh
tokens that expire after six months without use. On credential use, Control
refreshes an expired or near-expiry access token without browser interaction and
persists the new token pair encrypted in its own Secrets store. It does not run a
background renewal timer. A refreshable account remains connected even when its
current access token has expired. Listing accounts checks local refreshability,
not GitHub's current authorization state. Temporary refresh failures fail the
command without requesting logout or automatically retrying the command. Once
both credentials are unusable, or GitHub rejects authorization permanently, the
user must log out and authorize again. See GitHub's
[device authorization and refresh contract](https://docs.github.com/en/apps/oauth-apps/building-oauth-apps/authorizing-oauth-apps#refreshing-an-access-token-with-a-refresh-token).

Login sessions are memory-only, bounded by the provider's deadline, and do not
survive restart. One pending session is allowed per service. Cancellation, logout
and shutdown fence late authorization results. Pending and connected integrations
prevent the host from enrolling as an execution Runner. Logout cancels any pending
session, clears all credential caches and deletes the local encrypted credential.
It does not revoke the application's authorization at GitHub; users can revoke it
in GitHub settings. Existing processes retain credentials already injected at
startup.

## Management API

All routes use existing owner authentication and Space routing. There is no
public token-read, manual-token-write, account-selection or default-account API.

| Method and route | Contract |
| --- | --- |
| `GET /api/v1/integrations` | `{items: [...]}` containing service descriptors, status, account identity and current login session |
| `POST /api/v1/integrations/{service}/login` | Empty JSON object; returns a login session and browser instructions |
| `GET /api/v1/integrations/{service}/login/{id}` | Read the current login status |
| `DELETE /api/v1/integrations/{service}/login/{id}` | Cancel the login session |
| `DELETE /api/v1/integrations/{service}/connection` | `{expected_revision: ...}`; disconnect the current account |

Service states are `disconnected`, `connected` and `expired`. Login states are
`pending`, `succeeded`, `failed`, `expired` and `cancelled`. A session exposes only
its ID, service, status, authorization URL, optional user code, deadline and safe
error text. An account exposes identity, revision and optional expiration, never
secret references or credential values. Unknown services or sessions return
`not_found`. Connected services reject another login until logout. Stale logout
requests cannot remove a replacement connection.

## Execution credentials

`exec_command` and plugin manifests bind an environment variable to a service:

```json
{
  "cmd": "gh pr list --repo owner/repo",
  "credentials": {"GH_TOKEN": {"service": "github"}}
}
```

A plugin declares the same `credentials` under `contributes.tools`. Missing or
expired credentials fail before starting a process. The dispatcher resolves
credentials and injects values through the authenticated execution path; the
target does not perform a second lookup. Execution target and Host approval still
follow [machine policy](permissions.md).

Credentials are decrypted on first use and cached in memory by an opaque
execution scope. Control constructs that scope from owner, turn/run and generation.
Concurrent misses are coalesced. Near-expiry values are reloaded up to 30 seconds
before expiration, with a one-second minimum interval, and refreshed when the
adapter supports it. Failed refreshes never retry an external command. Expired
values are never injected. Scope completion, cancellation and shutdown clear
caches. Logout and credential rotation invalidate all scopes synchronously.

After an authentication failure, `refresh_credentials: true` rereads the selected
credential and attempts a supported refresh. Assess permission failures separately.
A non-expiring token revoked at GitHub is discovered by the actual command;
logged-in UI state does not perform continuous upstream validation. When refresh
cannot restore access, log out and log in again.

Transient environment fields are excluded from Runner command records, Sandbox
prepared inputs, guest request journals and shell initialization snapshots. Plugin
packages, tool arguments and launch manifests contain references only. Persistent
plugin sessions must be reset if credentials change; `session_mode: call` starts a
fresh process for each invocation.

## GitHub commands

The bundled `github` tool plugin is enabled by default. It runs `gh`
directly with `GH_TOKEN`; Integrations does not proxy business requests or execute
commands. Arguments are an argv array without the executable:

```json
{"args": ["pr", "list", "--repo", "owner/repo", "--json", "number,title"]}
```

For HTTPS Git, use the same credential binding and a per-command helper:

```sh
git -c credential.helper= -c 'credential.https://github.com.helper=!gh auth git-credential' push origin HEAD
```

`gh` and `git` must exist on the execution target; the Linux image recipe includes
both, and existing images require an explicit rebuild/update. SSH remotes require
SSH credentials. Processes can read injected credentials; avoid printing them,
putting them in command arguments/files, or persisting them with `gh auth login`.
