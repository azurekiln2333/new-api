# Effective subscription listing

## Scope and trigger

The administrator subscription tab lists effective subscription rows without
changing billing, reset state, user status or plan configuration.

## Signatures

- `GET /api/subscription/admin/active-users?p=1&page_size=20`
- `ListEffectiveUserSubscriptions(ctx, now, offset, limit)` returns items/total.
- `getEffectiveSubscriptionUsers({ p, page_size }, signal)` forwards cancellation.

## Contracts

AdminAuth and the explicit `billing:read` PAT route declaration both apply. A row
is effective exactly when its status is `active` and `end_time > now`; count and
page use the same clock value. Return one row per subscription, ordered by end
time then subscription ID. Left joins preserve subscriptions whose user or plan
display record is missing. The DTO contains subscription/user/plan IDs, names and
start/end times only. Default page size is20, capped at100; reject invalid positive
integers and offset overflow.

The browser query key includes live user ID, session SID, role and pagination.
Do not carry previous-data placeholders across identities. Gate requests and
rendering on the current admin role; assert unchanged identity and a non-aborted
signal both before and after transport. A same-SID role demotion must hide already
cached privileged rows. Pass AbortSignal to the shared API client. Keep pagination
in the existing page-footer portal when rendered inside the fixed-height tabs.

## Validation and errors

Anonymous/ordinary users and insufficient-scope PATs are denied by server gates.
Malformed pagination returns the existing Invalid parameters envelope. Fetch
failures show a retryable error; cancelled/obsolete replies cannot populate the
current view. Empty results use the existing no-subscription message.

## Good, base and bad cases

- Good: one user with two active unexpired subscriptions appears twice.
- Base: an empty page retains the authoritative total and bounded pagination.
- Bad: an expired row, a populated cache after demotion, or a late cancelled
  response is treated as currently visible administrator data.

## Required tests

`TestEffectiveSubscriptionListDatabaseMatrix` protects predicate, ordering,
count/pagination and read-only behavior on SQLite/MySQL/PostgreSQL.
`TestActiveSubscriptionListRequiresBillingReadAndAdminRole` and the route registry
coverage test protect every authentication route. Existing `active-users.test.tsx`
protects populated-cache demotion, identity/SID/role transitions, deliberately late
transport resolution and integrated tab/footer pagination. Execution remains a
separate hosted gate; source review alone proves none of those runtime results.

## Wrong versus correct

Wrong: key the administrator query only by page and rely on logout to clear cache.
Correct: partition by live identity/role, suppress privileged rendering immediately
and fence request completion independently of the transport's cancellation support.
