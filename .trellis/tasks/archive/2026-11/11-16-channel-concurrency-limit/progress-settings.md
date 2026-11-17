contracts ready

Main implemented DTO pointers, bounded validation, request snapshot/global option, missing retry default2, 504/524 retry eligibility, and startup-only exact legacy range migration with protected marker. SQLite migration/configuration and settings validation tests passed via go test ./model ./setting/operation_setting -run 'RequestPolicy|ChannelValidateSettings|InferencePresetSettings|ShouldRetryByStatusCode' -count=1. MySQL/PostgreSQL pending mandatory CI. No product commit yet.

Remaining frontend locale additions for backend source errors:
- Upstream first-byte timeout must be an integer between 0 and 86400
- max_concurrent_requests must be an integer between 0 and 1000000
- upstream_first_byte_timeout_seconds must be an integer between 0 and 86400
