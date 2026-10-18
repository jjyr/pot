# GitHub

Use `github` for GitHub CLI operations. `args` is an argv array without `gh`;
for example `["pr", "list", "--repo", "owner/repo", "--json", "number,title"]`.
Log in to GitHub through Settings → Third-party accounts on the owning Control.
Each service has one account, available to all assistants and tasks of that Control.
GitHub permissions and branch protection determine the allowed operations.

For Git operations, use HTTPS remotes and `exec_command` with
`credentials: {"GH_TOKEN": {"service": "github"}}`. Run Git with the per-command helper:

```sh
git -c credential.helper= -c 'credential.https://github.com.helper=!gh auth git-credential' push origin HEAD
```

Credentials are cached for the execution scope and injected into new processes.
After an authentication failure, `refresh_credentials: true` rereads the credential
for the requested command. Permission failures do not imply an expired token.
Check the original result before repeating writes. Never run `gh auth login` to
persist the injected token, print credentials, or include them in arguments/files.
When login expires and cannot be refreshed, log out and log in again in Settings.
