# Credentialed CORS origins

`middleware.CORS` grants browser response access only to exact validated origins.
The public `ServerAddress` option contributes its origin; operators may add a
comma-separated `CORS_ALLOWED_ORIGINS` environment variable (restart to apply).
HTTPS origins and HTTP loopback development origins are accepted. Wildcards,
`null`, userinfo, paths, queries and fragments in additional entries are rejected.
An empty effective list grants no browser origins. Never authorize from request
Host, forwarded headers or an unvalidated Origin.

Read mutable ServerAddress under `common.OptionMapRWMutex`, then normalize outside
the lock. The OAuth redirect allowlist is a different contract. Existing session
origin/CSRF protections remain necessary: CORS controls response readability and
is not authentication or authorization.

Every response varies by Origin, including requests without Origin. Preflight
also varies by requested method and headers. Allowed origins receive an exact
ACAO value and credentials permission; disallowed preflight receives no permission
headers and never runs the endpoint. No-Origin API clients continue normally.
Use explicit required methods/headers, including PATCH and X-Security-Proof.
Only routes already installing this middleware gain these response headers.

Regression cases in `middleware/cors_test.go` cover allowed/denied/null/malformed
and ambiguous origins, loopback, port/suffix attacks, preflight and no-Origin
responses. Review basis: OWASP CORS Security Cheat Sheet; the relevant ASVS 5.0
browser-origin controls do not imply a whole-application compliance claim.
