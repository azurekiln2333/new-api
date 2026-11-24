# 11-24-dashboard-node-ranking-details — pi-logs report

Owner: pi-logs (`w4C:p11`). Parent: `.trellis/tasks/11-24-console-responsiveness-diagnostics`.
Branch: `fix/console-responsiveness-20261124`. Status: **source frozen**, pending root CI.

## Scope delivered

### R7 — instance resources on the dashboard API cards

- **Public DTO** (`model/instance_resource.go`) is allowlist-only:
  `key`, `name`, `status`, `domains`, `resources{cpu_percent,memory_percent,storage_percent}`,
  `observed_at`, `stale`, `disabled`, plus envelope `generated_at` and a top-level
  `monitoring: enabled|disabled|unknown`.
- **Top-level monitoring** aggregates the sampling nodes' own recorded state:
  `unknown` when no instance recorded a state, `disabled` if any reporting instance
  had monitoring off, otherwise `enabled`. The reading process's configuration is
  never consulted.
- **Per-resource validity** comes from the sampler itself
  (`common/system_monitor.go` `CPUSampled`/`MemorySampled`/`SampledEnabled`,
  `service/system_instance.go` `Available` per resource + `Monitoring` pointer):
  explicit `available:false` → unknown; `available:true` keeps a legitimate `0%`;
  legacy payloads without the flag fall back to positive-only; storage can be known
  while CPU/memory are unknown.
- **Identity safety**: no node name, hostname, stored node address/URL, sync error,
  path, pid or OS string can reach the public payload. `domains`/`name` come only
  from configured console API settings (already public via `/api/status`).
  `node_name` is stripped from the public `/api/status` projection
  (`console_setting.GetPublicApiInfo`).
- **Multi-domain → one instance** preserved; the group key is the smallest persistent
  configured entry id, so it survives reordering.
- **UI**: `api-info-panel.tsx` / `api-info-item.tsx` show CPU/memory/storage for all
  signed-in users on a wrapping second row that stays visible on narrow screens,
  with explicit `Unknown` / `Stale` / `Disabled` states, and open a filtered node
  detail. Detail selection is a positional UI identity and the instance is derived on
  every render, so the drawer follows the 30s refetch instead of freezing a snapshot.
- **Settings**: `api-info-section.tsx` binds each entry to an instance via an
  optional `node_name`, supports per-entry reordering, and preserves ids. Legacy rows
  saved before ids existed receive **distinct** draft ids above the current maximum at
  parse time and are persisted on save, so one edit/delete/reorder cannot corrupt
  several rows.

### R8 — homepage avatar and admin onboarding

- Signed-in avatar (desktop and mobile) in `public-header.tsx` links straight to
  `/dashboard`; the console menu is unchanged.
- Admin overview onboarding is hidden in all entry/reminder states
  (`overview-dashboard.tsx` `showSetupGuide = !isAdmin`); the installer is preserved.

### R9 — model usage drawer from the ranking list

- Clicking a model in the leaderboard or in the movers/droppers list opens the model
  usage drawer (`entity-links.tsx` `onOpenModel`, wired through
  `model-leaderboard.tsx`, `models-section.tsx`, `pulse-section.tsx`, `index.tsx`).
  It never navigates to pricing.
- Server masking (`model/ranking_viewer.go`): admins see every live name; ordinary
  viewers see only their own; anonymous viewers see none. Masked rows emit the fixed
  placeholder `***` and omit `user_id` entirely. **A deleted account keeps only the
  generic label `Deleted user` and no id**, even for an admin.
- Totals aggregate **all** users for the model, independent of the bounded
  leaderboard (`GetModelUsageTotals`), so a popular model is not undercounted.
- `Cache-Control: no-store` on the viewer-scoped response; query key includes viewer
  identity and role.
- Quota is rendered with the shared currency formatter in both the totals and each
  ranked user row (tokens/requests/quota), never with the token-count formatter.

## Route contract

- `GET /api/rankings/model-usage` → `middleware.DisableCache(), middleware.HeaderNavModulePublicOrUserAuth("rankings"), controller.GetRankingModelUsage`.
  Module disabled ⇒ anonymous is challenged (401), authenticated viewers keep working.
- `GET /api/status/instances` → `middleware.DisableCache(), middleware.UserAuth(), controller.GetInstanceResourceSummary`.
- `GET /api/status` keeps using `GetPublicApiInfo` (no `node_name`).
- The existing root-only `/api/system-info/instances` is untouched and remains the
  full node API.

## Tests written (never executed locally — root runs CI)

- `model/instance_resource_test.go` — public JSON allowlist (exact key sets, no node
  identity leakage), legitimate 0%, legacy positive-only fallback, storage-known
  while CPU/memory unknown, stale, multi-domain, unbound, sampling-node monitoring
  state, plus ranking masking including the deleted account carrying no id.
- `router/access_token_scope_test.go` (extended, existing fixture with real
  `SetApiRouter`) — ranking masking per viewer with `no-store`, required `model` /
  period validation, disabled-module 401-vs-200 policy, and `/api/status/instances`
  authentication plus privacy.
- Frontend: `api-info-panel.test.tsx` (non-blocking resources, bindings, legitimate
  0%, click-to-detail, disabled state, refetch freshness), `model-usage-drawer.test.tsx`
  (masked identities preserved, no pricing link, error+retry, per-user quota),
  `api-info-section.test.tsx` (multi-domain binding, reorder keeps ids, two legacy
  rows get distinct ids with single-row edit/delete, unbound display).

## Known gaps

- No page-level ranking wiring test (that `ModelLink`/`MoverRow` open the drawer).
- No test for the 30s interval timer itself or AbortSignal cancellation.
- No test for same-session role downgrade invalidating the ranking query key
  (backend masking is covered; client cache-key behaviour is not).
- Backend router tests depend on the shared fixture continuing to call `SetApiRouter`.
- No local build/test/vet/gofmt/format/DB verification was performed: standing ban for
  this worker. All validation is GitHub Actions, run by root.
