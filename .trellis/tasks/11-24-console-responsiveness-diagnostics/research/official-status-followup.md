# Official provider status follow-up — source inventory and official evidence

## Scope, ownership and evidence limits

- Worker: Herdr `w6E:p2`, model `gpt-6.1-sol`; coordinator `w5D:pG`.
- **OLD source:** `D:\My2KProjects\new-api`. Every source command used this explicit cwd and/or absolute old-source paths.
- **TARGET, untouched:** `E:\MyNewProjects\WebProjects\new-api`. No target inspection/edit/pull/test/build or migration was performed.
- Sole write boundary: `D:\My2KProjects\new-api\.trellis\tasks\11-24-console-responsiveness-diagnostics\research\official-status-followup.md` (this report). No production source, tests, locales, specs or peer-owned files were edited; no staging/commit/push.
- Source snapshot: old `HEAD` was `ef8d72ff4fcff052b97d42641fb26b2d3c040367` on 2026-11-25 at 20:14 UTC. The inspected backend/UI/test paths had no scoped working-tree changes at that observation. Line references below describe that snapshot, not an eventual migration/release.
- Read gates completed in order: root `AGENTS.md`; task `implement.jsonl`; task PRD/design/implementation; `web/AGENTS.md`; project `.agents/skills/shadcn-ui/SKILL.md` and `web/components.json`; all four implementation-context entries (source findings, database guidelines, model selection, channel card interactions). No dedicated official-status spec was found in `.trellis/spec`.
- Public remote research: HTTPS GET of official documentation, status pages/feeds and the Google status page's public static JavaScript; bounded retries; one read-only public DNS provenance attempt. Responses were inspected with PowerShell, not by executing repository code. **No new-api application/production-instance requests, credential use, provider inference/internal RPC calls, browser/app launch, local tests/build/vet/lint/typecheck/format/DB/benchmark or executable application validation.** All future code validation remains hosted-only.
- Remote observations span **2026-11-25 19:59–20:14 UTC** (2026-11-26 03:59–04:14 UTC+08). They prove response shape/reachability from this research environment at those times, **not production egress, permanent provider health, or a validation pass**. Provider states can change.

## Executive findings

1. The old catalog has **four**, not eight, entries: OpenAI, Anthropic, DeepSeek and **Google Cloud Vertex Gemini**. xAI, Moonshot, MiniMax and Mistral are absent. The existing Google source explicitly excludes AI Studio; it cannot honestly become a Gemini API card by relabeling.
2. Five providers have directly verified, compatible official summary JSON: **OpenAI, Anthropic, Moonshot, MiniMax and Mistral**. All five also returned recent-incident JSON. These are official-source reads, not sample data.
3. **DeepSeek's real configured URL failed before HTTP/JSON parsing in this environment.** Repeated .NET requests failed TLS with a remote connection reset; `curl.exe` independently returned exit 35, `Recv failure: Connection was reset`. Its official docs successfully loaded and link the same status host. The production failure reason remains unproved because the current service discards each source error.
4. **xAI:** official developer docs link `https://status.x.ai`. That page and candidate JSON/RSS paths returned HTTP 403 Cloudflare HTML, including retries. The page is an official source; a working machine endpoint/schema was **not verified**. Do not describe the guessed Statuspage paths as documented xAI APIs.
5. **Gemini API:** `https://aistudio.google.com/status` is the correct official surface. Public static source explicitly says “Google AI Studio and the Gemini API Status.” Its initial HTML is only an app shell. The module uses an **internal** `ListIncidentsHistory` RPC, not a verified/documented public status-feed contract. No internal RPC, embedded credential or browser session was used. A source-link/unknown card is honest until a sanctioned data contract is established.
6. Current cards have no fixed height, expansion or internal component scroll. All components/incidents render inline and increase card/page height. The reusable pieces already exist; no registry install is needed.
7. Current `stale` means **the latest refresh failed after some prior success**, even if that success was only five minutes ago. It is **not a 30-minute age rule**. Preserve latest-attempt availability separately from a 30-minute last-success age assessment.
8. Statuspage-style **summary contains unresolved incidents**, not recent resolved history. Empty summary incidents means no listed active incidents, **not “no recent incidents.”** Explicit history reads are needed if the card promises latest/recent incidents.
9. There is **no uptime percentage** in the current official-status DTO or verified summary/history contract. Do not copy percentages from unrelated monitoring, status-page documentation examples or screenshots.

