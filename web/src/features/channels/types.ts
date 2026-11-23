/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/
import { z } from 'zod'

// ============================================================================
// Channel Schema & Types
// ============================================================================

const channelModelUnavailabilitySchema = z.object({
  upstream_model: z.string(),
  key_fingerprint: z.string(),
  status_code: z.number(),
  evidence: z.enum(['error_code', 'error_message']),
  observed_at: z.number(),
})

export type ChannelModelUnavailability = z.infer<
  typeof channelModelUnavailabilitySchema
>

export const channelInfoSchema = z.object({
  model_unavailable: z.array(channelModelUnavailabilitySchema).optional(),
  key_entry_revision: z.number().optional(),
  is_multi_key: z.boolean().default(false),
  multi_key_size: z.number().default(0),
  multi_key_status_list: z.record(z.string(), z.number()).optional(),
  multi_key_disabled_reason: z.record(z.string(), z.string()).optional(),
  multi_key_disabled_time: z.record(z.string(), z.number()).optional(),
  // Key index -> models that key may serve. Absent or empty = every channel model.
  multi_key_models: z.record(z.string(), z.array(z.string())).optional(),
  // Key index -> user ID that exclusively owns the key. Absent = public key.
  multi_key_users: z.record(z.string(), z.number()).optional(),
  multi_key_polling_index: z.number().default(0),
  multi_key_mode: z.enum(['random', 'polling']).default('random'),
})

export type ChannelInfo = z.infer<typeof channelInfoSchema>

/** One limit of a balance, such as a usage window that resets. */
const channelBalanceWindowSchema = z.object({
  name: z.union([z.string(), z.record(z.string(), z.string())]),
  used: z.number(),
  limit: z.number(),
  unit: z.string().nullish(),
  /** Unix seconds. */
  resetAt: z.number().nullish(),
})

const channelBalanceResultSchema = z.object({
  /** In `currency`; null when the upstream reports only windows. */
  amount: z.number().nullish(),
  currency: z.string().nullish(),
  unlimited: z.boolean().nullish(),
  windows: z.array(channelBalanceWindowSchema).nullish(),
  /** The last failed query, kept beside the last good result. */
  error: z.string().nullish(),
})

export const channelBalanceDetailSchema = channelBalanceResultSchema.extend({
  /** The balance of each key of a multi-key channel, by key index. */
  keys: z.record(z.string(), channelBalanceResultSchema).nullish(),
})

export type ChannelBalanceWindow = z.infer<typeof channelBalanceWindowSchema>
export type ChannelBalanceResult = z.infer<typeof channelBalanceResultSchema>
export type ChannelBalanceDetail = z.infer<typeof channelBalanceDetailSchema>

export const channelSchema = z.object({
  id: z.number(),
  type: z.number(),
  key: z.string(),
  openai_organization: z.string().nullish(),
  test_model: z.string().nullish(),
  status: z.number(), // 1: enabled, 0: manual disabled, 2: auto disabled
  name: z.string(),
  weight: z.number().nullish(),
  created_time: z.number(),
  test_time: z.number(),
  response_time: z.number(), // in milliseconds
  base_url: z.string().nullish(),
  other: z.string().default(''),
  balance: z.number().default(0), // in USD
  balance_updated_time: z.number(),
  balance_detail: channelBalanceDetailSchema.nullish(),
  models: z.string().default(''),
  group: z.string().default('default'),
  used_quota: z.number().default(0),
  model_mapping: z.string().nullish(),
  status_code_mapping: z.string().nullish(),
  priority: z.number().nullish(),
  auto_ban: z.number().nullish(),
  other_info: z.string().default(''),
  tag: z.string().nullish(),
  setting: z.string().nullish(),
  param_override: z.string().nullish(),
  header_override: z.string().nullish(),
  remark: z.string().default(''),
  max_input_tokens: z.number().default(0),
  channel_info: channelInfoSchema.default({
    is_multi_key: false,
    multi_key_size: 0,
    multi_key_polling_index: 0,
    multi_key_mode: 'random',
  }),
  settings: z.string().default('{}'), // other_settings JSON
})

export type Channel = z.infer<typeof channelSchema>

// ============================================================================
// Channel Settings Types
// ============================================================================

