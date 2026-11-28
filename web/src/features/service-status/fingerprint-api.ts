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
import type { KeyStatus } from '@/features/channels/types'
import { api } from '@/lib/api'
import { requireServerSuccess } from '@/lib/server-error-message'

export interface FingerprintProbe {
  id: string
  state: 'queued' | 'running' | 'complete' | 'failed' | 'cancelled'
  expected_count: number
  parsed_count: number
  prompt_tokens: number
  output_tokens: number
  max_tokens: number
  /** Actual challenge text sent for this probe. */
  prompt?: string
  /** True when the retained prompt was cut at the memory cap. */
  prompt_truncated?: boolean
  /** Actual model reply for this probe. */
  output?: string
  /** True when the retained reply was cut at the memory cap. */
  output_truncated?: boolean
  error?: string
  attempt?: number
  started_at?: number
  finished_at?: number
  timeout_seconds?: number
  retry_allowed?: boolean
}

export interface FingerprintCandidate {
  Model: string
  DisplayName: string
  Family: string
  FamilyName: string
  Score: number
  Probability: number | null
}

export interface FingerprintAnalysis {
  Decision: string
  Prediction: string
  PredictionName: string
  Family: string
  FamilyName: string
  Results: FingerprintCandidate[] | null
  Probability: number | null
  UsedOutputs: number
  Reason: string
  BankVersion: string
}

export interface FingerprintJob {
  id: string
  revision?: number
  channel_id: number
  channel_name: string
  model: string
  endpoint: string
  state: 'running' | 'completed' | 'inconclusive' | 'failed' | 'cancelled'
  probes: FingerprintProbe[]
  analysis?: FingerprintAnalysis
  error?: string
  request_budget: number
  requests_used: number
  token_budget: number
  tokens_used: number
  detector_version: string
  bank_sha256: string
  detector_sha256: string
  attribution: string
  limitation: string
  started_at: number
  finished_at?: number
}

export interface FingerprintContext {
  model: string
  endpoint_type?: string
  count: number
  key_entry_id?: string
  expected_rev?: number
  key_index?: number
}

export interface FingerprintPreview extends FingerprintContext {
  preview_id: string
  expires_at: number
  channel_id: number
  probes: { id: string; prompt: string; expected_count: number }[]
}

export interface StartFingerprintPayload extends FingerprintContext {
  preview_id: string
}

export interface RetryFingerprintPayload extends StartFingerprintPayload {
  probe_id: string
  expected_attempt: number
}

export interface FingerprintChannel {
  id: number
  type: number
  models: string[]
  auto_models?: string[]
  is_multi_key: boolean
}

interface FingerprintOptions extends FingerprintChannel {
  revision?: number
  keys: KeyStatus[] | null
  page: number
  total_pages: number
}

/** Read authoritative model scope and masked identities, never credentials. */
export async function getFingerprintChannel(
  channelId: number,
  signal?: AbortSignal
): Promise<FingerprintChannel> {
  const response = await api.get<{
    success: boolean
    data: FingerprintOptions
  }>(`/api/channel/${channelId}/fingerprint/options`, { signal })
  const channel = requireServerSuccess(response.data).data
  if (!channel || channel.id !== channelId) {
    throw new Error('Channel does not exist')
  }
  return {
    id: channel.id,
    type: channel.type,
    models: channel.models,
    auto_models: channel.auto_models,
    is_multi_key: channel.is_multi_key,
  }
}

/** Each page resolves aliases through the authoritative Key capability rule. */
export async function getFingerprintKeys(
  channelId: number,
  page: number,
  signal?: AbortSignal
) {
  const response = await api.get<{
    success: boolean
    data: FingerprintOptions
  }>(`/api/channel/${channelId}/fingerprint/options`, {
    params: { p: page, page_size: 50 },
    signal,
  })
  const data = requireServerSuccess(response.data).data
  if (!data || data.id !== channelId) {
    throw new Error('Channel key configuration changed, reload and retry')
  }
  return {
    page: data.page,
    total_pages: data.total_pages,
    revision: data.revision,
    keys: (data.keys ?? []).map(
      (key): KeyStatus => ({
        id: key.id,
        rev: key.rev,
        index: key.index,
        status: key.status,
        key_preview: key.key_preview,
        scope_mode: key.scope_mode,
        models: key.models,
      })
    ),
  }
}

/** Preview generates server-held prompts only; it does not dispatch a probe. */
export async function previewChannelFingerprint(
  channelId: number,
  payload: FingerprintContext,
  signal?: AbortSignal
): Promise<FingerprintPreview> {
  const response = await api.post<{
    success: boolean
    message?: string
    data: FingerprintPreview
  }>(`/api/channel/${channelId}/fingerprint/preview`, payload, { signal })
  return requireServerSuccess(response.data).data
}

export async function startChannelFingerprint(
  channelId: number,
  payload: StartFingerprintPayload,
  signal?: AbortSignal
) {
  const response = await api.post<{
    success: boolean
    message?: string
    data: FingerprintJob
  }>(`/api/channel/${channelId}/fingerprint`, payload, { signal })
  return requireServerSuccess(response.data).data
}

export async function retryChannelFingerprintProbe(
  channelId: number,
  jobId: string,
  payload: RetryFingerprintPayload,
  signal?: AbortSignal
): Promise<FingerprintJob> {
  const response = await api.post<{
    success: boolean
    message?: string
    data: FingerprintJob
  }>(`/api/channel/${channelId}/fingerprint/${jobId}/retry`, payload, { signal })
  return requireServerSuccess(response.data).data
}

export async function getChannelFingerprint(
  channelId: number,
  jobId: string,
  signal?: AbortSignal
) {
  const response = await api.get<{
    success: boolean
    message?: string
    data: FingerprintJob
  }>(`/api/channel/${channelId}/fingerprint/${jobId}`, { signal })
  return requireServerSuccess(response.data).data
}

export async function cancelChannelFingerprint(
  channelId: number,
  jobId: string,
  signal?: AbortSignal,
  expectedRevision = 1
) {
  const response = await api.post<{
    success: boolean
    message?: string
    data: FingerprintJob
  }>(
    `/api/channel/${channelId}/fingerprint/${jobId}/cancel`,
    { expected_revision: expectedRevision },
    { signal }
  )
  return requireServerSuccess(response.data).data
}
