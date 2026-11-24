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
import { useTranslation } from 'react-i18next'

import { ErrorState } from '@/components/error-state'
import { sideDrawerContentClassName } from '@/components/drawer-layout'
import { Skeleton } from '@/components/ui/skeleton'
import {
  Sheet,
  SheetContent,
  SheetDescription,
  SheetHeader,
  SheetTitle,
} from '@/components/ui/sheet'
import { toIntlLocale } from '@/i18n/languages'
import { formatQuotaWithCurrency } from '@/lib/currency'
import { getLobeIcon } from '@/lib/lobe-icon'
import { cn } from '@/lib/utils'

import { useRankingModelUsage } from '../hooks/use-rankings'
import { formatRankingTokens } from '../lib/format'
import type { RankingModelUsageUser, RankingPeriod } from '../types'

type ModelUsageDrawerProps = {
  /** Model to inspect. Empty string keeps the drawer closed. */
  modelName: string
  vendor?: string
  vendorIcon?: string
  period: RankingPeriod
  open: boolean
  onOpenChange: (open: boolean) => void
}

/**
 * Model-square-style side drawer showing a model's usage and user ranking for
 * the ranking period. Reuses the shared Sheet shell so it matches the existing
 * model details drawer; it never navigates to the pricing page.
 *
 * Every identity in `users` is already masked on the server when the viewer is
 * not allowed to see it, so the component renders `display_name` as-is and
 * must not attempt to unmask or re-derive a name.
 */
export function ModelUsageDrawer(props: ModelUsageDrawerProps) {
  const { t, i18n } = useTranslation()
  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language)
  const query = useRankingModelUsage(props.modelName, props.period, props.open)
  const usage = query.data?.data

  return (
    <Sheet open={props.open} onOpenChange={props.onOpenChange}>
      <SheetContent
        side='right'
        className={sideDrawerContentClassName('sm:max-w-xl lg:max-w-2xl')}
      >
        <SheetHeader className='border-border/70 bg-background/95 border-b px-4 py-3 backdrop-blur sm:px-6 sm:py-4'>
          <SheetTitle className='flex items-center gap-2'>
            <span className='shrink-0'>
              {getLobeIcon(props.vendorIcon, 20)}
            </span>
            <span className='truncate font-mono text-sm font-semibold'>
              {props.modelName || t('Model usage')}
            </span>
          </SheetTitle>
          <SheetDescription>
            {props.vendor
              ? t('Usage and user ranking for {{model}}', {
                  model: props.modelName,
                })
              : t('Usage and user ranking')}
          </SheetDescription>
        </SheetHeader>

        <div className='flex-1 overflow-y-auto px-4 py-4 sm:px-6 sm:py-5'>
          {query.isLoading ? (
            <div className='flex flex-col gap-3'>
              <Skeleton className='h-20 w-full rounded-lg' />
              <Skeleton className='h-40 w-full rounded-lg' />
            </div>
          ) : query.isError ? (
            // A failed request must never be rendered as zero usage. The shared
            // error state keeps the retry action consistent with the rest of
            // the console.
            <ErrorState
              title={t('Failed to load usage for this model')}
              onRetry={() => void query.refetch()}
              className='min-h-[240px]'
            />
          ) : (
            <div className='flex flex-col gap-6'>
              <section className='grid grid-cols-3 gap-2'>
                <SummaryStat
                  label={t('Requests')}
                  value={formatRankingTokens(usage?.totals.requests ?? 0, locale)}
                />
                <SummaryStat
                  label={t('Tokens')}
                  value={formatRankingTokens(usage?.totals.tokens ?? 0, locale)}
                />
                <SummaryStat
                  label={t('Quota')}
                  // Quota is a token amount, not a token count: render it with
                  // the shared currency formatter so units and display setting
                  // match the rest of the console.
                  value={formatQuotaWithCurrency(usage?.totals.quota ?? 0)}
                />
              </section>

              <section className='flex flex-col gap-2'>
                <h3 className='text-muted-foreground text-xs font-semibold tracking-wider uppercase'>
                  {t('User Ranking')}
                </h3>
                {usage && usage.users.length > 0 ? (
                  <ul className='divide-border/60 divide-y'>
                    {usage.users.map((row) => (
                      <UserRankingRow key={row.rank} row={row} locale={locale} />
                    ))}
                  </ul>
                ) : (
                  <p className='text-muted-foreground py-8 text-center text-sm'>
                    {t('No usage data for this period')}
                  </p>
                )}
              </section>

              <p className='text-muted-foreground/70 text-xs'>
                {t(
                  'Usage is aggregated from hourly export buckets and may lag behind live traffic.'
                )}
              </p>
            </div>
          )}
        </div>
      </SheetContent>
    </Sheet>
  )
}

function SummaryStat(props: { label: string; value: string }) {
  return (
    <div className='bg-muted/40 rounded-lg px-3 py-2'>
      <div className='text-muted-foreground/80 text-[10px] font-medium tracking-widest uppercase'>
        {props.label}
      </div>
      <div className='text-foreground mt-0.5 font-mono text-sm font-semibold tabular-nums'>
        {props.value}
      </div>
    </div>
  )
}

function UserRankingRow(props: {
  row: RankingModelUsageUser
  locale: string
}) {
  const { t } = useTranslation()
  const { row, locale } = props
  return (
    <li className='flex items-center gap-3 py-2.5'>
      <span className='text-muted-foreground/80 w-6 shrink-0 text-right font-mono text-xs tabular-nums'>
        {row.rank}.
      </span>
      <div className='min-w-0 flex-1'>
        <span
          className={cn(
            'block truncate text-sm font-medium',
            row.masked && 'text-muted-foreground/60 italic'
          )}
        >
          {row.display_name}
        </span>
      </div>
      <div className='shrink-0 text-right'>
        <div className='text-foreground font-mono text-sm font-semibold tabular-nums'>
          {formatRankingTokens(row.tokens, locale)}
        </div>
        <div className='text-muted-foreground/70 text-[11px]'>
          {t('{{count}} requests', { count: row.requests })}
        </div>
        {/* Quota is a token amount, so it uses the shared currency formatter
            rather than the token-count formatter. */}
        <div className='text-muted-foreground/70 text-[11px] tabular-nums'>
          <span className='sr-only'>{t('Quota')}: </span>
          {formatQuotaWithCurrency(row.quota)}
        </div>
      </div>
    </li>
  )
}
