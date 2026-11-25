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
import { Zap, Gauge } from 'lucide-react'
import { useTranslation } from 'react-i18next'

import { CopyButton } from '@/components/copy-button'
import { ResourceUsage } from '@/components/resource-usage'
import { StatusBadge } from '@/components/status-badge'
import { Button } from '@/components/ui/button'
import {
  Card,
  CardContent,
  CardDescription,
  CardFooter,
  CardHeader,
  CardTitle,
} from '@/components/ui/card'
import {
  getLatencyColorClass,
  openExternalSpeedTest,
} from '@/features/dashboard/lib/api-info'
import type {
  ApiInfoItem,
  InstanceResourceStatus,
  InstanceResourceUsage,
  PingStatus,
} from '@/features/dashboard/types'
import { getBgColorClass } from '@/lib/colors'
import { cn } from '@/lib/utils'

interface ApiInfoItemProps {
  item: ApiInfoItem
  status: PingStatus
  onTest: (url: string) => void
  /** Approved CPU/memory/storage percentages; null means unknown. */
  resources?: InstanceResourceUsage | null
  /** Coarse monitoring state for this entry's instance. */
  instanceStatus?: InstanceResourceStatus | null
  /** Opens the filtered node detail for this API entry. */
  onOpenDetail: () => void
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

  let stateLabel = t('Unknown')
  let stateVariant: 'success' | 'warning' | 'neutral' = 'neutral'
  if (props.instanceStatus === 'healthy') {
    stateLabel = t('Online')
    stateVariant = 'success'
  } else if (props.instanceStatus === 'stale') {
    stateLabel = t('Offline')
    stateVariant = 'warning'
  } else if (props.instanceStatus === 'disabled') {
    stateLabel = t('Disabled')
  }

  return (
    <Card
      role='article'
      aria-label={item.route}
      size='sm'
      className='relative min-w-0 transition-colors hover:bg-muted/40'
    >
      {/* One native button covers the card; controls are sibling buttons above
          it, so copy/test activation never opens the detail dialog. */}
      <Button
        type='button'
        variant='ghost'
        className='absolute inset-0 size-full rounded-xl bg-transparent p-0 hover:bg-transparent dark:hover:bg-transparent'
        aria-label={item.route}
        aria-haspopup='dialog'
        onClick={props.onOpenDetail}
      />
      <CardHeader className='pointer-events-none'>
        <div className='flex items-start justify-between gap-3'>
          <div className='flex min-w-0 items-center gap-2'>
            <span
              className={cn(
                'size-2 shrink-0 rounded-full',
                getBgColorClass(item.color)
              )}
              aria-hidden='true'
            />
            <CardTitle className='min-w-0 font-mono break-words'>
              {item.route}
            </CardTitle>
          </div>
          <StatusBadge
            variant={stateVariant}
            label={stateLabel}
            copyable={false}
          />
        </div>
        <CardDescription>{item.description}</CardDescription>
      </CardHeader>
      <CardContent className='pointer-events-none'>
        <p className='text-muted-foreground font-mono text-xs break-all'>
          {item.url}
        </p>
      </CardContent>
      <CardFooter className='pointer-events-none flex-wrap justify-between gap-3'>
        <ResourceMeters resources={props.resources ?? null} />
        <div className='relative flex flex-wrap items-center gap-1'>
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
          <div className='pointer-events-auto flex items-center gap-0.5'>
            <Button
              type='button'
              variant='ghost'
              size='icon-sm'
              onClick={() => props.onTest(item.url)}
              disabled={status.testing}
              title={t('Test Latency')}
              aria-label={t('Test Latency')}
            >
              <Zap
                className={cn('size-3.5', status.testing && 'animate-pulse')}
                aria-hidden='true'
              />
            </Button>
            <Button
              type='button'
              variant='ghost'
              size='icon-sm'
              onClick={() => openExternalSpeedTest(item.url)}
              title={t('External Speed Test')}
              aria-label={t('External Speed Test')}
            >
              <Gauge className='size-3.5' aria-hidden='true' />
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
          </div>
        </div>
      </CardFooter>
    </Card>
  )
}

/** Labels wrap on narrow cards; known and unknown values share one display. */
function ResourceMeters(props: { resources: InstanceResourceUsage | null }) {
  const { t } = useTranslation()

  return (
    <div className='flex flex-wrap items-center gap-x-4 gap-y-2'>
      {[
        { label: t('CPU'), value: props.resources?.cpu_percent },
        { label: t('Memory'), value: props.resources?.memory_percent },
        { label: t('Storage'), value: props.resources?.storage_percent },
      ].map((resource) => (
        <div
          key={resource.label}
          role='group'
          aria-label={resource.label}
          className='flex flex-col gap-1'
        >
          <span className='text-muted-foreground text-xs'>{resource.label}</span>
          <ResourceUsage value={resource.value} unknownLabel={t('Unknown')} />
        </div>
      ))}
    </div>
  )
}
