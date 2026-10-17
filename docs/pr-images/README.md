# PR images

Store screenshots used for PR review in `docs/pr-images/<normalized-branch>/`.
The directory is based on the source branch, so images can be committed before
opening a PR.

- Normalize the full branch name to lowercase, replace each run of characters
  outside `a-z` and `0-9` with `-`, and trim leading and trailing `-` characters.
  For example, `feature/GitHub-auth` becomes `feature-github-auth`; `gh` stays `gh`.
  If the result is empty or collides with another branch's directory, append the
  first eight hexadecimal characters of the original branch name's SHA-256
  (use `branch-<hash>` for an empty result).
- Use lowercase kebab-case filenames: `<view-or-scenario>-before.png` and
  `<view-or-scenario>-after.png`. Include the viewport or theme in the shared
  scenario name when several variants are needed.
- Capture each pair with the same viewport, theme and equivalent UI state.
  Label both images in the PR description. For a new view, explain that it is
  new and include only an `after` image.
- Keep draft captures in ignored `output/pr-images/`. Commit only the images
  used in the review. Use test data and omit credentials or private information.
  Do not commit unused captures or generated browser traces.
- Embed images in the PR description using a full GitHub URL pinned to the
  pushed commit, ending in `?raw=true`, so later branch changes do not change
  the comparison. Update the URL when replacing a screenshot.

Example path: `docs/pr-images/gh/service-accounts-after.png`.
