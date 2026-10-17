# GitHub

Use `github` for GitHub CLI operations. `args` is an argv array without `gh`;
for example `["pr", "list", "--repo", "owner/repo", "--json", "number,title"]`.
The default GitHub account is configured in Settings → Service accounts on the
owning Control. All assistants and tasks of that Control may use configured accounts.
GitHub permissions and branch protection determine the allowed operations.

For Git operations, use HTTPS remotes and `exec_command` with
`credentials: {"GH_TOKEN": {"service": "github"}}`. Select a specific account
by adding `account` to the reference. Run Git with the per-command helper:

```sh
git -c credential.helper= -c 'credential.https://github.com.helper=!gh auth git-credential' push origin HEAD
```

Credentials are cached for the execution scope and injected into new processes.
After an authentication failure, `refresh_credentials: true` rereads the credential
for the requested command. Permission failures do not imply an expired token.
Check the original result before repeating writes. Never run `gh auth login` to
persist the injected token, print credentials, or include them in arguments/files.
Expired PATs must be replaced in Settings; rereading cannot renew a static PAT.
