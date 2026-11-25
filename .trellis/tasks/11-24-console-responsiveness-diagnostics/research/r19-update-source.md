# R19 — Configurable update-source repository + fork tag comparison

Parent PRD: `.trellis/tasks/11-24-console-responsiveness-diagnostics/prd.md` (R19)
Branch: `fix/console-responsiveness-20261124` @ `9329486b8`
Worker: pi-review (initial implementation), pi-q7 (verification and gap completion) · Mode: **source inspection and edits only** — no local build/test/vet/lint/format/bun.
Validation runs exclusively in GitHub Actions.

## pi-q7 checkpoint — 2026-11-25

1. Read root `AGENTS.md`, parent `prd.md`, `queue.md`, and this existing report; item #7 is already CLAIMED by pi-q7. Parent PRD explicitly authorizes continued implementation; no new task is being created or global active-task pointer changed. Inspected scoped diff summary; implementation is present in the shared working tree, including untracked Go setting/tests. Notified root w5D:p1 of scope. Next: inspect the actual code and existing tests, finish only R19 gaps, and leave all executable validation to GitHub Actions.
2. Source review found real gaps: the persisted singleton release snapshot is not repository-scoped (switching repositories reuses a fresh old-source result for one hour, and a failure relabels old-source data); identical custom commits can be falsely reported newer if republished on a later day; calendar-invalid custom dates are accepted; client/form repository validators allow server-rejected dot segments and overlong values; Go default tests do not isolate the environment. Change boundary: only R19 release parsing/comparison, repository-aware snapshot/query handling, validator reuse in the existing settings form, focused existing R19 test files, and this report. No DB/schema/auth/billing/CI/other-worker changes. Existing upstream semver/prerelease behavior and protected attribution/default stay intact. CI #10 belongs to pi-web; sent provisional selectors and will send final paths/names/envs.
3. Applied narrow source fixes: repository identity is persisted with snapshots (old unscoped snapshots migrate to upstream only); query hydration/failure fallback are source-scoped; malformed status repository values safely fall back. Custom builds compare tag dates/commit identity before publication time, so delayed publication cannot advertise the installed commit or an older tag. Invalid calendar dates are rejected. Settings form now reuses the same frontend validator and resets its saved baseline after success. pi-auth p5 confirmed ownership of the missing backend translation key and will add it in the reserved seven-locale batch; pi-web owns CI only. Next: complete regression fixtures and Go env isolation, source-read back all changed files, send final CI selectors. No local validation commands run.
4. Extended existing Go fixture with environment isolation/restoration, stored-over-env precedence, valid/invalid env fallback and client/server-matching invalid repository cases. Extended existing frontend release and interaction suites with identity/date, calendar-invalid dates, source switch+failure isolation, legacy snapshot migration, fork URL/link and settings save/clear/rejection cases. One edit tool call failed closed on a duplicate match; reread file confirmed no partial write, then applied uniquely scoped replacements. pi-auth confirmed the missing backend key was delivered in all seven locales and static keys via sanctioned script+sync. Next: final source-only review, report corrections, CI handoff and queue completion.
5. Final source-only readback traced settings → existing root-only option route → registered config → cached status → repository-scoped query/snapshot → fork release details. Narrow source edits preserve original attribution headers/default and unrelated shared files. Confirmed seven backend-error translations/static key by search. pi-web ACKed FINAL unchanged test paths/prefixes and added `system-update/store.ts` to CI lint; no new env, DB/dialect, Redis or ClickHouse fixtures required. Root and pi-commit notified to include the added snapshot file in R19. Existing component/form actions were reused; no UI replacement/dependency added. No local tests/builds/vet/lint/format/typecheck/DB probes or git mutations executed. Code/test execution and formatter output remain pending GitHub Actions.
6. Marked queue #7 DONE and reread the queue; no OPEN items remain (#10 is explicitly CLAIMED by pi-web), so no next item was taken. This report is the self-contained R19 handoff; no staging/commit/push or local executable validation performed.

---

## 1. Problem

The system-update panel compares the running build `custom-20261124-53a71f8` against
`QuantumNous/new-api` releases (`v1.0.0-rc.51`) and permanently reports "update available".
Two independent defects:

