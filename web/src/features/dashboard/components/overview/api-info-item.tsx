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
import { Zap, ExternalLink, Gauge } from 'lucide-react'
import { useTranslation } from 'react-i18next'

import { CopyButton } from '@/components/copy-button'
import { StatusBadge } from '@/components/status-badge'
import { Button } from '@/components/ui/button'
import {
  getLatencyColorClass,
  openExternalSpeedTest,
} from '@/features/dashboard/lib/api-info'
import type { ApiInfoItem, PingStatus } from '@/features/dashboard/types'
import { getBgColorClass } from '@/lib/colors'
import { cn } from '@/lib/utils'

interface ApiInfoItemProps {
  item: ApiInfoItem
  status: PingStatus
  onTest: (url: string) => void
  /** Approved CPU/memory/storage percentages; null means unknown. */
  resources?: {
    cpu_percent: number | null
    memory_percent: number | null
    storage_percent: number | null
  } | null
  /** Coarse monitoring state for this entry's instance. */
  instanceStatus?: 'healthy' | 'stale' | 'unknown' | 'disabled' | null
  /** Opens the filtered node detail for this API entry. */
  onOpenDetail?: () => void
}

export function ApiInfoItemComponent(props: ApiInfoItemProps) {
  const { t } = useTranslation()
  const item = props.item
  const status = props.status

  let failureLabel = t('Unable to measure')
  if (status.reason === 'mixed-content') {
    failureLabel = t('Unable to measure (insecure target)')
  } else if (status.reason === 'timeout') {
    failureLabel = t('Timed out')
  }

  return (
    // One card per API entry. The identity row opens the filtered node detail;
    // resources and controls sit on their own wrapping row so nothing is
    // dropped on narrow screens.
    <div className='group hover:bg-muted/40 flex flex-col gap-2 px-3 py-2.5 transition-colors sm:px-5 sm:py-3'>
      <div className='flex items-center justify-between gap-2 sm:gap-3'>
        <div className='flex min-w-0 flex-1 items-center gap-2 sm:gap-3'>
          <span
            className={cn(
              'inline-block size-2 shrink-0 rounded-full',
              getBgColorClass(item.color)
            )}
          />

          <div className='flex min-w-0 flex-1 flex-col gap-0.5'>
            <div className='flex items-baseline gap-2'>
              <Button
                type='button'
                variant='link'
                onClick={props.onOpenDetail}
                className='h-auto min-w-0 cursor-pointer justify-start truncate p-0 text-left font-mono text-sm font-semibold underline decoration-1 underline-offset-4'
              >
                {item.route}
              </Button>
              <span className='text-muted-foreground/60 hidden truncate text-xs md:inline'>
                {item.description}
              </span>
            </div>
            <span className='text-muted-foreground/40 truncate font-mono text-xs'>
              {item.url}
            </span>
          </div>
        </div>

        <div className='flex shrink-0 items-center justify-end gap-2'>
          {status.testing && (
            <StatusBadge
              label={t('Testing...')}
              variant='warning'
              className='animate-pulse'
              copyable={false}
            />
          )}
          {status.latency !== null && !status.testing && (
            <StatusBadge
              variant='success'
              label={`${status.latency}${t('ms')}`}
              className={cn(
                'font-mono font-medium',
                getLatencyColorClass(status.latency)
              )}
              copyable={false}
            />
          )}
          {status.error && (
            <StatusBadge
              label={failureLabel}
              variant='neutral'
              copyable={false}
            />
          )}
        </div>
      </div>

      <div className='flex flex-wrap items-center justify-between gap-x-3 gap-y-1 pl-4'>
        <ResourceMeters
          resources={props.resources ?? null}
          status={props.instanceStatus ?? null}
        />

        <div className='flex items-center gap-0.5'>
          <Button
            variant='ghost'
            size='sm'
            onClick={() => props.onTest(item.url)}
            disabled={status.testing}
            className='size-7 p-0'
            title={t('Test Latency')}
          >
            <Zap
              className={cn('size-3.5', status.testing && 'animate-pulse')}
            />
          </Button>

          <Button
            variant='ghost'
            size='sm'
            onClick={() => openExternalSpeedTest(item.url)}
            className='hidden size-7 p-0 sm:inline-flex'
            title={t('External Speed Test')}
          >
            <Gauge className='size-3.5' />
          </Button>

          <CopyButton
            value={item.url}
            variant='ghost'
            size='sm'
            className='size-7 p-0'
            iconClassName='size-3.5'
            tooltip={t('Copy URL')}
            aria-label={t('Copy URL')}
          />

          <Button
            variant='ghost'
            size='sm'
            className='hidden size-7 p-0 sm:inline-flex'
            title={t('Open in New Tab')}
            render={<a href={item.url} target='_blank' rel='noreferrer' />}
          >
            <ExternalLink className='size-3.5' />
          </Button>
        </div>
      </div>
    </div>
  )
}

const RESOURCE_KEYS = ['cpu', 'memory', 'storage'] as const

const RESOURCE_LABELS: Record<(typeof RESOURCE_KEYS)[number], string> = {
  cpu: 'CPU',
  memory: 'Memory',
  storage: 'Storage',
}

/**
 * CPU/memory/storage meters for one API instance.
 *
 * Values are the approved public percentages; `null` renders an explicit
 * "Unknown" state so a missing sample is never shown as 0%. The coarse
 * instance state (stale/disabled) is surfaced as a short label.
 */
function ResourceMeters(props: {
  resources: {
    cpu_percent: number | null
    memory_percent: number | null
    storage_percent: number | null
  } | null
  status: 'healthy' | 'stale' | 'unknown' | 'disabled' | null
}) {
  const { t } = useTranslation()
  const values = [
    props.resources?.cpu_percent ?? null,
    props.resources?.memory_percent ?? null,
    props.resources?.storage_percent ?? null,
  ]

  let stateLabel: string | null = null
  if (props.status === 'stale') stateLabel = t('Stale')
  else if (props.status === 'disabled') stateLabel = t('Disabled')
  else if (props.status === 'unknown' || !props.resources)
    stateLabel = t('Unknown')

  return (
    // Required information, so it wraps onto its own line on narrow screens
    // instead of being hidden there.
    <div className='flex flex-wrap items-center justify-end gap-x-2 gap-y-1'>
      {RESOURCE_KEYS.map((key, index) => (
        <div key={key} className='flex items-center gap-1'>
          <span className='text-muted-foreground/60 text-[10px] font-medium tracking-wide uppercase'>
            {t(RESOURCE_LABELS[key])}
          </span>
          <span className='font-mono text-[11px] tabular-nums'>
            {values[index] === null
              ? t('Unknown')
              : `${values[index]?.toFixed(0)}%`}
          </span>
        </div>
      ))}
      {stateLabel && (
        <StatusBadge variant='neutral' label={stateLabel} copyable={false} />
      )}
    </div>
  )
}
