# Gateway reliability, playground and administration

## Goal

Approved delivery update: the user removed server verification (“不用校验服务器”). R25 now requires verified GitHub CI/tag/release/image publication, without a server-access prerequisite or claim of observed deployment. This supersedes server-verification language elsewhere in this task tree.

Complete every requirement collected on 2026-11-19/20, coordinate the four existing pi agents in Herdr workspace w4C, independently review their work, and deliver separately revertible commits followed by a verified GitHub release. The user authorized implementation, commits, push and tags, and requested continuing interrupted pi turns. The later instruction excludes server verification and further local tests/builds/lint/browser checks; remaining validation runs in GitHub CI.

## Requirements and observable acceptance

Each R item below receives its own product commit; grouped child tasks do not combine these commits. The execution plan records dependencies and the final report maps each requirement to its commit, checks and deployed version.

| ID | User requirement | Acceptance |
| --- | --- | --- |
| R1 | Playground hosted web search | Default off; enabled intent reaches a capable upstream; unsupported requests fail visibly; JSON and streamed citations populate the existing deduplicated source list; actual provider usage drives existing billing |
| R2 | Playground Group / Channel selector | Admin can switch views and truly select a channel; server validates role, group, model and key restrictions; returning to groups clears the pin; ordinary users cannot forge a pin |
| R3 | Model names sorted | Deterministic natural ascending order across desktop/mobile, filtering and both selector modes; a valid saved model stays selected |
| R4 | Channel supported upstream endpoints and conversion | Channel settings declare Chat + Responses or Responses only; inbound playground Chat and API Messages use a supported upstream conversion and return their original client protocol; no playground-only protocol switch; configured headers preserved |
| R5 | Accurate conversion logs | Successful and failed conversions record the real chain; missing legacy metadata is not falsely labelled Native format |
| R6 | Efficient live channel concurrency | Both list cards and details show current count and configured limit, including unlimited channels; one batched visible-set refresh/cache, hidden pages pause; failure is unavailable rather than a false zero; local versus cluster scope explicit |
| R7 | Failover on upstream concurrency failures | HTTP 429 and HTTP 200 early stream concurrency failures try another eligible channel without repeatedly choosing the same busy one; no replay or mixed answer after model output; preserved cancellation, quotas and error reporting |
| R8 | Queue when all eligible channels are busy | Keep the request pending in new-api and automatically admit it when capacity becomes available, including a single eligible channel; avoid immediate disconnection for ordinary saturation; cancellation removes the waiter; bounded resource use and upstream backoff; pins wait on their allowed channel |
| R9 | Priority Enter bug | Enter commits priority without opening details; card-focused Enter/Space and explicit details actions still work |
| R10 | Compact channel card layout | Priority and weight side by side; response metrics compact; used amount and used tokens on the same row at normal card widths, with usable narrow layouts |
| R11 | Move card groups into channel details | Card group display removed; details show groups and the models available through that channel/group, respecting effective restrictions rather than inventing availability |
| R12 | Exact and compact token counts | Channel cards use compact 万/亿 totals; hover/details include exact and abbreviated values. The later approved log correction restores plain exact input/output/cache counts without parentheses or taller rows; preserve the original compact collapsed mobile summary, locale safety, zero/missing distinctions and raw numeric sorting |
| R13 | Upstream price sync performance | Large catalogs do not freeze preview or confirmation; bounded rendered rows, batched preparation, responsive progress; all selected models saved once atomically with frozen pricing/rate/version and unchanged pricing semantics |
| R14 | Sortable used balance in users | Separate Used balance column; numeric ascending/descending sorting across server pagination/filtering, not only the visible page |
| R15 | One collapsible Logs menu | Usage, audit, drawing/image and task logs are four children of Logs; same expand/collapse style as System settings; child permissions and direct routes preserved |
| R16 | Access configuration and client cards | Rename Chat navigation to Access configuration; landing page shows configured client cards and working click-to-connect actions, reusing existing configuration flows |
| R17 | Per-channel Codex goal restrictions | User clarified this means Codex /goal. Recognized active-goal requests skip channels denying this feature and use another eligible channel for the model; if none allows it, reject locally before an upstream request; never match arbitrary prompt text merely containing goal |
| R18 | API key regeneration | Regenerate the credential on an existing key while retaining settings/history; old credential becomes invalid, including caches; ownership, sensitive-action checks and auditing verified |
| R19 | Custom API key secret | Optional custom secret on creation/regeneration, with server-side allowed format/strength/uniqueness and collision handling; normal random generation stays available; secret excluded from audit/log output |
| R20 | CORS allowlist | Replace wildcard-origin plus credential combination with validated exact origins; correct preflight/Vary/header behavior and allowed versus denied browser tests; report configuration location without claiming unobserved deployment settings |
| R21 | Official service status page | New Service status navigation, themed official-status page using supported official sources, timestamp/staleness and unavailable states; display: 标准数据来自各厂商官方状态页，可能存在延迟；本页不代表本站自身可用性。 |
| R22 | Local channel fingerprint checks | The intelligence-detection page offers admin-triggered channel fingerprint checks using reviewed lm-detector source/assets locally, without lm.ikale.io dependency; cancellation/progress/cost and reference-bank limits visible; no IQ or identity guarantee |
| R23 | LinuxDO login avatar | Successful LinuxDO login imports/synchronizes its avatar under the existing avatar-source policy; provider image failures do not break authentication; custom-avatar preference and URL security preserved |
| R24 | Collapsible code administration | Invitation codes and redemption codes share one collapsible sidebar group with their separate pages and permissions preserved |
| R25 | Verified release delivery | Each item checked and committed separately; pi self-checks followed by root review; push/tag the intended revision, wait for successful CI/build, verify published image/tag/assets; server revision/health verification is excluded |

