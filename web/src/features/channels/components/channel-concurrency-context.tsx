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
/* eslint-disable react-refresh/only-export-components */
import { createContext, useContext } from 'react'
import { useTranslation } from 'react-i18next'

import { StatusBadge } from '@/components/status-badge'
import {
  Tooltip,
  TooltipContent,
  TooltipProvider,
  TooltipTrigger,
} from '@/components/ui/tooltip'
import { toIntlLocale } from '@/i18n/languages'
import { formatNumber, formatTimestampToDate } from '@/lib/format'

import type { ChannelConcurrencyEntry } from '../types'

export type ChannelConcurrencyContextValue = {
  entriesById: Record<number, ChannelConcurrencyEntry>
  /** True while the first sample of this visible set is still loading. */
  isPending: boolean
  /** True when a refresh failed after a sample was already shown. */
  stale: boolean
}

const EMPTY: ChannelConcurrencyContextValue = {
  entriesById: {},
  isPending: false,
  stale: false,
}

export const ChannelConcurrencyContext =
  createContext<ChannelConcurrencyContextValue>(EMPTY)

/** Current count against the configured cap, or null when unrepresentable. */
export type ConcurrencyReading = {
  current: number | null
  limit: number | null
  scope: ChannelConcurrencyEntry['scope']
  sampleTime: number
  /** The sample belongs to a previous refresh that has since failed. */
  stale: boolean
}

/** Live reading for one channel, or null when this set has not sampled it. */
export function useChannelConcurrencyReading(
  channelId: number | null
): ConcurrencyReading | null {
  const context = useContext(ChannelConcurrencyContext)
  if (channelId === null) return null
  const entry = context.entriesById[channelId]
  if (!entry) return null
  return {
    current: entry.current,
    limit: entry.limit,
    scope: entry.scope,
    sampleTime: entry.sample_time,
    stale: context.stale,
  }
}

/**
 * `current / cap` for one channel. An unlimited channel shows its live count
 * against an infinite cap instead of a fabricated number, an aggregate row
 * shows nothing because it owns no channel, and an unavailable sample shows as
 * unavailable rather than as zero.
 */
export function ChannelConcurrencyCell(props: {
  channelId: number | null
  /** Tag aggregate rows have no channel of their own to sample. */
  aggregate?: boolean
}) {
  const { t, i18n } = useTranslation()
  const context = useContext(ChannelConcurrencyContext)
  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language)
  // Hooks stay unconditional; an aggregate row simply owns no channel to look
  // up and falls back to the dash below.
  const reading = useChannelConcurrencyReading(
    props.aggregate ? null : props.channelId
  )

  if (props.aggregate || props.channelId === null) {
    return <span className='text-muted-foreground'>-</span>
  }

  if (!reading || reading.current === null) {
    if (!reading && context.isPending) {
      return (
        <span className='text-muted-foreground' aria-busy='true'>
          …
        </span>
      )
    }
    return (
      <StatusBadge
        label={t('Unavailable')}
        variant='neutral'
        size='sm'
        copyable={false}
        showDot={false}
      />
    )
  }

  const current = formatNumber(reading.current, locale)
  const label =
    reading.limit === null
      ? `${current} / ∞`
      : `${current}/${formatNumber(reading.limit, locale)}`
  // A stale sample must not look live: it changes colour and gains visible text.
  let variant: 'warning' | 'neutral' | 'info' = 'info'
  if (reading.stale) {
    variant = 'warning'
  } else if (reading.limit === null) {
    variant = 'neutral'
  }

  return (
    <span className='inline-flex min-w-0 flex-wrap items-center gap-1.5'>
      <TooltipProvider delay={100}>
        <Tooltip>
          <TooltipTrigger
            render={
              <StatusBadge
                label={label}
                variant={variant}
                size='sm'
                copyable={false}
                showDot={false}
                className='font-mono tabular-nums'
              />
            }
          />
          <TooltipContent side='top' className='text-left'>
            {[
              `${t('Current requests')}: ${current}`,
              reading.limit === null
                ? `${t('Concurrency limit')}: ${t('No limit')}`
                : `${t('Concurrency limit')}: ${formatNumber(reading.limit, locale)}`,
              `${t('Scope')}: ${reading.scope === 'cluster' ? t('Cluster') : t('Instance')}`,
              `${t('Last updated')}: ${formatTimestampToDate(reading.sampleTime, 'milliseconds')}`,
            ].map((line) => (
              <p key={line}>{line}</p>
            ))}
            {reading.stale ? (
              <p>{t('Last known sample; the latest refresh failed')}</p>
            ) : null}
          </TooltipContent>
        </Tooltip>
      </TooltipProvider>
      {/* A stale sample stays readable as text, not only on hover. */}
      {reading.stale ? (
        <StatusBadge
          label={t('Refresh failed')}
          variant='warning'
          type='text'
          size='sm'
          copyable={false}
          showDot={false}
          data-concurrency-stale
        />
      ) : null}
    </span>
  )
}