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
import { useCallback, useMemo, useState } from 'react'

import { getInstanceResourceSummary } from '../api'
import type { InstanceResourceSummary } from '../types'

const API_ORDER_STORAGE_KEY = 'dashboard_overview_api_order'

function readSavedOrder(): number[] {
  if (typeof window === 'undefined') return []
  try {
    const raw = window.localStorage.getItem(API_ORDER_STORAGE_KEY)
    if (!raw) return []
    const parsed = JSON.parse(raw)
    return Array.isArray(parsed) ? parsed.filter((v) => typeof v === 'number') : []
  } catch {
    return []
  }
}

function saveOrder(ids: number[]) {
  if (typeof window === 'undefined') return
  window.localStorage.setItem(API_ORDER_STORAGE_KEY, JSON.stringify(ids))
}

/**
 * CPU/memory/storage summary for the configured API instances.
 *
 * Every signed-in user sees these cards; the payload is already viewer-safe.
 * Ordering is the user's own preference persisted locally, applied on top of
 * whatever order the server returns, so reordering never changes the stable
 * instance key.
 */
export function useInstanceResourceSummary() {  const query = useQuery({
    queryKey: ['dashboard', 'overview', 'instance-resources'],
    queryFn: async ({ signal }) => getInstanceResourceSummary(signal),
    staleTime: 30 * 1000,
    refetchInterval: 30 * 1000,
  })

  const [savedOrder, setSavedOrder] = useState<number[]>(() => readSavedOrder())
  const instances = useMemo<InstanceResourceSummary[]>(() => {
    const list = query.data?.data.instances ?? []
    if (savedOrder.length === 0) return list
    const rank = new Map(savedOrder.map((id, index) => [id, index]))
    return [...list].sort((a, b) => {
      const ai = rank.get(apiIdFromKey(a.key)) ?? Number.MAX_SAFE_INTEGER
      const bi = rank.get(apiIdFromKey(b.key)) ?? Number.MAX_SAFE_INTEGER
      if (ai !== bi) return ai - bi
      return a.key.localeCompare(b.key)
    })
  }, [query.data, savedOrder])

  const moveInstance = useCallback(
    (key: string, direction: -1 | 1) => {
      const ids = instances.map((instance) => apiIdFromKey(instance.key))
      const from = instances.findIndex((instance) => instance.key === key)
      const to = from + direction
      if (from < 0 || to < 0 || to >= ids.length) return
      const next = [...ids]
      const [moved] = next.splice(from, 1)
      next.splice(to, 0, moved)
      setSavedOrder(next)
      saveOrder(next)
    },
    [instances]
  )

  return {
    instances,
    loading: query.isLoading,
    error: query.isError,
    moveInstance,
  }
}

function apiIdFromKey(key: string): number {
  const parsed = Number.parseInt(key.replace(/^api-/, ''), 10)
  return Number.isFinite(parsed) ? parsed : Number.MAX_SAFE_INTEGER
}

export { apiIdFromKey }