## 1. Existing source inventory

All paths in this section are under the OLD root `D:\My2KProjects\new-api`.

| Path / lines | Responsibility and actual contract |
| --- | --- |
| `service/official_status.go:18–60` | Five-minute cache TTL; 2 MiB response limit; component/incident/provider DTOs; four fixed sources; one `googleCloud` parser discriminator. Provider carries `status`, source/fetch/check timestamps, `stale`, `unavailable`, components and incidents. No uptime/history/failure-reason/freshness-threshold field. |
| `service/official_status.go:72–84` | Shared HTTP client: eight-second request timeout; HTTPS same-original-host redirects only; fewer than three redirect hops. Keep this egress boundary. |
| `service/official_status.go:92–147` | Cache and `singleflight` shared refresh; per-source concurrent requests under an independent ten-second background context. Caller cancellation does not cancel the refresh other readers share. No database or Redis use here. |
| `service/official_status.go:114–128` | Error becomes unknown/unavailable, optionally retaining previous same-index/same-ID entry. `fetched_at` only advances on success; `checked_at` advances on every attempt. Source errors are swallowed rather than returned/logged as a diagnostic category. |
| `service/official_status.go:149–201` | GET exact configured feed, `Accept: application/json`, require HTTP 200 and bounded readable body, then JSON decode. Summary requires nonempty `page.id` and `status.indicator`; copies at most 100 components and 20 incidents. |
| `service/official_status.go:205–245` | Google Cloud incident adapter, product ID `Z0FZJAMvEB4j3NbCJs6B` only; absence of an open matching incident becomes operational. This is a published Cloud incident feed, **not Gemini Developer API health**. |
| `controller/official_status.go:9–17` | Thin request-context/service/API-success wrapper. Per-source failures normally appear in successful aggregate responses because the cache converts them to individual unavailable entries. |
| `router/api-router.go:18–31` | Public `GET /api/official-status` under the shared API middleware, with no added auth middleware on this route. Fixed public source data only; do not accidentally attach private application/credential details. This is a source observation, not a request made by this worker. |
| `web/src/features/service-status/api.ts:22–45` | Matching TypeScript DTO, accepts nullable component/incident arrays, consumes query `AbortSignal`, rejects API business failure using `requireServerSuccess`. |
| `web/src/features/service-status/index.tsx:36–77` | Status labels/tones; shared query key; five-minute `staleTime` and refetch interval; no background interval; retries disabled; interface language normalized through `toIntlLocale`. |
| `web/src/features/service-status/index.tsx:109–207` | Two-column large-screen grid, one plain `Card` per returned entry; unbounded component/incident lists. `unavailable` overrides the headline health badge; retained components can still show prior operational state. Source link and three timestamps already exist. |
| `web/src/routes/_authenticated/service-status/index.tsx:24–31` | Sidebar-module check and existing page component. No need to broaden route/auth scope for layout. |
| `service/official_status_test.go` | Existing suitable backend regression home; see section 6. |
| `web/src/features/service-status/__tests__/official-status.test.tsx` | Existing suitable UI regression home; see section 6. |
| `web/src/i18n/locales/{en,zh,zh-TW,fr,ru,ja,vi}.json`, `web/src/i18n/static-keys.ts` | Seven-locale/static-key integration must go through the sanctioned locale owner, not this research worker. Existing strings include Unknown, Unavailable, Stale, Last successful fetch, Last checked, Source updated, Official source. |

### Cache/parse behavior worth preserving or clarifying

- First-ever failure yields `status=unknown`, `unavailable=true`, `fetched_at=0`, `stale=false`, a nonzero `checked_at`, and no invented components/incidents. The UI maps zero timestamps to Unknown. **This is the correct never-success baseline.**
- Failure after success retains last known state and timestamp, sets `unavailable=true` and `stale=true`. Earlier snapshots are not mutated. A recovery advances the success timestamp and clears both flags.
- Cache entries are process memory. A restart loses last success; “no prior success” can mean no success in this process, not evidence that a vendor has never been reachable globally.
- Retention currently matches previous entries by **index plus ID**. Expanding/reordering the catalog can discard otherwise available snapshots. Future code should match stable provider identity, not assume the four-entry order remains unchanged.
- Unknown nonempty status indicators currently decode successfully but remain `status=unknown`. A valid transport/JSON receipt is not the same thing as a recognized health state.
- Parsing currently requires `page.id` and indicator but does not prove provider identity or validate every timestamp/component status. A future HTML/internal-app response must not count as successful machine status merely because HTTP is 200.
- Incident bodies, URLs, impact and component relationships are not retained. `scheduled_maintenances` is ignored. Avoid UI promises beyond the exposed fields, or separately define the additive contract first.

