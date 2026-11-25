# Admin subscription “Active Users” inventory (source-only)

Date: 2026-11-27. Scope: research for an admin subscriptions page tab showing currently effective user/plan/start/expiry data. This is not an implementation. No production access, DB probes, Git operations, tests, builds, linters, formatters, browser runs, or changes outside this file were performed.

## Existing UI and API surfaces

- The admin route is `web/src/routes/_authenticated/subscriptions/index.tsx:25-33`; its client route guard requires `ROLE.ADMIN` or higher. `web/src/features/subscriptions/index.tsx:33-75` currently renders one subscription-plan table and dialogs. `SubscriptionsTable` (`web/src/features/subscriptions/components/subscriptions-table.tsx`) fetches all admin plans and uses the shared `DataTablePage`/`useDataTable`; there is no Active Users tab or active-subscription listing table in this feature today.
- The existing shadcn/Tabs wrapper is `web/src/components/ui/tabs.tsx` (Base UI implementation). The shared paged table contract is documented in `web/src/components/data-table/README.md`; `DataTablePage` supports remote/manual TanStack pagination through `useDataTable` (see `web/src/components/data-table/layout/data-table-page.tsx`). Reuse these rather than build a custom tab/table/pagination primitive. The subscriptions feature currently has no component tests under its folder.
- `GET /api/subscription/admin/plans` returns every plan; the route group is protected by `middleware.AdminAuth()` (`router/api-router.go:202-218`). `AdminListSubscriptionPlans` (`controller/subscription.go:120-138`) does not paginate.
- `GET /api/subscription/admin/users/:id/subscriptions` is the existing per-user admin read (`router/api-router.go:212-217`, `controller/subscription.go:387-399`). It returns all statuses for one specified user by calling `model.GetAllUserSubscriptions`; it has no pagination/search and no plan/user join. It is used by the existing user-subscription drawer, not by the subscriptions page.
- `GET /api/subscription/self` is a user-authenticated self endpoint (`router/api-router.go:190-200`; `controller/subscription.go:52-74`). It returns `subscriptions` (active-only for compatibility) and `all_subscriptions`; errors from either model fetch are converted to empty arrays. Do not repurpose it for an admin-wide table or treat its empty arrays as a reliable admin query result.
- The generic user directory (`GET /api/user/` and `/api/user/search`) is also `AdminAuth`-protected (`router/api-router.go:162-169`) and SQL-paginated, but it does not carry subscription plan/start/end details and is not an active-subscription endpoint.
- Current protection is role-based `AdminAuth` (ADMIN and ROOT), not a subscription-specific fine-grained permission. Keep a new admin listing within the same admin route group; do not use the user self endpoint or expose the listing publicly.

## Stored subscription facts and exact source predicates

`model.UserSubscription` (`model/subscription.go:330-359`) stores `Id`, `UserId`, `PlanId`, `Status`, `StartTime`, `EndTime`, `AmountTotal`, `AmountUsed`, reset fields and group/overflow snapshots. Times are Unix seconds. The row does **not** snapshot a plan title or username. `SubscriptionPlan` stores the current title (`model/subscription.go:159-203`); a displayed name should be resolved from `plan_id` (a page query joining `subscription_plans` is preferable to one lookup per row). The existing admin drawer separately fetches plans and maps `plan_id` to the current title (`web/src/features/subscriptions/components/dialogs/user-subscriptions-dialog.tsx:126-132, 138-141, 293-306`).

The server’s existing temporal “active subscription” helpers agree on this predicate:

```text
user_id = target AND status = 'active' AND end_time > now
```

Evidence:

- `GetAllActiveUserSubscriptions` uses it and sorts `end_time DESC, id DESC` (`model/subscription.go:931-945`).
- `HasActiveUserSubscription` uses the same predicate (`model/subscription.go:947-961`).
- `UserActiveSubscriptionsAllowWalletOverflow` uses the same time/status predicate plus `allow_wallet_overflow = false` (`model/subscription.go:963-979`).
- The actual subscription pre-consume candidate query also uses `status='active' AND end_time > now`, and orders candidates `end_time ASC, id ASC` (`model/subscription.go:1381-1427`). `ExpireDueSubscriptions` marks `active` rows with a positive `end_time <= now` as expired (`model/subscription.go:1223-1258`).

Important semantics to preserve/label:

