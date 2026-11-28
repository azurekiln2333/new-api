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
import { VChart } from '@visactor/react-vchart'
import { Users, Loader2 } from 'lucide-react'
import {
  useEffect,
  useMemo,
  useState,
  useRef,
  useCallback,
  type ReactNode,
} from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { IconBadge } from '@/components/ui/icon-badge'
import { Skeleton } from '@/components/ui/skeleton'
import { Tabs, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { StatisticsCoverageNotice } from '@/lib/statistics-coverage'
import { useTheme } from '@/context/theme-provider'
import { getUserQuotaDataByUsers } from '@/features/dashboard/api'
import { ChartMetricToggle } from '@/features/dashboard/components/ui/chart-metric-toggle'
import {
  TIME_GRANULARITY_OPTIONS,
  TIME_RANGE_PRESETS,
} from '@/features/dashboard/constants'
import { useChartMetric } from '@/features/dashboard/hooks/use-chart-metric'
import {
  processUserChartData,
  getGranularityRangeReason,
  nearestAllowedGranularity,
  saveGranularity,
} from '@/features/dashboard/lib'
import type {
  ProcessedUserChartData,
  QuotaDataItem,
  UserChartsFilters,
} from '@/features/dashboard/types'
import { toIntlLocale } from '@/i18n/languages'
import { ROLE } from '@/lib/roles'
import { requireServerSuccess } from '@/lib/server-error-message'
import { getRollingDateRange, type TimeGranularity } from '@/lib/time'
import { VCHART_OPTION } from '@/lib/vchart'
import { useAuthStore } from '@/stores/auth-store'

let themeManagerPromise: Promise<
  (typeof import('@visactor/vchart'))['ThemeManager']
> | null = null

const USER_CHARTS: {
  value: string
  labelKey: string
  specKey: keyof ProcessedUserChartData
}[] = [
  {
    value: 'rank',
    labelKey: 'User Consumption Ranking',
    specKey: 'spec_user_rank',
  },
  {
    value: 'trend',
    labelKey: 'User Consumption Trend',
    specKey: 'spec_user_trend',
  },
]

const TOP_USER_LIMIT_OPTIONS = [5, 10, 20, 50]
const EMPTY_USER_CHART_DATA: QuotaDataItem[] = []

interface UserChartsProps {
  filters: UserChartsFilters
  onFiltersChange: (filters: UserChartsFilters) => void
}

export function UserCharts(props: UserChartsProps) {
  const { t, i18n } = useTranslation()
  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language)
  const { resolvedTheme } = useTheme()
  const user = useAuthStore((state) => state.auth.user)
  const isAdmin = Boolean(user?.id && user.role >= ROLE.ADMIN)
  const [themeReady, setThemeReady] = useState(false)
  const themeManagerRef = useRef<
    (typeof import('@visactor/vchart'))['ThemeManager'] | null
  >(null)

  // The selection is owned by the dashboard parent so it persists across
  // sub-section switches; the rolling window is derived from the chosen range.
  const timeGranularity = props.filters.timeGranularity
  const selectedRange = props.filters.selectedRange
  const topUserLimit = props.filters.topUserLimit
  const onFiltersChange = props.onFiltersChange
  const [metric, setMetric] = useChartMetric()

  const timeRange = useMemo(() => {
    const { start, end } = getRollingDateRange(selectedRange)
    return {
      start_timestamp: Math.floor(start.getTime() / 1000),
      end_timestamp: Math.floor(end.getTime() / 1000),
    }
  }, [selectedRange])

  // The range and the granularity are independent: changing the range keeps
  // the granularity unless that combination cannot be bucketed.
  const handleRangeChange = useCallback(
    (days: number) => {
      const nextGranularity = nearestAllowedGranularity(timeGranularity, days)
      if (nextGranularity !== timeGranularity) {
        saveGranularity(nextGranularity)
        const label =
          TIME_GRANULARITY_OPTIONS.find(
            (option) => option.value === nextGranularity
          )?.label ?? nextGranularity
        toast.info(
          t('Switched to {{granularity}} granularity', {
            granularity: t(label),
          })
        )
      }
      onFiltersChange({
        ...props.filters,
        selectedRange: days,
        timeGranularity: nextGranularity,
      })
    },
    [onFiltersChange, props.filters, t, timeGranularity]
  )

  const handleGranularityChange = useCallback(
    (g: TimeGranularity) => {
      saveGranularity(g)
      onFiltersChange({ ...props.filters, timeGranularity: g })
    },
    [onFiltersChange, props.filters]
  )

  const handleTopUserLimitChange = useCallback(
    (limit: number) => {
      onFiltersChange({ ...props.filters, topUserLimit: limit })
    },
    [onFiltersChange, props.filters]
  )

  useEffect(() => {
    const updateTheme = async () => {
      setThemeReady(false)
      if (!themeManagerPromise) {
        themeManagerPromise = import('@visactor/vchart').then(
          (m) => m.ThemeManager
        )
      }
      const ThemeManager = await themeManagerPromise
      themeManagerRef.current = ThemeManager
      ThemeManager.setCurrentTheme(resolvedTheme === 'dark' ? 'dark' : 'light')
      setThemeReady(true)
    }
    updateTheme()
  }, [resolvedTheme])

  const userQuery = useQuery({
    queryKey: [
      'dashboard',
      'user-quota',
      timeRange,
      user?.id ?? 'anon',
      user?.role ?? 0,
    ],
    queryFn: async ({ signal }) =>
      requireServerSuccess(await getUserQuotaDataByUsers(timeRange, signal)),
    enabled: isAdmin,
    staleTime: 60_000,
  })
  const isLoading = userQuery.isLoading
  const hasUnverifiedEmptyData =
    !isLoading &&
    (userQuery.data?.data.length ?? 0) === 0 &&
    userQuery.data?.coverage?.complete !== true

  const chartData = useMemo(
    () =>
      processUserChartData(
        !isAdmin || isLoading
          ? EMPTY_USER_CHART_DATA
          : (userQuery.data?.data ?? EMPTY_USER_CHART_DATA),
        timeGranularity,
        t,
        topUserLimit,
        metric,
        locale
      ),
    [
      userQuery.data,
      isAdmin,
      isLoading,
      timeGranularity,
      t,
      topUserLimit,
      metric,
      locale,
    ]
  )

  if (!isAdmin) return null

  return (
    <div className='space-y-3'>
      {!isLoading && userQuery.data && (
        <StatisticsCoverageNotice coverage={userQuery.data.coverage} />
      )}
      <div className='flex items-center gap-1.5 overflow-x-auto pb-1 sm:gap-2'>
        <Tabs
          value={String(selectedRange)}
          onValueChange={(value) => handleRangeChange(Number(value))}
          className='shrink-0'
        >
          <TabsList>
            {TIME_RANGE_PRESETS.map((preset) => (
              <TabsTrigger
                key={preset.days}
                value={String(preset.days)}
                className='px-2.5 text-xs'
              >
                {t(preset.label)}
              </TabsTrigger>
            ))}
          </TabsList>
        </Tabs>

        <Tabs
          value={timeGranularity}
          onValueChange={(value) =>
            handleGranularityChange(value as TimeGranularity)
          }
          className='shrink-0'
        >
          <TabsList>
            {TIME_GRANULARITY_OPTIONS.map((opt) => {
              const reason = getGranularityRangeReason(opt.value, selectedRange)
              return (
                <TabsTrigger
                  key={opt.value}
                  value={opt.value}
                  disabled={reason != null}
                  title={
                    reason ? t(reason.key, { days: reason.days }) : undefined
                  }
                  className='px-2.5 text-xs'
                >
                  {t(opt.label)}
                </TabsTrigger>
              )
            })}
          </TabsList>
        </Tabs>

        <Tabs
          value={String(topUserLimit)}
          onValueChange={(value) => handleTopUserLimitChange(Number(value))}
          className='shrink-0'
        >
          <TabsList>
            <span className='text-muted-foreground px-2 text-xs font-medium whitespace-nowrap'>
              {t('Top Users')}
            </span>
            {TOP_USER_LIMIT_OPTIONS.map((limit) => (
              <TabsTrigger
                key={limit}
                value={String(limit)}
                className='px-2.5 text-xs'
              >
                {t('Top {{count}}', { count: limit })}
              </TabsTrigger>
            ))}
          </TabsList>
        </Tabs>

        <ChartMetricToggle value={metric} onChange={setMetric} />

        {isLoading && (
          <Loader2 className='text-muted-foreground size-4 animate-spin' />
        )}
      </div>

      <div className='grid gap-3'>
        {USER_CHARTS.map((chart) => {
          const spec = chartData[chart.specKey]
          let content: ReactNode = null
          if (isLoading) {
            content = <Skeleton className='h-full w-full' />
          } else if (hasUnverifiedEmptyData) {
            content = (
              <div
                role='status'
                className='text-muted-foreground flex h-full items-center justify-center px-4 text-center text-sm'
              >
                {t('No verified usage data available')}
              </div>
            )
          } else if (themeReady && spec) {
            content = (
              <VChart
                key={`user-${chart.value}-${topUserLimit}-${metric}-${resolvedTheme}`}
                spec={{
                  ...spec,
                  theme: resolvedTheme === 'dark' ? 'dark' : 'light',
                  background: 'transparent',
                }}
                option={VCHART_OPTION}
              />
            )
          }

          return (
            <div
              key={chart.value}
              className='overflow-hidden rounded-lg border'
            >
              <div className='flex w-full items-center gap-2 border-b px-3 py-2 sm:px-5 sm:py-3'>
                <IconBadge tone='info' size='sm'>
                  <Users />
                </IconBadge>
                <div className='text-sm font-semibold'>{t(chart.labelKey)}</div>
              </div>

              <div className='h-[300px] p-1.5 sm:h-96 sm:p-2'>
                {content}
              </div>
            </div>
          )
        })}
      </div>
    </div>
  )
}
