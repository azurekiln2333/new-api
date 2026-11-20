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
import {
  ArrowDownToLine,
  ArrowUpFromLine,
  Coins,
  Server,
  Sigma,
  Waypoints,
} from 'lucide-react'
import { useTranslation } from 'react-i18next'

import { StaticDataTable } from '@/components/data-table'
import { EmptyState } from '@/components/empty-state'
import { StatusBadge } from '@/components/status-badge'
import { toIntlLocale } from '@/i18n/languages'
import { formatQuotaWithCurrency } from '@/lib/currency'
import { formatNumber, formatTimestampToDate, formatTokens } from '@/lib/format'
import { cn } from '@/lib/utils'

import type { ChannelModelUsage, ChannelUsageDetail } from '../../types'

const NUMERIC_HEADER_CLASS =
  'text-muted-foreground text-right text-xs whitespace-nowrap'
const NUMERIC_CELL_CLASS =
  'py-2 text-right font-mono whitespace-normal tabular-nums'

function OverviewMetric(props: {
  icon: React.ComponentType<{ className?: string }>
  label: string
  value: string
}) {
  const Icon = props.icon

  return (
    <div className='bg-background flex min-w-0 items-center gap-2 px-3 py-2.5'>
      <Icon
        className='text-muted-foreground/70 size-3.5 shrink-0'
        aria-hidden='true'
      />
      <div className='min-w-0 flex-1'>
        <div className='text-muted-foreground truncate text-[10px] font-medium tracking-wider uppercase'>
          {props.label}
        </div>
        <div className='text-foreground font-mono text-sm font-semibold break-words tabular-nums'>
          {props.value}
        </div>
      </div>
    </div>
  )
}

function UsageOverview(props: { usage: ChannelUsageDetail }) {
  const { t, i18n } = useTranslation()
  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language)
  const usage = props.usage

  return (
    <div className='bg-muted/20 grid overflow-hidden rounded-lg border sm:grid-cols-2 xl:grid-cols-3'>
      <OverviewMetric
        icon={Sigma}
        label={t('Total tokens')}
        value={formatTokens(usage.total_tokens, locale)}
      />
      <OverviewMetric
        icon={ArrowDownToLine}
        label={t('Input tokens')}
        value={formatTokens(usage.prompt_tokens, locale)}
      />
      <OverviewMetric
        icon={ArrowUpFromLine}
        label={t('Output tokens')}
        value={formatTokens(usage.completion_tokens, locale)}
      />
      <OverviewMetric
        icon={Waypoints}
        label={t('Requests')}
        value={formatNumber(usage.request_count, locale)}
      />
      <OverviewMetric
        icon={Coins}
        label={t('Consumed quota')}
        value={formatQuotaWithCurrency(usage.quota, {
          digitsLarge: 2,
          digitsSmall: 4,
          locale,
        })}
      />
    </div>
  )
}

function ModelUsageTable(props: { models: ChannelModelUsage[] }) {
  const { t, i18n } = useTranslation()
  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language)
  const formatCount = (value: number) => formatNumber(value, locale)

  return (
    <StaticDataTable<ChannelModelUsage>
      tableClassName='text-sm'
      headerRowClassName='hover:bg-transparent'
      data={props.models}
      getRowKey={(model) => model.model_name}
      columns={[
        {
          id: 'model_name',
          header: t('Model'),
          className: 'text-muted-foreground text-xs',
          cellClassName: 'py-2',
          cell: (model) => (
            <span className='font-mono break-words'>{model.model_name}</span>
          ),
        },
        {
          id: 'status',
          header: t('Status'),
          className: 'text-muted-foreground text-xs whitespace-nowrap',
          cellClassName: 'py-2',
          cell: (model) => (
            <StatusBadge
              label={model.enabled ? t('Enabled') : t('Disabled')}
              variant={model.enabled ? 'success' : 'neutral'}
              size='sm'
              copyable={false}
              showDot={false}
            />
          ),
        },
        {
          id: 'prompt_tokens',
          header: t('Input tokens'),
          className: NUMERIC_HEADER_CLASS,
          cellClassName: NUMERIC_CELL_CLASS,
          cell: (model) => formatTokens(model.prompt_tokens, locale),
        },
        {
          id: 'completion_tokens',
          header: t('Output tokens'),
          className: NUMERIC_HEADER_CLASS,
          cellClassName: NUMERIC_CELL_CLASS,
          cell: (model) => formatTokens(model.completion_tokens, locale),
        },
        {
          id: 'total_tokens',
          header: t('Total tokens'),
          className: cn(NUMERIC_HEADER_CLASS, 'hidden sm:table-cell'),
          cellClassName: cn(NUMERIC_CELL_CLASS, 'hidden sm:table-cell'),
          cell: (model) => formatTokens(model.total_tokens, locale),
        },
        {
          id: 'request_count',
          header: t('Requests'),
          className: NUMERIC_HEADER_CLASS,
          cellClassName: NUMERIC_CELL_CLASS,
          cell: (model) => formatCount(model.request_count),
        },
        {
          id: 'last_used_at',
          header: t('Last used'),
          className: NUMERIC_HEADER_CLASS,
          cellClassName:
            'text-muted-foreground py-2 text-right whitespace-nowrap tabular-nums',
          cell: (model) => formatTimestampToDate(model.last_used_at),
        },
      ]}
    />
  )
}

/** Totals plus the per-model usage and status table of one channel. */
export function ChannelUsageSummary(props: { usage: ChannelUsageDetail }) {
  const { t } = useTranslation()
  const usage = props.usage

  return (
    <div className='space-y-6'>
      <section className='space-y-3' aria-label={t('Model usage')}>
        <h3 className='text-muted-foreground text-xs font-semibold tracking-wider uppercase'>
          {t('Model usage')}
        </h3>
        <UsageOverview usage={usage} />
      </section>

      <section className='space-y-3' aria-label={t('Model status')}>
        <h3 className='text-muted-foreground text-xs font-semibold tracking-wider uppercase'>
          {t('Model status')}
        </h3>
        {usage.models.length > 0 ? (
          <ModelUsageTable models={usage.models} />
        ) : (
          <EmptyState
            icon={Server}
            title={t('No usage recorded yet')}
            className='min-h-[200px] border'
          />
        )}
      </section>
    </div>
  )
}