## 2. Eight official sources — evidence ledger

### 2.1 OpenAI — verified summary and history

- Official page: <https://status.openai.com/>.
- First-party cross-reference: <https://developers.openai.com/api/docs/guides/error-codes> returned HTTP 200 and links the status page for server errors. <https://help.openai.com/en/> also returned 200 and links it as “Service Status.”
- Summary: <https://status.openai.com/api/v2/summary.json> returned **HTTP 200, `application/json`**, with `page`, `status`, flat `components`, and `incidents`. `page.id=01JMDK9XYNY6RXSED6SDWW50WY`. Example observed fields were real `indicator=minor`, components including Chat Completions/Responses/Embeddings, and a monitoring incident; this is a dated snapshot, not an instruction to hardcode that state.
- Recent history: <https://status.openai.com/api/v2/incidents.json> returned **200**, 25 records at the observation, including monitoring and resolved records. Timestamps are RFC3339 strings; `shortlink` was absent/null in inspected records, so never require that field or synthesize a link from another provider's conventions.
- Human page is now powered by incident.io; the compatible JSON works despite <https://status.openai.com/api> returning 404. **Do not require every vendor to have the same HTML host implementation or `/api` documentation page.**
- Scope: the official provider page includes non-gateway products as well as API components. Provider-wide status must not be labeled API-only without explicit component filtering.

### 2.2 Anthropic — verified summary, API docs and history

- Official page: <https://status.claude.com/>.
- First-party cross-reference: <https://docs.claude.com/en/api/errors> redirected to <https://platform.claude.com/docs/en/api/errors>, HTTP 200; footer links `https://status.claude.com/` as Status.
- Public feed documentation: <https://status.claude.com/api>, HTTP 200, explicitly documents summary/components/status/unresolved/all incidents and maintenance endpoints.
- Summary: <https://status.claude.com/api/v2/summary.json>, **200, `application/json; charset=utf-8`**; `page.id=tymt9n04zgry`; compatible flat schema. Components include Claude API (`api.anthropic.com`), claude.ai, console and other platforms/products.
- Recent history: <https://status.claude.com/api/v2/incidents.json>, **200**, 50 records at the observation.
- The documented summary definition is component status plus **unresolved** incidents and upcoming/in-progress maintenance. Documentation contains illustrative incident examples; those were **not** treated as live evidence.
- Scope: an overall minor state can coexist with an operational API component and degraded non-API product. Render official component distinctions rather than equating the overall card with the gateway's own availability.

### 2.3 Gemini — correct official page; machine integration not yet established

- Correct official page: <https://aistudio.google.com/status>, **HTTP 200, `text/html; charset=utf-8`**.
- Initial response contains `<app-root></app-root>` and public static-script references, **not a summary/status JSON object**. HTTP 200 proves the page loaded, not that all Gemini services are operational.
- Read-only inspection of the status page's linked `www.gstatic.com` status module (`m=Zg7ntb`) found:
  - title **“Google AI Studio and the Gemini API Status”**;
  - services **API**, **Multimodal Live API**, **Google AI Studio**;
  - official UI states OPERATIONAL / PARTIAL_OUTAGE / FULL_OUTAGE;
  - separate free/paid tier handling and an explicit warning that availability varies by billing surface, model and API feature;
  - a call to `/$rpc/google.internal.alkali.applications.makersuite.v1.MakerSuiteService/ListIncidentsHistory`, polled by its own UI every ten minutes.
- That RPC is **internal application transport**, not a verified/documented public feed. Static-source discovery does not establish a supported server-to-server status contract. **No RPC call, embedded key extraction, session borrowing, browser automation or production API request was made.** Do not add this transport to production on the strength of this report.
- Honest interim projection: a Gemini card with its official link and explicit unknown/unavailable automated data, no invented successful-fetch timestamp, components, incidents or “all operational” state. A supported official machine source remains a follow-up blocker.