1. **Wrong repository** — `web/src/features/system-update/api.ts` hardcoded
   `https://api.github.com/repos/QuantumNous/new-api/releases?per_page=100`, so the fork's
   `custom-*` releases are never listed.
2. **Missing fork comparator** — `parseSystemVersion` only matched `v<semver>[-alpha|beta|rc|patch]`;
   `custom-*` parsed to `null`, so the prior semver-only checker could not meaningfully order
   the running fork. The initial report attributed an alert to `comparison === null`, but the
   inspected hook uses `comparison === -1`; unknown comparison must remain non-alerting.

AGENTS.md "protected project information" forbids deleting or rewriting existing
`QuantumNous/new-api` references, so the fork had to become an **override** with upstream
remaining the default.

---

## 2. Resolution order

```
system_update_setting.repository   (DB option, root-only, validated owner/repo)
        └─ blank → NEW_API_UPDATE_SOURCE_REPOSITORY (startup default, validated)
                └─ blank/invalid → UpdateSourceDefaultRepository = "QuantumNous/new-api"
```

A **setting** was chosen over env-only because the frontend fetches GitHub directly from the
browser: the resolved value must reach every client. It is exposed through the already-cached
`/api/status` (`statusQueryOptions`), so no new endpoint or auth surface was added.
The env var remains as a server-side startup default for deployments that never open the console.

---

## 3. Implemented change set

### Backend (Go)

| File | Change |
| --- | --- |
| `setting/system_setting/update_source.go` | **new** — `UpdateSourceSetting{Repository}` registered as config module `system_update_setting`; `UpdateSourceDefaultRepository = "QuantumNous/new-api"`; `UpdateSourceRepositoryOption`; `UpdateSourceRepositoryEnv`; `ErrUpdateSourceRepositoryInvalid`; `ValidateUpdateSourceRepository` (`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`, ≤140 chars, rejects `.`/`..`/trailing-dot segments); `UpdateSourceRepository()` |
| `setting/system_setting/update_source_test.go` | **new** — default-is-upstream, stored-fork, accept/reject table |
| `model/option.go` | `validateOptionValueWithUpdates`: new branch `key == system_setting.UpdateSourceRepositoryOption` → `ValidateUpdateSourceRepository(value)`. Runs for single `PUT /api/option/`, `PUT /api/option/batch` and `UpdateOrdinaryOptionsBatch` before any write commits |
| `controller/misc.go` | `GetStatus` adds `"update_source_repository": system_setting.UpdateSourceRepository()` |

Default precedence is enforced inside `UpdateSourceRepository()`, so an installation with no
stored option keeps following upstream — no behaviour change on upgrade.

### Frontend (TypeScript)

| File | Change |
| --- | --- |
| `web/src/features/system-update/releases.ts` | `DEFAULT_UPDATE_SOURCE_REPOSITORY` (= `QuantumNous/new-api`, retained); `isUpdateSourceRepository` / `resolveUpdateSourceRepository` (regex-validated, falls back to default); `getSystemReleasesUrl(repository)`; `parseCustomBuildVersion` (`custom-YYYYMMDD-<sha>`, full calendar-date validation); `isPrereleaseBuild`; `compareCustomBuildRelease`; `selectLatestRelease` now accepts both tag families and orders mixed families by publication time; `getSystemReleaseUrl(release, repository?)` |
| `web/src/features/system-update/api.ts` | `fetchLatestSystemRelease(signal, repository = DEFAULT_UPDATE_SOURCE_REPOSITORY)` → `getSystemReleasesUrl(repository)` |
| `web/src/features/system-update/use-system-update.ts` | `systemUpdateQueryKey(repository)` (`['system-update', repository]`); `systemUpdateQueryOptions(repository)`; `compareRunningVersion` (fork builds → identity/date with cross-family timeline fallback, everything else → unchanged semver); hook reads `status.update_source_repository`, returns `repository` |
| `web/src/features/system-update/store.ts` | persisted snapshot records `repository`; legacy unscoped snapshots migrate only to upstream; query hydration and failure recovery never reuse another repository's release |
| `web/src/features/system-update/system-update-dialog.tsx` | release link and prerelease badge use `update.repository` / `isPrereleaseBuild` |
| `web/src/features/system-settings/maintenance/update-checker-section.tsx` | RHF + Zod field for `system_update_setting.repository`, shared frontend repository validator, `SettingsPageFormActions`, saves via existing single-option `useUpdateOption` path; successful save resets its baseline so repeated clicks do not duplicate writes |
| `web/src/features/system-settings/operations/section-registry.tsx` | passes `settings['system_update_setting.repository']` |
| `web/src/features/system-settings/operations/index.tsx` | default `''` |
| `web/src/features/system-settings/types.ts` | `'system_update_setting.repository': string` |
| `web/src/features/system-settings/hooks/use-update-option.ts` | key added to `STATUS_RELATED_KEYS` so the checker refetches after a save |

