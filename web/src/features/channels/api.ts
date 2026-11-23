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
import type {
  RequestPluginConfigSchema,
  RequestPluginSample,
} from '@/features/task-plugins/types'
import { getGroups as getUserGroups } from '@/features/users/api'
import { api, type ApiRequestConfig } from '@/lib/api'
import type { LocalizedTextValue } from '@/lib/localized-text'
import { requireServerSuccess } from '@/lib/server-error-message'

import type { PriorityMove, PriorityOrder } from './lib/channel-priority-order'
import type { InferenceStatus } from './lib/inference-status'
import type {
  PluginCredentialSummary,
  PluginLoginInput,
  PluginLoginStep,
} from './lib/plugin-login'
import type {
  AddChannelRequest,
  BatchDeleteParams,
  BatchSetTagParams,
  Channel,
  ChannelBalanceResponse,
  ChannelBudgetView,
  ChannelOpsResponse,
  ChannelTestResponse,
  ChannelConcurrencyEntry,
  ChannelUpdateRequest,
  ChannelUsageDetail,
  ChannelUsageSummary,
  CopyChannelParams,
  CopyChannelResponse,
  FetchModelsResponse,
  GetChannelResponse,
  GetChannelsParams,
  GetChannelsResponse,
  MultiKeyDedupResult,
  MultiKeyManageParams,
  MultiKeyModelSyncResult,
  MultiKeyStatusResponse,
  ResetChannelBudgetParams,
  SearchChannelsParams,
  SearchChannelsResponse,
  SetChannelBudgetParams,
  TagOperationParams,
} from './types'

const channelActionConfig = (
  config: ApiRequestConfig = {}
): ApiRequestConfig => ({
  ...config,
  skipBusinessError: true,
  skipErrorHandler: true,
})

/** Clear remembered model refusals; the next eligible request probes again. */
export async function restoreChannelModelAvailability(
  id: number
): Promise<boolean> {
  const response = await api.post<{ success: boolean; data: boolean }>(
    `/api/channel/${id}/model_unavailable/restore`,
    {},
    channelActionConfig()
  )
  return requireServerSuccess(response.data).data
}

export async function getChannelPriorityOrder(): Promise<PriorityOrder> {
  const response = await api.get<{ success: boolean; data: PriorityOrder }>(
    '/api/channel/priority/order',
    { disableDuplicate: true }
  )
  return requireServerSuccess(response.data).data
}

export async function reorderChannelPriority(
  move: PriorityMove
): Promise<PriorityOrder> {
  const response = await api.post<{ success: boolean; data: PriorityOrder }>(
    '/api/channel/priority/reorder',
    move
  )
  return requireServerSuccess(response.data).data
}

export async function getInferenceStatus(
  channelId: number,
  provider: 'vllm' | 'sglang',
  signal?: AbortSignal
): Promise<InferenceStatus> {
  const response = await api.get<{ success: boolean; data: InferenceStatus }>(
    `/api/channel/${channelId}/${provider}/status`,
    { signal, disableDuplicate: true }
  )
  return requireServerSuccess(response.data).data
}

export async function getChannelUsageSummaries(
  ids: number[],
  signal?: AbortSignal
): Promise<ChannelUsageSummary[]> {
  const response = await api.get<{
    success: boolean
    data: { items: ChannelUsageSummary[] }
  }>('/api/channel/usage', {
    params: { ids: ids.join(',') },
    signal,
    disableDuplicate: true,
  })
  return requireServerSuccess(response.data).data.items ?? []
}

export async function getChannelUsageDetail(
  channelId: number,
  signal?: AbortSignal
): Promise<ChannelUsageDetail> {
  const response = await api.get<{
    success: boolean
    data: ChannelUsageDetail
  }>(`/api/channel/${channelId}/usage`, { signal, disableDuplicate: true })
  return requireServerSuccess(response.data).data
}