#### Why the current Google Cloud entry is not a substitute

- <https://status.cloud.google.com/> returned 200 and directly links its JSON history/product catalog/schema:
  - <https://status.cloud.google.com/incidents.json>
  - <https://status.cloud.google.com/incidents.schema.json>
  - <https://status.cloud.google.com/products.json>
- All three returned 200. Product catalog identifies `Z0FZJAMvEB4j3NbCJs6B` as historical title **Vertex Gemini API**, current title **Gemini on Agent Platform**. The schema calls product titles unstable; ID is the stable association.
- The current source adapter uses that ID and the official Cloud incident history correctly **for that Cloud product**. It neither covers the AI Studio service group nor establishes the Developer API state.
- The history's latest matching incident modification may be months old on a quiet Cloud product. That timestamp is an incident update, not the time of the last successful status retrieval.
- If a Cloud view is retained later, label its exact scope explicitly; do not count it as the required Gemini Developer API source or silently replace it during migration.

### 2.4 xAI — official page verified by docs; remote access blocked

- First-party docs: <https://docs.x.ai/docs/overview> redirected to <https://docs.x.ai/overview>, **HTTP 200**. Its Status navigation explicitly links **`https://status.x.ai`**.
- Official page: <https://status.x.ai/>, **HTTP 403**, Cloudflare “Sorry, you have been blocked” HTML.
- Candidate paths tried under the same official host: `/api/v2/summary.json`, `/api/v2/status.json`, `/summary.json`, `/api`, `/feed.rss`, `/history.rss`. Each returned **403 HTML**, not a JSON/RSS schema. Summary/page were retried with HTTP/1.1/TLS1.2 and remained blocked.
- These candidate paths are **not verified xAI feed contracts**; uniform 403 does not establish that the paths exist or that xAI uses Statuspage. No challenge/captcha bypass or alternate third-party status proxy was attempted.
- Honest result: official attribution/link is established, automated status/incident data is unavailable **from this environment**. This is not evidence of a Grok API outage. Keep one card with unknown state rather than omit xAI or copy another monitor's health.

### 2.5 DeepSeek — real configured URL; transport fails before parse

- Official docs: <https://api-docs.deepseek.com/>, **HTTP 200**; navigation links <https://status.deepseek.com/> as **API Status**.
- Existing configured feed: <https://status.deepseek.com/api/v2/summary.json>.
- Repeated requests to the configured summary, status root, `/api` and `/api/v2/status.json` failed with the .NET exception chain:

  ```text
  System.Net.Http.HttpRequestException: The SSL connection could not be established
  System.IO.IOException: Unable to read data from the transport connection
  System.Net.Sockets.SocketException: remote host forcibly closed an existing connection
  ```

- A bounded summary retry forcing HTTP/1.1/TLS1.2 gave the same result. Independent Windows `curl.exe` summary read, with normal TLS verification and no redirects:

  ```text
  curl: (35) Recv failure: Connection was reset
  curl_exit=35
  ```

- Control read of **official DeepSeek documentation**, not inference API: `curl.exe` reported HTTP 200, SSL verification result 0, exit 0.
- A public CNAME lookup for the status host also failed via this environment's DNS server; that is an additional local resolver observation, **not proof of a globally invalid domain**. The other fetches are sufficient to record the narrower TLS/reset finding.
- **Actual old fetch path:** `fetchOfficialStatus` calls `client.Do` at lines 156–159. On these observed failures it would return before checking HTTP status, body size or `common.Unmarshal`. There was no body available to diagnose a schema mismatch. The summary's live JSON contract could not be independently verified here.
- **Production symptom limitation:** this report does not reproduce the production service's Go client or egress. The old code suppresses the per-source error, so the visible never-success entry cannot distinguish DNS/TLS/timeout/HTTP/parse failure. Do not claim this environment's TLS reset is the proven production root cause.
- Preserve `fetched_at=0`, unknown/unavailable and “no successful fetch” semantics until a genuinely validated fetch succeeds. Do not fill it with docs examples, process start time, `checked_at`, another provider's status, or guessed component values. No previous successful feed or uptime has been established by this research.
- Future authorized diagnostics can add a bounded non-secret failure category (transport/timeout, HTTP, invalid feed), without returning challenge bodies, full transport details, private egress information or secrets. Keep normal certificate validation, fixed hosts and redirect restrictions; disabling TLS checks is **not** a fix.

