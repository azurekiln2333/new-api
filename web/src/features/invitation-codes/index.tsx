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
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { getRouteApi } from '@tanstack/react-router'
import type { ColumnDef } from '@tanstack/react-table'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'

import { ConfirmDialog } from '@/components/confirm-dialog'
import {
  CodeExportProvider,
  useCodeExport,
} from '@/components/code-export/code-export-provider'
import {
  DataTableBulkActions,
  DataTablePage,
  useDataTable,
} from '@/components/data-table'
import { Download, History } from 'lucide-react'
import { ErrorState } from '@/components/error-state'
import { SectionPageLayout } from '@/components/layout'
import { MaskedValueDisplay } from '@/components/masked-value-display'
import { Button } from '@/components/ui/button'
import { Checkbox } from '@/components/ui/checkbox'
import { useTableUrlState } from '@/hooks/use-table-url-state'
import { formatTimestampToDate } from '@/lib/format'

import {
  deleteInvitations,
  listInvitations,
  updateInvitation,
  type Invitation,
} from './api'
import { InvitationEditor } from './components/invitation-editor'

const route = getRouteApi('/_authenticated/invitation-codes/')

export function InvitationCodes() {
  return (
    <CodeExportProvider scope='invitation'>
      <InvitationCodesContent />
    </CodeExportProvider>
  )
}