/** One bounded batch of live concurrency samples; callers cap `ids` at 200. */
export async function getChannelConcurrency(
  ids: number[],
  signal?: AbortSignal
): Promise<ChannelConcurrencyEntry[]> {
  const response = await api.get<{
    success: boolean
    data: { items: ChannelConcurrencyEntry[] }
  }>('/api/channel/concurrency', {
    params: { ids: ids.join(',') },
    signal,
    disableDuplicate: true,
  })
  return requireServerSuccess(response.data).data.items ?? []
}

export type TaskPluginOption = {
  sortPriority?: number
  website?: string
  key: string
  name: string
  /** The name in other languages; `name` is the fallback. */
  name_i18n?: LocalizedTextValue | null
  description?: Record<string, string> | null
  icon?: string
  hasIcon?: boolean
  baseUrl?: string
  models: string[]
  channelTypes?: number[] | null
  upstreams?: string[] | null
  /** The plugin can submit tasks. */
  task?: boolean
  /** The plugin serves relay routes. */
  provider?: boolean
  /** The plugin logs in to get its credentials; task-only plugins can too. */
  login?: boolean
  refresh?: boolean
  /** The plugin processes requests; a plugin with only this part has no channel. */
  transform?: boolean
  /** A failure of the plugin is skipped instead of refusing the request. */
  optional?: boolean
  /** The request processing stages the plugin runs at. */
  transform_hooks?: string[] | null
  /** The models and channel types the plugin is made for, if it names them. */
  scope?: { models?: string[] | null; channelTypes?: number[] | null } | null
  configSchema?: RequestPluginConfigSchema
  /** Requests the plugin offers for trying it out. */
  samples?: RequestPluginSample[] | null
  /** The settings a channel of the plugin takes, kept in its settings. */
  channelConfig?: RequestPluginConfigSchema | null
  /** The format of a channel key line, shown on the key field. */
  keyHint?: LocalizedTextValue | null
  /** The plugin checks typed-in keys when the channel is saved. */
  checkKey?: boolean
  /** The plugin can query the channel balance. */
  balance?: boolean
  /** The client paths of the plugin's relay routes. */
  relayPaths?: string[] | null
  /** A channel of the plugin can serve the Responses WebSocket. */
  responsesWebsocket?: boolean
}

/** The plugins a channel can be bound to: request processing alone is not one. */
export async function getTaskPluginOptions(): Promise<TaskPluginOption[]> {
  const response = await api.get<{
    success: boolean
    data: TaskPluginOption[]
  }>('/api/task_plugin_options')
  return (requireServerSuccess(response.data).data ?? []).filter(
    (option) => option.task !== false || option.provider === true
  )
}

export type CodexUsageResponse = {
  success: boolean
  message?: string
  upstream_status?: number
  data?: Record<string, unknown>
}

export type CodexResetCreditsResponse = CodexUsageResponse

export type CodexUsageResetResponse = CodexUsageResponse

export type CodexCredentialRefreshResponse = {
  success: boolean
  message?: string
  data?: {
    expires_at?: string
    last_refresh?: string
    account_id?: string
    email?: string
    channel_id?: number
    channel_type?: number
    channel_name?: string
  }
}

// ============================================================================
// Base Channel CRUD Operations
// ============================================================================

/**
 * Get paginated list of channels
 */
export async function getChannels(
  params: GetChannelsParams = {}
): Promise<GetChannelsResponse> {
  const res = await api.get('/api/channel', { params })
  return res.data
}

/**
 * Search channels with filters
 */
export async function searchChannels(
  params: SearchChannelsParams
): Promise<SearchChannelsResponse> {
  const res = await api.get('/api/channel/search', { params })
  return res.data
}

/**
 * Get single channel by ID
 */
export async function getChannel(id: number): Promise<GetChannelResponse> {
  const res = await api.get(`/api/channel/${id}`)
  return res.data
}

/**
 * Get channel operations summary for administrators
 */
export async function getChannelOps(
  autoBan?: boolean
): Promise<ChannelOpsResponse> {
  const res = await api.get(
    '/api/channel/ops',
    channelActionConfig({
      params: autoBan === undefined ? undefined : { auto_ban: autoBan },
    })
  )
  return res.data
}

