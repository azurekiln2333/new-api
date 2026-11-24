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
import { useContext, type ReactNode } from 'react'
import { useTranslation } from 'react-i18next'

import { QuotaDetailsPopover } from '@/components/quota-details-popover'
import { toIntlLocale } from '@/i18n/languages'
import { formatQuotaWithCurrency } from '@/lib/currency'

import { ChannelBudgetContext } from './channel-budget-context'
import { useChannels } from './channels-provider'
import type { ChannelBudgetView } from '../types'

/** Reads the single visible-page request; never performs a per-row fetch. */
export function ChannelBudgetCell(props: {
  channelId: number
  aggregate?: boolean
}) {
  const { t } = useTranslation()
  const { sensitiveVisible } = useChannels()
  const { byChannel, loading } = useContext(ChannelBudgetContext)
  const view = byChannel[props.channelId]
  if (props.aggregate) return <span className='text-muted-foreground'>-</span>
  if (!sensitiveVisible) return <span>••••</span>
  if (loading) {
    return <span className='text-muted-foreground'>{t('Loading...')}</span>
  }
  if (!view) {
    return <span className='text-muted-foreground'>{t('Unavailable')}</span>
  }
  if (!view.ledger_enabled) {
    return <span className='text-muted-foreground'>{t('Not enabled')}</span>
  }
  return <ChannelBudgetViewDisplay view={view} variant='remaining' />
}

/**
 * The card quota region. The local budget fills it only when a local budget is
 * known and enabled. A known-but-disabled budget keeps the upstream balance,
 * which is then the authoritative figure. A loading read or a missing/failed
 * projection is *not* known to be disabled, so it renders an explicit
 * Loading/Unavailable state instead of passing the upstream balance off as the
 * local budget.
 */
export function ChannelQuotaCell(props: {
  channelId: number
  aggregate?: boolean
  fallback: ReactNode
}) {
  const { t } = useTranslation()
  const { sensitiveVisible } = useChannels()
  const { byChannel, loading } = useContext(ChannelBudgetContext)
  const view = byChannel[props.channelId]
  if (props.aggregate) {
    return <span className='text-muted-foreground'>-</span>
  }
  if (!sensitiveVisible) return <span>••••</span>
  if (loading) {
    return <span className='text-muted-foreground'>{t('Loading...')}</span>
  }
  if (!view) {
    return <span className='text-muted-foreground'>{t('Unavailable')}</span>
  }
  if (!view.ledger_enabled) {
    return <span className='text-muted-foreground'>{t('Not enabled')}</span>
  }
  // Known and enabled: the local budget is the authoritative figure.
  if (view.enabled) {
    return <ChannelBudgetViewDisplay view={view} variant='quota' />
  }
  // Known and explicitly disabled: the upstream balance is authoritative.
  return props.fallback
}

function ChannelBudgetViewDisplay({
  view,
  variant,
}: {
  view: ChannelBudgetView
  variant: 'remaining' | 'quota'
}) {
  const { t, i18n } = useTranslation()
  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language)
  const format = (value: number | null | undefined) =>
    value == null
      ? '-'
      : formatQuotaWithCurrency(value, {
          digitsLarge: 2,
          digitsSmall: 4,
          locale,
        })
  const remaining = view.enabled ? format(view.remaining_quota) : t('Unlimited')
  const limit = view.enabled ? format(view.limit_quota) : t('Unlimited')
  return (
    <QuotaDetailsPopover
      title={t('Local Budget')}
      triggerLabel={t('Local Budget')}
      details={[
        {
          label: t('Limit'),
          value: view.enabled ? format(view.limit_quota) : t('Unlimited'),
        },
        { label: t('Used this period'), value: format(view.period_used_quota) },
        { label: t('Reserved'), value: format(view.reserved_quota) },
        { label: t('Remaining'), value: remaining },
        { label: t('Lifetime used'), value: format(view.lifetime_used_quota) },
      ]}
      description={t(
        'Wallet or subscription charges and API token quotas still apply.'
      )}
    >
      <span className={view.exhausted ? 'text-destructive' : 'tabular-nums'}>
        {variant === 'quota' ? `${remaining} / ${limit}` : remaining}
      </span>
    </QuotaDetailsPopover>
  )
}
