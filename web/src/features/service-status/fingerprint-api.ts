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
  error?: string
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

export interface StartFingerprintPayload {
  model: string
  endpoint_type: string
  count: number
  token_budget: number
}

export async function startChannelFingerprint(
  channelId: number,
  payload: StartFingerprintPayload
) {
  const response = await api.post<{
    success: boolean
    message?: string
    data: FingerprintJob
  }>(`/api/channel/${channelId}/fingerprint`, payload)
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
  jobId: string
) {
  const response = await api.post<{
    success: boolean
    message?: string
    data: FingerprintJob
  }>(`/api/channel/${channelId}/fingerprint/${jobId}/cancel`, {})
  return requireServerSuccess(response.data).data
}