export async function getChannelDefaultBaseURLs(): Promise<
  Partial<Record<number, string>>
> {
  const response = await api.get<{
    success: boolean
    data: Partial<Record<number, string>>
  }>('/api/channel/default_base_urls')
  return requireServerSuccess(response.data).data
}

/**
 * Create new channel(s)
 * Supports single, batch, and multi-key modes
 */
export async function createChannel(
  data: AddChannelRequest
): Promise<{ success: boolean; message?: string; data?: { ids?: number[] } }> {
  const res = await api.post('/api/channel', data, channelActionConfig())
  return res.data
}

/**
 * Update existing channel
 */
export async function updateChannel(
  id: number,
  data: ChannelUpdateRequest
): Promise<{ success: boolean; message?: string; data?: Channel }> {
  const res = await api.put(
    '/api/channel/',
    { id, ...data },
    channelActionConfig({ singleUseAuthorization: data.key_mode === 'append' })
  )
  return res.data
}

/**
 * Update channel enabled/disabled status.
 */
export async function updateChannelStatus(
  id: number,
  status: number
): Promise<{ success: boolean; message?: string; data?: boolean }> {
  const res = await api.post(
    `/api/channel/${id}/status`,
    { status },
    channelActionConfig()
  )
  return res.data
}

/**
 * Batch update channel enabled/disabled status.
 */
export async function batchUpdateChannelStatus(
  ids: number[],
  status: number
): Promise<{ success: boolean; message?: string; data?: number }> {
  const res = await api.post(
    '/api/channel/status/batch',
    { ids, status },
    channelActionConfig()
  )
  return res.data
}

/**
 * Delete single channel
 */
export async function deleteChannel(
  id: number
): Promise<{ success: boolean; message?: string }> {
  const res = await api.delete(`/api/channel/${id}`, channelActionConfig())
  return res.data
}

/**
 * Batch delete channels
 */
export async function batchDeleteChannels(
  data: BatchDeleteParams
): Promise<{ success: boolean; message?: string; data?: number }> {
  const res = await api.post('/api/channel/batch', data, channelActionConfig())
  return res.data
}

/**
 * Batch set tag for channels
 */
export async function batchSetChannelTag(
  data: BatchSetTagParams
): Promise<{ success: boolean; message?: string; data?: number }> {
  const res = await api.post(
    '/api/channel/batch/tag',
    data,
    channelActionConfig()
  )
  return res.data
}

// ============================================================================
// Channel Operations
// ============================================================================

/**
 * Test channel connectivity
 */
export async function testChannel(
  id: number,
  params?: {
    model?: string
    endpoint_type?: string
    stream?: boolean
    key_index?: number
  }
): Promise<ChannelTestResponse> {
  const res = await api.get(
    `/api/channel/test/${id}`,
    channelActionConfig({ params })
  )
  return res.data
}

/**
 * Update channel balance
 */
export async function updateChannelBalance(
  id: number
): Promise<ChannelBalanceResponse> {
  const res = await api.get(
    `/api/channel/update_balance/${id}`,
    channelActionConfig()
  )
  return res.data
}

/**
 * Fetch available models from upstream provider
 */
export async function fetchUpstreamModels(
  id: number
): Promise<FetchModelsResponse> {
  const res = await api.get(
    `/api/channel/fetch_models/${id}`,
    channelActionConfig()
  )
  return res.data
}

/**
 * Copy/clone a channel
 */
export async function copyChannel(
  id: number,
  params: CopyChannelParams = {}
): Promise<CopyChannelResponse> {
  const res = await api.post(
    `/api/channel/copy/${id}`,
    null,
    channelActionConfig({ params })
  )
  return res.data
}

/**
 * Fix channel abilities
 */
export async function fixChannelAbilities(): Promise<{
  success: boolean
  message?: string
  data?: { success: number; fails: number }
}> {
  const res = await api.post(
    '/api/channel/fix',
    undefined,
    channelActionConfig()
  )
  return res.data
}

