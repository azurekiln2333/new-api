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
import { createContext, useContext } from 'react'
import { useTranslation } from 'react-i18next'

import {
  Tooltip,
  TooltipContent,
  TooltipProvider,
  TooltipTrigger,
} from '@/components/ui/tooltip'
import { toIntlLocale } from '@/i18n/languages'
import {
  formatCompactNumber,
  formatNumber,
  formatTokens,
} from '@/lib/format'
import { cn } from '@/lib/utils'

import type { ChannelUsageSummary, StatisticsCoverage } from '../types'
import { ChannelRowActionsLayoutContext } from './channel-row-actions-context'

// eslint-disable-next-line react-refresh/only-export-components
export interface ChannelUsageContextValue {
  usageByChannel: Record<number, ChannelUsageSummary>
  coverage?: StatisticsCoverage
}

export const ChannelUsageContext = createContext<ChannelUsageContextValue>({
  usageByChannel: {},
})

type ChannelUsedTokensProps = {
  channelId: number
  /**
   * Shows the readable abbreviation alone, keeping the card metric on one
   * line. The exact count remains in the tooltip, and the table view keeps the
   * exact value visible inline. Defaults to the surrounding row layout.
   */
  compact?: boolean
  /** Forces the tooltip open; used by tests, which have no pointer geometry. */
  tooltipOpen?: boolean
}

/**
 * Total tokens with the exact count, its localized abbreviation in
 * parentheses, and the request split on hover. The value is plain text rather
 * than a badge, so no extra frame surrounds it, and it is focusable so the
 * tooltip is reachable without a pointer.
 */
export function ChannelUsedTokens(props: ChannelUsedTokensProps) {
  const { t, i18n } = useTranslation()
  const usageContext = useContext(ChannelUsageContext)
  const usage = usageContext.usageByChannel[props.channelId]
  const layout = useContext(ChannelRowActionsLayoutContext)
  const isCompact = props.compact ?? layout === 'card'
  if (!usage) {
    const missingValue =
      usageContext.coverage?.complete === true ? '-' : '—'
    return <span className='text-muted-foreground'>{missingValue}</span>
  }

  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language)
  const exact = formatTokens(usage.total_tokens, locale)
  const compact = formatCompactNumber(usage.total_tokens, locale)
  // A small count has no shorter form; repeating it would only add noise.
  let visible = exact
  if (isCompact) {
    visible = compact !== '-' && compact !== exact ? compact : exact
  }
  const details = [
    `${t('Total tokens')}: ${exact}`,
    `${t('Input tokens')}: ${formatNumber(usage.prompt_tokens, locale)}`,
    `${t('Output tokens')}: ${formatNumber(usage.completion_tokens, locale)}`,
    `${t('Requests')}: ${formatNumber(usage.request_count, locale)}`,
  ].join('\n')

  return (
    <TooltipProvider delay={100}>
      <Tooltip defaultOpen={props.tooltipOpen}>
        <TooltipTrigger
          render={
            <span
              tabIndex={0}
              className={cn(
                'cursor-help font-mono tabular-nums',
                isCompact
                  ? 'block max-w-full truncate'
                  : 'inline-block max-w-full break-words whitespace-normal'
              )}
            />
          }
        >
          {visible}
        </TooltipTrigger>
        <TooltipContent side='top' className='text-left whitespace-pre-line'>
          {details}
        </TooltipContent>
      </Tooltip>
    </TooltipProvider>
  )
}
