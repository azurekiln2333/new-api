# Implementation Plan

1. Resolve backend and workflow conflicts.
2. Resolve frontend, test, and locale conflicts.
3. Format changed source and scan for markers/whitespace errors.
4. Run focused and broad supported checks; document unavailable services.
5. Review the final diff, commit, push branch, push a release tag, and inspect CI/CD.

Commands: `git diff --name-only --diff-filter=U`, `git diff --check`, `go build ./...`, `go vet ./...`, `GOWORK=off go build ./...` in independent modules, and `bun run typecheck`, `bun test`, `bun run build` in `web/`.
