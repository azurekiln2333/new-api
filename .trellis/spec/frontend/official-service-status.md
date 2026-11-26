# Official service status

## Scope and trigger

The service-status console displays eight fixed providers from the host's bounded
official-feed adapter. A failed feed fetch is not proof of a provider outage.

## Signatures

`GET /api/official-status` returns `OfficialProviderStatus[]` through the existing
success envelope. `getOfficialServiceStatus(signal)` passes AbortSignal. Each row
contains status/components/active incidents, recent incidents, source identity,
separate summary/history successful-fetch/check times, stale/unavailable flags and
fixed error codes with an optional bounded HTTP status.

## Contracts

OpenAI, Anthropic, Gemini API, xAI, DeepSeek, Moonshot, MiniMax and Mistral remain
visible even without a verified machine feed. Do not substitute Vertex AI for
Gemini API. Unverified sources perform no request; DeepSeek has summary only until
a history feed is verified. Source URLs belong to the fixed catalog.

Cache official results for five minutes. Summary and history failures retain their
own last successful values while updating their own checked time/error. Exactly
30 minutes old is fresh; older is stale. Negative, future or absent successful
timestamps cannot be fresh. The mounted UI also advances its observation clock
when a later query response arrives, avoiding invalidation of a legitimate fetch
that completed after mount.

Deduplicate history by incident ID, retain its newest update, sort newest-first,
then cap at20. Preserve cached provider values by provider ID, never array index.
Suppress recent entries already present among active incidents. Preserve raw
provider IDs; use fixed translated labels and neutral Unknown fallbacks for
unknown statuses. Guard each label/tone/error dictionary lookup with Object.hasOwn
so prototype properties cannot reach i18next or a status component.

## Validation and errors

Invalid transport/body/source/redirect/size conditions become fixed value-free
error codes. Never render arbitrary upstream errors, response bodies or transport
addresses. Only valid HTTP error status numbers appear in the diagnostic label.
Unknown labels including toString, constructor and __proto__ use fixed fallbacks.
Unavailable history does not erase a successful summary or imply no incidents.

## Good, base and bad cases

- Good: a failed new check shows last known status with its actual success age.
- Base: an unverified source has its card and safe source link, with no invented
  availability percentage or historical incidents.
- Bad: a refreshed checked time makes old successful data appear fresh, or a map
  prototype property is interpreted as a translation selector.

## Required tests

Extend existing `service/official_status_test.go` for source catalog, independent
cache recovery, freshness boundaries, newest-ID deduplication, redaction and body
bounds. Existing `official-status.test.tsx` protects all eight cards, after-mount
freshness, history/active separation and prototype-key fallback across headline,
component, active/recent incident and both error surfaces. Hosted execution and
locale generation are separate requirements; source acceptance is not live-feed
reachability or translation completeness.

## Wrong versus correct

Wrong: `STATUS_LABELS[value] ?? 'Unknown'` for arbitrary upstream strings.
Correct: check `Object.hasOwn(STATUS_LABELS, value)` before indexing and otherwise
use the explicit Unknown label and neutral tone.