export interface ChannelSettings {
  max_concurrent_requests?: number
  upstream_first_byte_timeout_seconds?: number | null
  supported_endpoints?: ('chat' | 'responses' | 'messages')[]
  task_plugin_key?: string
  task_extend_plugin_keys?: string[]
  /** Request processing instance ids, in the order they run. */
  request_plugins?: string[]
  force_format?: boolean
  thinking_to_content?: boolean
  proxy?: string
  pass_through_body_enabled?: boolean
  responses_websocket_enabled?: boolean
  /**
   * Refuses recognized goal-triggered turns, such as Codex automatic goal
   * continuations. Only turns carrying the canonical turn_trigger signal are
   * recognized, so this is not complete coverage of goal-associated traffic.
   */
  deny_goal?: boolean
  system_prompt?: string
  system_prompt_override?: boolean
  http_protocol?: 'auto' | 'http1' | string
  http2_connection_shards?: number
  tls_insecure_skip_verify?: boolean
}

export interface ChannelOtherSettings {
  azure_responses_version?: string
  vertex_key_type?: 'json' | 'api_key'
  vertex_project_id?: string
  vertex_api_version?: 'v1' | 'v1beta1'
  openrouter_enterprise?: boolean
  aws_key_type?: 'ak_sk' | 'api_key'
  aws_api_type?: 'invoke_model' | 'mantle'
  allow_service_tier?: boolean
  disable_store?: boolean
  allow_safety_identifier?: boolean
  allow_include_obfuscation?: boolean
  allow_inference_geo?: boolean
  allow_speed?: boolean
  claude_beta_query?: boolean
  ollama_openai_chat?: boolean
  disable_task_polling_sleep?: boolean
  upstream_model_update_check_enabled?: boolean
  upstream_model_update_auto_sync_enabled?: boolean
  upstream_model_update_ignored_models?: string[]
  upstream_model_update_last_check_time?: number
  upstream_model_update_last_detected_models?: string[]
  // Group -> models visible to that group. Absent or empty = every channel model.
  group_models?: Record<string, string[]>
  advanced_custom?: AdvancedCustomConfig
  /** The values of the bound plugin's channel settings (type 61). */
  plugin_config?: Record<string, unknown>
}

export interface AdvancedCustomConfig {
  advanced_routes?: AdvancedCustomRoute[]
}

export interface AdvancedCustomRoute {
  incoming_path?: string
  upstream_path?: string
  converter?: AdvancedCustomConverter
  models?: string[]
  auth?: AdvancedCustomRouteAuth
  pass_through_body_enabled?: boolean
}

export interface AdvancedCustomRouteAuth {
  type?: AdvancedCustomAuthType
  name?: string
  value?: string
}

export type AdvancedCustomConverter =
  | 'none'
  | 'anthropic_messages_to_openai_chat_completions'
  | 'openai_chat_completions_to_anthropic_messages'
  | 'openai_chat_completions_to_openai_responses'
  | 'openai_responses_to_openai_chat_completions'
  | 'openai_responses_to_gemini_generate_content'
  | 'gemini_generate_content_to_openai_chat_completions'
  | 'openai_chat_completions_to_gemini_generate_content'
  | 'claude_messages_to_openai_responses'
  | 'gemini_generate_content_to_openai_responses'

export type AdvancedCustomAuthType = 'none' | 'header' | 'query'

// ============================================================================
// API Response Types
// ============================================================================

export interface GetChannelsResponse {
  success: boolean
  message?: string
  data?: {
    items: Channel[]
    total: number
    page: number
    page_size: number
    type_counts?: Record<string, number>
  }
}

export interface SearchChannelsResponse {
  success: boolean
  message?: string
  data?: {
    items: Channel[]
    total: number
    type_counts?: Record<string, number>
  }
}

export interface GetChannelResponse {
  success: boolean
  message?: string
  data?: Channel
}

export interface ChannelOpsResponse {
  success: boolean
  message?: string
  data?: {
    retry_times: number
    request_policy?: {
      automatic_disable: boolean
      source: string
    }
  }
}

export interface ChannelTestResponse {
  success: boolean
  message?: string
  error_code?: string
  time?: number
  data?: {
    response_time?: number
    error?: string
  }
}

export interface ChannelBalanceResponse {
  success: boolean
  message?: string
  balance?: number
  balance_detail?: ChannelBalanceDetail | null
  currency?: string
  raw_response?: string
}

export interface FetchModelsResponse {
  success: boolean
  message?: string
  data?: string[]
}

export interface CopyChannelResponse {
  success: boolean
  message?: string
  data?: {
    id: number
  }
}

// ============================================================================
// Multi-Key Management Types
// ============================================================================

