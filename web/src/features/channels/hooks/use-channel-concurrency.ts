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
import { keepPreviousData, useQuery } from '@tanstack/react-query'
import { useMemo } from 'react'

import { getChannelConcurrency } from '../api'
import { channelsQueryKeys } from '../lib/channel-actions'
import type { ChannelConcurrencyEntry } from '../types'

/** Contract bound of the batch endpoint; larger sets are split, not truncated. */
export const CHANNEL_CONCURRENCY_BATCH_SIZE = 200

/** Cards and an open drawer read one live sample on this cadence. */
export const CHANNEL_CONCURRENCY_REFRESH_MS = 5000

/**
 * Live concurrency for one visible set of channels, sampled as one batched,
 * shared query: every card and the open details drawer read this result instead
 * of polling on their own. Sets larger than the endpoint's 200-ID contract are
 * split into bounded requests and merged, never silently truncated.
 *
 * The timer pauses when the tab is hidden (`refetchIntervalInBackground`) and
 * when no channel is visible, and the last sample is retained on failure so the
 * UI can present it as stale rather than replacing it with zero.
 */
export function useChannelConcurrency(ids: number[]) {
  const idsKey = [...new Set(ids)].sort((a, b) => a - b).join(',')
  const uniqueIds = useMemo(
    () => (idsKey ? idsKey.split(',').map(Number) : []),
    [idsKey]
  )

  const query = useQuery({
    queryKey: channelsQueryKeys.concurrency(uniqueIds),
    queryFn: async ({ signal }) => {
      const entries: ChannelConcurrencyEntry[] = []
      for (
        let start = 0;
        start < uniqueIds.length;
        start += CHANNEL_CONCURRENCY_BATCH_SIZE
      ) {
        const batch = uniqueIds.slice(start, start + CHANNEL_CONCURRENCY_BATCH_SIZE)
        entries.push(...(await getChannelConcurrency(batch, signal)))
      }
      return entries
    },
    enabled: uniqueIds.length > 0,
    refetchInterval: CHANNEL_CONCURRENCY_REFRESH_MS,
    // A hidden tab must not keep sampling the coordinator every 5 seconds.
    refetchIntervalInBackground: false,
    refetchOnWindowFocus: false,
    placeholderData: keepPreviousData,
    meta: { errorToast: false },
  })

  const entriesById = useMemo(() => {
    const map: Record<number, ChannelConcurrencyEntry> = {}
    for (const entry of query.data ?? []) map[entry.id] = entry
    return map
  }, [query.data])

  return {
    ...query,
    entriesById,
    /**
     * True once a sample has been received and later refreshes failed: the
     * values on screen are the last known ones, not a live reading.
     */
    stale: query.isError && query.data !== undefined,
  }
}