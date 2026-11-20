# Codex credential refresh source guard

Owner: management checker. Production ownership is limited to
`service/codex_credential_refresh.go`; frozen balance changes are unchanged.

## Interface and persistence contract

- `CodexCredentialRefreshOptions.ExpectedKey *string` is optional. A non-nil
  value must equal the initial row's raw key byte for byte before OAuth exchange.
- `ErrCodexCredentialSourceChanged` is a stable sentinel for expected-source
  mismatch and for replacement/type/multi-key changes discovered at persistence.
  Discovery must reload the current channel and re-evaluate eligibility before
  deciding whether to retry; do not reuse the refresh result after this error.
- Initial fresh read, external exchange and final write share the existing
  10-second operation deadline. Reads and guarded writes disable GORM SQL logs.
- OAuth HTTP remains outside transactions. All callers, including nil
  `ExpectedKey`, persist through `model.UpdateChannelOtherInfoLocked`, comparing
  current Codex type, single-key mode and raw source key in Go while row locked.
  SQL collation is not used to compare credentials. Current `other_info` remains
  intact. A concurrent administrator replacement is never overwritten.
- Write errors retain sentinel/context identity and otherwise become a fixed
  value-free persistence error, because raw database driver errors can expose
  credential values. Success alone bumps channel sync and optionally resets
  caches. The returned channel remains the original source snapshot, preserving
  prior behavior.

## Consolidated regression handoff to pi 4

Extend `service/codex_models_test.go`; do not create another test file.

1. Expected source differs before refresh, including case-only differences on a
   case-insensitive MySQL database: sentinel, zero OAuth requests, no writes.
2. Replace A with B while exchange is pending: sentinel and exact B retained.
3. Switch channel type or single-key mode during exchange: sentinel, current
   row preserved.
4. Nil `ExpectedKey` still rejects concurrent replacement at persistence.
5. Successful refresh stores rotated access/refresh credentials, preserves
   `other_info`, and retains existing cache-reset/sync behavior.
6. Cancellation/deadline covers initial read, exchange and final store; no
   canceled/stale result is written and no failure performs success cache work.
7. Discovery handles the sentinel by reloading B and revalidating current
   channel policy, without a second exchange for stale A.
8. Failing persistence returns no usable credential or raw secret-bearing
   driver error, and leaves the prior row unchanged.

Persistence regressions require real SQLite, MySQL and PostgreSQL cloud jobs.

## Review status

Read the cached official OWASP Authentication, Session Management and OAuth 2.0
Cheat Sheets in full before this edit. Apply credential confidentiality and
rotation/source-integrity guidance; ASVS baseline 5.0.0 was established earlier
in this task. This narrow source guard is not a claim of ASVS compliance or a
complete OAuth/security audit.

`gofmt` completed and the final source/diff were read back. No local tests,
lint, typecheck, build, browser checks or upstream probes were run. No files
were staged, committed or pushed by this checker. Root owns cloud verification
and release.