/**
 * Delete all disabled channels
 */
export async function deleteDisabledChannels(): Promise<{
  success: boolean
  message?: string
  data?: number
}> {
  const res = await api.delete('/api/channel/disabled', channelActionConfig())
  return res.data
}

/**
 * Get channel key (requires 2FA verification)
 */
export async function getChannelKey(
  id: number,
  proofToken: string,
  signal?: AbortSignal
): Promise<{ success: boolean; message?: string; data?: { key: string } }> {
  const res = await api.post(
    `/api/channel/${id}/key`,
    undefined,
    channelActionConfig({
      headers: { 'X-Security-Proof': proofToken },
      signal,
    })
  )
  return res.data
}

// ============================================================================
// Codex Channel Operations
// ============================================================================

export async function refreshCodexCredential(
  channelId: number
): Promise<CodexCredentialRefreshResponse> {
  const res = await api.post(
    `/api/channel/${channelId}/codex/refresh`,
    {},
    channelActionConfig()
  )
  return res.data
}

export async function getCodexUsage(
  channelId: number
): Promise<CodexUsageResponse> {
  const res = await api.get(
    `/api/channel/${channelId}/codex/usage`,
    channelActionConfig({ disableDuplicate: true })
  )
  return res.data
}

export async function getCodexResetCredits(
  channelId: number
): Promise<CodexResetCreditsResponse> {
  const res = await api.get(
    `/api/channel/${channelId}/codex/usage/reset-credits`,
    channelActionConfig({ disableDuplicate: true })
  )
  return res.data
}

export async function resetCodexUsage(
  channelId: number
): Promise<CodexUsageResetResponse> {
  const res = await api.post(
    `/api/channel/${channelId}/codex/usage/reset`,
    {},
    channelActionConfig({ disableDuplicate: true })
  )
  return res.data
}

// ============================================================================
// Plugin Login
// ============================================================================

/**
 * keyIndex null adds a credential; an index logs in again on that line. `add`
 * keeps the account of a single-key channel and adds the new one next to it.
 */
export async function startPluginLogin(
  channelId: number,
  keyIndex: number | null,
  add = false
): Promise<{ flow: string; step: PluginLoginStep }> {
  const res = await api.post<{
    success: boolean
    data: { flow: string; step: PluginLoginStep }
  }>(
    `/api/channel/${channelId}/plugin_login`,
    add ? { add: true } : { key_index: keyIndex },
    channelActionConfig()
  )
  return requireServerSuccess(res.data).data
}

export async function continuePluginLogin(
  channelId: number,
  flow: string,
  input: PluginLoginInput
): Promise<PluginLoginStep> {
  const res = await api.post<{
    success: boolean
    data: { step: PluginLoginStep }
  }>(
    `/api/channel/${channelId}/plugin_login/continue`,
    { flow, input },
    channelActionConfig()
  )
  return requireServerSuccess(res.data).data.step
}

/** params holds every query parameter the vendor redirected back with. */
export async function submitPluginLoginCallback(
  plugin: string,
  params: Record<string, string>
): Promise<{ channel_id: number; step: PluginLoginStep }> {
  const res = await api.post<{
    success: boolean
    data: { channel_id: number; step: PluginLoginStep }
  }>(
    '/api/channel/plugin_login/callback',
    { plugin, params },
    channelActionConfig()
  )
  return requireServerSuccess(res.data).data
}

export async function getPluginCredentials(
  channelId: number
): Promise<PluginCredentialSummary[]> {
  const res = await api.get<{
    success: boolean
    data: { items: PluginCredentialSummary[] | null }
  }>(`/api/channel/${channelId}/plugin_credentials`, channelActionConfig())
  return requireServerSuccess(res.data).data.items ?? []
}

// ============================================================================
// Multi-Key Management
// ============================================================================

/**
 * Manage multi-key channel operations
 */
