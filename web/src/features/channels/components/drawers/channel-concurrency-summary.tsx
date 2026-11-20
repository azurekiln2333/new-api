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
import { useContext } from 'react'
import { useTranslation } from 'react-i18next'

import { StatusBadge } from '@/components/status-badge'
import { toIntlLocale } from '@/i18n/languages'
import { formatNumber, formatTimestampToDate } from '@/lib/format'

import {
  ChannelConcurrencyContext,
  useChannelConcurrencyReading,
} from '../channel-concurrency-context'

/**
 * Live concurrency in the details drawer. It reads the same shared sample the
 * cards use, so opening a visible channel's details issues no extra request and
 * shows the identical number.
 */
export function ChannelConcurrencySummary(props: { channelId: number }) {
  const { t, i18n } = useTranslation()
  const context = useContext(ChannelConcurrencyContext)
  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language)
  const reading = useChannelConcurrencyReading(props.channelId)

  const unavailable = (
    <StatusBadge
      label={t('Unavailable')}
      variant='neutral'
      size='sm'
      copyable={false}
      showDot={false}
    />
  )

  let currentValue: React.ReactNode
  if (!reading) {
    // Only the first sample of this set may be unknown-but-loading. Once the
    // refresh has settled, a missing sample means it could not be read, so it
    // must say unavailable instead of waiting forever.
    currentValue = context.isPending ? (
      <span className='text-muted-foreground' aria-busy='true'>
        …
      </span>
    ) : (
      unavailable
    )
  } else if (reading.current === null) {
    currentValue = unavailable
  } else {
    currentValue = (
      <span className='font-mono text-sm tabular-nums'>
        {formatNumber(reading.current, locale)}
        <span className='text-muted-foreground'>
          {' / '}
          {reading.limit === null
            ? '∞'
            : formatNumber(reading.limit, locale)}
        </span>
      </span>
    )
  }

  let limitValue = '-'
  if (reading) {
    limitValue =
      reading.limit === null
        ? t('No limit')
        : formatNumber(reading.limit, locale)
  }

  let scopeValue = '-'
  if (reading) {
    scopeValue = reading.scope === 'cluster' ? t('Cluster') : t('Instance')
  }

  return (
    <section className='space-y-3' aria-label={t('Concurrency')}>
      <h3 className='text-muted-foreground text-xs font-semibold tracking-wider uppercase'>
        {t('Concurrency')}
      </h3>
      <dl className='divide-border divide-y rounded-lg border text-sm'>
        <div className='flex items-center justify-between gap-3 px-3 py-2'>
          <dt className='text-muted-foreground'>{t('Current requests')}</dt>
          <dd data-concurrency-current>{currentValue}</dd>
        </div>
        <div className='flex items-center justify-between gap-3 px-3 py-2'>
          <dt className='text-muted-foreground'>{t('Concurrency limit')}</dt>
          <dd className='font-mono tabular-nums'>{limitValue}</dd>
        </div>
        <div className='flex items-center justify-between gap-3 px-3 py-2'>
          <dt className='text-muted-foreground'>{t('Scope')}</dt>
          <dd>{scopeValue}</dd>
        </div>
        <div className='flex items-center justify-between gap-3 px-3 py-2'>
          <dt className='text-muted-foreground'>{t('Last updated')}</dt>
          <dd className='text-muted-foreground tabular-nums'>
            {reading
              ? formatTimestampToDate(reading.sampleTime, 'milliseconds')
              : '-'}
          </dd>
        </div>
        {reading?.stale ? (
          <div className='px-3 py-2'>
            <dd className='text-muted-foreground text-xs'>
              {t('Last known sample; the latest refresh failed')}
            </dd>
          </div>
        ) : null}
      </dl>
    </section>
  )
}