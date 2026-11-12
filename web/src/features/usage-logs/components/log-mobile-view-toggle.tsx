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
import { Grid2X2, Rows3 } from 'lucide-react'
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'
import {
  Tooltip,
  TooltipContent,
  TooltipTrigger,
} from '@/components/ui/tooltip'
import { cn } from '@/lib/utils'

import {
  MOBILE_LOG_VIEW_MODES,
  type MobileLogViewMode,
} from '../lib/mobile-log-view'

type LogMobileViewToggleProps = {
  value: MobileLogViewMode
  onChange: (mode: MobileLogViewMode) => void
  className?: string
}

/**
 * Segmented control that switches the mobile common-log list between the dense
 * compact rows and the expanded cards.
 */
export function LogMobileViewToggle(props: LogMobileViewToggleProps) {
  const { t } = useTranslation()

  const segments = [
    {
      value: MOBILE_LOG_VIEW_MODES.COMPACT,
      icon: Rows3,
      label: t('Compact view'),
    },
    {
      value: MOBILE_LOG_VIEW_MODES.CARD,
      icon: Grid2X2,
      label: t('Card view'),
    },
  ]

  return (
    <div
      role='group'
      aria-label={t('View mode')}
      className={cn(
        'bg-muted/60 inline-flex h-8 items-center rounded-lg border p-0.5',
        props.className
      )}
    >
      {segments.map((segment) => {
        const Icon = segment.icon
        const isActive = segment.value === props.value
        return (
          <Tooltip key={segment.value}>
            <TooltipTrigger
              render={
                <Button
                  type='button'
                  variant={isActive ? 'default' : 'ghost'}
                  size='icon-sm'
                  onClick={() => props.onChange(segment.value)}
                  aria-label={segment.label}
                  aria-pressed={isActive}
                  className='h-full w-7'
                >
                  <Icon className='size-3.5' />
                </Button>
              }
            />
            <TooltipContent side='bottom' className='text-xs'>
              {segment.label}
            </TooltipContent>
          </Tooltip>
        )
      })}
    </div>
  )
}
