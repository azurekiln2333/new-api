# Capacity implementation progress

- Read approved design, implementation scope, billing rules and backend specs.
- Added process-local channel-ID admission and atomic Redis owner-token leases with server time, stale cleanup, renewal cancellation, fail-closed errors and idempotent release.
- Added hard request-scoped saturated-channel exclusions and no-repeat exhaustion for failed-channel avoidance. Capacity errors bypass health/disable/error-log handling.
- Integrated ordinary HTTP/AWS handler lifetime, generic async submission, administrator tests, Realtime connection cancellation and Responses WebSocket connection lifetime.
- Moved Claude early keepalive inside admitted attempts and suppress it while first-byte timeout is enabled. Realtime admission now precedes downstream upgrade.
- Controller failover regression worker owns channel_pin_retry_test.go. Main is coordinating legacy Midjourney's separate final-channel HTTP boundary.
- Implemented native SSE detached submission ownership: client disconnect does not release or stop renewing a live lease; Redis coordination loss still cancels the upstream. Persistence keeps the pre-existing detached context policy.
- Added regression coverage for local/Redis owner lifetime, saturation routing with retry budget zero, two keys sharing channel cap2, health probes skipping busy channels, all-busy429, stream cancellation/completion, detached cancellation, async cancellation with one refund, and idle/reused WebSocket occupancy through close.
- Full service17.253s, relay0.274s and jsplugin1.375s passed. Controller full run hit its total3-minute timeout and found two assertion expectations; fixed both. Main owns final controller run with10-minute allowance.
- Final review fixes prevent client cancellation from disabling a healthy channel and preserve same-group capacity traversal when RetryTimes=0. Review regression passed service5.130s/controller2.737s, including all new stream/task/WebSocket lifecycle regressions and both previous failing assertions. Product files frozen and handed to main for full checks/release. Redis integration requires TEST_REDIS_ADDR in CI; no local real Redis pass claimed.
- Final narrow follow-up: positive retry-budget boundary restores selected candidate's retry/reset/affinity state on capacity rejection; snapshots are scoped by channel. Unlimited fallback masks stale detached lease markers. Updated regression passed service5.177s/controller1.195s/relay5.606s; final channel-scope selection check also passed. Files frozen again; no Git writes performed.