### i18n (script-only, all seven locales)

Initial implementation applied these 4 keys in all seven locales using
`web/scripts/add-missing-keys.mjs`:

- `Update source repository`
- `Enter the repository as owner/repo.`
- `Leave blank to follow the official release channel.`
- `Save update source`

The initial worker did not run sync. During pi-q7 verification, the missing backend error key
`Enter the repository as owner/repo, for example owner/new-api.` was sent to the designated
single locale writer, pi-auth p5. That worker confirmed script + sync delivery in all seven
locales and `static-keys.ts`; pi-q7 verified the exact entries by source search. The locale
script remains reserved for the single writer and root owns eventual deletion. CI remains the
only test/type/lint/format validation authority.

---

## 4. Version comparison semantics

`custom-YYYYMMDD-<sha>` is **date ordered, not semver ordered**:

1. `parseCustomBuildVersion` validates the full UTC calendar date (including leap days),
   extracts the commit hash, and accepts either short or full SHA text.
2. For custom → custom comparisons, identical/prefix-matching commits are already installed;
   distinct commits on the same date cannot be ordered reliably and do not trigger an update.
   Otherwise the tag dates determine ordering, not potentially delayed `published_at` values.
   Release selection also prioritizes custom tag dates over re-publication of older builds.
3. Cross-family comparisons use `published_at` against the **end** of the current build's UTC
   date (`T23:59:59.999Z`); missing/unknown timing is not reported as an available update.
4. A stable custom build does not advertise a prerelease as an update, whether marked by the
   GitHub prerelease flag or a recognized alpha/beta/rc tag. Existing upstream-to-upstream
   semver comparison and inclusion of prereleases remain unchanged.
5. Unknown or unorderable versions produce no automatic update alert, rather than treating
   `null` as “update available”. No hash lexicographic ordering is invented.

Result for the reported case: build `custom-20261124-53a71f8` vs `v1.0.0-rc.51`
→ **no update**, false positive removed. Republishing the exact running custom tag later
also produces no update; a different later-date custom tag does.

---

## 5. Test coverage (all executed by CI only)

| Suite | Assertion |
| --- | --- |
| `web` `__tests__/releases.test.ts` | upstream default, fork URL/link; malformed/type/dot/length repository fallbacks; real UTC dates/leap days; identical/prefix SHA, delayed publication, older/later custom dates, same-day ambiguity; stable custom builds ignore prereleases |
| `web` `__tests__/update-checking.test.tsx` (extended, upstream assertions preserved) | configured fork request+link; no false upstream alert; settings save/clear/unchanged repeat and invalid-input rejection; changed repo immediately checked despite fresh old snapshot; failed new source cannot display old release; legacy persisted snapshots migrate only to upstream |
| Go `setting/system_setting/update_source_test.go` | upstream/default, stored-over-env precedence, cleared override, valid/invalid environment fallback, accept/reject validator cases; every changed fixture restores globals and isolates env |

Existing assertions that pin the upstream URL and upstream release link were **kept**, so
the protected default is regression-guarded. Source inspection only: no test is claimed green
until GitHub Actions reports it.

---

## 6. CI selectors for `pi-newapi-ci` (`w4C:p1D`)

Standard dispatch on branch `fix/console-responsiveness-20261124`, no extra flags. Gates that
actually exercise R19:

- **Changed Go formatting/oxlint**: `setting/system_setting/update_source.go`,
  `setting/system_setting/update_source_test.go`, `model/option.go`, `controller/misc.go`.
- **Changed frontend formatting/oxlint**: the five `web/src/features/system-update/*` files plus
  `__tests__/releases.test.ts`, `__tests__/update-checking.test.tsx`,
  `web/src/features/system-settings/maintenance/update-checker-section.tsx`,
  `.../operations/index.tsx`, `.../operations/section-registry.tsx`,
  `.../hooks/use-update-option.ts`, `.../types.ts`, and **`system-update/store.ts`**.
- **`bun check -b`** (typecheck) covers the changed query-key tuple, `compareRunningVersion`
  signature and the new Zod schema.
- **`bun run test`** covers the two extended spec files.
- **Backend** `go vet ./...`, `go build ./...`, `go test ./setting/... ./model/... ./controller/...`.
- **i18n**: confirm `bun run i18n:sync` produces no further changes (script-written files only).
- "Lint updated console flows" explicit list: add
  `src/features/system-settings/maintenance/update-checker-section.tsx` if that enumerated list is
  maintained by hand.

---

## Final CI handoff (pi-web w5D:p6 ACKed)

- Go file: `setting/system_setting/update_source_test.go`; selector
  `^(TestUpdateSource|TestValidateUpdateSourceRepository)` covers all five top-level tests:
  `TestUpdateSourceRepositoryDefaultsToUpstream`, `TestUpdateSourceRepositoryFollowsStoredFork`,
  `TestValidateUpdateSourceRepository`, `TestUpdateSourceOptionClearedFallsBackToUpstream`,
  `TestUpdateSourceRepositoryEnvironmentFallback`.
- Frontend files (existing, extended):
  `web/src/features/system-update/__tests__/releases.test.ts` and
  `web/src/features/system-update/__tests__/update-checking.test.tsx`.
  Test groups: `update source repository`, `fork build ordering`, `update source settings`,
  and repository-isolation additions to `update cache and scheduling`.
- No new test environment variables or service/dialect fixtures. Go isolates
  `NEW_API_UPDATE_SOURCE_REPOSITORY` using `t.Setenv`; frontend fetch/API are mocked. Existing
  broad backend and full Vitest runs remain; no special race/passkey/router/DB selector needed
  for R19. Shared option persistence changes still require their owners' CI matrix.
- Important lint/commit addition beyond the initial report: **`system-update/store.ts`**.
- Same-day different custom commits conservatively do not alert; see residual risk R19-R5.

## 7. Residual risks

- **R19-R1** `/api/status` gains a field. Audited all consumers: `statusQueryOptions` maps a
  permissive `StatusData` record; no strict shape parser exists.
- **R19-R2** The query key and persisted snapshot both carry repository identity. Legacy
  snapshots have a schema default of `QuantumNous/new-api`, so they cannot hydrate a fork's
  cache. A failed fetch keeps only same-source prior results. In-flight results remain under
  their own React Query source key; changing the setting cannot relabel them.
- **R19-R3** A fork could be tempted to delete the upstream default; `update_source_test.go` and
  the retained `update-checking.test.tsx` URL assertions guard against that.
- **R19-R4** Go is authoritative; the frontend settings form and URL builders reuse one
  validator with matching character, length and dot-segment checks. No arbitrary host, URL,
  credentials or query-string override is accepted.
- **R19-R5** Same-day different custom commits cannot be ordered without server commit time or
  release metadata for the installed commit. Conservative behavior is deliberate: display the
  latest release details, but do not invent an update/downgrade from unordered SHAs.
- **R19-R6** Empty stored setting follows `NEW_API_UPDATE_SOURCE_REPOSITORY` when configured,
  then upstream; it does not erase an operator's env default. Existing UI blank guidance calls
  this the official channel; configure the environment consistently with that desired fallback.
- **R19-R7** No schema/query/DB/auth/billing change was added by pi-q7. Existing option writes
  remain on their root-only route and normal configuration sync path. Shared option transaction
  changes belong to their owners and their database matrix, not this source-only R19 check.
