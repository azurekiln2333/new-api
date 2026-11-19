# Design

Queue all-busy requests and auto-admit with cancellation-safe bounded FIFO. Upstream concurrency without cap uses bounded backoff gates. Parent design defines proposed limits and dedicated keepalive-only outer state; no Redis connection per waiter, live ticket trimming, duplicated billing or replay after output.

Use parent design.md section “Admission, metrics, failover and waiting” for full architecture, contracts, compatibility and rollback. Source evidence: ../11-19-playground-capabilities/research/channel-queue.md, ../11-19-playground-capabilities/research/channel-capacity.md, ../11-19-playground-capabilities/research/stream-failover.md.

Dependencies: R6 counting, R7 stream failure; R17 rechecked on wake. Ownership: p9 / pK review; root serializes shared files and locales. Use existing business components/services. Real database checks apply to all changed persistence/query behavior; independent modules build with GOWORK=off. Roll back owned requirement commits in reverse dependency order without overwriting parallel work or deleting user data.