export async function manageMultiKeys(
  params: MultiKeyManageParams
): Promise<MultiKeyStatusResponse | { success: boolean; message?: string }> {
  const write =
    params.action !== 'get_key_status' &&
    params.action !== 'fetch_key_models' &&
    !(
      (params.action === 'deduplicate_keys' ||
        params.action === 'sync_key_models') &&
      !params.apply
    )
  const res = await api.post(
    '/api/channel/multi_key/manage',
    params,
    channelActionConfig({ singleUseAuthorization: write })
  )
  return res.data
}

/**
 * Set or clear the budget of one stable key allocation. Both the allocation
 * revision and the budget account version are sent: the first guards the entry,
 * the second guards the account counters.
 */
export async function setMultiKeyQuota(params: {
  channelId: number
  keyEntryId: string
  expectedRev: number
  expectedConfigVersion: number
  enabled: boolean
  limitQuota: number
}): Promise<{
  success: boolean
  message?: string
  data?: ChannelBudgetView
}> {
  return manageMultiKeys({
    channel_id: params.channelId,
    action: 'set_key_quota',
    key_entry_id: params.keyEntryId,
    expected_rev: params.expectedRev,
    expected_config_version: params.expectedConfigVersion,
    enabled: params.enabled,
    limit_quota: params.limitQuota,
  }) as Promise<{
    success: boolean
    message?: string
    data?: ChannelBudgetView
  }>
}

/**
 * Reset only the used amount of one key allocation's current period. The
 * operation ID makes a retry idempotent and is generated by the caller.
 */
export async function resetMultiKeyQuota(params: {
  channelId: number
  keyEntryId: string
  expectedRev: number
  expectedConfigVersion: number
  operationId: string
}): Promise<{
  success: boolean
  message?: string
  data?: ChannelBudgetView
}> {
  return manageMultiKeys({
    channel_id: params.channelId,
    action: 'reset_key_quota',
    key_entry_id: params.keyEntryId,
    expected_rev: params.expectedRev,
    expected_config_version: params.expectedConfigVersion,
    operation_id: params.operationId,
  }) as Promise<{
    success: boolean
    message?: string
    data?: ChannelBudgetView
  }>
}

/**
 * Preview (apply=false) or commit (apply=true) an explicit dedup. The server
 * revalidates the current configuration and skips entries with live work.
 */
export async function deduplicateMultiKeys(
  channelId: number,
  apply: boolean,
  expectedRevision?: number,
  previewToken?: string
): Promise<{ success: boolean; message?: string; data?: MultiKeyDedupResult }> {
  return manageMultiKeys({
    channel_id: channelId,
    action: 'deduplicate_keys',
    apply,
    expected_revision: expectedRevision,
    preview_token: previewToken,
  }) as Promise<{
    success: boolean
    message?: string
    data?: MultiKeyDedupResult
  }>
}

/**
 * Get key status for multi-key channel
 */
export async function getMultiKeyStatus(
  channelId: number,
  page = 1,
  pageSize = 50,
  status?: number
): Promise<MultiKeyStatusResponse> {
  return manageMultiKeys({
    channel_id: channelId,
    action: 'get_key_status',
    page,
    page_size: pageSize,
    status,
  }) as Promise<MultiKeyStatusResponse>
}

/**
 * Enable a specific key in multi-key channel
 */
export async function enableMultiKey(
  channelId: number,
  keyIndex: number,
  keyEntryId?: string,
  expectedRev?: number
): Promise<{ success: boolean; message?: string }> {
  return manageMultiKeys({
    channel_id: channelId,
    action: 'enable_key',
    key_index: keyEntryId ? undefined : keyIndex,
    key_entry_id: keyEntryId,
    expected_rev: expectedRev,
  }) as Promise<{ success: boolean; message?: string }>
}

/**
 * Disable a specific key in multi-key channel
 */
