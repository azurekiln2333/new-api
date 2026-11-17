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
import { useMemo } from 'react'

import { getChannelUsageDetail, getChannelUsageSummaries } from '../api'
import { channelsQueryKeys } from '../lib/channel-actions'
import type { ChannelUsageSummary } from '../types'

const EMPTY_USAGE: Record<number, ChannelUsageSummary> = {}

/**
 * Token usage per visible channel, keyed by channel id. Ids are deduplicated
 * and sorted so the same set of rows never triggers a second request.
 */
export function useChannelUsageSummaries(ids: number[]) {
  const idsKey = [...new Set(ids)].sort((a, b) => a - b).join(',')
  const uniqueIds = useMemo(
    () => (idsKey ? idsKey.split(',').map(Number) : []),
    [idsKey]
  )

  const query = useQuery({
    queryKey: channelsQueryKeys.usageSummaries(uniqueIds),
    queryFn: ({ signal }) => getChannelUsageSummaries(uniqueIds, signal),
    enabled: uniqueIds.length > 0,
    placeholderData: (previousData) => previousData,
    refetchOnWindowFocus: false,
    meta: { errorToast: false },
  })

  const usageByChannel = useMemo(() => {
    if (!query.data) return EMPTY_USAGE
    return Object.fromEntries(
      query.data.map((item) => [item.channel_id, item])
    ) as Record<number, ChannelUsageSummary>
  }, [query.data])

  return { ...query, usageByChannel }
}

/** Per-model usage and status for one channel; idle while `channelId` is null. */
export function useChannelUsageDetail(channelId: number | null) {
  return useQuery({
    queryKey: channelsQueryKeys.usageDetail(channelId ?? 0),
    queryFn: ({ signal }) => getChannelUsageDetail(channelId ?? 0, signal),
    enabled: channelId !== null,
    refetchOnWindowFocus: false,
    meta: { errorToast: false },
  })
}
