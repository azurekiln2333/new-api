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
import { useQuery } from '@tanstack/react-query'
import { getRouteApi } from '@tanstack/react-router'
import type {
  ColumnFiltersState,
  OnChangeFn,
  SortingState,
  Row,
  ColumnDef,
} from '@tanstack/react-table'
import { Eye, EyeOff, RefreshCw } from 'lucide-react'
import {
  useState,
  useMemo,
  useEffect,
  type KeyboardEvent,
  type MouseEvent,
} from 'react'
import { createPortal } from 'react-dom'
import { useTranslation } from 'react-i18next'

import {
  DISABLED_ROW_DESKTOP,
  DISABLED_ROW_MOBILE,
  DataTablePage,
  useDebouncedColumnFilter,
  useDataTable,
  useDataTableViewMode,
} from '@/components/data-table'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import {
  Tooltip,
  TooltipContent,
  TooltipTrigger,
} from '@/components/ui/tooltip'
import { useMediaQuery } from '@/hooks'
import { useTableUrlState } from '@/hooks/use-table-url-state'
import { hasPermission } from '@/lib/admin-permissions'
import { requireServerSuccess } from '@/lib/server-error-message'
import { cn } from '@/lib/utils'
import { useAuthStore } from '@/stores/auth-store'

import {
  getChannels,
  searchChannels,
  getGroups,
  getChannelBudgets,
} from '../api'
import {
  DEFAULT_PAGE_SIZE,
  CHANNEL_STATUS,
  CHANNEL_STATUS_OPTIONS,
} from '../constants'
import { useChannelPriorityDrag } from '../hooks/use-channel-priority-drag'
import { useChannelPriorityOrder } from '../hooks/use-channel-priority-order'
import { useChannelUsageSummaries } from '../hooks/use-channel-usage'
import {
  channelsQueryKeys,
  aggregateChannelsByTag,
  getChannelTableRowId,
  isTagAggregateRow,
  getChannelTypeLabel,
} from '../lib'
import type { Channel, ChannelSortBy } from '../types'
import { ChannelBudgetContext } from './channel-budget-context'
import { ChannelCard } from './channel-card'
import { ChannelPriorityContext } from './channel-priority-context'
import {
  ChannelPriorityHandle,
  ChannelPriorityModeToggle,
  ChannelPriorityStatus,
} from './channel-priority-controls'
import { ChannelPriorityDropHint } from './channel-priority-drop-hint'
import { ChannelTypeLogo } from './channel-type-badge'
import { ChannelUsageContext } from './channel-usage-context'
import { useChannelsColumns } from './channels-columns'
import { useChannels } from './channels-provider'
import { DataTableBulkActions } from './data-table-bulk-actions'

const route = getRouteApi('/_authenticated/channels/')
const CHANNELS_COLUMN_VISIBILITY_STORAGE_KEY = 'channels:column-visibility'
const CHANNELS_COLUMN_SIZING_STORAGE_KEY = 'channels:column-sizing'
const CHANNELS_VIEW_MODE_STORAGE_KEY = 'channels:view-mode'
const CHANNELS_STATUS_FILTER_STORAGE_KEY = 'channel-status-filter'

const CHANNEL_SORTABLE_COLUMNS = new Set<ChannelSortBy>([
  'id',
  'name',
  'priority',
  'balance',
  'response_time',
  'test_time',
])

function isDisabledChannelRow(channel: Channel) {
  return (
    !isTagAggregateRow(channel) && channel.status !== CHANNEL_STATUS.ENABLED
  )
}

// Clicks on these keep their own behaviour instead of opening channel details.
const ROW_INTERACTIVE_SELECTOR =
  'button, a, input, select, textarea, label, [role=button], [role=menuitem], [role=checkbox], [role=switch], [role=combobox], [role=option], [contenteditable=true]'
const CHANNEL_ROW_ID_PREFIX = 'channel:'

/** Channel id of the row or card that contains `target`, if any. */
function getChannelRowId(target: HTMLElement): number | null {
  const rowId = target
    .closest<HTMLElement>('[data-sortable-id]')
    ?.getAttribute('data-sortable-id')
  if (!rowId?.startsWith(CHANNEL_ROW_ID_PREFIX)) {
    return null
  }
  const channelId = Number(rowId.slice(CHANNEL_ROW_ID_PREFIX.length))
  return Number.isInteger(channelId) && channelId > 0 ? channelId : null
}

