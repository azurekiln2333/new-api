# Timeout worker progress

Boundary: service HTTP client selection and the final outbound RoundTripper own first-body-byte waiting. Existing header/total/idle limits are retained. Changes are limited to the assigned transport/policy/new timeout production file and api_request context/pinger behavior plus existing tests. No billing formula, controller lifecycle, settings persistence, frontend or Git edits.

Implemented first-body-byte gate with synchronous one-byte read/replay, synchronized timer winner, cancellation and body cleanup, 101 write-interface preservation, resolved timeout cache keys, client context binding and API early-ping suppression. Dedicated timeout bypasses ErrOptionWithHideErrMsg because that option destroys the typed cause. Existing task settings fields/constants are needed before compilation.

Next: deterministic transport and AWS SDK regressions, request cancellation/keepalive checks, gofmt and focused tests.
