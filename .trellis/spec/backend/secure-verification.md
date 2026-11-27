# Bounded sensitive-action verification

## 1. Scope / trigger

Apply to administrator step-up verification, shared authentication refresh and
password/2FA, Passkey or OAuth proof responses. Cross-node log database latency
must not leave the console waiting indefinitely or bypass audit persistence.

## 2. Entry points

`writeSecurityProof`, the existing durable management audit outbox, frontend
`withAuthRequestDeadline`, shared refresh ownership, and secure-verification API
requests form the boundary. The verification dialog uses the existing loading,
failure and retry states.

## 3. Contracts

All proof response sinks persist the audit before returning a usable proof.
Use the existing outbox and its bounded direct fallback; if both fail, return no
proof. Each audit phase is bounded separately, so this is not a single 10-second
whole-request guarantee. Recheck request cancellation before proof delivery.
Never log passwords, factor codes, proof tokens or usable credentials.

Frontend network/coordination waits have a 30-second bound and caller cancellation.
A caller abandoning a shared refresh stops only its own wait. The refresh owner
has its own deadline, aborts its HTTP/browser-lock operation, and rejects stale
results after abort or authentication epoch change. A timeout is a failure, not
verification success. Requirements loading releases its retry guard in finally.

Refresh before consuming a one-use verification credential. Credential-consuming
verification POSTs must not be replayed after a 401. Preserve target/action/session
binding, single use, expiry, authorization and revocation rules.

## 4. Validation and error matrix

| Condition | Required result |
| --- | --- |
| Remote log store is unavailable, outbox write succeeds | Durable audit permits proof response |
| Outbox and bounded direct audit fallback both fail | No proof returned |
| Request cancelled before proof response | No proof returned |
| One of two shared-refresh callers cancels | Other caller may still finish |
| Owner refresh times out or epoch changes | Late response cannot change auth state |
| Verification returns 401 | Do not replay consumed credentials |

## 5. Good / base / bad cases

Good: queue the audit durably and return proof after its persistence. Base: an
ordinary request completes under the existing permission checks. Bad: remove
audit requirements to hide a slow remote store, or resolve timeout as success.

## 6. Required regressions

Extend the existing administrator verification/enrollment suites and frontend
verification-api, verification-flow, step-up-verification and shared-auth suites.
Cover durable audit with independent log storage, double persistence failure,
redaction, cancellation/late results, blocked browser locks, shared waiters and no
duplicate dispatch. Run affected backend contracts with real SQLite, MySQL and
PostgreSQL and retain the existing audit delivery/ClickHouse coverage.
Source review is not measured production latency or security compliance.

## 7. Wrong vs correct

Wrong: pass one caller's cancellation directly to a shared refresh owner, or use
the generic 401 replay path for a single-use factor. Correct: separate owner and
waiter lifetimes and explicitly mark non-replayable requests before dispatch.
