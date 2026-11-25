# Scoped Fix Contracts — replay expiry, 409 status, actor-bound export

Source-only findings. No local validation performed. Files marked OWNER belong to
another agent and are NOT edited here.

## 1. Replay must win over expiry validation (OWNER: native worker — controller/invitation.go, controller/redemption.go)

### Defect

Expiry is validated before the operation identity is consulted, so a lost-response
retry of an already-committed batch fails validation instead of returning the
original keys.

Redemption (`controller/redemption.go`):
```go
if err := validateExpiredTime(redemption.ExpiredTime); err != nil {   // ~line 96
    common.ApiError(c, err)
    return
}
...
result, err := model.CreateCodeBatch(...)                            // ~line 121
```

Invitation (`controller/invitation.go`):
```go
if request.ExpiredTime != 0 && request.ExpiredTime <= common.GetTimestamp() {  // ~line 58
    common.ApiErrorT(c, "Expiration time cannot be earlier than current time")
    return
}
...
result, err := model.CreateCodeBatch(...)                            // ~line 71
```

Consequence: an operator creates 100 codes with a short expiry, the response is
lost, they retry two minutes later, and they get "Expiration time cannot be earlier
than current time" while the batch exists in the database and is unreachable from
the console. The keys are effectively lost even though they were committed.

### Required behaviour

A matching replay must return the committed batch even when its expiry is now in the
past. A fresh create with an expired timestamp must still be rejected.

### Contract

Move expiry validity out of the pre-transaction validation into the create, so it
runs only on the path that actually inserts rows.

```go
// controller/redemption.go — remove the pre-create validateExpiredTime call.
// Keep every other validation (name length, count bounds, quota, wallet bound).

result, err := model.CreateCodeBatch(model.CodeBatchRequest{
    OperationId:     c.Query("operation_id"),
    OperationType:   model.CodeOperationRedemption,
    RequestDigest:   digest,          // unchanged: still includes expired_time
    ActorUserId:     snapshot.UserId,
    ActorRole:       snapshot.Role,
    RequestId:       snapshot.RequestId,
    CreationBatchId: batchId,
    BuildRows: func() (any, []string, error) {
        // Expiry is validated HERE, on the insert path only. A replay never
        // reaches BuildRows, so an already-committed batch is returned even
        // after its codes have expired.
        if err := validateExpiredTime(redemption.ExpiredTime); err != nil {
            return nil, nil, err
        }
        ... // unchanged row construction
    },
    Audit: buildAuditDraft(snapshot, "redemption.create", params),
})
```

Same shape for invitation: move the expiry check inside `BuildRows`, returning
`(nil, nil, err)`.

### Why the digest must keep covering `expired_time`

`CodeRequestDigest` includes `expired_time`. That is required: a retry with a
*different* expiry is a different request and must return `ErrCodeBatchConflict`,
not silently return the old batch. Keeping the field in the digest preserves that
while moving only the *validity* check later.

### Error mapping

`BuildRows` errors surface from `CreateCodeBatch` through
`sanitizeCodeBatchError`, which currently collapses anything that is not a batch
sentinel into `ErrCodeBatchStorage`. That would turn an expired-timestamp rejection
into an opaque storage error. The fix must let the validation error through:

```go
// model/code_operation.go — sanitizeCodeBatchError
// Preserve caller-supplied validation errors from BuildRows so the controller can
// report the real reason instead of an opaque storage failure.
var ErrCodeBatchRejected = errors.New("management create request rejected")

// In CreateCodeBatch, wrap a BuildRows error:
if err := ...; err != nil {
    return nil, nil, fmt.Errorf("%w: %v", ErrCodeBatchRejected, err)
}
// and in sanitizeCodeBatchError, return ErrCodeBatchRejected unchanged so the
// controller can map it. Do NOT include the wrapped detail in any response.
```

The controller then maps `ErrCodeBatchRejected` to the existing user-facing
expiration message. The wrapped detail stays out of the HTTP response and out of
logs, because it can echo operator input.

