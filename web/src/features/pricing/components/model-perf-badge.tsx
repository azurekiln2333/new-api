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
import { memo, useMemo } from 'react'
import { useTranslation } from 'react-i18next'

import { LearnMore } from '@/components/learn-more'
import {
  formatLatency,
  formatThroughput,
  getSuccessRateDotClass,
} from '@/features/performance-metrics/lib/format'
import type { SuccessRatePoint } from '@/features/performance-metrics/types'
import { cn } from '@/lib/utils'

export type ModelPerfBadgeData = {
  window_start?: number
  window_end?: number
  avg_latency_ms: number
  success_rate: number
  avg_tps: number
  recent_success_series?: SuccessRatePoint[]
}

export interface ModelPerfBadgeProps extends React.HTMLAttributes<HTMLDivElement> {
  perf: ModelPerfBadgeData | undefined
}

const STATUS_SAMPLE_LIMIT = 24

export const ModelPerfBadge = memo(function ModelPerfBadge(
  props: ModelPerfBadgeProps
) {
  const { t } = useTranslation()
  const hasLatency =
    Number.isFinite(props.perf?.avg_latency_ms) &&
    (props.perf?.avg_latency_ms ?? 0) > 0
  const hasThroughput =
    Number.isFinite(props.perf?.avg_tps) && (props.perf?.avg_tps ?? 0) > 0
  const latencyText = hasLatency
    ? formatLatency(props.perf?.avg_latency_ms ?? 0)
    : '—s'
  const throughputText = hasThroughput
    ? formatThroughput(props.perf?.avg_tps ?? 0).replace(' t/s', 't/s')
    : '—t/s'
  const successRate = props.perf?.success_rate
  const hasSuccessRate =
    successRate != null &&
    Number.isFinite(successRate) &&
    successRate >= 0 &&
    successRate <= 100
  const statusSamples = useMemo(
    () =>
      (props.perf?.recent_success_series ?? [])
        .filter(
          (point) =>
            Number.isFinite(point.ts) &&
            Number.isFinite(point.success_rate) &&
            point.success_rate >= 0 &&
            point.success_rate <= 100
        )
        .sort((a, b) => a.ts - b.ts)
        .slice(-STATUS_SAMPLE_LIMIT),
    [props.perf?.recent_success_series]
  )
  const statusLabel =
    statusSamples.length > 0
      ? t('Success rate')
      : t('No performance data available')

  return (
    <div
      role='group'
      aria-label={t('Performance')}
      className={cn('w-full min-w-0', props.className)}
    >
      <dl className='grid w-full min-w-0 grid-cols-[repeat(auto-fit,minmax(7rem,1fr))] gap-x-3 gap-y-2 text-xs tabular-nums'>
        <div title={t('Average latency')} className='min-w-0'>
          <dt className='text-muted-foreground text-[11px] leading-4'>
            {t('Average latency')}
          </dt>
          <dd className='mt-1 font-mono whitespace-nowrap'>{latencyText}</dd>
        </div>
        <div title={t('Throughput')} className='min-w-0'>
          <dt className='text-muted-foreground text-[11px] leading-4'>
            {t('Throughput')}
          </dt>
          <dd className='mt-1 font-mono whitespace-nowrap'>{throughputText}</dd>
        </div>
        <div className='min-w-0'>
          <dt className='text-muted-foreground flex min-w-0 items-center gap-1 text-[11px] leading-4'>
            <span>{t('Status')}</span>
            <LearnMore triggerProps={{ 'aria-label': t('Success rate') }}>
              <p>
                {t(
                  'Success rate excludes business rejections and includes the current partial hour.'
                )}
              </p>
            </LearnMore>
            {hasSuccessRate ? (
              <span className='text-foreground ml-auto shrink-0 font-mono'>
                {successRate.toFixed(2)}%
              </span>
            ) : (
              <span className='ml-auto shrink-0 font-mono'>—</span>
            )}
          </dt>
          <dd
            role='img'
            aria-label={statusLabel}
            title={statusLabel}
            className='mt-1 flex h-3 min-w-0 items-center gap-px'
          >
            {statusSamples.length > 0 ? (
              statusSamples.map((sample) => (
                <span
                  key={sample.ts}
                  aria-hidden
                  className={cn(
                    'h-full min-w-[2px] flex-1 rounded-xs',
                    getSuccessRateDotClass(sample.success_rate)
                  )}
                />
              ))
            ) : (
              <span className='text-muted-foreground font-mono'>—</span>
            )}
          </dd>
        </div>
      </dl>
      {props.children}
    </div>
  )
})