export async function disableMultiKey(
  channelId: number,
  keyIndex: number,
  keyEntryId?: string,
  expectedRev?: number
): Promise<{ success: boolean; message?: string }> {
  return manageMultiKeys({
    channel_id: channelId,
    action: 'disable_key',
    key_index: keyEntryId ? undefined : keyIndex,
    key_entry_id: keyEntryId,
    expected_rev: expectedRev,
  }) as Promise<{ success: boolean; message?: string }>
}

/**
 * Delete a specific key in multi-key channel. A stable id is required once the
 * channel holds duplicate credentials, so the server never guesses the entry.
 */
export async function deleteMultiKey(
  channelId: number,
  keyIndex: number,
  keyEntryId?: string,
  expectedRev?: number
): Promise<{ success: boolean; message?: string }> {
  return manageMultiKeys({
    channel_id: channelId,
    action: 'delete_key',
    key_index: keyEntryId ? undefined : keyIndex,
    key_entry_id: keyEntryId,
    expected_rev: expectedRev,
  }) as Promise<{ success: boolean; message?: string }>
}

/**
 * Enable all keys in multi-key channel
 */
export async function enableAllMultiKeys(
  channelId: number,
  expectedRevision?: number
): Promise<{ success: boolean; message?: string }> {
  return manageMultiKeys({
    channel_id: channelId,
    action: 'enable_all_keys',
    expected_revision: expectedRevision,
  }) as Promise<{ success: boolean; message?: string }>
}

/**
 * Disable all keys in multi-key channel
 */
export async function disableAllMultiKeys(
  channelId: number,
  expectedRevision?: number
): Promise<{ success: boolean; message?: string }> {
  return manageMultiKeys({
    channel_id: channelId,
    action: 'disable_all_keys',
    expected_revision: expectedRevision,
  }) as Promise<{ success: boolean; message?: string }>
}

/**
 * Delete all disabled keys in multi-key channel
 */
export async function deleteDisabledMultiKeys(
  channelId: number,
  expectedRevision?: number
): Promise<{ success: boolean; message?: string; data?: number }> {
  return manageMultiKeys({
    channel_id: channelId,
    action: 'delete_disabled_keys',
    expected_revision: expectedRevision,
  }) as Promise<{ success: boolean; message?: string; data?: number }>
}

/**
 * Restrict a key to a subset of the channel models. An explicit empty
 * allowlist denies all models; only the legacy request treats it as all.
 */
export async function setMultiKeyModels(
  channelId: number,
  keyIndex: number,
  models: string[],
  keyEntryId?: string,
  expectedRev?: number,
  scopeMode?: 'all' | 'allowlist'
): Promise<{ success: boolean; message?: string }> {
  return manageMultiKeys({
    channel_id: channelId,
    action: 'set_key_models',
    key_index: keyEntryId ? undefined : keyIndex,
    key_entry_id: keyEntryId,
    expected_rev: expectedRev,
    scope_mode: scopeMode,
    models,
  }) as Promise<{ success: boolean; message?: string }>
}

/**
 * Fetch the upstream model list with one key only. Read-only.
 */
export async function fetchMultiKeyModels(
  channelId: number,
  keyIndex: number,
  keyEntryId?: string,
  expectedRev?: number
): Promise<{
  success: boolean
  message?: string
  data?: { models: string[] }
}> {
  return manageMultiKeys({
    channel_id: channelId,
    action: 'fetch_key_models',
    key_index: keyEntryId ? undefined : keyIndex,
    key_entry_id: keyEntryId,
    expected_rev: expectedRev,
  }) as Promise<{
    success: boolean
    message?: string
    data?: { models: string[] }
  }>
}

/**
 * Fetch the upstream models of every enabled key. Preview by default; `apply`
 * writes the channel models and each successful key's model scope.
 */
export async function syncMultiKeyModels(
  channelId: number,
  apply: boolean,
  expectedRevision?: number
): Promise<{
  success: boolean
  message?: string
  data?: MultiKeyModelSyncResult
}> {
  return manageMultiKeys({
    channel_id: channelId,
    action: 'sync_key_models',
    apply,
    expected_revision: expectedRevision,
  }) as Promise<{
    success: boolean
    message?: string
    data?: MultiKeyModelSyncResult
  }>
}