### 2.6 Moonshot — verified summary, documented API and recent history

- Official page: <https://status.moonshot.cn/>. Human HTML identifies **Moonshot AI**, links Moonshot's own site/privacy policy, and is powered by Atlassian Statuspage on the vendor's domain.
- Public API docs: <https://status.moonshot.cn/api>, **200**, explicitly document `/api/v2/summary.json` and incident-history endpoints.
- Summary: <https://status.moonshot.cn/api/v2/summary.json>, **200, `application/json; charset=utf-8`**; `page.id=10y9y5wx5xh3`; **22 flat components** at the observation; `indicator=none`; no unresolved incidents in that snapshot.
- Components contain both groups and children. Examples: Kimi, Kimi Code, SaaS; `Open API` ID `8psr5dfdld0s` under SaaS; `API Service` under Kimi. Exact scopes matter—Kimi consumer service, code products and Open API are not interchangeable.
- Recent history: <https://status.moonshot.cn/api/v2/incidents.json>, **200**, 50 records. The observation included resolved Kimi Code incidents even though summary incidents were empty.
- **Important real fetch distinction:** `Accept: application/json` on the **root page** returned a different JSON shape with group-only components/nested children and historical incidents, while an HTML Accept returned the human page. `/api/v2/summary.json` returned the flat catalog and unresolved incidents expected by the existing parser. Use the exact feed URL, **not root JSON content negotiation**, or component coverage and incident semantics change silently.

### 2.7 MiniMax — verified first-party link, summary, API docs and history

- First-party docs: <https://platform.minimax.io/docs/> redirected to <https://platform.minimax.io/docs/guides/models-intro>, **200**; footer explicitly links <https://status.minimax.io/> as Status. An earlier guessed docs route returned a 404 with the same footer; the successful root-docs read independently verifies the link.
- Official page and public API docs: <https://status.minimax.io/>, <https://status.minimax.io/api>, **200** with HTML Accept. API documentation explicitly describes the compatible summary/incident contracts.
- Summary: <https://status.minimax.io/api/v2/summary.json>, **200, `application/json; charset=utf-8`**; `page.id=01jl8mrfc0rh`; **3 components**: Large Language Models (LLM), Text-to-Speech, Video Generation. Observed summary had no active incidents.
- Recent history: <https://status.minimax.io/api/v2/incidents.json>, **200**, 50 records; real resolved Text-to-Speech/LLM incidents were present.
- Root `Accept: application/json` also returned a different JSON/history representation. As with Moonshot, use the documented `/api/v2/summary.json`, not the root's negotiated payload.
- Scope: provider-wide model/audio/video service status, not a measurement of this site's configured MiniMax channel.

### 2.8 Mistral — verified official link and compatible feeds despite HTML challenge

- First-party docs: <https://docs.mistral.ai/llms/resources/error-glossary/llms.txt>, **200**. For persistent 500/503 errors it explicitly links <https://status.mistral.ai> to check ongoing incidents.
- Human page <https://status.mistral.ai/> and `/api` returned **403 Cloudflare HTML** from this environment. That did **not** prevent its machine paths from working.
- Summary: <https://status.mistral.ai/api/v2/summary.json>, **200, `application/json; charset=utf-8`** on repeated reads; `page.id=ae27f3c4-86a4-4290-a053-87252545d7f9`; **16 flat components** at the observation. Examples: Completion/Embedding/OCR/Agents/Conversations/Audio APIs, Console, Responses API and Vibe.
- Recent history: <https://status.mistral.ai/api/v2/incidents.json>, **200**, 50 records, including resolved and monitoring states.
- **Freshness counterexample from real data:** summary `page.updated_at=2026-10-16T02:30:27+08:00`; its listed monitoring incident had `updated_at=2026-11-20T01:23:43+08:00`; history included a resolved incident updated `2026-11-24T19:19:16+08:00`. The page-level timestamp is not consistently the latest incident time. A recent HTTP fetch must not be labeled over-30-minutes stale solely from `page.updated_at`, and an old page timestamp must not be rewritten to “now.”
- Scope: provider-wide page, including Vibe/non-completion products. Preserve component-level distinctions and don't present every minor aggregate incident as an API outage.