## Confirmed background

- Current direct upstream probes on 2026-11-19 returned 400 chat_completions_disabled for Chat with diagnostic and Codex headers, versus Responses 200. This does not prove the deployed gateway configuration is fixed. Evidence: research/upstream-verification.md; historical anchors retained there.
- Local capacity limiting exists but unlimited channels are not counted. An early SSE write can prevent existing retry logic; native Responses stream failures can return nil relay error. Evidence: research/channel-capacity.md, research/channel-failover.md, research/stream-failover.md.
- Price sync repeatedly parses/serializes the full catalog for each selected model; preview mounts every row. These are source-backed hotspots, not measured timings. Evidence: research/price-sync-performance.md.
- middleware/cors.go currently sets both AllowAllOrigins and AllowCredentials true. New security/configuration work is recorded separately from unverified live settings.

## Constraints and scope boundaries

Preserve existing uncommitted work, protected project attribution, group/key eligibility, billing semantics and all seven locales. Use shared UI components. Do not persist supplied credentials or forward them to third-party analysis sites. No model pricing changes, global UA impersonation, provider-domain special cases, arbitrary prompt keyword blocking, or claims that a fingerprint proves identity/IQ. Actual database behavior changes require real SQLite/MySQL/PostgreSQL checks; schema changes additionally require upgrade/idempotency validation. Independent modules must build with GOWORK=off. Existing live requests are not replayed after model output.

Push/tag/release are explicitly in scope, superseding the earlier no-publication boundary. Server verification was subsequently excluded; no target host is guessed and no observed rollout is claimed. Service-status data is vendor status, not this gateway's availability.

## Planning status and remaining evidence

Implementation and publication are authorized and active. Research covers all 25 requirements, and 19 child tasks have PRD/design/execution plans and curated context manifests. Independent review corrected unlimited-channel Redis failure semantics, credential cache races and unsupported fingerprint identity claims. Current commit and cloud-verification evidence is recorded in report.md and the verified-release task; no server target is required for the approved final scope.