/**
 * Bind a key exclusively to a user. User ID 0 unbinds it again.
 */
export async function setMultiKeyUser(
  channelId: number,
  keyIndex: number,
  userId: number,
  keyEntryId?: string,
  expectedRev?: number
): Promise<{ success: boolean; message?: string }> {
  return manageMultiKeys({
    channel_id: channelId,
    action: 'set_key_user',
    key_index: keyEntryId ? undefined : keyIndex,
    key_entry_id: keyEntryId,
    expected_rev: expectedRev,
    user_id: userId,
  }) as Promise<{ success: boolean; message?: string }>
}

// ============================================================================
// Local channel budget (independent of the upstream balance)
// ============================================================================

/**
 * Read the channel's local billed budget. It is a separate, local limit; the
 * upstream balance remains available only where the provider reports it.
 */
export async function getChannelBudget(
  channelId: number,
  signal?: AbortSignal
): Promise<ChannelBudgetView | null> {
  const res = await api.get<{ success: boolean; data?: ChannelBudgetView }>(
    `/api/channel/${channelId}/quota`,
    channelActionConfig({ signal, disableDuplicate: true })
  )
  return requireServerSuccess(res.data).data ?? null
}

export async function getChannelBudgets(
  ids: number[],
  signal?: AbortSignal
): Promise<ChannelBudgetView[]> {
  // A page of grouped tags can contain more than 200 underlying channels.
  // Keep requests batched, within the server's maximum number of IDs.
  const requests: Promise<ChannelBudgetView[]>[] = []
  const batchSize = 200
  for (let start = 0; start < ids.length; start += batchSize) {
    const batch = ids.slice(start, start + batchSize)
    const request = api
      .get<{
        success: boolean
        data?: { items: (ChannelBudgetView | null)[] }
      }>(
        '/api/channel/quota',
        channelActionConfig({
          signal,
          params: { ids: batch.join(',') },
          disableDuplicate: true,
        })
      )
      .then(({ data }) =>
        (requireServerSuccess(data).data?.items ?? []).filter(
          (view) => view != null
        )
      )
    requests.push(request)
  }
  return (await Promise.all(requests)).flat()
}

/**
 * Set the local budget limit. The expected configuration version makes a stale
 * console reload instead of overwriting another administrator's change.
 */
export async function setChannelBudget(
  channelId: number,
  params: SetChannelBudgetParams
): Promise<{
  success: boolean
  message?: string
  data?: ChannelBudgetView
}> {
  const res = await api.put(
    `/api/channel/${channelId}/quota`,
    params,
    channelActionConfig({ singleUseAuthorization: true })
  )
  return res.data
}

/**
 * Reset only the used amount of the channel's current budget period. The
 * operation ID makes a retry idempotent; live holds and lifetime use survive.
 */
export async function resetChannelBudget(
  channelId: number,
  params: ResetChannelBudgetParams
): Promise<{
  success: boolean
  message?: string
  data?: ChannelBudgetView
}> {
  const res = await api.post(
    `/api/channel/${channelId}/quota/reset`,
    params,
    channelActionConfig({ singleUseAuthorization: true })
  )
  return res.data
}

// ============================================================================
// Tag Operations
// ============================================================================

/**
 * Enable all channels with a specific tag
 */
export async function enableTagChannels(
  tag: string
): Promise<{ success: boolean; message?: string }> {
  const res = await api.post(
    '/api/channel/tag/enabled',
    { tag },
    channelActionConfig()
  )
  return res.data
}

/**
 * Disable all channels with a specific tag
 */
export async function disableTagChannels(
  tag: string
): Promise<{ success: boolean; message?: string }> {
  const res = await api.post(
    '/api/channel/tag/disabled',
    { tag },
    channelActionConfig()
  )
  return res.data
}

/**
 * Edit all channels with a specific tag
 */
