# R14 subscription-consumption design note (source-only)

## Current authoritative record

Subscription consumption is stored on `model.UserSubscription.AmountUsed` (`model/subscription.go:265-292`, field at line 271). It is changed by the existing billing paths:

- Initial request pre-consumption selects and locks the user's active subscription, creates an idempotency row in `subscription_pre_consume_records`, increments `UserSubscription.AmountUsed`, and commits (`model/subscription.go:1315-1416`; increment at `model/subscription.go:1400`). The pre-consume record stores request-level `PreConsumed`, `UserId`, and `UserSubscriptionId` (`model/subscription.go:1255-1265`) but is not itself a durable per-user cumulative total.
- Settlement/refund deltas update the same `UserSubscription.AmountUsed` under a row lock (`model/subscription.go:1527-1550`). `service.BillingSession` routes subscription reserve, settlement, and rollback through `PostConsumeUserSubscriptionDelta` (`service/billing_session.go:350-390`), while the request log exposes request-level fields such as `subscription_consumed`, `subscription_used`, and `subscription_remain` in `service/log_info_generate.go:204-249`.
- `UserSubscription.AmountUsed` is reset to zero when the subscription's configured reset period advances (`model/subscription.go:1279-1312` and the administrative reset path around `model/subscription.go:1030-1050`). Therefore it is the current subscription-period usage, not an all-time historical total.

## Is there already a per-user cumulative value?

No single per-user cumulative subscription-consumption column exists on `users`. `UserSubscription.AmountUsed` is per subscription instance and is resettable. A user may have multiple subscription rows, including expired/cancelled historical rows, so a user-list value must be an aggregate projection rather than a new billing counter.

For the user list's “subscription consumption” column, aggregate `SUM(user_subscriptions.amount_used)` grouped by `user_subscriptions.user_id`, using the same user visibility/deleted-row scope as the list query. The product wording should state that this is the current recorded cumulative amount across the user's subscription records; because resettable `AmountUsed` is the existing source, it must not be described as immutable lifetime usage. If the intended meaning is current active-period consumption only, add `status = 'active'` and document that scope explicitly; the source code currently exposes all subscription records through `GetAllUserSubscriptions`, so the safer compatibility projection is all rows for the listed user unless product clarifies active-only semantics.

The existing `subscription_pre_consume_records` table is request idempotency/recovery state, not a replacement aggregate: summing `pre_consumed` would double-count refunds/settlement corrections and would diverge from `AmountUsed`. Do not derive the column from consume logs: logs contain request-level fields and may use a separate log database.

## API/projection change

Extend the user-list projection with an additive numeric field, e.g. `subscription_used_quota` (quota units), without changing `users.used_quota` or merging the values. Keep the existing `User` model's `UsedQuota` semantics unchanged. Preferred implementation boundary:

1. Add a read-only projection field to the list DTO (a dedicated response view is preferable to exposing an aggregate field as a persisted `User` column).
2. In `model.GetAllUsers` and `model.SearchUsers`, retain the existing count/page/order behavior and join a grouped subquery or run one bounded aggregate query for the returned page's user IDs. The aggregate must use the caller's DB connection/context and remain valid on SQLite, MySQL, and PostgreSQL; avoid a dialect-specific JSON function or a schema change.
3. Populate missing aggregates as zero. Preserve `used_quota` as the wallet/general accumulated usage column. No write path, quota arithmetic, settlement, reset, or billing source behavior changes.
4. If the frontend/API contract intentionally keeps serialized `User` rows, an additive `json:"subscription_used_quota"` field may be used as a non-persisted projection field, but it must not be included in GORM writes or update allowlists. A separate `UserListItem`/response struct is safer for preventing accidental update persistence.

A page-scoped second query avoids multiplying user rows and does not change pagination totals. A grouped `LEFT JOIN` subquery can also be one SQL statement; choose the existing user-list query style after root review. The aggregate query must use `context.Context` from the HTTP request once the model methods accept it; current `GetAllUsers`/`SearchUsers` signatures do not take context, so request cancellation is an existing limitation to address only if the owner elects to extend the API.

## Database/migration impact

No schema migration is required if the value remains a computed projection over `user_subscriptions.amount_used`. Fresh and upgrade verification therefore needs to confirm only query compatibility and existing data behavior on real SQLite, MySQL, and PostgreSQL; there is no new column/index or `AutoMigrate` change. Run the user-list aggregate tests against all three engines, with:

- no subscription rows → zero;
- one user with one subscription;
- one user with multiple subscription rows;
- a resettable subscription after reset (the aggregate follows the stored `AmountUsed`);
- users with equal values and the existing sort/pagination options;
- filtered search and deleted-user scope;
- fresh schema and a representative latest-release upgraded schema, with startup/migration twice even though this change adds no migration.

The separate log database/ClickHouse matrix is not implicated because the projection reads the primary `user_subscriptions` table, not `logs`.

## Frontend column

The existing user table is `web/src/features/users/components/users-columns.tsx`. It currently renders `quota` / Available Balance and `used_quota` / Total Used independently. Add a third independent column, for example:

- accessor: `subscription_used_quota`
- header: `Subscription Used (Tokens)` or the currency-aware equivalent, using the existing `quotaUnit` and `formatQuotaWithCurrency`
- value: the server-provided aggregate only
- position: adjacent to `Total Used`, without changing `UserQuotaCell` or adding it to available balance/total used

Add the optional field to `web/src/features/users/types.ts` and keep the API sort allowlist unchanged unless product explicitly requests sorting by this computed value. If sorting is requested, implement an explicit aggregate sort in both list queries; do not sort the current page in the browser. Any new user-facing text requires all seven locale updates through the i18n skill's script-only workflow, owned by the frontend worker.

## Non-goals and risks

- Do not add `subscription_used_quota` to `users`, change `User.UsedQuota`, or alter billing math.
- Do not sum `subscription_pre_consume_records.PreConsumed`; refunds and retries make it unsuitable as a cumulative source.
- Do not treat `AmountUsed` as immutable lifetime usage because reset paths intentionally set it to zero.
- Resolve with root/product owner whether “cumulative” means all subscription rows or active subscriptions only before implementation; the current storage model supports both projections, but they answer different questions.
