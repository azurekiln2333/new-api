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

import { QuotaDetailsPopover } from '@/components/quota-details-popover'
import { toIntlLocale } from '@/i18n/languages'
import { formatQuotaWithCurrency } from '@/lib/currency'

import { ChannelBudgetContext } from './channel-budget-context'
import { useChannels } from './channels-provider'

/** Reads the single visible-page request; never performs a per-row fetch. */
export function ChannelBudgetCell(props: {
  channelId: number
  aggregate?: boolean
}) {
  const { t, i18n } = useTranslation()
  const { sensitiveVisible } = useChannels()
  const { byChannel, loading } = useContext(ChannelBudgetContext)
  const view = byChannel[props.channelId]
  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language)
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
  const format = (value: number | null | undefined) =>
    value == null
      ? '-'
      : formatQuotaWithCurrency(value, {
          digitsLarge: 2,
          digitsSmall: 4,
          locale,
        })
  const remaining = view.enabled ? format(view.remaining_quota) : t('Unlimited')
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
        {remaining}
      </span>
    </QuotaDetailsPopover>
  )
}