export async function editTagChannels(
  params: TagOperationParams
): Promise<{ success: boolean; message?: string }> {
  const res = await api.put('/api/channel/tag', params, channelActionConfig())
  return res.data
}

/**
 * Get models for a specific tag
 */
export async function getTagModels(
  tag: string
): Promise<{ success: boolean; message?: string; data?: string }> {
  const res = await api.get('/api/channel/tag/models', { params: { tag } })
  return res.data
}

// ============================================================================
// Utility Functions
// ============================================================================

/**
 * Fetch models from the current unsaved channel form configuration.
 */
export async function fetchModels(data: {
  /** Omitted while editing to keep the saved channel address. */
  base_url?: string
  type: number
  key?: string
  channel_id?: number
  task_plugin_key?: string
  advanced_custom?: string
  header_override?: string
  proxy?: string
}): Promise<FetchModelsResponse> {
  const res = await api.post(
    '/api/channel/fetch_models',
    data,
    channelActionConfig()
  )
  return res.data
}

/**
 * Delete an Ollama model from a channel
 */
export async function deleteOllamaModel(params: {
  channel_id: number
  model_name: string
}): Promise<{ success: boolean; message?: string }> {
  const res = await api.delete(
    '/api/channel/ollama/delete',
    channelActionConfig({ data: params })
  )
  return res.data
}

/**
 * Test all enabled channels
 */
export async function testAllChannels(): Promise<{
  success: boolean
  message?: string
}> {
  const res = await api.get('/api/channel/test', channelActionConfig())
  return res.data
}

/**
 * Update balance for all enabled channels. The server queues the update as a
 * system task and answers with its id.
 */
export async function updateAllChannelsBalance(): Promise<{
  success: boolean
  message?: string
  /** `started` is false when a run was already queued or running; `task_id` is that run. */
  data?: { started?: boolean; task_id?: string; status?: string } | null
}> {
  const res = await api.get(
    '/api/channel/update_balance',
    channelActionConfig()
  )
  return res.data
}

/** Progress of an update of all balances. */
export type ChannelBalanceTask = {
  task_id: string
  status: 'pending' | 'running' | 'succeeded' | 'failed'
  state?: { total?: number; processed?: number; progress?: number } | null
  error?: string
}

/**
 * Get the progress of an update of all balances, which any server node can
 * answer.
 */
export async function getChannelBalanceTask(taskId: string): Promise<{
  success: boolean
  message?: string
  data?: ChannelBalanceTask | null
}> {
  const res = await api.get(
    `/api/channel/update_balance/task/${encodeURIComponent(taskId)}`,
    channelActionConfig()
  )
  return res.data
}

/**
 * Get all available models
 */
export async function getAllModels(): Promise<{
  success: boolean
  message?: string
  data?: Array<{ id: string; [key: string]: unknown }>
}> {
  const res = await api.get('/api/channel/models')
  return res.data
}

/**
 * Get all enabled models
 */
export async function getEnabledModels(): Promise<{
  success: boolean
  message?: string
  data?: string[]
}> {
  const res = await api.get('/api/channel/models_enabled')
  return res.data
}

// ============================================================================
// Ollama Utilities
// ============================================================================

/**
 * Check Ollama version for a given channel
 */
export async function getOllamaVersion(
  channelId: number
): Promise<{ success: boolean; message?: string; data?: { version: string } }> {
  const res = await api.get(`/api/channel/ollama/version/${channelId}`)
  return res.data
}

// ============================================================================
// Group Management
// ============================================================================

/**
 * Get all available groups (re-exported from users API for convenience)
 */
export const getGroups = getUserGroups

// ============================================================================
// Prefill Groups (Model Groups)
// ============================================================================

/**
 * Get prefill groups for quick model selection
 */
export async function getPrefillGroups(
  type: 'model' | 'group' = 'model'
): Promise<{
  success: boolean
  message?: string
  data?: Array<{ id: number; name: string; items: string | string[] }>
}> {
  const res = await api.get('/api/prefill_group', { params: { type } })
  return res.data
}
