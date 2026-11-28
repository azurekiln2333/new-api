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

import type {
  RankingModelUsageResponse,
  RankingPeriod,
  RankingsResponse,
} from './types'

export async function getRankings(
  period: RankingPeriod
): Promise<RankingsResponse> {
  const res = await api.get<RankingsResponse>('/api/rankings', {
    params: { period },
  })
  return res.data
}

/**
 * Per-model usage plus the user ranking for the model usage drawer.
 *
 * `model` is sent as a query param (not a path segment) so model names that
 * contain a slash still resolve. The server masks other users' identities
 * before responding, so the payload is already viewer-safe.
 */
export async function getRankingModelUsage(
  modelName: string,
  period: RankingPeriod,
  signal?: AbortSignal
): Promise<RankingModelUsageResponse> {
  const res = await api.get<RankingModelUsageResponse>(
    '/api/rankings/model-usage',
    { params: { model: modelName, period }, signal }
  )
  return res.data
}