1. The current server predicate does **not** require `start_time <= now`; do not add that condition while claiming to mirror it. No current creation path schedules a future start (`CreateUserSubscriptionFromPlanTx`, `model/subscription.go:562-636`, sets start to now), but the field exists.
2. `end_time == now` is not active (strict `>`). `end_time == 0` also fails the current predicate; it is not treated as “never expires” by these helpers.
3. The user account’s enabled/disabled role/status is not part of these subscription-row predicates. “Active Users” should therefore mean users represented by a currently time/status-eligible subscription, not users currently signed in, enabled, or otherwise account-active. If account status is shown, keep it as a separate attribute/filter.
4. Multiple simultaneous subscription rows per user are supported. There is no canonical single plan per user in storage or in the billing selector. Avoid silently choosing one “current plan” by latest-created or latest-expiry; either show one row per eligible subscription instance (key by `subscription.id`, so a user may repeat) or group a user’s complete set of eligible plans/periods under one user row.
5. `GetAllActiveUserSubscriptions`/`HasActiveUserSubscription` use `common.GetTimestamp()` (application clock). The write-time/pre-consume selection uses `GetDBTimestamp()` (`model/subscription.go:1392`, via `model/db_time.go`, with an app-clock fallback on DB error). A list intended to mirror the general active helper should use that helper’s clock convention and clearly remain a report; a claim of exact request-time chargeability must account for the pre-consume clock and quota checks too.

### Temporal “active” is not identical to “will pay this request from this plan”

`service/billing_session.go:538-563` shows subscription-first behavior: `HasActiveUserSubscription` is a lightweight status/time existence check, then pre-consume tries eligible subscriptions; insufficient subscription quota can fall back to wallet only if all active rows allow wallet overflow. `subscription_only` attempts subscription directly; `wallet_only` bypasses it.

`PreConsumeUserSubscription` is request-amount-dependent and can first apply a due periodic quota reset through `maybeResetUserSubscriptionWithPlanTx` (`model/subscription.go:1345-1378, 1381-1483`). It selects active candidates in `end_time ASC, id ASC` order; a row with `amount_total <= 0` is unlimited, otherwise it is usable only when the post-reset `amount_total - amount_used` covers this request’s amount. It proceeds to another active row if a candidate cannot cover the amount. Thus the set of temporally active users/plans is reportable without a write, but a GET must not promise that a given subscription will fund a future request or mutate `amount_used` to perform a lazy reset. “Effective” should be defined in the tab as the existing status/time predicate unless the product explicitly wants request-amount-specific chargeability (which cannot be represented as one static plan without additional inputs).

Expiry is not dependent on the cleanup job for request eligibility: the strict `end_time > now` read predicates stop matching at expiry even before stored `status` is updated. A master-only maintenance task runs once per minute (`service/subscription_reset_task.go:16-44`) and batches `ExpireDueSubscriptions`/quota resets in groups of 300 (`:46-90`). On expiration it may downgrade the user group when no other qualifying upgraded subscription remains (`model/subscription.go:1246-1317`). Plan binding can upgrade the user group immediately (`model/subscription.go:600-636`); group is not itself an active-subscription filter.

Cancellation/deletion are immediate server mutations. `AdminInvalidateUserSubscription` transactionally sets `status='cancelled'` and `end_time=now`, and applies any needed group downgrade (`model/subscription.go:1010-1053`); it then fails both status and time predicates. `AdminDeleteUserSubscription` hard-deletes the row (`:1055-1094`). The per-user dialog disables reset/invalidate when its local check sees a non-active status or an elapsed end time (`web/src/features/subscriptions/components/dialogs/user-subscriptions-dialog.tsx:73-106, 352-358`). A new tab must refresh after these existing actions or invalidate its query; it should not rely on the periodic expiry job to hide an expired row.

## Bounded listing recommendation / contract to confirm

There is no existing cross-user active subscription API to reuse. `GetAllActiveUserSubscriptions` accepts one `userId` and returns an unbounded slice; `GetAllUserSubscriptions` returns all statuses for one user. For the new UI, prefer a dedicated, read-only admin endpoint rather than loading every user or subscription into the browser, e.g. `GET /api/subscription/admin/active-users?p=1&page_size=20` under the existing `AdminAuth` group.

A straightforward row contract is one row per eligible `UserSubscription`, with only `user_id`, a minimal current username/display name, `subscription_id`, `plan_id`, current plan title, stored `start_time`, and `end_time` (plus `status` if useful). Count and page the same joined subscription-row set using `status='active' AND end_time > now`; use SQL `COUNT` and a deterministic order, cap page size (the common convention is `p`/`page_size`, max 100), and propagate request context. The matching `users` and current plan title should be fetched in the bounded page query/join or bounded batch projection, not one query per subscription. Do not filter out disabled users or add a user-status rule unless requested; it changes the source predicate. Do not change plan or subscription storage, and do not return unrelated personal fields.

