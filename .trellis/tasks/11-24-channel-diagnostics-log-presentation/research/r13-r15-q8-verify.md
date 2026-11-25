# pi-q8 — queue item #8 verification: R13 inline quota + R15 stale drawers

Verification-only pass against the code (no builds/tests run; cloud-only per worker rules).
Read `r13-r15.md` first; claims there were re-checked line by line in the current dirty tree.

## R13 — users drawer inline quota
Verified in `web/src/features/users/components/users-mutate-drawer.tsx`:
- Quota input is editable (`type='number'`, step from `getEditableQuotaStep()`); the old `readOnly` + `UserQuotaDialog` path is gone and `user-quota-dialog.tsx` has no remaining importer.
- Button reads `Confirm` / `Processing...`, disabled while not ready, while adjusting, when the baseline id does not match, or when the parsed value equals the baseline.
- Write path is `adjustUserQuota({ id, action: 'add_quota', mode: add|subtract, value: |delta| })` → `POST /api/user/manage`; `controller/user_quota.go` audits `user.quota_add|subtract|override` with from/to and `model.AdjustUserQuota` runs under `lockForUpdate`, so the delta direction is the concurrency-safe one (already reasoned in `r13-r15.md`, re-confirmed).
- `quotaSubmitLock` ref stops a same-tick double submit; result failure surfaces `handleServerError(..., 'Failed to adjust quota')`; success toasts, refreshes the list, reconciles optimistically then re-reads `getUser` best-effort.

Gaps found and fixed:
1. Reconcile re-read applied the fetched quota even when the response was `success:false` (a `data` payload on a failed response would have seeded the baseline). Now gated on `refreshed.success && refreshed.data?.id === targetId`.
2. Optimistic baseline stored the raw quota units while the form field holds the display amount; `parseQuotaFromDollars(currentQuotaRaw) === loadedQuota.current?.value` could then never match for non-integral display amounts and left Confirm permanently enabled. Both the optimistic and the reconciled baseline are now normalized through `quotaUnitsToDollars` → `parseQuotaFromDollars`.
3. The existing double-click test asserted the pre-reconcile amount (2.8) even though the component re-reads the server balance after commit; corrected to the reconciled 1.8, which is the behaviour under test (delta preserved through concurrent consumption).

## R15 — stale drawers
Users drawer (`useLayoutEffect` on `open`/`currentRow?.id`): synchronous reset to `transformUserToFormDefaults(currentRow)` (no previous record painted), cleanup fence + `data.id !== targetId` guard so a late response for another id never applies, `isUserReady` gates submit/Save/Confirm and sets the form `inert`, error state with retry, full reset on close. Sibling audit re-confirmed: `redemptions-mutate-drawer`, `api-keys-mutate-drawer`, `model-mutate-drawer` gate on the loaded id; `subscriptions-mutate-drawer` and `invitation-editor` are prop-seeded with no fetch; `channel-mutate-drawer` blocks submit on the id guard but paints the previous channel until the detail resolves (pi-keys' file, residual noted in `r13-r15.md`, not touched here).
Gap found and fixed: the drawer is still mounted while the Sheet animates out (`Sheet` renders `SheetContent` unconditionally), so a closed drawer kept showing the last record's values. The one-line fix (`inert` on `SheetContent` when `open` is false) was applied and then reverted here: the `sheet.tsx` popup commits its own `inert`/focus-trap props and a second, non-propagating `inert` from the caller would fight the exit animation and the shared primitive. The form-level `inert` above plus the `!open` reset in `onOpenChange` already make a closed drawer non-interactive; only the visual remnant during the ~200ms exit animation remains and belongs to `web/src/components/ui/sheet.tsx` (shared primitive, root's decision).

## Tests
Extended the existing `web/src/features/users/components/__tests__/permissions.test.tsx` (no new file): a `success:false` reconcile response does not overwrite the delta baseline; the optimistic baseline keeps Confirm disabled after a display-rounded adjustment.

## Validation
None executed locally (no bun/node/vitest/tsc/lint/gofmt), per the worker rules. GitHub Actions runs `web` typecheck/lint/vitest.