## 3. Feed contract and incident implications

### Compatible summary parsing

For the five successfully read compatible summaries, the current mapping is directly reusable as an **official page aggregate**, with original component states retained:

| `status.indicator` | Current normalized status |
| --- | --- |
| `none` | `operational` |
| `minor` | `degraded_performance` |
| `major` | `partial_outage` |
| `critical` | `major_outage` |
| `maintenance` | `under_maintenance` (current code supports it; do not assume every vendor publishes it) |
| Other nonempty value | `unknown`, never automatically operational |

- Treat a successful body read/parse and a recognized health value as different facts. HTML shell, challenge page, `{}`, unrelated JSON, malformed JSON, oversized response and failed transport are not evidence of healthy service.
- Preserve feed ID/name/status/timestamp; provider source URLs come from the fixed catalog, not feed-controlled arbitrary links or dashboard input. Do not add broad cross-host redirects just because official branding has moved.
- Component groups and duplicate display names exist. Use stable source IDs; don't merge by text or model name. Current UI/tests already preserve same-name entries.
- Vendor-wide badges and component detail can legitimately differ. Label the badge “official overall status” or equivalent if no API-only projection is intended.

### Active vs latest/recent incidents

- Claude/Moonshot/MiniMax `/api` documentation explicitly states summary = unresolved incidents plus current/upcoming maintenance. The history endpoint includes recent resolved/postmortem entries. No fixed historical-coverage/uptime promise follows from that.
- Actual recent-history endpoint observations: OpenAI **25** records; Claude/Moonshot/MiniMax/Mistral **50** each. These are retrieved record counts, not guaranteed retention windows or request limits for all providers.
- To satisfy **latest/recent incidents**, a future implementation needs a bounded history read and explicit active/history distinction, or must accurately say it displays **active incidents only**. An empty active list is not no history.
- Merge/deduplicate by incident ID. Do not assume endpoint order equals latest valid `updated_at`; validate timestamp sorting and preserve unknown timestamps honestly. Do not transform a resolved event into current degradation.
- If status summary succeeds but optional history fails, do not falsely assert there are no incidents or that summary health failed. Define separate incident-data availability/freshness, or explicitly display “incident data unavailable.” This would be an additive contract, not a change made here.
- Do not follow every shortlink or fetch unbounded pages. Keep the existing timeout/body limits and bounded projection; the inspected history responses were under 200 KiB, but that observation is not a permanent maximum.
- Maintenance remains separate in the published summary. Current DTO ignores that array; do not advertise complete maintenance coverage without a deliberate contract.

## 4. Honest 30-minute freshness and failure presentation

**Recommended interpretation for coordinator approval:** 30 minutes is the maximum **age of a validated successful retrieval**, not a requirement to delay fetching for 30 minutes and not an incident-age threshold. The current five-minute cache/poll cadence can remain independent.

| Situation | Honest presentation / timestamp rule |
| --- | --- |
| Never fetched successfully | Unknown status + feed/data unavailable; no last-success time; `fetched_at=0`; checked time may exist. Do not show an invented healthy default or historical warning claiming a retained success. |
| Valid last success within 30 min, latest check successful | Fresh retrieval; render recognized official state, explicit unknown if the source status is not recognized. Source event/page timestamp remains separate. |
| Latest check failed but last success within 30 min | Show feed unavailable and explicitly last-known state; preserve last success. Age can still be within 30 min—failure is not the age threshold. |
| Last success older than 30 min | Stale last-known data, never present it as current healthy service; latest-attempt availability is a separate fact. |
| Source update is old but retrieval recent | Show the original source update and recent retrieval separately. Quiet incident feeds or imperfect page timestamps do not prove a stale fetch. |
| Browser query failed or polling paused | Retained provider data must age visibly; do not keep green/current merely because server flags were last calculated before a hidden tab/laptop sleep. |
| Future/invalid timestamp | Unknown or invalid freshness, not fresh forever; avoid negative age. |

Implementation implications, **not code ownership**:

