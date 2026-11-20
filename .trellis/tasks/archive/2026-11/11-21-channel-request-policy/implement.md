# Execution plan

1. [x] Recover audit; inspect existing four idle pi workers in new-api workspace w4C.
2. [x] Record PRD/design, file ownership and user authorization to implement after planning.
3. [x] Activate task; send each pi its task brief and confirm actual work begins.
4. [x] Pi 1: diagnostic endpoint/request policy and narrow endpoint UI regressions, independently reviewed and committed as 97313aded; buffered failure usage follow-up is 32be8c771. Cloud validation remains required.
5. [x] Pi 2: fingerprint freshness/defaults, Wham, metrics and balance follow-ups implemented; integrated diagnostics cover post-wait policy. All workers are frozen for final validation.
6. [x] Pi 3: discovery draft transferred to native reviewer; current previews, cancellation, complete enabled-key inventory and guarded persistence integrated with independent regressions.
7. [x] Pi 4: price/inference/shared management/Ollama and Codex discovery implemented; independent service audit found no remaining blocker.
8. [x] Root and independent reviewers checked double lease, settlement, cancellation, refresh, specialized endpoints and latest policy; final stale ChannelInfo/routing publication fixes included.
9. [x] Update narrow executable specs without staging unrelated dirty specs; root commits independent changes with hooks disabled.
10. [x] Pushed fork/merge/upstream; exact-SHA cloud CI 44822062923 passed. Fast-forwarded fork/main to de5c96736f24968a81e62dee8b9960f360915ed1; release run 44824384740 completed successfully. Verified the published custom-20261121-de5c967 tag, seven release assets, four binary checksums and independent amd64/arm64 GHCR evidence. No server verification.

## Cloud validation only

Existing .github/workflows/ci.yml runs root/relaykit/tokenkit vet/build/test, frontend typecheck/test/lint/build, real SQLite/MySQL/PostgreSQL policy integration and Redis/race checks. Extend focused selectors as needed. Do not execute local tests, builds, lint, typecheck, benchmark or browser checks; source review and formatting allowed. No real paid upstream calls.

Workers report exact files, helper contracts, authored-but-unrun tests, risks and completion to worker-N.md. Root alone stages/commits/pushes. Preserve unrelated dirty files. Do not contact deployment servers or claim they pulled a version.

## Integration checkpoints

- `58d462bfe`: fixed-channel trusted admission; cloud run `44796410840` passed backend, frontend and real database/Redis jobs.
- `ae68b4125`: default-three/current-policy fingerprint orchestration. Backend and frontend jobs passed; the remaining integration job was superseded by the next push, so this SHA is not independently all-green.
- `c2b8026c0`: management HTTP admission/body lifetime/transport, including same-origin absolute-redirect Host preservation. Independently source-reviewed and pushed; cloud run `44801500137` passed all three jobs.
- `1b99bc8c3`: saved-channel upstream pricing synchronization respects current channel transport, static headers, enabled keys and admission.
- `5ad885e9c`: current-policy inference management requests; cloud run `44803946541` passed backend/frontend but exposed a delayed metrics Redis-global race in the integration race gate.
- `a1a1faea7`: Wham management and byte-exact source credential CAS, with real-database selectors. Cloud run `44808207366` passed backend; remaining jobs were superseded by the metrics correction.
- `d6294cbb4`: snapshot the selected Redis backend synchronously before scheduling metrics work. All three cloud jobs passed in run `44809265363`. Integration logs record MySQL 8.4.13, PostgreSQL 16.16 and Redis 7.4.12.
- `92941dc0d`: guarded current-row result writes and atomic checked cache status/routing foundation. Independent source review passed; later review identified missing static-auth/config cache identity fields, under correction before final validation.
- `8ab5fb06a`: exact-whitelist console management error localization, both pricing paths and stored balance display. All seven locales normalized; independent source review and all three cloud jobs passed in run `44812151297`.
- `d2f140f3b`: extend checked cache publication to static authentication, provider config and multikey model/user scopes; new cases preserve replacement status/routing. Pushed for cloud checks.
- `5805d982b`: balance dispatch and proven credential-refresh provenance. Cloud run `44814512084` exposed cap-only database/cache status divergence; no completion claim was made.
- `de2f3d8de`: exact nested diagnostic/discovery error localization while preserving unrecognized provider text.
- `32be8c771`: retain reported usage on buffered Responses failure and reject diagnostic EOF without a terminal event; ordinary fallback unchanged.
- `97313aded`: shared fixed-channel diagnostic preparation, current-policy admission/regrant and safe busy replay; source review passed.
- `76489e414`: correct cap-only status publication while preserving cached configuration and every non-cap identity guard; also correct the unrelated plugin field in the built-in balance fixture.
- `24f607376`: cloud run `44817543281` passed backend/frontend and database selectors; final race stage exposed missing balance-fixture i18n initialization. Fixed separately in `3e59f70ec`; final exact-SHA validation remains required.
- `f9315ecc9`: strict inventory failures reuse existing translated categories without translating arbitrary provider details.
- `593c0976f`: final discovery/Codex/Gemini/Ollama integration, source guards, current saved-preview policy, original per-key refresh index and scheduling/routing preservation. Independent source review passed; cloud pending.
- `ab2cf1b36`: external database selectors now include all management discovery regressions and original-key refresh cases.
- Balance ownership includes plugin query chains and cancellation through `channel_balance.go`; keep its inner sender free of duplicate admission.
- API-origin Midjourney/async task polling is a separate lifecycle boundary. It is not part of the console diagnostics/management acceptance claim; preserve task-bound credentials, and do not infer those pollers are covered by ordinary Relay.

## Closed integration review

- All four pi implementation scopes are finished and frozen: diagnostics/UI, fingerprint/metrics/Wham/balance, discovery/Gemini, and pricing/inference/Ollama/Codex discovery.
- Independent reviews confirmed guarded credential refresh and result persistence, current scheduling/routing publication, admission/body lifetime and exactly-once usage handling. No remaining source blocker was found.
- Cloud database and race selectors cover consolidated balance, Wham, multikey discovery and original-key refresh regressions. The final exact-SHA integration run and repeated main-release validation both passed.
- Task completion and release evidence are recorded in report.md. Follow-up archive/journal commits contain metadata only; product main and the published tag remain the exact tested SHA.