export function ChannelsTable() {
  const { t } = useTranslation()
  const {
    enableTagMode,
    sorting,
    setSorting,
    batchMode,
    sensitiveVisible,
    setSensitiveVisible,
    priorityActionContainer,
    setCurrentRow,
    setOpen,
    currentRow,
    open,
    setConcurrencyIds,
  } = useChannels()
  const isMobile = useMediaQuery('(max-width: 640px)')

  const idSort = sorting[0]?.id === 'id'

  // URL state management
  const {
    globalFilter,
    onGlobalFilterChange,
    columnFilters,
    onColumnFiltersChange,
    pagination,
    onPaginationChange,
    ensurePageInRange,
  } = useTableUrlState({
    search: route.useSearch(),
    navigate: route.useNavigate(),
    pagination: {
      defaultPage: 1,
      defaultPageSize: isMobile ? 10 : DEFAULT_PAGE_SIZE,
    },
    globalFilter: { enabled: true, key: 'filter' },
    columnFilters: [
      {
        columnId: 'status',
        searchKey: 'status',
        type: 'array',
        deserialize: (value) => {
          if (value !== undefined) return value
          const stored = localStorage.getItem(
            CHANNELS_STATUS_FILTER_STORAGE_KEY
          )
          return stored === 'enabled' || stored === 'disabled' ? [stored] : []
        },
      },
      { columnId: 'type', searchKey: 'type', type: 'array' },
      { columnId: 'group', searchKey: 'group', type: 'array' },
      { columnId: 'model', searchKey: 'model', type: 'string' },
    ],
  })

  const handleColumnFiltersChange: OnChangeFn<ColumnFiltersState> = (
    updater
  ) => {
    onColumnFiltersChange((previous) => {
      const next = typeof updater === 'function' ? updater(previous) : updater
      const status = next.find((f) => f.id === 'status')?.value as
        | string[]
        | undefined
      localStorage.setItem(
        CHANNELS_STATUS_FILTER_STORAGE_KEY,
        status?.[0] ?? 'all'
      )
      return next
    })
  }

  // Extract filters from column filters
  const statusFilter =
    (columnFilters.find((f) => f.id === 'status')?.value as string[]) || []
  const typeFilter = useMemo(
    () => (columnFilters.find((f) => f.id === 'type')?.value as string[]) || [],
    [columnFilters]
  )
  const groupFilter =
    (columnFilters.find((f) => f.id === 'group')?.value as string[]) || []
  const {
    value: modelFilter,
    inputValue: modelFilterInput,
    onChange: onModelFilterInputChange,
    onCompositionStart: onModelFilterCompositionStart,
    onCompositionEnd: onModelFilterCompositionEnd,
    resetInput: resetModelFilterInput,
  } = useDebouncedColumnFilter({
    columnFilters,
    columnId: 'model',
    onColumnFiltersChange,
  })

  // Determine whether to use search or regular list API
  const shouldSearch = Boolean(globalFilter?.trim() || modelFilter.trim())

  const sortParams = useMemo(() => {
    const activeSort = sorting[0]
    if (
      !activeSort ||
      !CHANNEL_SORTABLE_COLUMNS.has(activeSort.id as ChannelSortBy)
    ) {
      return {}
    }

    return {
      sort_by: activeSort.id as ChannelSortBy,
      sort_order: activeSort.desc ? 'desc' : 'asc',
    } as const
  }, [sorting])

  const handleSortingChange: OnChangeFn<SortingState> = (updater) => {
    setSorting(updater)
    if (pagination.pageIndex > 0) {
      onPaginationChange({ ...pagination, pageIndex: 0 })
    }
  }

  // Fetch groups for filter
  const { data: groupsData } = useQuery({
    queryKey: ['groups'],
    queryFn: async () => requireServerSuccess(await getGroups()),
  })

  const groupOptions = useMemo(
    () =>
      (groupsData?.data || []).map((g) => ({
        label: g,
        value: g,
      })),
    [groupsData]
  )

  // Fetch channels data
  // eslint-disable-next-line @tanstack/query/exhaustive-deps
  const { data, isLoading, isFetching, refetch } = useQuery({
    queryKey: channelsQueryKeys.list({
      keyword: globalFilter,
      model: modelFilter,
      group:
        groupFilter.length > 0 && !groupFilter.includes('all')
          ? groupFilter[0]
          : undefined,
      status:
        statusFilter.length > 0 && !statusFilter.includes('all')
          ? statusFilter[0]
          : undefined,
      type:
        typeFilter.length > 0 && !typeFilter.includes('all')
          ? Number(typeFilter[0])
          : undefined,
      tag_mode: enableTagMode,
      id_sort: idSort,
      ...sortParams,
      p: pagination.pageIndex + 1,
      page_size: pagination.pageSize,
    }),
    queryFn: async ({ signal }) => {
      if (shouldSearch) {
        return requireServerSuccess(
          await searchChannels(
            {
              keyword: globalFilter,
              model: modelFilter,
              group:
                groupFilter.length > 0 && !groupFilter.includes('all')
                  ? groupFilter[0]
                  : undefined,
              status:
                statusFilter.length > 0 && !statusFilter.includes('all')
                  ? statusFilter[0]
                  : undefined,
              type:
                typeFilter.length > 0 && !typeFilter.includes('all')
                  ? Number(typeFilter[0])
                  : undefined,
              tag_mode: enableTagMode,
              id_sort: idSort,
              ...sortParams,
              p: pagination.pageIndex + 1,
              page_size: pagination.pageSize,
            },
            signal
          )
        )
      } else {
        return requireServerSuccess(
          await getChannels(
            {
              group:
                groupFilter.length > 0 && !groupFilter.includes('all')
                  ? groupFilter[0]
                  : undefined,
              status:
                statusFilter.length > 0 && !statusFilter.includes('all')
                  ? statusFilter[0]
                  : undefined,
              type:
                typeFilter.length > 0 && !typeFilter.includes('all')
                  ? Number(typeFilter[0])
                  : undefined,
              tag_mode: enableTagMode,
              id_sort: idSort,
              ...sortParams,
              p: pagination.pageIndex + 1,
              page_size: pagination.pageSize,
            },
            signal
          )
        )
      }
    },
    placeholderData: (previousData) => previousData,
  })

  // Apply tag aggregation if tag mode is enabled
  const channels = useMemo(() => {
    const rawChannels = data?.data?.items || []

    if (enableTagMode && rawChannels.length > 0) {
      return aggregateChannelsByTag(rawChannels)
    }

    return rawChannels
  }, [data, enableTagMode])

  const totalCount = data?.data?.total || 0
  const typeCounts = data?.data?.type_counts

  const pageChannels = data?.data?.items
  const pageChannelIds = useMemo(
    () => (pageChannels ?? []).map((channel) => channel.id),
    [pageChannels]
  )
  const { usageByChannel, refetch: refetchUsage } =
    useChannelUsageSummaries(pageChannelIds)
  // Only real channels have accounts. Keep the query key stable across sorts.
  const budgetChannelIds = useMemo(
    () =>
      [
        ...new Set(
          (pageChannels ?? [])
            .filter((channel) => !isTagAggregateRow(channel))
            .map((channel) => channel.id)
        ),
      ].sort((a, b) => a - b),
    [pageChannels]
  )
  const budgetQuery = useQuery({
    queryKey: channelsQueryKeys.budgetSummaries(budgetChannelIds),
    queryFn: ({ signal }) => getChannelBudgets(budgetChannelIds, signal),
    enabled: budgetChannelIds.length > 0,
    retry: false,
    refetchOnWindowFocus: false,
    meta: { errorToast: false },
  })
  const budgetContext = useMemo(
    () => ({
      byChannel: Object.fromEntries(
        (budgetQuery.data ?? []).map((view) => [view.channel_id, view])
      ),
      loading: budgetQuery.isLoading,
    }),
    [budgetQuery.data, budgetQuery.isLoading]
  )

  // Publish the visible set (plus the open drawer's channel) to the provider,
  // which owns the one live-concurrency subscription shared by cards and the
  // details drawer. Tag aggregate rows own no channel and are excluded.
  const concurrencyIds = useMemo(() => {
    const ids = (pageChannels ?? [])
      .filter((channel) => !isTagAggregateRow(channel))
      .map((channel) => channel.id)
    if (open === 'channel-details' && currentRow) ids.push(currentRow.id)
    return [...new Set(ids)].sort((a, b) => a - b)
  }, [pageChannels, open, currentRow])
  useEffect(() => {
    setConcurrencyIds(concurrencyIds)
  }, [concurrencyIds, setConcurrencyIds])

  const [viewMode, setViewMode] = useDataTableViewMode({
    storageKey: CHANNELS_VIEW_MODE_STORAGE_KEY,
    defaultMode: 'card',
  })
  const user = useAuthStore((state) => state.auth.user)
  const sortField = (sorting[0]?.id ??
    (idSort ? 'id' : 'priority')) as ChannelSortBy
  const sortDesc = sorting[0]?.desc ?? true
  const [reorderEnabled, setReorderEnabled] = useState(false)
  const canReorder =
    sortField === 'priority' &&
    !enableTagMode &&
    hasPermission(user, 'channel', 'write')
  const reorderActive = reorderEnabled && canReorder
  useEffect(() => {
    if (!canReorder) setReorderEnabled(false)
  }, [canReorder])
  const reorderScope = JSON.stringify([
    viewMode,
    reorderActive,
    sorting,
    idSort,
    enableTagMode,
    columnFilters,
    globalFilter,
    pagination,
    channels.map((channel) => [channel.id, channel.priority]),
  ])
  const priorityOrder = useChannelPriorityOrder({
    channels,
    desc: sortDesc,
    scope: reorderScope,
    enabled: reorderActive,
  })
  const drag = useChannelPriorityDrag({
    items: channels,
    desc: sortDesc,
    // A background refetch keeps the current list valid, and the gesture already
    // snapshots its items and is cancelled by the scope when the visible set or
    // priorities actually change. Only a first load with no data yet (or an
    // in-flight save) must block reordering.
    enabled: reorderActive && !priorityOrder.saving && !isLoading,
    scope: reorderScope,
    begin: priorityOrder.begin,
    select: priorityOrder.select,
    commit: priorityOrder.commit,
    cancel: priorityOrder.cancel,
    previewStore: priorityOrder.previewStore,
  })
  let dragReason: 'sort' | 'tag' | 'permission' | 'busy' | null = null
  if (sortField !== 'priority') dragReason = 'sort'
  else if (enableTagMode) dragReason = 'tag'
  else if (!hasPermission(user, 'channel', 'write')) dragReason = 'permission'
  else if (priorityOrder.saving || isLoading) dragReason = 'busy'

  const dragControls = useMemo(
    () => ({ reason: dragReason, start: drag.start, keyDown: drag.keyDown }),
    [dragReason, drag.start, drag.keyDown]
  )

  // Rows and cards open the details drawer on click. Controls inside a row
  // (buttons, links, checkboxes, tags themselves, any action menu that
  // teleports its content) keep their own behaviour, and tag aggregate rows
  // expand instead. Reorder mode disables click-to-open.
  const openChannelDetails = (event: MouseEvent<HTMLDivElement>) => {
    if (reorderActive) return
    const target = event.target as HTMLElement
    if (!event.currentTarget.contains(target)) return
    // The focusable card body is itself a [role=button]; only controls inside
    // it keep their own click behaviour.
    const interactiveTarget = target.closest(ROW_INTERACTIVE_SELECTOR)
    if (
      interactiveTarget &&
      !interactiveTarget.hasAttribute('data-activates-channel-details')
    ) {
      return
    }
    if (window.getSelection()?.toString()) return
    const channelId = getChannelRowId(target)
    const channel = pageChannels?.find((item) => item.id === channelId)
    if (!channel) return
    setCurrentRow(channel)
    setOpen('channel-details')
  }

  // Keyboard equivalent for the card view: only the focused card body itself
  // opens the drawer. Keys pressed inside a nested control (inline spinner,
  // action menu, status toggle, select box) belong to that control and must
  // not bubble into detail activation, and an event a control already handled
  // must never reopen the drawer. The table view handles keys on the
  // channel-name button in the name column instead.
  const handleCardKeyDown = (event: KeyboardEvent<HTMLDivElement>) => {
    if (reorderActive) return
    if (event.key !== 'Enter' && event.key !== ' ') return
    if (event.defaultPrevented) return
    const target = event.target as HTMLElement
    if (!target.matches('[role=button][data-activates-channel-details]')) return
    event.preventDefault()
    const channelId = getChannelRowId(target)
    const channel = pageChannels?.find((item) => item.id === channelId)
    if (!channel) return
    setCurrentRow(channel)
    setOpen('channel-details')
  }

  // Reuse the same handle cell in the table and channel cards.
  const baseColumns = useChannelsColumns({ enableSelection: batchMode })
  const columns = useMemo<ColumnDef<Channel>[]>(
    () =>
      reorderActive
        ? [
            {
              id: 'reorder',
              header: '',
              size: 40,
              minSize: 40,
              maxSize: 40,
              enableSorting: false,
              enableHiding: false,
              enableResizing: false,
              meta: { mobileHidden: true },
              cell: ({ row }) => <ChannelPriorityHandle id={row.original.id} />,
            },
            ...baseColumns,
          ]
        : baseColumns,
    [baseColumns, reorderActive]
  )

  // React Table instance
  const { table } = useDataTable({
    data: priorityOrder.items,
    columns,
    totalCount,
    sorting,
    initialColumnVisibility: {
      models: false,
      tag: false,
    },
    columnVisibilityStorageKey: CHANNELS_COLUMN_VISIBILITY_STORAGE_KEY,
    columnSizingStorageKey: isMobile
      ? false
      : CHANNELS_COLUMN_SIZING_STORAGE_KEY,
    columnFilters,
    pagination,
    globalFilter,
    enableRowSelection: batchMode
      ? (row: Row<Channel>) => !isTagAggregateRow(row.original)
      : false,
    onSortingChange: handleSortingChange,
    onColumnFiltersChange: handleColumnFiltersChange,
    onPaginationChange,
    onGlobalFilterChange,
    getRowId: getChannelTableRowId,
    getSubRows: (row: Channel & { children?: Channel[] }) => row.children,
    manualPagination: true,
    manualSorting: true,
    manualFiltering: true,
    withExpandedRowModel: true,
    enableColumnResizing: !isMobile,
    ensurePageInRange,
  })

  useEffect(() => {
    if (!batchMode) {
      table.resetRowSelection()
    }
  }, [batchMode, table])

  // Prepare filter options from existing channel types only.
  const typeFilterOptions = useMemo(() => {
    const counts = typeCounts || {}
    const typeIds = Object.entries(counts)
      .map(([type, count]) => ({
        type: Number(type),
        count: Number(count) || 0,
      }))
      .filter((item) => item.type > 0 && item.count > 0)
      .sort((a, b) => {
        const labelA = t(getChannelTypeLabel(a.type))
        const labelB = t(getChannelTypeLabel(b.type))
        return labelA.localeCompare(labelB)
      })

    const selectedType = typeFilter.find((value) => value !== 'all')
    if (selectedType) {
      const selectedTypeId = Number(selectedType)
      const alreadyIncluded = typeIds.some(
        (item) => item.type === selectedTypeId
      )
      if (selectedTypeId > 0 && !alreadyIncluded) {
        typeIds.push({
          type: selectedTypeId,
          count: Number(counts[selectedType]) || 0,
        })
      }
    }

    const totalTypes = Object.values(counts).reduce(
      (sum, count) => sum + (Number(count) || 0),
      0
    )

    return [
      {
        label: 'All Types',
        value: 'all',
        count: totalTypes,
      },
      ...typeIds.map((item) => {
        return {
          label: getChannelTypeLabel(item.type),
          value: String(item.type),
          count: item.count,
          iconNode: <ChannelTypeLogo type={item.type} size={16} />,
        }
      }),
    ]
  }, [t, typeCounts, typeFilter])

  const groupFilterOptions = [
    { label: t('All Groups'), value: 'all' },
    ...groupOptions.map((option) => ({
      ...option,
      label: sensitiveVisible ? option.label : '••••',
    })),
  ]

  return (
    <ChannelPriorityContext.Provider value={dragControls}>
      <ChannelBudgetContext.Provider value={budgetContext}>
        <ChannelUsageContext.Provider value={usageByChannel}>
          {priorityActionContainer &&
            createPortal(
              <ChannelPriorityModeToggle
                active={reorderActive}
                viewMode={viewMode}
                reason={dragReason}
                onChange={() => setReorderEnabled((value) => !value)}
              />,
              priorityActionContainer
            )}
          <ChannelPriorityDropHint
            container={drag.feedbackContainer}
            order={priorityOrder}
            channels={channels}
          />
          <div
            ref={drag.root}
            role='region'
            aria-label={t('Channels')}
            onClick={openChannelDetails}
            onKeyDown={handleCardKeyDown}
            {...drag.rootEvents}
            className='relative h-full min-h-0 data-[priority-dragging=true]:select-none data-[priority-dragging=true]:[&_[data-sortable-id]]:pointer-events-none'
          >
            <DataTablePage
              table={table}
              columns={columns}
              isLoading={isLoading}
              isFetching={isFetching}
              emptyTitle={t('No Channels Found')}
              emptyDescription={t(
                'No channels available. Create your first channel to get started.'
              )}
              skeletonKeyPrefix='channel-skeleton'
              enableCardView
              viewMode={viewMode}
              onViewModeChange={setViewMode}
              renderCard={(row, { isSelected }) => {
                // Tag aggregate rows only expand, so they are not activation targets.
                const isTagRow = isTagAggregateRow(row.original)
                return (
                  <ChannelCard
                    row={row}
                    isSelected={isSelected}
                    reorderEnabled={reorderActive}
                    interactive={!isTagRow && !reorderActive}
                  />
                )
              }}
              cardGridClassName='grid grid-cols-1 gap-3 sm:gap-4 lg:grid-cols-3'
              applyHeaderSize
              afterTable={<ChannelPriorityStatus order={priorityOrder} />}
              toolbarProps={{
                collapsibleOnMobile: true,
                searchPlaceholder: t('Filter by name, ID, or key...'),
                searchDebounceMs: 500,
                onReset: () => {
                  resetModelFilterInput()
                },
                additionalSearch: (
                  <Input
                    placeholder={t('Filter by model...')}
                    value={modelFilterInput}
                    onChange={onModelFilterInputChange}
                    onCompositionStart={onModelFilterCompositionStart}
                    onCompositionEnd={onModelFilterCompositionEnd}
                    className='w-full sm:w-[150px] lg:w-[180px]'
                  />
                ),
                filters: [
                  {
                    columnId: 'status',
                    title: t('Status'),
                    options: [...CHANNEL_STATUS_OPTIONS],
                    singleSelect: true,
                  },
                  {
                    columnId: 'type',
                    title: t('Type'),
                    options: typeFilterOptions,
                    singleSelect: true,
                  },
                  {
                    columnId: 'group',
                    title: t('Group'),
                    options: groupFilterOptions,
                    singleSelect: true,
                  },
                ],
                preActions: (
                  <>
                    <Tooltip>
                      <TooltipTrigger
                        render={
                          <Button
                            variant='ghost'
                            size='icon'
                            onClick={() =>
                              setSensitiveVisible(!sensitiveVisible)
                            }
                            aria-label={
                              sensitiveVisible ? t('Hide') : t('Show')
                            }
                            className='text-muted-foreground hover:text-foreground size-8'
                          />
                        }
                      >
                        {sensitiveVisible ? (
                          <Eye aria-hidden />
                        ) : (
                          <EyeOff aria-hidden />
                        )}
                      </TooltipTrigger>
                      <TooltipContent>
                        {sensitiveVisible ? t('Hide') : t('Show')}
                      </TooltipContent>
                    </Tooltip>
                    <Tooltip>
                      <TooltipTrigger
                        render={
                          <Button
                            variant='ghost'
                            size='icon'
                            onClick={() => {
                              void refetch()
                              void refetchUsage()
                              void budgetQuery.refetch()
                            }}
                            aria-label={t('Refresh')}
                            aria-busy={isFetching}
                            className='text-muted-foreground hover:text-foreground size-8'
                          />
                        }
                      >
                        <RefreshCw
                          aria-hidden
                          className={cn(isFetching && 'animate-spin')}
                        />
                      </TooltipTrigger>
                      <TooltipContent>{t('Refresh')}</TooltipContent>
                    </Tooltip>
                  </>
                ),
              }}
              getRowClassName={(row, { isMobile }) => {
                // Tag rows keep their own expand/collapse behaviour.
                if (isTagAggregateRow(row.original)) return undefined
                if (!isDisabledChannelRow(row.original)) return 'cursor-pointer'
                if (isMobile) {
                  return cn(DISABLED_ROW_MOBILE, 'cursor-pointer')
                }
                return cn(DISABLED_ROW_DESKTOP, 'cursor-pointer')
              }}
              bulkActions={
                batchMode ? <DataTableBulkActions table={table} /> : null
              }
            />
          </div>
        </ChannelUsageContext.Provider>
      </ChannelBudgetContext.Provider>
    </ChannelPriorityContext.Provider>
  )
}
