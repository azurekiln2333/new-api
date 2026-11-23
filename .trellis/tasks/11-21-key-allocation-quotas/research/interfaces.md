# Allocation implementation boundary and interfaces

Current worker: pi-keys in Herdr. Ownership follows parent dispatch.md. Replace unfinished index aliases with authoritative rows; preserve shared billing/log/UI edits. No commits or nested agents.

## Frozen API naming (coordinate with UI)

`POST /api/channel/multi_key/manage`, `channel_id` required.

Read `get_key_status.data`: existing pagination and statistics plus `revision:number`; every key has `id:string`, `rev:number`, `index:number` (display only), existing masked fields, `scope_mode:all|allowlist`, and `quota:ChannelBudgetView` (`scope_id` is decimal string).

Single allocation edits use `key_entry_id:string`, `expected_rev:number`. Mutations of a whole list use `expected_revision:number` matching the read revision. Legacy index mutations are accepted only when stored credentials are unique; duplicated lists require IDs/revisions. Stale CAS => HTTP 409, `error_code:channel_key_revision_conflict`, no mutation. Existing channel sensitive-write permission remains required for configuration, binding, delete, limit/reset/dedup; status uses existing operate permission.

- `set_key_models`: `models:string[]`, optional `scope_mode:all|allowlist` (omitted preserves prior empty=all legacy semantics).
- `set_key_user`: `user_id:number` (0 public).
- `set_key_quota`: additionally `expected_config_version:number`, `enabled:bool`, `limit_quota:number`.
- `reset_key_quota`: additionally `expected_config_version:number`, `operation_id:string`.
- `reorder_keys`: `key_entry_ids:string[]`, `expected_revision:number`.
- `deduplicate_keys`: `apply:false` => masked preview + `preview_token:string`, `revision:number`; `apply:true` additionally requires returned `preview_token` and `expected_revision`. Recompute and compare complete policy/account state under lock. Skip live reservations/tasks. Merge through billing Tx API, never a second ledger.

Create/append accepts duplicate static keys. Existing append endpoint keeps `key_mode:append`; it appends every submitted nonempty credential. Raw replacement of a duplicated list is rejected if changed; explicit ID actions perform deletes/reorders. Managed account identities remain unique. Scope mode distinguishes unrestricted from an empty explicit allowlist.

## Billing boundary

ChannelKeyEntry columns: id/channel_id/position/key/credential_ref/bound_user_id/scope_mode/models/status/disabled_reason/disabled_time/config_revision/retired_at/merged_into_id/created_time/updated_time. `key` never JSON/audit.

Provide `ValidateBudgetSelectionTx(tx *gorm.DB, channel *Channel, selection *BudgetSelection) error`; billing owns BudgetSelection and adds Group string. Validate current ID/revision/status/model/ownership and public group under channel -> sorted entry locks. Context carries entry ID and revision. No wallet/subscription/token amount changes.

Use billing `SetChannelBudgetLimitTx`, `ResetChannelBudgetUsageTx`, `MergeChannelBudgetAccountsTx`, `HasLiveChannelBudgetReservationsTx` under one channel+entries transaction. Public ledger wrappers cannot be nested. Real verification uses worker-specific newapi_keys only.
