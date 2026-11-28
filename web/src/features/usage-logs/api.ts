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
import { api, getFreshAuthHeaders, type ApiRequestConfig } from '@/lib/api'
import {
  createServerError,
  requireServerSuccess,
} from '@/lib/server-error-message'

import type { UsageLog } from './data/schema'
import { buildQueryParams } from './lib/query-params'
import { parseTaskArtifactsResponse } from './lib/task-artifacts'
import type {
  GetLogsParams,
  GetLogsResponse,
  GetLogStatsParams,
  GetLogStatsResponse,
  GetMidjourneyLogsParams,
  GetTaskLogsParams,
  LogRequestContent,
  TaskArtifactsResponse,
  UserInfo,
} from './types'

// ============================================================================
// Generic API Helpers
// ============================================================================

function buildApiPath(endpoint: string, isAdmin: boolean): string {
  return isAdmin ? endpoint : `${endpoint}/self`
}

function selfLogParams<T extends GetLogStatsParams>(params: T) {
  const {
    user_scope: _userScope,
    username: _username,
    channel: _channel,
    ...selfParams
  } = params
  return selfParams
}

async function fetchLogs<T>(
  endpoint: string,
  params: T,
  isAdmin: boolean,
  signal?: AbortSignal
): Promise<GetLogsResponse> {
  const paramRecord = params as unknown as Record<string, unknown>
  const queryParams = buildQueryParams({
    p: paramRecord.p || 1,
    page_size: paramRecord.page_size || 20,
    ...params,
  })
  const path = buildApiPath(endpoint, isAdmin)
  const res = await api.get(
    `${path}?${queryParams}`,
    signal ? { signal } : undefined
  )
  return res.data
}

async function fetchLogStats<T>(
  endpoint: string,
  params: T,
  isAdmin: boolean,
  signal?: AbortSignal
): Promise<GetLogStatsResponse> {
  const queryParams = buildQueryParams(
    params as unknown as Record<string, unknown>
  )
  const path = buildApiPath(endpoint, isAdmin)
  const res = await api.get<GetLogStatsResponse>(
    `${path}/stat?${queryParams}`,
    signal ? { signal } : undefined
  )
  return res.data
}

// ============================================================================
// Common Log APIs
// ============================================================================

export const getAllLogs = (
  params: GetLogsParams = {},
  signal?: AbortSignal
) => fetchLogs('/api/log', params, true, signal)


export const getUserLogs = (
  params: Omit<GetLogsParams, 'username' | 'channel' | 'user_scope'> = {},
  signal?: AbortSignal
) => fetchLogs('/api/log', selfLogParams(params), false, signal)

export const getLogStats = (
  params: GetLogStatsParams = {},
  signal?: AbortSignal
) => fetchLogStats('/api/log', params, true, signal)

export const getUserLogStats = (
  params: Omit<
    GetLogStatsParams,
    'username' | 'channel' | 'user_scope'
  > = {},
  signal?: AbortSignal
) => fetchLogStats('/api/log', selfLogParams(params), false, signal)


/**
 * Stream the logs matching params, newest first. The server sends one JSON
 * log per line and ends with an API response object; a stream that ends
 * without a successful one is a failed export. Resolves with the log count.
 */
export async function exportLogs(
  params: GetLogsParams,
  isAdmin: boolean,
  options: { signal: AbortSignal; onLog: (log: UsageLog) => void }
): Promise<number> {
  const queryParams = buildQueryParams(
    (isAdmin ? params : selfLogParams(params)) as unknown as Record<
      string,
      unknown
    >
  )
  const response = await fetch(
    `${buildApiPath('/api/log', isAdmin)}/export?${queryParams}`,
    {
      credentials: 'include',
      headers: await getFreshAuthHeaders(),
      signal: options.signal,
    }
  )
  if (!response.ok || !response.body) {
    const body: unknown = await response.json().catch(() => null)
    throw createServerError(body ?? { status: response.status })
  }

  const reader = response.body.getReader()
  const decoder = new TextDecoder()
  let buffer = ''
  let result: { success?: unknown; data?: { count?: number } } | null = null
  for (;;) {
    const { done, value } = await reader.read()
    buffer += done ? decoder.decode() : decoder.decode(value, { stream: true })
    const lines = buffer.split('\n')
    buffer = done ? '' : (lines.pop() ?? '')
    for (const line of lines) {
      if (!line.trim()) continue
      let parsed
      try {
        parsed = JSON.parse(line)
      } catch {
        throw createServerError(null)
      }
      if (typeof parsed.success === 'boolean') result = parsed
      else options.onLog(parsed as UsageLog)
    }
    if (done) break
  }
  if (result?.success !== true) {
    throw createServerError(result)
  }
  return result.data?.count ?? 0
}

export async function getUserInfo(
  userId: number
): Promise<{ success: boolean; message?: string; data?: UserInfo }> {
  const res = await api.get(`/api/user/${userId}`)
  return res.data
}

// ============================================================================
// Original request content API (administrator only)
// ============================================================================

const logRequestContentRequestConfig = {
  skipBusinessError: true,
  skipErrorHandler: true,
  disableDuplicate: true,
} satisfies ApiRequestConfig

/**
 * Read the original prompt/messages behind one log reference. The server
 * returns nothing for a reference without a surviving associated log, so a
 * guessed ref never exposes content. Call only while an authorized detail
 * dialog is open; the list never preloads content.
 */
export async function getLogRequestContent(
  ref: string,
  signal?: AbortSignal
): Promise<LogRequestContent> {
  const response = await api.get<{
    success: boolean
    message?: string
    data?: LogRequestContent
  }>(`/api/log/request/${encodeURIComponent(ref)}`, {
    ...logRequestContentRequestConfig,
    signal,
  })
  const detail = requireServerSuccess(response.data).data
  if (!detail || detail.ref !== ref || detail.payload == null) {
    throw new Error('Original request content is no longer available')
  }
  return detail
}

// ============================================================================
// MjProxy (Drawing) Logs API
// ============================================================================

export const getAllMidjourneyLogs = (
  params: GetMidjourneyLogsParams,
  signal?: AbortSignal
) => fetchLogs('/api/mj', params, true, signal)

export const getUserMidjourneyLogs = (
  params: GetMidjourneyLogsParams,
  signal?: AbortSignal
) => fetchLogs('/api/mj', params, false, signal)

// ============================================================================
// Task Logs API
// ============================================================================

export const getAllTaskLogs = (
  params: GetTaskLogsParams,
  signal?: AbortSignal
) => fetchLogs('/api/task', params, true, signal)

export const getUserTaskLogs = (
  params: GetTaskLogsParams,
  signal?: AbortSignal
) => fetchLogs('/api/task', params, false, signal)

const taskArtifactRequestConfig = {
  skipBusinessError: true,
  skipErrorHandler: true,
} satisfies ApiRequestConfig

export async function getTaskArtifacts(taskId: string) {
  const response = await api.get<TaskArtifactsResponse>(
    `/api/task/${encodeURIComponent(taskId)}/artifacts`,
    taskArtifactRequestConfig
  )
  return parseTaskArtifactsResponse(response.data)
}
