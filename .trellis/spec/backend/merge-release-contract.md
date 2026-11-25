# Merge and release verification contract

## 1. Scope / trigger

Apply when integrating upstream into a fork with custom relay, channel or release behavior. A clean Git index only proves conflicts are marked resolved.

## 2. Signatures

- Reconstruct conflicts without changing the working tree: `git merge-tree --write-tree --name-only <parent1> <parent2>`.
- Root: `go vet ./...`, `go build ./...`, `go test ./...`.
- Independent modules: `GOWORK=off go build ./...` in `relaykit/` and `tokenkit/`.
- Frontend: `bun install --frozen-lockfile`, `bun check -b`, `bun run test`, `bun run build`.

## 3. Contracts

- Every conflicted file preserves compatible changes from both parents, including non-conflicting hunks.
- `relay/channel.RequestSigner` and its HTTP/form/WebSocket/task calls must match the provider plugin adaptor. Request plugin ingress, reply, report and outbound-header effects remain connected.
- Channel key mutation remaps `MultiKeyModels` and `MultiKeyUsers` against the old locked key list before persistence. Credential refresh retains the index bindings, and ordinary channel edits cannot restore stale credentials.
- PGO module lookup in release workflows uses the dependency path in `go.mod` (`github.com/MoeForge/moejs`).
- Fork GHCR publication follows `.github/workflows/docker-fork.yml` on `main`; pushing an integration branch or release tag is not evidence of this publication.
- CI records `TEST_MYSQL_DSN`, `TEST_POSTGRES_DSN` and `TEST_REDIS_ADDR` service versions without exposing credentials. `TEST_TASK_DB_DIALECT` selects external engines for scheduled-channel lock tests.

## 4. Validation and error matrix

| Evidence | Meaning |
| --- | --- |
| No conflict markers | Syntax marker cleanup only |
| Frontend bundler passes | Does not establish TypeScript or Go correctness |
| `not a known dependency`, `/default.pgo` missing | Workflow/dependency path mismatch |
| Docker Hub login fails | No image publication; inspect fork registry workflow |
| Hosted run is failed, skipped or pending | Release is not complete |
| Windows VirtualAlloc / paging-file failure | Validation blocked by resources; retry with lower concurrency or hosted CI |

## 5. Good / base / bad cases

- Good: retain timeout admission alongside request-plugin lifecycle and verify both regressions.
- Base: resolve a simple import conflict with both required imports.
- Bad: use `checkout --ours` over all unmerged paths, then infer feature preservation from a successful frontend bundle.

## 6. Tests required

- Key deletion, replacement, reorder and credential-copy filtering preserve bindings to the remaining intended keys.
- Plugin signatures run after final request headers/body are prepared; secret values stay out of previews/logs.
- Real SQLite, MySQL and PostgreSQL checks cover restored DB operations and locks; schema changes additionally require released-schema upgrades and repeated migrations.
- Inspect Actions jobs for the actual commit; verify image/release assets and target SHA before marking task completed.

## 7. Wrong vs correct

Wrong: tag push succeeded, therefore deployment succeeded.

Correct: record tag SHA, workflow run URL and conclusion; inspect failed steps, repair, and verify published artifacts. Live deployment is a separate observation.
