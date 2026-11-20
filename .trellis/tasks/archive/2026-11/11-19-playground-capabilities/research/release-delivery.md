# Release and server-delivery research

Research only, 2026-11-20 Asia/Shanghai. No source/workflow changes, tags, pushes, release creation, registry writes or server commands were performed.

## Verified repository and remote state

- Current branch `merge/upstream`, HEAD `ed21eb3d4030a639e2f44bf2c79ced9e6dc251bd`.
- Push destination is remote `fork`, `https://github.com/azurekiln2333/new-api.git`. Remote `upstream` is `https://github.com/QuantumNous/new-api.git`; do not publish the fork's work there.
- Live `git ls-remote --heads fork main merge/upstream` returned `main=8052607e0d0c4f26bdd6e29837ead5fc14bf26c9`, `merge/upstream=ed21eb3d4030a639e2f44bf2c79ced9e6dc251bd`. `git merge-base --is-ancestor fork/main HEAD` exited 0. Recheck these values immediately before publication; this is not permission to force-update a later remote.
- Public GitHub API returned latest release `custom-20261118-8052607`, target `8052607e0d0c4f26bdd6e29837ead5fc14bf26c9`, published `2026-11-17T22:17:02Z`, with all seven expected binary/checksum assets. The custom suffix uses the commit's date, so its date need not match UTC publication day.
- Latest successful fork release run is `44185381934`, for the same SHA. Later tag-triggered runs `44194890759` (binaries) and `44194890695` (legacy Docker) failed for `9b21c12d280ca3ddb0e19cf2dd69797982e83f70`. These facts match the reopened 11-18 task; that failed tag is not a successful new release.
- Sources checked with read-only HTTPS requests:
  - `https://api.github.com/repos/azurekiln2333/new-api/actions/runs?per_page=5`
  - `https://api.github.com/repos/azurekiln2333/new-api/releases/latest`
  - `https://github.com/azurekiln2333/new-api/actions/runs/44185381934`
  - `https://github.com/azurekiln2333/new-api/releases/tag/custom-20261118-8052607`

## Actual publication contract

`.trellis/spec/backend/merge-release-contract.md` is authoritative; reviewed together with the reopened `.trellis/tasks/11-18-finish-workspace-changes/{task.json,report.md,implement.md,research/verification.md}`.

1. `.github/workflows/ci.yml` runs on pushes to `merge/upstream`, PR events and reusable workflow calls. The current working copy includes real MySQL 8.4 / PostgreSQL 16 / Redis 7.4 jobs plus SQLite fixtures, independent module builds, root tests and frontend checks. These workflow edits are **pre-existing uncommitted changes** and must be independently reviewed/committed as part of their owning repair, not silently included in a new feature commit.
2. `.github/workflows/docker-fork.yml:3-6` runs on pushes to **main** or manual dispatch. Its validation job only permits repository `azurekiln2333/new-api` on `refs/heads/main`, then invokes the CI workflow. Pushing only an integration branch or tag does not publish the intended fork image.
3. It emits `ghcr.io/azurekiln2333/new-api` (line 16). Version is `custom-<commit-date YYYYMMDD>-<SHA first 7>` (lines 35-38). AMD64/ARM64 image tags are `sha-<full SHA>-amd64` / `-arm64`; publication creates `latest`, `sha-<full SHA>` and the custom version tag (lines 114-122).
4. It creates a draft GitHub Release at that SHA, calls `release.yml` for Linux/macOS/Windows downloads, verifies seven asset names, and only then publishes the release (lines 126-209). The workflow creates the source tag through `gh release create --target`; read it back and verify its peeled target. Do not overwrite an existing tag or create competing versions merely to repeat a tag operation.
5. `.github/workflows/docker-build.yml` is the legacy tag-triggered Docker Hub path for `calciumion/new-api`. The current uncommitted repair skips its fork build job. `release.yml` still matches non-alpha tags and can run separately from the fork pipeline if manually pushed. Follow the fork pipeline to avoid redundant or misleading publication.
6. Existing workflow has **no production deploy job**. A green run proves artifacts were published, not that a server pulled or started them.

## Server delivery: concrete unresolved prerequisite