function InvitationCodesContent() {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const { exportIds, exportBatch, exportFiltered, openHistory } = useCodeExport()
  const [editor, setEditor] = useState<Invitation | 'new' | null>(null)
  const [remove, setRemove] = useState<number[] | 'invalid' | null>(null)
  const state = useTableUrlState({
    search: route.useSearch(),
    navigate: route.useNavigate(),
    pagination: { defaultPage: 1, defaultPageSize: 20 },
    globalFilter: { enabled: true, key: 'filter' },
    columnFilters: [{ columnId: 'status', searchKey: 'status', type: 'array' }],
  })
  const status =
    (
      state.columnFilters.find((filter) => filter.id === 'status')?.value as
        | string[]
        | undefined
    )?.[0] ?? ''
  const query = useQuery({
    queryKey: ['invitations', state.pagination, state.globalFilter, status],
    queryFn: () =>
      listInvitations(
        state.pagination.pageIndex + 1,
        state.pagination.pageSize,
        state.globalFilter ?? '',
        status
      ),
  })
  const refresh = () =>
    queryClient.invalidateQueries({ queryKey: ['invitations'] })
  // Patch every cached page from the committed values, filtered the same way the
  // server would, so a row that no longer matches the active filter leaves the
  // page instead of lingering. Only rows the page already holds are counted;
  // the background refetch supplies cross-page totals.
  const patchInvitation = (changed: {
    id: number
    name?: string
    expired_time?: number
    status?: number
  }) => {
    for (const [queryKey, previous] of queryClient.getQueriesData<
      Awaited<ReturnType<typeof listInvitations>>
    >({ queryKey: ['invitations'] })) {
      if (!previous) continue
      const filter = queryKey[3]
      let removed = 0
      const items = previous.items.flatMap((item) => {
        if (item.id !== changed.id) return [item]
        const updated = { ...item, ...changed }
        const expired =
          updated.expired_time > 0 && updated.expired_time <= Date.now() / 1000
        let matches = true
        if (filter === '1') matches = updated.status === 1 && !expired
        else if (filter === 'expired') {
          matches = updated.status === 1 && expired
        } else if (filter === '2' || filter === '3') {
          matches = updated.status === Number(filter)
        }
        if (matches) return [updated]
        removed++
        return []
      })
      queryClient.setQueryData(queryKey, {
        ...previous,
        items,
        total: Math.max(0, previous.total - removed),
      })
    }
  }
  const update = useMutation({
    mutationFn: updateInvitation,
    onSuccess: (_, changed) => {
      void queryClient.cancelQueries({ queryKey: ['invitations'] })
      patchInvitation(changed)
      void refresh()
    },
  })
  const deletion = useMutation({
    mutationFn: deleteInvitations,
    onSuccess: (_, deleted) => {
      void queryClient.cancelQueries({ queryKey: ['invitations'] })
      queryClient.setQueriesData<Awaited<ReturnType<typeof listInvitations>>>(
        { queryKey: ['invitations'] },
        (previous) => {
          if (!previous) return previous
          const items = previous.items.filter((item) => {
            if (deleted !== 'invalid') return !deleted.includes(item.id)
            return (
              item.status !== 2 &&
              item.status !== 3 &&
              (item.expired_time === 0 ||
                item.expired_time > Date.now() / 1000)
            )
          })
          // Only adjust for rows this cached page proves were removed. The
          // refetch supplies cross-page totals and fills any resulting gaps.
          return {
            ...previous,
            items,
            total: Math.max(
              0,
              previous.total - (previous.items.length - items.length)
            ),
          }
        }
      )
      setRemove(null)
      table.resetRowSelection()
      void refresh()
    },
  })
  const columns: ColumnDef<Invitation>[] = [
    {
      id: 'select',
      header: ({ table }) => (
        <Checkbox
          aria-label={t('Select all')}
          checked={table.getIsAllPageRowsSelected()}
          indeterminate={table.getIsSomePageRowsSelected()}
          onCheckedChange={(value) => table.toggleAllPageRowsSelected(!!value)}
        />
      ),
      cell: ({ row }) => (
        <Checkbox
          aria-label={t('Select row')}
          checked={row.getIsSelected()}
          onCheckedChange={(value) => row.toggleSelected(!!value)}
        />
      ),
      enableSorting: false,
      enableHiding: false,
      size: 40,
    },
    { accessorKey: 'id', header: t('ID'), meta: { mobileHidden: true } },
    {
      accessorKey: 'name',
      header: t('Name'),
      meta: { mobileTitle: true },
      cell: ({ row }) => <span className='break-all'>{row.original.name}</span>,
    },
    {
      accessorKey: 'key',
      header: t('Invitation code'),
      cell: ({ row }) => (
        <MaskedValueDisplay
          label={t('Invitation code')}
          fullValue={row.original.key}
          maskedValue={`${row.original.key.slice(0, 4)}••••`}
          copyTooltip={t('Copy')}
          copyAriaLabel={t('Copy invitation code')}
        />
      ),
    },
    {
      accessorKey: 'status',
      header: t('Status'),
      cell: ({ row }) => {
        if (row.original.status === 3) return t('Used')
        if (row.original.status === 2) return t('Disabled')
        if (
          row.original.expired_time > 0 &&
          row.original.expired_time <= Date.now() / 1000
        ) {
          return t('Expired')
        }
        return t('Unused')
      },
    },
    {
      accessorKey: 'created_time',
      header: t('Created time'),
      cell: ({ row }) => formatTimestampToDate(row.original.created_time),
    },
    {
      accessorKey: 'expired_time',
      header: t('Expiration time'),
      cell: ({ row }) =>
        row.original.expired_time
          ? formatTimestampToDate(row.original.expired_time)
          : t('Never'),
    },
    {
      accessorKey: 'used_user_id',
      header: t('Used by'),
      cell: ({ row }) => row.original.used_user_id || '-',
    },
    {
      accessorKey: 'used_time',
      header: t('Used time'),
      cell: ({ row }) =>
        row.original.used_time
          ? formatTimestampToDate(row.original.used_time)
          : '-',
    },
    {
      id: 'actions',
      header: t('Actions'),
      cell: ({ row }) => (
        <div className='flex flex-wrap gap-1'>
          <Button
            variant='ghost'
            size='sm'
            disabled={row.original.status === 3 || update.isPending}
            onClick={() => setEditor(row.original)}
          >
            {t('Edit')}
          </Button>
          <Button
            variant='ghost'
            size='sm'
            disabled={row.original.status === 3 || update.isPending}
            onClick={() =>
              update.mutate({
                id: row.original.id,
                status: row.original.status === 1 ? 2 : 1,
              })
            }
          >
            {row.original.status === 1 ? t('Disable') : t('Enable')}
          </Button>
          <Button
            variant='ghost'
            size='sm'
            onClick={() => exportIds([row.original.id])}
          >
            {t('Download')}
          </Button>
          <Button
            variant='ghost'
            size='sm'
            onClick={() => setRemove([row.original.id])}
          >
            {t('Delete')}
          </Button>
        </div>
      ),
    },
  ]
  const { table } = useDataTable({
    data: query.data?.items ?? [],
    columns,
    enableRowSelection: true,
    getRowId: (row) => String(row.id),
    pagination: state.pagination,
    onPaginationChange: state.onPaginationChange,
    globalFilter: state.globalFilter,
    onGlobalFilterChange: state.onGlobalFilterChange,
    columnFilters: state.columnFilters,
    onColumnFiltersChange: state.onColumnFiltersChange,
    manualPagination: true,
    manualFiltering: true,
    totalCount: query.data?.total ?? 0,
    ensurePageInRange: state.ensurePageInRange,
  })
  const selected = table.getSelectedRowModel().rows.map((row) => row.original)
  return (
    <>
      <SectionPageLayout fixedContent>
        <SectionPageLayout.Title>
          {t('Invitation codes')}
        </SectionPageLayout.Title>
        <SectionPageLayout.Actions>
          <div className='flex flex-wrap gap-2'>
            <Button onClick={() => setEditor('new')}>
              {t('Generate invitation codes')}
            </Button>
            <Button variant='outline' onClick={() => setRemove('invalid')}>
              {t('Clean invalid codes')}
            </Button>
            <Button variant='outline' onClick={openHistory}>
              <History className='h-4 w-4' />
              {t('Create history')}
            </Button>
            <Button
              variant='outline'
              onClick={() =>
                exportFiltered({
                  keyword: state.globalFilter ?? '',
                  status,
                })
              }
            >
              <Download className='h-4 w-4' />
              {t('Export filtered')}
            </Button>
            <Button
              variant='outline'
              disabled={!query.data?.items.length}
              onClick={() =>
                exportIds((query.data?.items ?? []).map((row) => row.id))
              }
            >
              {t('Export current page')}
            </Button>
          </div>
        </SectionPageLayout.Actions>
        <SectionPageLayout.Content>
          {query.isError ? (
            <ErrorState
              title={t('Failed to load invitation codes')}
              onRetry={() => void query.refetch()}
            />
          ) : (
            <DataTablePage
              table={table}
              columns={columns}
              isLoading={query.isLoading}
              isFetching={query.isFetching}
              emptyTitle={t('No invitation codes found')}
              toolbarProps={{
                searchPlaceholder: t('Search by name or code'),
                searchDebounceMs: 300,
                filters: [
                  {
                    columnId: 'status',
                    title: t('Status'),
                    singleSelect: true,
                    options: [
                      { value: '1', label: t('Unused') },
                      { value: '2', label: t('Disabled') },
                      { value: '3', label: t('Used') },
                      { value: 'expired', label: t('Expired') },
                    ],
                  },
                ],
              }}
              mobileProps={{ enableRowSelection: true }}
              showMobileBulkActions
              bulkActions={
                <DataTableBulkActions
                  table={table}
                  entityName={t('Invitation codes')}
                >
                  <Button
                    variant='outline'
                    onClick={() => exportIds(selected.map((row) => row.id))}
                  >
                    {t('Export')}
                  </Button>
                  <Button
                    variant='destructive'
                    onClick={() => setRemove(selected.map((row) => row.id))}
                  >
                    {t('Delete')}
                  </Button>
                </DataTableBulkActions>
              }
            />
          )}
        </SectionPageLayout.Content>
      </SectionPageLayout>
      {editor && (
        <InvitationEditor
          invitation={editor === 'new' ? undefined : editor}
          onClose={() => setEditor(null)}
          onUpdated={(saved) => {
            // Patch the cached rows with the committed name/expiry immediately.
            // The single list refresh happens in onSaved, which also closes the
            // dialog, so an edit triggers exactly one invalidation and no second
            // PUT.
            patchInvitation(saved)
          }}
          onSaved={(result) => {
            setEditor(null)
            void refresh()
            // Route the committed batch through the shared dialog so a fresh
            // create and a recovered replay download through one path. The batch
            // identity is what the server re-reads under the actor binding, which
            // keeps key exposure limited to the operator who committed it.
            if (editor === 'new' && result.operationId) {
              exportBatch(result.operationId)
            }
          }}
        />
      )}
      <ConfirmDialog
        open={remove !== null}
        onOpenChange={(open) => {
          if (!open && !deletion.isPending) setRemove(null)
        }}
        title={t('Delete invitation codes')}
        desc={t('Deleted invitation codes cannot be restored')}
        destructive
        isLoading={deletion.isPending}
        handleConfirm={() => {
          if (remove) deletion.mutate(remove)
        }}
      />
    </>
  )
}
