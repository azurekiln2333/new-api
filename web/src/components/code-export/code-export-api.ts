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

/**
 * Shared export client for invitation and redemption codes.
 *
 * One shape covers the three selections the console offers, so a single dialog and
 * a single row-menu action serve both resources:
 *   - `ids`              an explicit selection, from the current page or across pages
 *   - `operationId`      one historical create batch (actor-bound on the server)
 *   - `keyword`/`status` the active filters, exported across every page
 */
export type CodeExportScope = 'invitation' | 'redemption'

export type CodeExportFormat = 'txt' | 'md'

export type CodeExportRequest = {
  scope: CodeExportScope
  ids?: number[]
  operationId?: string
  keyword?: string
  status?: string
  format?: CodeExportFormat
  includeName?: boolean
  includeQuota?: boolean
}

export type CodeBatchSummary = {
  operation_id: string
  operation_type: string
  actor_user_id: number
  created_at: number
  creation_batch_id: string
  count: number
}

function exportPath(scope: CodeExportScope): string {
  return scope === 'invitation'
    ? '/api/invitation/export'
    : '/api/redemption/export'
}

function batchPath(scope: CodeExportScope): string {
  return scope === 'invitation'
    ? '/api/invitation/batches'
    : '/api/redemption/batches'
}

/**
 * Downloads a code export. The server returns a file body on success and a JSON
 * `{success:false}` envelope when the selection is rejected, so the envelope is
 * detected before anything is saved — otherwise a rejected export would persist a
 * file containing the error JSON.
 */
export async function downloadCodeExport(
  request: CodeExportRequest
): Promise<void> {
  const params = new URLSearchParams()
  if (request.ids?.length) {
    params.set('ids', request.ids.join(','))
  }
  if (request.operationId) {
    params.set('operation_id', request.operationId)
  }
  if (request.keyword) {
    params.set('keyword', request.keyword)
  }
  if (request.status) {
    params.set('status', request.status)
  }
  params.set('format', request.format ?? 'txt')
  if (request.includeName === false) {
    params.set('include_name', 'false')
  }
  if (request.includeQuota === false) {
    params.set('include_quota', 'false')
  }

  const response = await api.get(
    `${exportPath(request.scope)}?${params.toString()}`,
    {
      responseType: 'blob',
    }
  )

  const contentType = String(response.headers?.['content-type'] ?? '')
  if (contentType.includes('application/json')) {
    const text = await (response.data as Blob).text()
    let message = ''
    try {
      const parsed = JSON.parse(text) as { message?: string }
      message = parsed.message ?? ''
    } catch {
      message = ''
    }
    throw new Error(message || 'Failed to export codes')
  }

  const blob = response.data as Blob
  const url = URL.createObjectURL(blob)
  const link = document.createElement('a')
  link.href = url
  link.download = `${request.scope}-codes-${Date.now()}.${request.format ?? 'txt'}`
  document.body.append(link)
  link.click()
  link.remove()
  URL.revokeObjectURL(url)
}

/** Lists the caller's committed create batches, newest first. */
export async function listCodeBatches(
  scope: CodeExportScope,
  page = 1,
  pageSize = 20
): Promise<{ items: CodeBatchSummary[]; total: number }> {
  const response = await api.get<{
    success: boolean
    message: string
    data: { items: CodeBatchSummary[]; total: number }
  }>(`${batchPath(scope)}?p=${page}&page_size=${pageSize}`)
  if (!response.data.success) {
    throw new Error(response.data.message || 'Failed to load create batches')
  }
  return response.data.data
}
