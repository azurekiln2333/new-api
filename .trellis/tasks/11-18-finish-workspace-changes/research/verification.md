# Verification evidence

## Upstream client restriction

Direct requests to api.xingchiapi.com with the same user-provided credential and gpt-6.1-sol model, minimal Reply with only OK prompt:

- chat/completions + new-api-diagnostic/1.0: 403 client_not_allowed.
- chat/completions + exact saved Codex CLI template UA/originator: 403 client_not_allowed.
- chat/completions + opencode/1.19.5: 200 chat.completion, OK.
- responses + exact Codex CLI template UA/originator: 200 response completed, OK.

User confirmed selecting Codex CLI. Static channel header overrides are applied to playground requests; template selection does not enable protocol conversion. This reproduces the incompatible endpoint/client combination. No live new-api channel configuration was changed or retrieved. Credentials are intentionally excluded.

## Prior release failure

Release run 44194890759 failed all backend builds: Calcium-Ion/moejs is not a known dependency; /default.pgo not found. go.mod uses MoeForge/moejs.
Docker run 44194890695 failed Docker Hub login: Username and password required. No multi-platform manifest was published.
fork/main remained 8052607e0; fork/merge/upstream was ed21eb3d4.

## Local tools and limits

Official Go 1.26.10 archive downloaded and SHA-256 checked against go.dev release metadata, extracted only into OS temporary directory. Official Bun 1.4.4 extracted into same temporary tool directory.
WSL Ubuntu could not start: HCS_E_HYPERV_NOT_INSTALLED. No host settings modified.
Initial go build failed from Windows commit/pagefile exhaustion. Retry with GOMAXPROCS=2, GOMEMLIMIT=768MiB and -p 1 also failed from compiler VirtualAlloc error 1455. These are failed checks, not passing compilation evidence. Full verification moves to branch CI with real MySQL/PostgreSQL/Redis services and SQLite tests.