/**
 * One channel/Key budget account, in station quota units. `remaining_quota` is
 * absent (or null) while the scope is unlimited; `exhausted` reports that an
 * enforced limit has no capacity left for the next request.
 */
export interface ChannelBudgetView {
  scope_type: 'channel' | 'key'
  scope_id: string
  channel_id: number
  ledger_enabled: boolean
  enabled: boolean
  limit_quota: number
  period_used_quota: number
  lifetime_used_quota: number
  reserved_quota: number
  remaining_quota?: number | null
  reset_epoch: number
  config_version: number
  accounting_started_at: number
  exhausted?: boolean
}

/** One key's budget view, nested in its stable allocation status. */
export type KeyQuotaView = ChannelBudgetView

export interface SetChannelBudgetParams {
  enabled: boolean
  limit_quota: number
  expected_config_version: number
}

export interface ResetChannelBudgetParams {
  expected_config_version: number
  operation_id: string
}

export interface KeyStatus {
  index: number
  /** Stable allocation ID as a decimal string; survives add, reorder, delete. */
  id?: string
  /** Allocation configuration revision this status was read at. */
  rev?: number
  status: number // 1: enabled, 2: manual disabled, 3: auto disabled
  disabled_time?: number
  reason?: string
  key_preview?: string
  // An empty allowlist denies all models; legacy rows may omit scope_mode.
  models?: string[]
  // User that exclusively owns this key; absent or 0 = public key.
  user_id?: number
  username?: string
  scope_mode?: 'all' | 'allowlist'
  // Absent when the budget ledger is not available; never faked as unlimited.
  quota?: KeyQuotaView
}

export type MultiKeyConfirmAction = {
  type:
    | 'enable'
    | 'disable'
    | 'delete'
    | 'enable-all'
    | 'disable-all'
    | 'delete-disabled'
  keyIndex?: number
  /** Stable allocation ID for a single-entry action. */
  keyEntryId?: string
  expectedRev?: number
  /** Channel allocation-set revision shown when a bulk action was confirmed. */
  expectedRevision?: number
}

export interface MultiKeyStatusResponse {
  success: boolean
  message?: string
  error_code?: string
  code?: string
  data?: {
    revision?: number
    keys: KeyStatus[]
    total: number
    page: number
    page_size: number
    total_pages: number
    enabled_count: number
    manual_disabled_count: number
    auto_disabled_count: number
    /** Current ledger activation, independent of the filtered rows. */
    quota_supported?: boolean
  }
}

/** One set of allocations an explicit dedup would merge. */
export interface MultiKeyDedupGroup {
  survivor_id: string
  entry_ids: string[]
  merged_period_used: number
  merged_lifetime_used: number
  limit_quota?: number | null
  /** Entries left out because they are busy; they stay as they are. */
  skipped: { id: string; reason: string }[]
}

/** Preview of a dedup, plus its apply summary when `apply` was true. */
export interface MultiKeyDedupResult {
  preview_token?: string
  revision?: number
  applied?: boolean
  groups: MultiKeyDedupGroup[]
  merged_groups?: number
  retired_entries?: number
  skipped_entries?: number
}

// ============================================================================
// API Request Parameters
// ============================================================================

export type ChannelSortBy =
  | 'id'
  | 'name'
  | 'priority'
  | 'balance'
  | 'response_time'
  | 'test_time'

export type ChannelSortOrder = 'asc' | 'desc'

export interface GetChannelsParams {
  p?: number
  page_size?: number
  status?: string // 'enabled', 'disabled', or empty for all
  type?: number
  group?: string
  id_sort?: boolean
  tag_mode?: boolean
  sort_by?: ChannelSortBy
  sort_order?: ChannelSortOrder
}

export interface SearchChannelsParams {
  keyword?: string
  group?: string
  model?: string
  status?: string
  type?: number
  id_sort?: boolean
  tag_mode?: boolean
  sort_by?: ChannelSortBy
  sort_order?: ChannelSortOrder
  p?: number
  page_size?: number
}

export interface ChannelTestParams {
  test_model?: string
}

export interface CopyChannelParams {
  suffix?: string
  reset_balance?: boolean
}

export type MultiKeyManageAction =
  | 'get_key_status'
  | 'disable_key'
  | 'enable_key'
  | 'enable_all_keys'
  | 'disable_all_keys'
  | 'delete_key'
  | 'delete_disabled_keys'
  | 'set_key_models'
  | 'set_key_user'
  | 'set_key_quota'
  | 'reset_key_quota'
  | 'deduplicate_keys'
  | 'reorder_keys'
  | 'fetch_key_models'
  | 'sync_key_models'

