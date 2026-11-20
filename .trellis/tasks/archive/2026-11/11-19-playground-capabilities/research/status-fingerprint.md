# Service status + model fingerprint research

Research only. No product edits, no commits, no billable probes, no credentials sent anywhere.
All findings below come from the pages listed in §4 with exact URLs.

## 1. tcping.cn status page — official feeds, no public API

Observed page: `https://www.tcping.cn/status` (HTTP 200, title
`AI状态监控_Claude/ChatGPT/Gemini可用性实时检测 - TCPING.CN`).

- The page states in its own FAQ: 「全部直接取自各厂商**官方状态页**的公开接口：组件级的实时状态…以及官方发布的故障事件记录。本站不做自建拨测」. It claims a 5-minute sync cadence and 90 days of history, and separates web from API components.
- Page body contains no documented public API, no OpenAPI/schema link, no terms page, and no "开放接口" section; the only navigational page found is `/about`. The status UI loads its data from an **internal endpoint** `/api/status` referenced from
  `https://www.tcping.cn/_astro/status.astro_astro_type_script_index_0_lang.DavHXn1n.js`.
- Therefore: undocumented internal endpoint, not a public API. Do not build against it and do not scrape it. If the sidebar needs status data, consume the **providers' own official feeds** directly (the same upstreams tcping consumes), with attribution and caching.
- The project already has an admin status surface: `router/api-router.go:28-31` (`GET /api/status`, `GET /api/uptime/status`, `GET /api/status/test` → `controller.TestStatus` at `controller/misc.go:25`), and an existing Uptime Kuma integration path (`GetUptimeKumaStatus`). A "Official status" sidebar entry should extend that surface rather than add a parallel fetcher.

Required disclaimer text (verbatim, per user):
> 标准数据来自各厂商官方状态页，可能存在延迟；本页不代表本站自身可用性。

## 2. lm-detector / lm.ikale.io — license, algorithm, data, self-hosting

Repository `https://github.com/Ikaleio/lm-detector` (GitHub API, checked 2026-11-19):
`MIT` license (`https://api.github.com/repos/Ikaleio/lm-detector`, LICENSE file:
"MIT License / Copyright (c) 2026 xqy2006"), created 2026-09-13, last push
2026-11-19T05:39:33Z, 2403 stars, 92 forks, 8 open issues, not archived, homepage
`https://lm.ikale.io`.

**What it actually is** (`https://lm.ikale.io/docs/principles`,
`https://raw.githubusercontent.com/Ikaleio/lm-detector/main/docs/content/docs/principles/ranking.mdx`):
a *statistical* fingerprint of numeric-preference distributions, not an identity proof.

- Challenge: the model is asked to write ~292–332 (reference bank: 218–333) integers in
  1–355 by first instinct; tools/code/ordered patterns are forbidden.
- Parsing: longest run of 1–355 integers; an answer is dropped when valid integers are
  fewer than `max(80, ceil(0.55N))`.
- Features: 355-value smoothed Hellinger embedding (`alpha = 0.5`), 4 position segments ×
  16 value bins, last-digit distribution, position kernels, and lag/transition statistics
  (751 dims total across four blocks with fixed weights 0.6/0.15/0.125/0.125).
- Ranking: `s = 0.5·z(f) + 0.5·z(q)` with `f = 0.5·z(LDA) + 0.25·z(kNN=7) + 0.25·z(centroid)`,
  environment nuisance subspace removed by SVD, LDA shrinkage 0.5, weights chosen by
  leave-one-environment-out CV.
- Confidence: temperature softmax **within the reference bank only** (`p_k` sums to 100%
  across enrolled models). Docs are explicit: a model outside the bank still receives a
  "closest" candidate; `p_k` is **not** the probability of being that model.
- Sequence sanity screen: lag-1 autocorrelation and adjacent-pair ratios; failing it prints
  "不像语言模型回答" and gives no ranking. The doc states this cannot prove the numbers'
  origin.

**Data actually shipped in the repo** (`https://api.github.com/repos/Ikaleio/lm-detector/contents/data`):
`unified_bank.json` 15,080,046 B, `unified_reference.jsonl` 19,911,838 B,
`shared_detector.bin` 5,443,668 B, `enrollment-suite.json` 152,315 B,
`tokenizer_bank.json` 78,722 B, plus `collections/` and a `README.md`. MIT covers code and
data in the repository, so vendoring the bank plus detector binary is license-compatible
with this AGPL-3.0 project as long as the MIT notice and copyright are preserved.

**Offline / self-host path** (`https://api.github.com/repos/Ikaleio/lm-detector/contents/offline`):
`train.ts`, `retrain.ts`, `calibrate.ts`, `reference.ts`, `export.ts`, `math.ts` — the
sampling → enroll → retrain pipeline runs locally (`fpd sample|enroll|retrain` in the README).
No hosted `lm.ikale.io` dependency for scoring.

