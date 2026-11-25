# Implementation Plan

1. Resolve backend and workflow conflicts.
2. Resolve frontend, test, and locale conflicts.
3. Format changed source and scan for markers/whitespace errors.
4. Run focused and broad supported checks; document unavailable services.
5. Review the final diff, commit, push branch, push a release tag, and inspect CI/CD.

Commands: `git diff --name-only --diff-filter=U`, `git diff --check`, `go build ./...`, `go vet ./...`, `GOWORK=off go build ./...` in independent modules, and `bun run typecheck`, `bun test`, `bun run build` in `web/`.

## Reopened execution

- [ ] Integrate each reconstructed conflict against both parents with non-overlapping worker ownership.
- [ ] Restore workflow module paths; obtain official temporary Go/Bun binaries when local tools are absent.
- [ ] Run formatting, frontend typecheck/tests/build, independent module builds, root vet/build/tests.
- [ ] Verify database behavior through the real-engine CI matrix; cover migrations if schema changes.
- [ ] Independently review the combined diff and fix findings.
- [ ] Commit fixes, push branch, run CI, then publish through authorized fork workflow after checks.
- [ ] Record exact CI/CD results; archive only after acceptance criteria are met.

Frontend manifest uses `bun check -b`, `bun run test`, `bun run build`. WSL cannot start (HCS_E_HYPERV_NOT_INSTALLED); do not alter host virtualization settings.
