# R14 implementation verification

Implemented the standalone Total Used column and server-wide `used_quota` sorting. The list and filtered search use the existing approved sort mechanism before pagination, retaining `id desc` for equal values. No schema or index changes were made. Invalid sort fields retain the existing safe default.

The frontend reuses the existing DataTable sort menu, server query flow, page reset, mobile cards, `formatQuotaWithCurrency`, and `QuotaDetailsPopover`. Available balance no longer repeats usage below the balance; the detail popover still includes both figures and retains its accessible trigger. Existing `Total Used` translations cover the new column, so no locale change is required. Currency and tokens formatting remain owned by the shared formatter.

## Changed product files

- `model/user.go`: approved `used_quota` sort key.
- `model/user_pagination_test.go`: extend the existing suite with an explicit SQLite/MySQL/PostgreSQL query fixture, asc/desc across pages, equal-value tie ordering, all search filters, and invalid-key fallback.
- `web/src/features/users/types.ts`: sort-key type.
- `web/src/features/users/components/users-table.tsx`: client sort-key allowlist.
- `web/src/features/users/components/users-columns.tsx`: independent Total Used column.
- `web/src/features/users/components/user-quota-cell.tsx`: remove duplicated visible usage while retaining details.
- `web/src/features/users/components/__tests__/quota-display.test.tsx`: separate columns, API sorting, page reset, filters, keyboard details, mobile Chinese labels, zero/negative balances, and currency/tokens formatting.

## Checks observed

Frontend regression was run before implementation: 10 of 12 cases failed, including the absent Total Used column and server sort UI. After implementation, all 12 passed. The final rerun also passed after test compatibility/lint corrections.

Commands (from `web/`, Bun executable at `D:/AppData/Local/Temp/new-api-build-tools/bun-windows-x64/bun.exe`):

```text
bun run test src/features/users/components/__tests__/quota-display.test.tsx
bun check -b
bun run oxlint -c .oxlintrc.json src/features/users/types.ts src/features/users/components/users-table.tsx src/features/users/components/users-columns.tsx src/features/users/components/user-quota-cell.tsx src/features/users/components/__tests__/quota-display.test.tsx
bun run oxfmt --check src/features/users/types.ts src/features/users/components/users-table.tsx src/features/users/components/users-columns.tsx src/features/users/components/user-quota-cell.tsx src/features/users/components/__tests__/quota-display.test.tsx
```

Results: 12 frontend cases passed; typecheck passed for 1,570 files across two projects; scoped lint and format checks passed. `gofmt` and scoped `git diff --check` passed. Protected headers were unchanged. No new dependency, custom interaction, or shared formatter implementation was introduced.

Backend command from the repository root, with Go 1.26.10 at `D:/AppData/Local/Temp/new-api-build-tools/go/bin/go.exe`:

```text
GOWORK=off GOMAXPROCS=1 GOMEMLIMIT=512MiB go test -p 1 ./model -run 'TestGetAllUsersSortsBeforePagination|TestSearchUsersSortsBeforePagination|TestUserUsedQuotaSortingDatabaseMatrix' -count=1 -v
```

Result: passed in 5.124 seconds. Real SQLite **3.50.4** passed. MySQL and PostgreSQL cases explicitly skipped because `TEST_MYSQL_DSN` and `TEST_POSTGRES_DSN` were not configured locally. These cases must pass against real engines in integration CI before declaring database compatibility complete. The existing CI model-package command discovers this test automatically. No migration is part of R14.

Local logs: `D:/AppData/Local/Temp/new-api-user-usage-red.log`, `D:/AppData/Local/Temp/new-api-user-usage-green.log`, and `D:/AppData/Local/Temp/new-api-user-usage-go.log`.

Independent read-only review by `baseline_workflows` found no blocker in sorting order, stable ties, filters, page reset, formatting subscriptions, mobile reuse, or accessible details. Root owns final integration checks, product commit, CI matrix, and publication; this worker did not stage, commit, or edit locale files.