### Regression cases (for the native worker's own test file)

1. Create with `expired_time = now - 60` and a client `operation_id` → rejected, no
   rows, no batch identity.
2. Create with a valid expiry, then retry the identical request after the expiry has
   passed → `Replayed == true`, original keys returned, no new rows.
3. Retry with the same `operation_id` but a different `expired_time` → 
   `ErrCodeBatchConflict`, no new rows.

## 2. Mismatch must be HTTP 409 (OWNER: native worker — controller/invitation.go, controller/redemption.go)

### Defect

`common.ApiErrorT` returns HTTP 200:

```go
func ApiErrorT(c *gin.Context, key string, params ...map[string]any) {
    ApiError(c, NewMessage(key, params...))       // common/gin.go:305
}
```
`ApiError` writes the failure body with `http.StatusOK`, so the current
`ErrCodeBatchActorMismatch` / `ErrCodeBatchConflict` branches return 200 with
`success: false`.

The published contract says 409. A 200 body is indistinguishable from a validation
message, and a client retry policy cannot tell "conflict, stop" from "transient".

### Contract

Use the status-carrying helper that already exists:

```go
common.ApiErrorStatus(c, http.StatusConflict, common.NewMessage("..."))
```

Branches:

| error | status | body |
|---|---|---|
| `ErrCodeBatchActorMismatch` | 409 | actor mismatch message |
| `ErrCodeBatchConflict` | 409 | conflict message |
| `ErrCodeBatchInvalid` | 400 | invalid request message |
| `ErrCodeBatchRejected` | 200 (or 400) | the existing validation message |
| default | 500 | opaque failure message |

`ErrCodeBatchActorMismatch` and `ErrCodeBatchConflict` must NOT be distinguishable
from each other in a way that reveals whether another operator's batch exists. Both
are 409; the message text should be the same generic conflict sentence, with the
distinction kept server-side only.

## 3. Historical batch export must be actor-bound (DONE in model/code_export.go)

### Defect found in my own code

`ExportRedemptions` / `ExportInvitations` called `GetOperationBatch(operationId)` and
decoded `result_json` with no ownership check, so any operator with the invitation or
redemption read permission could export another operator's keys by supplying a known
`operation_id`. `ListCodeBatches` was already actor-scoped, which made the gap easy
to miss.

### Fix applied

```go
func operationBatchKeysForActor(operationId string, actorUserId int) (*CodeBatchResult, error) {
    batch, err := GetOperationBatch(operationId)
    if err != nil {
        return nil, err
    }
    if batch.ActorUserId != actorUserId {
        return nil, ErrCodeExportEmpty      // same error as a missing batch
    }
    return decodeOperationKeys(batch)
}
```

Both export functions now take `actorUserId` and the controllers pass
`c.GetInt("id")`. A mismatched actor receives the same error as a nonexistent batch,
so the endpoint cannot be used to probe for the existence of another operator's
batch. No admin bypass is implemented, per the schema contract that generated keys
are reachable only through authorized actor-bound replay or export.

## 4. Dialect-correct column quoting (DONE in model/code_export.go)

`key` is a reserved word. The export used a literal backtick-quoted `` `key` ``,
which is invalid on PostgreSQL. Both the select and the `IN` filter now use
`model.commonKeyCol`, which resolves to `"key"` on PostgreSQL and `` `key` `` on
MySQL/SQLite via `initCol()` in `model/main.go:47,52`.

## 5. Explicit selection must not be silently reshaped (DONE)

`parseCodeExportIds` previously truncated at 5000 and dropped malformed ids with
`continue`, so an oversized or partially invalid selection silently exported a
different set than the operator selected — and an all-invalid selection fell through
to a whole-filter export. It now returns `(ids, ok)` and fails the request on any
malformed entry or an oversized list.

Filtered export also queries `limit + 1` rows so a selection above the bound reports
`ErrCodeExportTooLarge` instead of returning a truncated file.