export interface MultiKeyManageParams {
  channel_id: number
  action: MultiKeyManageAction
  key_index?: number
  key_entry_id?: string
  expected_revision?: number
  key_entry_ids?: string[]
  preview_token?: string
  scope_mode?: 'all' | 'allowlist'
  page?: number
  page_size?: number
  status?: number // 1=enabled, 2=manual_disabled, 3=auto_disabled
  models?: string[] // set_key_models: empty array clears the restriction
  user_id?: number // set_key_user: 0 unbinds
  apply?: boolean // sync_key_models / deduplicate_keys: write the previewed result
  // set_key_quota / reset_key_quota
  expected_rev?: number
  expected_config_version?: number
  enabled?: boolean
  limit_quota?: number
  operation_id?: string
}

/** Upstream models reported for one key by `fetch_key_models` / `sync_key_models`. */
export interface MultiKeyModelFetchResult {
  key_index: number
  key_entry_id?: string
  rev?: number
  models?: string[]
  error?: string
  skipped?: boolean
}

/** `sync_key_models` preview: per-key results plus the models it would add. */
export interface MultiKeyModelSyncResult {
  revision?: number
  results: MultiKeyModelFetchResult[]
  added_models: string[]
  channel_models?: string[]
  applied: boolean
}

export interface BatchDeleteParams {
  ids: number[]
}

export interface BatchSetTagParams {
  ids: number[]
  tag: string | null
}

export interface TagOperationParams {
  tag: string
  new_tag?: string
  priority?: number
  weight?: number
  model_mapping?: string
  models?: string
  groups?: string
}

// ============================================================================
// Form Data Types
// ============================================================================

export interface ChannelFormData {
  name: string
  type: number
  base_url: string
  key: string
  openai_organization?: string
  models: string
  group: string
  model_mapping?: string
  priority?: number
  weight?: number
  test_model?: string
  auto_ban?: number
  status: number
  status_code_mapping?: string
  tag?: string
  remark?: string
  setting?: string
  param_override?: string
  header_override?: string
  settings?: string
  other?: string
  // Multi-key specific
  multi_key_mode?: 'single' | 'batch' | 'multi_to_single'
  multi_key_type?: 'random' | 'polling'
  batch_add_set_key_prefix_2_name?: boolean
}

//
// Update (PUT /api/channel/) adds a few control fields on top of the channel
// payload: `key_mode` decides append/replace for multi-key channels and
// `to_multi_key` converts a single-key channel into a multi-key one, using
// `multi_key_mode` for the resulting key selection strategy.
//
export interface ChannelUpdateRequest extends Partial<Channel> {
  expected_revision?: number
  key_mode?: 'append' | 'replace'
  to_multi_key?: boolean
  multi_key_mode?: 'random' | 'polling'
}

// ============================================================================
// Add Channel Request (special structure)
// ============================================================================

export interface AddChannelRequest {
  mode: 'single' | 'batch' | 'multi_to_single'
  multi_key_mode?: 'random' | 'polling'
  batch_add_set_key_prefix_2_name?: boolean
  channel: Partial<Channel>
}

// ============================================================================
// Channel usage (consume-log aggregates)
// ============================================================================

export interface ChannelUsageSummary {
  channel_id: number
  request_count: number
  prompt_tokens: number
  completion_tokens: number
  total_tokens: number
  quota: number
}

export interface ChannelModelUsage {
  model_name: string
  enabled: boolean
  request_count: number
  prompt_tokens: number
  completion_tokens: number
  total_tokens: number
  quota: number
  last_used_at: number
}

export interface ChannelUsageDetail extends ChannelUsageSummary {
  models: ChannelModelUsage[]
}

/** Which coordinator produced a live concurrency sample. */
export type ChannelConcurrencyScope = 'cluster' | 'instance'

/**
 * One channel's live attempt sample from `GET /api/channel/concurrency`.
 * `current` is null when the sample is unavailable — never a stand-in for a
 * real zero. `limit` is null for an unlimited channel, so the UI shows the
 * current count without inventing a cap.
 */
export interface ChannelConcurrencyEntry {
  id: number
  current: number | null
  limit: number | null
  scope: ChannelConcurrencyScope
  /** Coordinator/server clock, Unix milliseconds. */
  sample_time: number
}
