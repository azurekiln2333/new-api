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
import { flexRender, type Row } from '@tanstack/react-table'
import { memo } from 'react'
import { useTranslation } from 'react-i18next'

import { StatusBadgeTypeContext } from '@/components/status-badge'
import { cn } from '@/lib/utils'

import { CHANNEL_STATUS } from '../constants'
import { isTagAggregateRow } from '../lib'
import type { Channel } from '../types'
import { ChannelRowActionsLayoutContext } from './channel-row-actions-context'
import { useChannels } from './channels-provider'

const SENSITIVE_MASK = '••••'

/**
 * Bespoke channel card for the card view. Reuses every column's existing cell
 * renderer via `flexRender`, so the table's information and interactions are
 * preserved: row selection, provider/multi-key/IO.NET type badge, id,
 * name/remark + warning icons, status (with tooltips), inline
 * priority/weight spinners, balance refresh, response/test times, tag
 * expand-collapse, and the per-row (or per-tag) actions menu. Channel groups
 * and the models each group reaches live in the details drawer.
 */
function ChannelCardComponent({
  row,
  isSelected,
  reorderEnabled = false,
  interactive = false,
}: {
  row: Row<Channel>
  isSelected: boolean
  reorderEnabled?: boolean
  /**
   * Makes the card body a keyboard target for opening channel details; the
   * pointer click is handled by the table region. Disabled for tag rows and
   * while reordering.
   */
  interactive?: boolean
}) {
  const { t } = useTranslation()
  const { sensitiveVisible } = useChannels()
  const isTagRow = isTagAggregateRow(row.original)
  const cells = row.getAllCells()

  const renderCell = (id: string) => {
    const cell = cells.find((c) => c.column.id === id)
    if (!cell || !cell.column.columnDef.cell) {
      return null
    }
    return flexRender(cell.column.columnDef.cell, cell.getContext())
  }

  const reorderCell = reorderEnabled ? renderCell('reorder') : null
  const selectCell = renderCell('select')
  const typeCell = renderCell('type')
  const nameCell = renderCell('name')
  const statusCell = renderCell('status')
  const actionsCell = renderCell('actions')
  const priorityCell = renderCell('priority')
  const weightCell = renderCell('weight')
  const balanceCell = renderCell('balance')
  const usedTokensCell = renderCell('used_tokens')
  const responseCell = renderCell('response_time')
  const testCell = renderCell('test_time')

  const labelClass = 'text-muted-foreground text-[11px] font-medium select-none'

  // In card view the enable/disable state is already conveyed by the inline
  // power toggle, so the plain "Enabled"/"Disabled" badge is redundant. Keep
  // only the informative states (e.g. auto-disabled, unknown) and tag rows.
  const showStatusBadge =
    isTagRow ||
    (row.original.status !== CHANNEL_STATUS.ENABLED &&
      row.original.status !== CHANNEL_STATUS.MANUAL_DISABLED)

  return (
    <ChannelRowActionsLayoutContext.Provider value='card'>
      <div
        data-state={isSelected ? 'selected' : undefined}
        role={interactive ? 'button' : undefined}
        tabIndex={interactive ? 0 : undefined}
        data-activates-channel-details={interactive ? true : undefined}
        className='focus-visible:ring-ring flex min-w-0 flex-col gap-2 rounded-sm focus-visible:ring-2 focus-visible:outline-none'
      >
        {/* Row 1: selection, type logo (its name shows on hover), channel name
          and id, with the status badge and actions menu on the right. */}
        <div className='flex items-start justify-between gap-2'>
          <div className='flex min-w-0 flex-1 items-center gap-2'>
            {reorderCell}
            {!isTagRow && selectCell && (
              <span className='shrink-0'>{selectCell}</span>
            )}
            <div
              className={cn(
                'min-w-0 shrink-0 overflow-hidden',
                !isTagRow &&
                  '[&_[data-slot=provider-badge]>[data-slot=status-badge]]:sr-only'
              )}
            >
              {typeCell}
            </div>
            <div className='min-w-0 flex-1 overflow-hidden text-sm'>
              {nameCell}
              {!isTagRow && (
                <div className={labelClass}>
                  #{sensitiveVisible ? row.original.id : SENSITIVE_MASK}
                </div>
              )}
            </div>
          </div>
          <div className='flex shrink-0 items-center gap-1.5'>
            {showStatusBadge && statusCell}
            {actionsCell}
          </div>
        </div>

        {/* Metrics: the usage pair, then the priority/weight pair, then a
          compact response row. Each pair is its own list, so it stays on a
          single line at normal card widths and degrades to stacked metrics in
          a narrow card instead of overflowing it. */}
        <StatusBadgeTypeContext.Provider value='text'>
          <div className='flex min-w-0 flex-col gap-2'>
            <dl
              data-card-metric-row='usage'
              className='flex min-w-0 flex-wrap gap-x-6 gap-y-1.5'
            >
              <div className='flex min-w-0 flex-[1_1_7rem] flex-col gap-0.5'>
                <dt className={labelClass}>{t('Used / Remaining')}</dt>
                <dd className='min-w-0 text-sm tabular-nums [&_[data-slot=status-badge]]:!ml-0 [&>div]:ml-0 [&>div]:flex-wrap [&>div]:gap-x-3'>
                  {balanceCell ?? (
                    <span className='text-muted-foreground'>-</span>
                  )}
                </dd>
              </div>
              <div className='flex min-w-0 flex-[1_1_7rem] flex-col gap-0.5'>
                <dt className={labelClass}>{t('Used tokens')}</dt>
                <dd className='min-w-0 text-sm tabular-nums'>
                  {usedTokensCell ?? (
                    <span className='text-muted-foreground'>-</span>
                  )}
                </dd>
              </div>
            </dl>

            <dl
              data-card-metric-row='priority'
              className='flex min-w-0 flex-wrap items-center gap-x-5 gap-y-1'
            >
              <div className='flex min-w-0 items-center gap-1.5'>
                <dt className={labelClass}>{t('Priority')}</dt>
                <dd data-column-id='priority'>{priorityCell}</dd>
              </div>
              <div className='flex min-w-0 items-center gap-1.5'>
                <dt className={labelClass}>{t('Weight')}</dt>
                <dd>{weightCell}</dd>
              </div>
            </dl>

            <dl
              data-card-metric-row='response'
              className='flex min-w-0 flex-wrap items-center gap-x-4 gap-y-1'
            >
              <div className='flex min-w-0 items-center gap-1.5'>
                <dt className={labelClass}>{t('Response')}</dt>
                <dd className='min-w-0 text-sm tabular-nums [&_[data-slot=status-badge]]:!ml-0'>
                  {responseCell ?? (
                    <span className='text-muted-foreground'>-</span>
                  )}
                </dd>
              </div>
              <div className='flex min-w-0 items-center gap-1.5'>
                <dt className={labelClass}>{t('Last Tested')}</dt>
                <dd className='min-w-0 text-sm [&_[data-slot=status-badge]]:!ml-0'>
                  {testCell ?? <span className='text-muted-foreground'>-</span>}
                </dd>
              </div>
            </dl>
          </div>
        </StatusBadgeTypeContext.Provider>
      </div>
    </ChannelRowActionsLayoutContext.Provider>
  )
}

/**
 * Memoized so each card only re-renders when its own react-table row reference
 * changes, instead of every card re-rendering whenever the parent table state
 * (filters, pagination, sensitive toggle, etc.) updates.
 */
export const ChannelCard = memo(ChannelCardComponent)
