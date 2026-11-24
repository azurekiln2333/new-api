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
import { useQuery } from '@tanstack/react-query'

import { requireServerSuccess } from '@/lib/server-error-message'
import { useAuthStore } from '@/stores/auth-store'

import { getRankingModelUsage, getRankings } from '../api'
import type { RankingPeriod } from '../types'

export function useRankings(period: RankingPeriod) {
  return useQuery({
    queryKey: ['rankings', period],
    queryFn: async () => requireServerSuccess(await getRankings(period)),
    staleTime: 5 * 60 * 1000,
  })
}

/**
 * Per-model usage and user ranking for the model usage drawer.
 *
 * The query key includes the viewer identity and role so a payload masked for
 * one viewer is never served to another. Only a session change clears the
 * query cache, so an in-session role downgrade must also invalidate the key —
 * otherwise an admin-scoped (fully named) response could be reused after the
 * role dropped. AbortSignal cancels the request when the drawer closes or the
 * model/period changes.
 */
export function useRankingModelUsage(
  modelName: string,
  period: RankingPeriod,
  enabled: boolean
) {
  const user = useAuthStore((state) => state.auth.user)
  const viewerKey = user?.id ?? 'anon'
  const roleKey = user?.role ?? 0

  return useQuery({
    queryKey: ['rankings', 'model-usage', modelName, period, viewerKey, roleKey],
    queryFn: async ({ signal }) => {
      const result = await getRankingModelUsage(modelName, period, signal)
      return requireServerSuccess(result)
    },
    enabled: enabled && modelName.length > 0,
    staleTime: 60 * 1000,
  })
}