- Existing backend `stale` encodes failed-refresh fallback, so don't silently redefine it without updating tests/UI/copy together. Use explicit calculated age/freshness or an additive field, and preserve availability information.
- `fetched_at` is the last valid machine retrieval; `checked_at` is last attempt; `source_updated_at` is a provider's timestamp. Never substitute one for another.
- If latest source change is useful, name it accurately and derive only from parsed source events/page values. Do not call `page.updated_at` an authoritative latest incident time (Mistral disproves this assumption).
- Use a controllable clock and boundary tests for below/at/above 30 minutes. If “stale after 30 minutes” is the product wording, the exact inclusive boundary should be selected explicitly; no executable validation was performed here.
- Status collection is not live inference monitoring. Keep the current attribution/delay disclaimer and never claim this proves this site's online/offline state or configured channel access.

## 5. Fixed-height eight-card UI — reuse inventory and acceptance boundary

### Existing candidates inspected

| Candidate | Reuse / limitation |
| --- | --- |
| `web/src/components/ui/card.tsx` | Existing Card is already flex-column and overflow-hidden; use it as frame. A fixed **outer** height plus `min-h-0` flex sizing is still required; minimum height alone will grow with expansion. |
| `web/src/components/ui/scroll-area.tsx` | Existing Base UI ScrollArea. Actual `data-slot=scroll-area-viewport` uses `size-full`; ancestors need a bounded height/min-height chain. Test the real scroll target, not just an overflow class on a wrapper. |
| `web/src/components/ui/collapsible.tsx` | Existing Base UI Collapsible/Trigger/Panel; use existing accessible expansion instead of hand-rolled click-only state/markup. |
| `web/src/components/ui/accordion.tsx` | Existing alternative when grouping multiple sections. No registry installation required. |
| `web/src/components/status-badge.tsx` | Existing shared tone/unknown badge; keep `copyable=false` for status. Its label truncates by default, so long translation/header sizing needs deliberate review. |
| `web/src/features/dashboard/components/overview/faq-panel.tsx:38–82` | Existing Accordion inside fixed ScrollArea, relevant expansion-inside-scroll composition. |
| `web/src/features/dashboard/components/overview/uptime-panel.tsx:62–160` | Existing bounded monitoring list/ScrollArea composition. **Its Uptime Kuma percentages are unrelated monitoring data and must not be imported into official-provider cards.** |
| `web/src/features/dashboard/components/ui/panel-wrapper.tsx` | Feature-specific wrapper. Its `height` controls body/loading/empty content, not a guaranteed equal-height outer card. Importing it alone would not satisfy this requirement. Prefer existing shared Card composition over a dashboard-only abstraction. |
| `web/src/components/data-table/toolbar/mobile-filter-panel.tsx:45–96` | Actual local example of shared CollapsibleTrigger rendering a shared Button with accessible Expand/Collapse naming. |

### Requested observable behavior to protect later

- Exactly **eight stable provider cards**: OpenAI / Anthropic / Gemini / xAI / DeepSeek / Moonshot / MiniMax / Mistral. Keep cards when machine data is unavailable; don't shrink the catalog to successful feeds.
- One fixed outer height at a given responsive breakpoint, **unchanged before/after expansion**, across normal, loading/no-success, stale, failure, long names and incident content. No huge expanded card or sibling-row stretching.
- Component list scrolls **inside that card's bounded component section**. Expansion changes content inside its allocated budget, not document height. Bound incident overflow too, without inaccessible clipping or accidental nested page/body scrollers.
- Header/source link, status/freshness and controls remain reachable. Long text wraps within width; the eight-provider grid itself must not cause horizontal document overflow.
- Expansion uses keyboard-accessible shared controls, correct `aria-expanded`/controlled content, stable provider identity and understandable labels. Refetch must not cross-wire expansion/state when lists change.
- No extra fetch per component or card render. Continue one bounded aggregate query/cache refresh and retain cancellation/StrictMode behavior.
- Localized UI labels in all seven locales; reuse `toIntlLocale` for timestamps. Genuine vendor names/incident titles remain source text, not invented translations or sample content.
- Scope of this report is source/UI inventory, **not approval of a particular pixel height or production implementation**. The coordinator/assigned UI owner chooses dimensions and extends the existing suite.

## 6. Existing regressions and hosted-only follow-up matrix

**Read only. None of these tests was run by this worker.** Extend existing suitable suites rather than scattering new files.

### Backend: `service/official_status_test.go`

Current tests protect:

