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
import type { ColumnDef } from '@tanstack/react-table'
import { Search } from 'lucide-react'
import { useEffect, useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { ConfirmDialog } from '@/components/confirm-dialog'
import {
  DataTablePagination,
  DataTableView,
  useDataTable,
} from '@/components/data-table'
import { ErrorState } from '@/components/error-state'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import type { PricingCurrency } from '@/features/model-pricing/currency'

import type { PricingSyncValues } from '../types'
import type { PriceAdjustment } from './upstream-price-adjustment'
import {
  describeSyncPrice,
  getSyncExpressionPricing,
  getUpstreamDisplayName,
} from './upstream-ratio-sync-helpers'

/**
 * One frozen preview row. The raw values, adjustment mode and exchange rate are
 * captured when the preview opens, so a later rate change cannot alter the
 * confirmed charge. Prices are formatted per rendered page, not for the whole
 * selection.
 */
export type ConflictItem = {
  model: string
  source: string
  current: PricingSyncValues
  next: PricingSyncValues
  adjustment: PriceAdjustment
  rate: number | null
  siteCurrency?: PricingCurrency
}
type ConflictConfirmDialogProps = {
  open: boolean
  onOpenChange: (open: boolean) => void
  conflicts: ConflictItem[]
  onConfirm: () => void
  isLoading?: boolean
  error?: string
  onReload?: () => void
  exchangeRateSummary?: string
}

const PAGE_SIZE = 10

export function ConflictConfirmDialog(props: ConflictConfirmDialogProps) {
  const { t } = useTranslation()
  const [search, setSearch] = useState('')
  const filtered = useMemo(() => {
    const query = search.trim().toLowerCase()
    if (!query) return props.conflicts
    return props.conflicts.filter((conflict) =>
      conflict.model.toLowerCase().includes(query)
    )
  }, [props.conflicts, search])
  const columns = useMemo<ColumnDef<ConflictItem>[]>(() => {
    const adjustmentLabel = (item: ConflictItem) => {
      if (!item.rate || item.adjustment === 'keep') {
        return item.adjustment === 'keep' ? t('Keep source price') : undefined
      }
      if (item.adjustment === 'divide') {
        return t('Divide by exchange rate (÷ {{rate}})', {
          rate: String(item.rate),
        })
      }
      if (item.adjustment === 'multiply') {
        return t('Multiply by exchange rate (× {{rate}})', {
          rate: String(item.rate),
        })
      }
      return undefined
    }
    return [
      {
        id: 'channel',
        header: t('Price source'),
        cell: ({ row }) => (
          <span className='font-medium'>
            {getUpstreamDisplayName(row.original.source, t)}
          </span>
        ),
      },
      {
        id: 'model',
        header: t('Model'),
        cell: ({ row }) => (
          <span className='font-mono text-sm'>{row.original.model}</span>
        ),
      },
      {
        id: 'current',
        header: t('Current Billing'),
        cell: ({ row }) => (
          <pre className='text-sm whitespace-pre-wrap'>
            {describeSyncPrice(row.original.current, t)}
          </pre>
        ),
      },
      {
        id: 'new',
        header: t('Final saved price'),
        cell: ({ row }) => {
          const item = row.original
          const sitePrice =
            item.siteCurrency &&
            (item.next.billing_mode !== 'tiered_expr' ||
              getSyncExpressionPricing(String(item.next.billing_expr), t))
              ? describeSyncPrice(item.next, t, item.siteCurrency)
              : undefined
          return (
            <div className='space-y-2'>
              {adjustmentLabel(item) && (
                <p className='text-muted-foreground text-xs'>
                  {adjustmentLabel(item)}
                </p>
              )}
              <pre className='text-sm whitespace-pre-wrap'>
                {describeSyncPrice(item.next, t)}
              </pre>
              {sitePrice && (
                <pre className='text-muted-foreground text-xs whitespace-pre-wrap'>
                  {sitePrice}
                </pre>
              )}
            </div>
          )
        },
      },
    ]
  }, [t])
  const { table } = useDataTable({
    data: filtered,
    columns,
    getRowId: (row) => row.model,
    initialPagination: { pageIndex: 0, pageSize: PAGE_SIZE },
    withSortedRowModel: false,
    withFilteredRowModel: false,
    withFacetedRowModel: false,
  })
  // Each preview is a new selection, so the previous page and search must not
  // carry over to rows that no longer exist.
  useEffect(() => {
    if (!props.open) return
    setSearch('')
    table.setPageIndex(0)
  }, [props.open, props.conflicts, table])
  return (
    <ConfirmDialog
      open={props.open}
      onOpenChange={props.onOpenChange}
      title={t('Preview price changes')}
      desc={t('Review and confirm the selected prices.')}
      className='max-h-[min(90dvh,var(--dialog-available-height))] overflow-y-auto data-[size=default]:max-w-[calc(100%-2rem)] data-[size=default]:sm:max-w-4xl'
      confirmText={t('Confirm Changes')}
      isLoading={props.isLoading}
      handleConfirm={props.onConfirm}
      disabled={Boolean(props.error)}
    >
      {props.exchangeRateSummary && (
        <p className='text-muted-foreground text-sm'>
          {props.exchangeRateSummary}
        </p>
      )}
      {props.error && (
        <ErrorState
          description={props.error}
          action={
            <Button variant='outline' onClick={props.onReload}>
              {t('Reload pricing')}
            </Button>
          }
        />
      )}
      <div className='flex flex-wrap items-center gap-2'>
        <div className='relative min-w-40 flex-1 sm:max-w-72'>
          <Search
            className='text-muted-foreground absolute top-1/2 left-2 size-4 -translate-y-1/2'
            aria-hidden
          />
          <Input
            value={search}
            onChange={(event) => {
              setSearch(event.target.value)
              table.setPageIndex(0)
            }}
            placeholder={t('Search model name...')}
            aria-label={t('Search models')}
            className='ps-8'
          />
        </div>
        <span className='text-muted-foreground text-sm' role='status'>
          {t('{{count}} selected models', { count: props.conflicts.length })}
        </span>
      </div>
      <DataTableView
        table={table}
        containerClassName='max-h-96 overflow-y-auto'
        tableClassName='min-w-[640px]'
        tableHeaderClassName='[background-color:var(--table-header)]'
        emptyContent={t('No results found')}
        emptyCellClassName='h-24 text-center'
      />
      <DataTablePagination table={table} />
    </ConfirmDialog>
  )
}
