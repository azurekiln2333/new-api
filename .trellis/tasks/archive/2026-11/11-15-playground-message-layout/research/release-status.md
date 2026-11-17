# Release status — 2026-11-16

## Current state — published successfully

The user restored Actions. `docker-fork.yml` run `43738287984` completed successfully for commit `43bf1a563636f9d301d90eb7b7245e944e282050`. Backend and frontend validation passed; both architecture image builds, multi-platform publication, Linux/macOS/Windows release builds, and final release verification passed.

- Public GitHub Release: `custom-20261116-43bf1a5`; published 2026-11-15 17:11:55 UTC / 2026-11-16 01:11:55 +08:00; `draft=false`, `prerelease=false`, `target_commitish` matches the fix commit, and the GitHub latest-release API returns this tag.
- All seven expected assets are uploaded: three platform binaries, the Windows executable, and three checksum files.
- GHCR image `ghcr.io/azurekiln2333/new-api:custom-20261116-43bf1a5` was assembled from AMD64 and ARM64 images. Successful workflow log reports index digest `sha256:a19e1d07d75dcf0a6d30f5b6bec580e7da5b72bd842d4706b0a71e8c78ce4e5d`; `latest` and commit SHA aliases were also pushed.
- Remote `main` and the peeled annotated release tag both point to the fix commit. No other Actions runs were created for the tag.
- This workflow publishes artifacts; it does not deploy to a running server.

## Implemented and pushed

- Commit: `43bf1a563636f9d301d90eb7b7245e944e282050`
- Subject: `fix(playground): constrain streamed replies and align user bubbles`
- Remote: `azurekiln2333/new-api`, branch `main`; remote SHA matches the local commit.
- Working tree is clean. The six reviewed frontend files are committed.
- User bubbles align right and their contents align left. Assistant content and reasoning stay within the centered column; wide code scrolls inside its own block.

## Validation already completed

- Frontend suite: 233 files / 2698 tests passed before adding the final reasoning regression.
- Final affected suite: 14 files / 97 tests passed.
- Typecheck: `bun check -b`, 1470 files / 2 projects, passed.
- Changed-file oxlint / oxfmt, production build and `git diff --check` passed.
- Existing unrelated full-repository lint / format findings were not changed.
- Real Chrome component fixtures: 60 snapshots at 390px / 1440px, streaming, completion and remount. No document or conversation horizontal overflow; independent code scrolling verified. See `browser-report.md`.
- Independent review found no blocking issues. Production deployment has not been verified.

## Previous publication blocker (resolved)

Read-only GitHub API checks confirm:

- No Actions runs exist for this SHA. No `github-actions` check suite was created.
- Repository is not archived or disabled; authenticated user has admin/push permissions.
- Actions repository permissions: `enabled=true`, `allowed_actions=all`.
- Workflow `docker-fork.yml` (ID `412999128`) has state `active`.
- Re-enabling repository Actions and the workflow in the previous turn did not restore dispatch.
- Tag `custom-20261116-43bf1a5` was created and pushed on explicit user request. It points to commit `43bf1a563636f9d301d90eb7b7245e944e282050` (annotated tag object `5f0c6681131a72ba2bd336ed6eafe752f5f9e991`). The Release does not exist.

One final dispatch with response headers was attempted on 2026-11-16 at 00:24:48 +08:00 (2026-11-15 16:24:48 UTC):

```text
POST /repos/azurekiln2333/new-api/actions/workflows/docker-fork.yml/dispatches
ref=main
HTTP 422
Actions has been disabled for this repository.
X-GitHub-Request-Id: B4DB:3E1FF0:47ABBE2:7219D67:6AF9DCD0
```

GitHub's official repository Actions-settings documentation says that a repository/account can have a separate GitHub-controlled disabled state; changing ordinary permissions does not restore that state, and GitHub Support must review it. This matches the observed symptoms but does not establish why GitHub disabled this repository/account.

Source verified by HTTPS retrieval on 2026-11-16:
`https://docs.github.com/en/repositories/managing-your-repositorys-settings-and-features/enabling-features-for-your-repository/managing-github-actions-settings-for-a-repository`

Browser inspection was stopped by the Computer Use tool because the current Windows browser does not support its required URL policy enforcement. No browser actions were performed after that stop. No support message has been sent.

## Support request draft (not sent)

GitHub Actions dispatch for repository azurekiln2333/new-api returns HTTP 422: "Actions has been disabled for this repository." Repository Actions permissions report enabled=true and allowed_actions=all, and workflow docker-fork.yml reports active. Re-enabling these settings did not restore dispatch. No Actions run is created for commit 43bf1a563636f9d301d90eb7b7245e944e282050. Please review whether the repository or account has a GitHub-controlled Actions disabled state. Request ID: B4DB:3E1FF0:47ABBE2:7219D67:6AF9DCD0, timestamp 2026-11-15 16:24:48 UTC.

## User-requested tag push — 2026-11-16 00:36 +08:00

The user again explicitly requested push, tag and CI/CD. Retrying `docker-fork.yml` dispatch still returned HTTP 422. Created and pushed the annotated tag `custom-20261116-43bf1a5`; remote peeled SHA matches the reviewed commit. Subsequent API checks show zero Actions runs for this commit and no Release. The tag is a source version marker, not evidence of successful CI/CD.

Read-only workflow review confirms this custom tag matches `docker-build.yml` and `release.yml`, but not `docker-fork.yml` or Electron. The legacy Docker tag workflow targets `calciumion/new-api`; the intended fork workflow targets `ghcr.io/azurekiln2333/new-api`. If any delayed tag-triggered runs appear after platform recovery, inspect them before resuming fork publication and verify all seven required assets rather than assuming an existing public Release is complete.

## Completion

All acceptance criteria are complete: the code, tag, CI, multi-platform image and public release have been verified. No server deployment was requested or performed. Task can be archived; journal bookkeeping uses `--no-commit` so it does not start another release.