**Go SDK** (`https://api.github.com/repos/Ikaleio/lm-detector/contents/sdk/go`):
module `github.com/Ikaleio/lm-detector/sdk/go`, `go 1.23`, single dependency
`github.com/klauspost/compress v1.18.4`; files `lmfpd.go`, `ranker.go`, `features.go`,
`analysis.go`, `challenge.go`, `codec.go`, `data.go`, `request.go`, `sample.go`,
`templates.json`. This is directly usable from this project's Go backend (root module is
Go 1.26) and is the cleanest reuse path: scoring and ranking stay in-process, and the
reference bank can be embedded or served locally.

**The hosted site is not required.** `https://lm.ikale.io/docs/reference/proxy-api/index.mdx`
documents only a forwarding proxy (`GET/POST/OPTIONS /api/proxy`) used so browsers can reach
upstream APIs past CORS; it does no scoring. Scoring happens client-side or in the SDK/CLI.
The web page at `https://lm.ikale.io/` is a static bundle (2315 B HTML + `/assets/*.js`), so
nothing about detection depends on the author's server beyond optional proxying and the
reference-data download.

**Maintenance and limits** (`https://lm.ikale.io/docs/disclaimer`,
`https://lm.ikale.io/docs/principles/ranking`):
actively maintained (commits on 2026-11-17/18/19: `f359c49`, `7663b65`, `33fb43f`,
`727d7b3`, `70159b7`); results are explicitly research-only; reference bank coverage changes
over time; confidence is bank-relative; tokenizer probing is optional, off by default, sends
~12 extra short requests, and "分词器一致不能证明模型身份".

**Hard limitation to state in the UI:** a fingerprint match cannot establish model IQ,
identity, or authenticity. The upstream docs say the same, and the sequence check "不能证明数字的来源".

## 3. Comparison with existing channel test / inference UI

- Backend: `controller/channel-test.go` builds a synthetic request and runs it through the
  normal relay (`runChannelTestRequest` at `:319`, `info.IsChannelTest = true` at `:395`),
  with key scoping (`checkChannelTestKey` `:122`) and endpoint normalization
  (`normalizeChannelTestEndpoint` `:62`). Route: `GET /api/channel/test/:id`
  (`router/channel-router.go:70`, permission `authz.ChannelOperate`), list at
  `web/src/features/channels/api.ts:867`, single test at `:360-370`.
- Frontend: `web/src/features/channels/components/dialogs/channel-test-dialog.tsx` already
  offers endpoint selection (`endpointTypeOptions` `:179`, Gemini variant `:207`) and stream
  handling (`STREAM_INCOMPATIBLE_ENDPOINTS` `:227`).
- Implication: a fingerprint probe is a *multi-request, multi-turn* flow (3 challenges ×
  3 answers, plus optional tokenizer probes), so it does not fit the single-shot channel
  test endpoint. It needs its own admin-triggered action with explicit cost disclosure and a
  per-run budget, reusing the existing test-context plumbing (`middleware.SetupChannelTestContext`,
  `controller/channel-test.go:290`) so keys stay server-side.

## 4. Source list (primary)

- `https://www.tcping.cn/status` — page and its FAQ (official-feed claim, 5-minute sync, 90-day history).
- `https://www.tcping.cn/_astro/status.astro_astro_type_script_index_0_lang.DavHXn1n.js` — references internal `/api/status`.
- `https://www.tcping.cn/about` — only related navigational page found; no API documentation.
- `https://api.github.com/repos/Ikaleio/lm-detector` — license, stars, forks, push date.
- `https://raw.githubusercontent.com/Ikaleio/lm-detector/main/LICENSE` — MIT, Copyright (c) 2026 xqy2006.
- `https://raw.githubusercontent.com/Ikaleio/lm-detector/main/README.md` — algorithm summary, protocols, data, disclaimers.
- `https://lm.ikale.io/docs/principles` and `.../docs/principles/ranking` — challenge, parsing, features, ranking, calibration.
- `https://lm.ikale.io/docs/disclaimer` — research-only, no identity claim, user pays upstream costs.
- `https://lm.ikale.io/docs/reference/proxy-api/index.mdx` — proxy-only API (`/api/proxy`), no scoring endpoint.
- `https://api.github.com/repos/Ikaleio/lm-detector/contents/data` — reference bank sizes.
- `https://api.github.com/repos/Ikaleio/lm-detector/contents/offline` — local train/retrain/calibrate pipeline.
- `https://api.github.com/repos/Ikaleio/lm-detector/contents/sdk/go` and `.../sdk/go/go.mod` — Go SDK, Go 1.23, one dependency.
- `https://api.github.com/repos/Ikaleio/lm-detector/commits?per_page=5` — recent maintenance activity.

## 5. Proposal shape (for the approved plan, not implemented)

1. Sidebar "Service status" → "Official status": fetch each provider's own official status
   feed server-side, cache with a short TTL, render component-level state, show the required
   disclaimer verbatim. Do not call or scrape `tcping.cn`.
2. Fingerprint check as an admin-only, explicitly opt-in action next to the existing channel
   test, not inside the user playground. Prefer the MIT Go SDK in-process over any hosted
   service; if not vendored, gate on a configured local binary/SDK and never call
   `lm.ikale.io` implicitly.
3. Every result screen must carry: bank-relative ranking only, no identity/IQ guarantee,
   upstream token cost paid by the operator, and "not evidence of substitution".