If the product means one unique user per row, define how all overlapping subscriptions/plans are represented before implementation. Billing permits multiple active rows and chooses based on end time, ID, remaining quota, request amount and billing preference; selecting one arbitrarily would misstate the existing server rule. A grouped row containing all effective subscription instances, or one row per subscription, is truthful. If the actual request is to display only the subscription currently funding a particular request, that is request-log/billing data and a different feature from a current active-subscription roster.

`common.GetPageQuery(c)` parses the existing `p` and `page_size` convention, defaults page/size, and caps positive oversized page sizes at 100 (`common/page_info.go:9-55`). It does not bound `page * page_size` overflow and does not normalize a negative nonzero page size; a new endpoint should validate/cap both size and offset safely (the channel-list handler has an explicit overflow guard in `controller/channel.go:572-590`). Return the standard `PageInfo` shape (`page`, `page_size`, `total`, `items`) to integrate with existing frontend pagination. Clarify that `total` counts subscription rows if using the per-subscription view; it is not necessarily distinct users.

Frontend integration can place tabs around the existing plans table and a new remote table while retaining page title, plan actions, and payment-compliance warning. Use the feature’s existing API/module and shared `DataTablePage`; query with `manualPagination` and server `total`, reset page on tab/filter changes, use `subscription_id` as row key, and render Unix seconds through the existing `formatTimestamp` (`web/src/features/subscriptions/lib/format.ts`). No payment/plan mutation should be coupled to the read-only tab.

## Tests and compatibility boundaries for implementation

- **Model/query contract:** extend the appropriate existing subscription model test (or create one focused list-query test) with a fixed `now` boundary and rows for active/future-end, `end_time == now`, elapsed end, `status=expired`, `status=cancelled`, and zero end time; verify stable page/count/order, plan/user projection, no-row behavior, multiple active subscriptions for one user, and that a future start remains included if preserving the literal existing predicate. Exercise real SQLite, MySQL and PostgreSQL in hosted CI because the query must be dialect-compatible. No schema/index change is suggested by this inventory; existing subscription indexes are declared on `user_id`, `status`, `end_time` and their active composite (`model/subscription.go:333-341`).
- **Endpoint authorization/pagination:** cover ADMIN/ROOT success, ordinary-user denial, malformed ID-free list parameters, page bounds, exact `total` semantics, and prove the API does not serialize all active rows. Existing router-level admin auth tests establish the middleware convention; there is no current subscription controller/router test file in the source tree discovered during this inventory.
- **Frontend behavior:** add tests under the feature’s dedicated `__tests__/` directory for tab selection, paginated query/page changes, loading/empty/error state, duplicate users with distinct subscriptions, and displayed user/plan/start/expiry values. Reuse shared table/Tabs components and keep the client’s selected tab from changing mutation permissions.
- **Billing separation:** the endpoint is a read-only temporal projection. Do not update `AmountUsed`, perform lazy reset, debit/refund quota, alter the subscription order/plan model, or change billing preference/overflow behavior. The relevant billing guidance was read in full before examining billing-field sources.

## Source map

- `model/subscription.go`: plan/subscription/order storage; current active helpers (`931-979`); all-history read (`981-1008`); invalidate/delete (`1010-1094`); expiry maintenance (`1223-1317`); request-amount-dependent pre-consume selection/reset (`1345-1483`).
- `model/db_time.go`: database clock with application-clock fallback.
- `service/billing_session.go:538-563`: billing preference, active existence, quota exhaustion and wallet-overflow behavior.
- `service/subscription_reset_task.go:16-90`: master-only one-minute expiry/reset job and batch size.
- `controller/subscription.go:31-74, 120-138, 387-399`: self, plan-list and per-user admin reads.
- `router/api-router.go:190-218`: user/admin subscription groups and role middleware.
- `web/src/routes/_authenticated/subscriptions/index.tsx`; `web/src/features/subscriptions/index.tsx`; `web/src/features/subscriptions/components/subscriptions-table.tsx`; `web/src/features/subscriptions/components/dialogs/user-subscriptions-dialog.tsx`; `web/src/features/subscriptions/lib/format.ts`.
- UI reuse references: `.agents/skills/shadcn-ui/SKILL.md`, `web/AGENTS.md`, `web/src/components/ui/tabs.tsx`, `web/src/components/data-table/README.md`, `web/src/components/data-table/layout/data-table-page.tsx`.