- Repository `docker-compose.yml:19` uses **`calciumion/new-api:latest`**, whereas the fork pipeline publishes **`ghcr.io/azurekiln2333/new-api:latest`**. If the running server uses that unmodified template, it will not consume the fork's new image.
- The template labels `new-api` for Watchtower (`:69`) and includes `nickfedor/watchtower:latest` (`:125-135`) with label-only updates, cleanup, and `WATCHTOWER_POLL_INTERVAL=3600`. This proves a template exists, not that the target server runs it.
- Template health check uses `/api/status` (`:63-64`). `controller/misc.go:51` returns `common.Version`, providing an application-level version check after rollout.
- Existing 11-15 and 11-16 release records explicitly state that no server deployment was performed or verified. Neither the current task records nor inspected project deployment files identify a production SSH host/compose directory/public gateway origin. The local SSH config contains only a GitLab Git alias, not a deployment target. No credential file contents were read or printed.
- **Root needs an established target/access path** (existing server connector/SSH alias and application directory, or a known already configured deployment automation with read access). Do not infer the target from `api.xingchiapi.com`: that is the user's upstream provider, not evidence of their new-api server.
- `ssh.exe` is available. `gh` and `docker` were not on this shell's PATH; common GitHub CLI locations were also absent. Public GitHub API reads work. Earlier task notes describe temporary Go/Bun tools; no tooling was installed or altered here.

## Execution sequence after final planning review and implementation

### Preserve independent rollback

- Freeze a baseline manifest of pre-existing dirty paths and their diffs before workers write. Root is the only Git index/commit owner; workers must not run `git add`, commits, stash/reset or checkout-overwrites.
- Each requested behavior gets its own focused commit, meaningful tests and corresponding validation note. Shared files/locales are handed over sequentially; stage only that feature's reviewed hunks. Never `git add -A` the current 50+ dirty paths.
- Existing merge/workflow repair must land in its own reviewed commit(s), with the owning task's prior authorization and verification. New feature commits must not smuggle those baseline repairs into their diff.
- Before publishing, the final tested product tree must match the committed SHA. A successful working-tree test does not validate a commit missing untracked/new source or existing dirty dependencies.

### CI, image and release

Run from the root's final reviewed branch, substituting the captured final SHA and resolved custom version:

```powershell
git diff --check
git log --oneline <recorded-baseline>..HEAD
git ls-remote --heads fork main merge/upstream
git push fork HEAD:refs/heads/merge/upstream
```

Inspect CI for **that exact SHA**, including real engines/race gates. Resolve failed tests/workflows and commit each correction before continuing. After integration checks pass and remote main remains an ancestor:

```powershell
git push fork HEAD:refs/heads/main
```

Use a regular fast-forward push, never `--force`. Monitor `docker-fork.yml` for the pushed SHA through Actions API (or an available authenticated GitHub CLI); investigate failures instead of creating speculative new tags. Successful completion must show validation, both image architectures, multi-platform publication, all binary builds and public release publication. Verify:

```powershell
git ls-remote --tags fork "refs/tags/<custom-version>*"
```

Read release `target_commitish`, draft status, all seven assets, checksum content, and GHCR manifest/OCI revision. Capture the run URL and digest. If the fork workflow's tag is lightweight, its direct SHA must match; if annotated, the peeled SHA must match.

### Actual target server

Only after the correct deployment host/path has been established, inspect restricted fields, avoiding full `docker inspect` or compose config dumps that expose environment secrets:

```text
docker inspect new-api --format '{{.Config.Image}} {{.Image}} {{.State.Status}}'
docker inspect new-api --format '{{index .Config.Labels "com.centurylinklabs.watchtower.enable"}}'
docker inspect watchtower --format '{{.State.Status}}'
```

Confirm the image follows the fork registry or the intended immutable version. For an authorized direct compose rollout, from the verified deployment directory:

```text
docker compose pull new-api
docker compose up -d --no-deps new-api
```

Do not restart/update the database, Redis or unrelated containers. Alternatively observe the configured Watchtower update. Verify running container image/digest and application `/api/status` version against the newly published build, health checks and a minimal non-billable management check. Store old/new image identifiers for rollback. Do not assert delivery merely because an image exists locally or Watchtower is configured.

## Acceptance and limitations

Final handoff must list per-requirement commit IDs, exact CI run/SHA, source tag, published image digest/version, release URL/assets, and observed server version/health. If server access remains unknown, report **release published; server delivery unverified** and the exact missing target/access input. Current research has not executed any validation build, publication or deployment.
