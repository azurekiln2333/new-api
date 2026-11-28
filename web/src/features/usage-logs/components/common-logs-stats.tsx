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
import { getRouteApi } from '@tanstack/react-router'
import { Zap } from 'lucide-react'
import { useTranslation } from 'react-i18next'

import { QuotaDetailsPopover } from '@/components/quota-details-popover'
import { Skeleton } from '@/components/ui/skeleton'
import { useMediaQuery } from '@/hooks/use-media-query'
import { toIntlLocale } from '@/i18n/languages'
import { formatLogQuota, formatNumber } from '@/lib/format'
import { requireServerSuccess } from '@/lib/server-error-message'
import { cn } from '@/lib/utils'
import { useAuthStore } from '@/stores/auth-store'

import { getLogStats, getUserLogStats } from '../api'
import { DEFAULT_LOG_STATS } from '../constants'
import { formatCacheHitRate } from '../lib/format'
import { buildLogStatsParams } from '../lib/utils'
import { useLogsViewScope, useUsageLogsContext } from './usage-logs-provider'

const route = getRouteApi('/_authenticated/usage-logs/$section')

function StatBadge(props: {
  label: string
  value: string | number
  accent: string
}) {
  return (
    <span className='border-border/60 bg-muted/25 inline-flex h-7 shrink-0 items-center gap-1 rounded-md border px-1.5 text-[10px] whitespace-nowrap shadow-xs sm:gap-2 sm:px-2.5 sm:text-xs'>
      <span
        className={cn('h-3.5 w-0.5 rounded-full max-sm:hidden', props.accent)}
      />
      <span className='text-muted-foreground'>{props.label}</span>
      <span className='text-foreground/85 font-mono font-semibold tabular-nums'>
        {props.value}
      </span>
    </span>
  )
}

export function CommonLogsStats() {
  const { t, i18n } = useTranslation()
  const isMobile = useMediaQuery('(max-width: 640px)')
  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language)
  const { isAdminView: isAdmin, userScope } = useLogsViewScope()
  const userId = useAuthStore((state) => state.auth.user?.id)
  const searchParams = route.useSearch()
  const { sensitiveVisible } = useUsageLogsContext()

  const { data: stats, isLoading } = useQuery({
    queryKey: ['usage-logs-stats', isAdmin, userScope, userId, searchParams],
    queryFn: async () => {
      const params = buildLogStatsParams({
        searchParams,
        columnFilters: [],
        isAdmin,
        userScope,
      })

      const result = isAdmin
        ? requireServerSuccess(await getLogStats(params))
        : requireServerSuccess(await getUserLogStats(params))

      return result.success
        ? result.data || DEFAULT_LOG_STATS
        : DEFAULT_LOG_STATS
    },
    placeholderData: (previousData, previousQuery) =>
      previousQuery?.queryKey[1] === isAdmin &&
      previousQuery.queryKey[2] === userScope &&
      previousQuery.queryKey[3] === userId
        ? previousData
        : undefined,
  })

  const rowClassName =
    'flex max-w-full min-w-0 flex-nowrap items-center gap-1 overflow-x-auto sm:flex-wrap sm:gap-2 sm:overflow-visible'

  if (isLoading) {
    return (
      <div
        role='group'
        aria-label={t('Usage')}
        aria-busy='true'
        className={cn(rowClassName, 'sm:flex-nowrap')}
      >
        <Skeleton className='h-7 w-24 shrink-0 rounded-md sm:w-[150px]' />
        <Skeleton className='h-7 w-14 shrink-0 rounded-md sm:w-[100px]' />
        <Skeleton className='h-7 w-16 shrink-0 rounded-md sm:w-[120px]' />
        <Skeleton className='h-7 w-16 shrink-0 rounded-md sm:w-[130px]' />
      </div>
    )
  }

  const cacheHitRate = formatCacheHitRate(
    stats?.cache_read_tokens,
    stats?.input_tokens
  )

  return (
    <div role='group' aria-label={t('Usage')} className={rowClassName}>
      <StatBadge
        label={t('Usage')}
        value={sensitiveVisible ? formatLogQuota(stats?.quota || 0) : '••••'}
        accent='bg-sky-500/70'
      />
      <StatBadge
        label={t('RPM')}
        value={stats?.rpm || 0}
        accent='bg-rose-500/65'
      />
      <StatBadge
        label={t('TPM')}
        value={formatNumber(stats?.tpm ?? 0, locale)}
        accent='bg-slate-400/70'
      />
      {isMobile ? (
        <QuotaDetailsPopover
          title={t('Cache Hit Rate')}
          triggerLabel={`${t('Cache Hit Rate')} ${cacheHitRate}`}
          className='w-auto shrink-0'
          triggerClassName='border-border/60 bg-muted/25 text-foreground/85 h-7 w-auto justify-center gap-1 rounded-md border px-1.5 py-0 text-[10px] shadow-xs'
          details={[
            { label: t('Cache Hit Rate'), value: cacheHitRate },
            {
              label: t('Cache Read'),
              value: formatNumber(stats?.cache_read_tokens ?? 0, locale),
            },
            {
              label: t('Input Tokens'),
              value: formatNumber(stats?.input_tokens ?? 0, locale),
            },
          ]}
        >
          <Zap className='size-3 text-emerald-500/70' aria-hidden='true' />
          <span className='font-mono font-semibold tabular-nums'>
            {cacheHitRate}
          </span>
        </QuotaDetailsPopover>
      ) : (
        <StatBadge
          label={t('Cache Hit Rate')}
          value={cacheHitRate}
          accent='bg-emerald-500/70'
        />
      )}
    </div>
  )
}