- `TestOfficialStatusFeeds`: compatible summary mapping, `{}`/HTML rejection, unknown indicator, Google empty/invalid/unrelated/active/resolved incident behavior, no outbound Authorization/Cookie headers.
- `TestOfficialStatusPreservesIdentifiersWithinResponseBounds`: stable IDs and 100-component / 20-incident projection bounds.
- `TestOfficialStatusCacheRetainsLastSuccessAndRecovers`: cache reuse, immutable earlier snapshots, retained timestamps on failure and recovery.
- `TestOfficialStatusBoundsAndCancellation`: non-200, oversized body, caller cancellation, never-success unknown/unavailable and restrictive redirects.

Missing user-facing cases to extend after ownership is assigned:

1. Fixed eight-provider catalog and exact official source/scope association; failure never drops a card. If parser types change, table fixtures exercise actual sanitized provider schema rather than asserting private file layout.
2. DeepSeek-style transport failure at first fetch vs after success, invalid feed vs HTTP error; no fake last-success timestamp. Do not require live DeepSeek reachability in deterministic tests.
3. One failure does not erase healthy peers; identity-keyed prior snapshot survives catalog reorder/addition.
4. Thirty-minute age boundary independently of five-minute cache expiry, source-update age, latest-check failure, recovery and invalid/future timestamps.
5. Summary active list vs history latest/resolved; merge-by-ID, timestamp ordering, bounded response and history-only failure semantics if history is added.
6. HTML app/challenge 200/403 cannot become operational. Gemini shell and unverified xAI payloads must not be accepted as generic Statuspage success.
7. Preserve no credentials/cookies, response limits, cancellation/shared-refresh behavior and HTTPS allowlist redirect policy.

### Frontend: `web/src/features/service-status/__tests__/official-status.test.tsx`

Current cases protect StrictMode cancellation/remount; attribution/links/timestamps/components; stale/unavailable retained data; loading/Refresh/recovery; successful empty data; same-name components/incidents with and without IDs through refresh; seven interface languages plus invalid-language fallback.

Missing cases to extend in that same suite:

1. Eight cards including no-success and unavailable entries; explicit official links and source scope.
2. Fixed outer-height contract before/after mouse/keyboard expansion, and actual internal scroll target. Assert stable layout intent, not browser-pixel timing or huge class snapshots.
3. Long component/incident/provider strings, empty/null lists, many components and responsive/narrow content without card growth or unreadable controls.
4. Latest fetch failure vs last-known healthy state, never-success copy without a stale-history claim, and over-30-minute age after a mounted/paused query.
5. Correct active-vs-recent incident empty/failure presentation if history is introduced.
6. `aria-expanded`, focusability, state retention by provider ID, and independent source-link activation.
7. Keep existing locale switching, source IDs, cancellation and API business-error assertions. Freeze time explicitly when age is under test; existing historical fixture milliseconds must not accidentally become time-dependent failures.

All test/build/type/lint/format execution belongs in approved GitHub Actions at the coordinator's eventual exact SHA. This document is **source review and remote-source evidence only**, not a pass/compatibility/release claim.

## 7. Handoff / unresolved decisions

- This worker has **not** edited `service/official_status.go`, UI/tests/locales, plans/specs, or target files. Only this report is owned/written. Peer production boundaries remain untouched.
- Ready for implementation planning: five verified compatible feeds plus verified history URLs; eight-card source catalog requirement; current cache/parser/layout inventory; DeepSeek failure-layer evidence; bounded shared-component reuse and meaningful regression gaps.
- **Unresolved:** supported public Gemini machine feed, reachable/verified xAI machine schema, and DeepSeek's production-specific failure reason. These must remain visible limitations. Do not claim eight functioning automated feeds merely because eight official links/cards are present.
- Recommended interim rule: every provider gets its own fixed-height card and official link; missing/unverified feed data is honestly unknown/unavailable, not omitted and not replaced with Vertex/third-party/synthetic health.
- Decide explicitly whether incidents means active only or active plus latest history, and whether optional history has independent availability/timestamps. Decide the exact 30-minute inclusive boundary and header wording before code/test changes.
- No copied/sample uptime numbers, production probes, browser/app/local validation, target work or migration occurred. Any implementation, cloud validation and later migration require the coordinator's separate owned steps.
