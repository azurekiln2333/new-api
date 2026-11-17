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
import { RefreshCw, Server, Waypoints } from 'lucide-react'
import { useTranslation } from 'react-i18next'

import {
  sideDrawerContentClassName,
  sideDrawerHeaderClassName,
} from '@/components/drawer-layout'
import { ErrorState } from '@/components/error-state'
import { LoadingState } from '@/components/loading-state'
import { ProviderBadge } from '@/components/provider-badge'
import { StatusBadge } from '@/components/status-badge'
import { Button } from '@/components/ui/button'
import { IconBadge } from '@/components/ui/icon-badge'
import {
  Sheet,
  SheetContent,
  SheetDescription,
  SheetHeader,
  SheetTitle,
} from '@/components/ui/sheet'
import { getServerErrorMessage } from '@/lib/server-error-message'
import { cn } from '@/lib/utils'

import { useChannelUsageDetail } from '../../hooks/use-channel-usage'
import {
  getChannelStatusBadge,
  getChannelTypeIcon,
  getChannelTypeLabel,
} from '../../lib/channel-utils'
import type { Channel } from '../../types'
import { ChannelUsageSummary } from './channel-usage-summary'

export interface ChannelDetailsDrawerProps {
  channel: Channel | null
  open: boolean
  onOpenChange: (open: boolean) => void
}

/**
 * Channel identity under the title. It stays outside `SheetDescription`
 * because that slot renders a `<p>`, which cannot host the badges.
 */
function ChannelIdentity(props: { channel: Channel }) {
  const { t } = useTranslation()
  const statusBadge = getChannelStatusBadge(props.channel.status)

  return (
    <div className='flex min-w-0 flex-wrap items-center gap-x-2 gap-y-1'>
      <span className='font-mono text-xs tabular-nums'>
        #{props.channel.id}
      </span>
      <ProviderBadge
        iconKey={`${getChannelTypeIcon(props.channel.type)}.Color`}
        iconSize={14}
        label={t(getChannelTypeLabel(props.channel.type))}
        colorText={false}
        copyable={false}
        showDot={false}
      />
      <StatusBadge
        label={t(statusBadge.label)}
        variant={statusBadge.variant}
        size='sm'
        copyable={false}
        showDot={false}
      />
    </div>
  )
}

export function ChannelDetailsDrawer(props: ChannelDetailsDrawerProps) {
  const { t } = useTranslation()
  // A closed drawer must not fetch, so the channel id only reaches the hook
  // while the details are actually visible.
  const channelId = props.open ? (props.channel?.id ?? null) : null
  const usageQuery = useChannelUsageDetail(channelId)

  let body: React.ReactNode = null
  if (channelId !== null) {
    if (usageQuery.isPending) {
      body = <LoadingState message={t('Loading...')} />
    } else if (usageQuery.isError) {
      body = (
        <ErrorState
          icon={Waypoints}
          title={t('Failed to load channel usage')}
          description={getServerErrorMessage(usageQuery.error)}
          onRetry={() => void usageQuery.refetch()}
        />
      )
    } else if (usageQuery.data) {
      body = <ChannelUsageSummary usage={usageQuery.data} />
    }
  }

  return (
    <Sheet open={props.open} onOpenChange={props.onOpenChange}>
      <SheetContent
        side='right'
        className={sideDrawerContentClassName('sm:max-w-2xl lg:max-w-3xl')}
      >
        <SheetHeader className={sideDrawerHeaderClassName('pr-12 sm:pr-14')}>
          <div className='flex min-w-0 items-start gap-3'>
            <IconBadge tone='info' size='title'>
              <Server className='size-4' aria-hidden='true' />
            </IconBadge>
            <div className='min-w-0 flex-1 space-y-1'>
              <SheetTitle className='truncate'>
                {props.channel?.name ?? t('Channel details')}
              </SheetTitle>
              <SheetDescription className='sr-only'>
                {t('Channel details')}
              </SheetDescription>
              {props.channel ? (
                <ChannelIdentity channel={props.channel} />
              ) : null}
            </div>
            <Button
              type='button'
              variant='outline'
              size='sm'
              className='shrink-0'
              aria-label={t('Refresh')}
              disabled={channelId === null || usageQuery.isFetching}
              onClick={() => void usageQuery.refetch()}
            >
              <RefreshCw
                className={cn(
                  'size-3.5',
                  usageQuery.isFetching && 'animate-spin'
                )}
                aria-hidden='true'
              />
              <span className='hidden sm:inline'>{t('Refresh')}</span>
            </Button>
          </div>
        </SheetHeader>

        <div className='min-h-0 flex-1 overflow-y-auto overscroll-contain px-4 py-4 sm:px-6 sm:py-5'>
          {body}
        </div>
      </SheetContent>
    </Sheet>
  )
}
