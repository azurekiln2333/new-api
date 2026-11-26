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
import type { ColumnDef, PaginationState } from '@tanstack/react-table'
import { useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { DataTablePage, useDataTable } from '@/components/data-table'
import { ErrorState } from '@/components/error-state'
import { TableId } from '@/components/table-id'
import { useIsAdmin } from '@/hooks/use-admin'
import {
  createServerError,
  getServerErrorMessage,
} from '@/lib/server-error-message'
import { useAuthStore } from '@/stores/auth-store'

import { getEffectiveSubscriptionUsers } from '../api'
import { formatTimestamp } from '../lib'
import type { EffectiveSubscriptionUser } from '../types'

export function ActiveUsersTable() {
  const { t } = useTranslation()
  const user = useAuthStore((state) => state.auth.user)
  const sessionID = useAuthStore((state) => state.auth.session?.sid)
  const isAdmin = useIsAdmin()
  const [pagination, setPagination] = useState<PaginationState>({
    pageIndex: 0,
    pageSize: 20,
  })

  // Same-session role refreshes do not clear the global cache. Partition this
  // admin-only payload by the live viewer and never carry placeholders across.
  const query = useQuery({
    queryKey: [
      'admin-effective-subscriptions',
      user?.id,
      sessionID,
      user?.role,
      pagination.pageIndex,
      pagination.pageSize,
    ],
    enabled: isAdmin,
    queryFn: async ({ signal }) => {
      const assertCurrentAdmin = () => {
        signal.throwIfAborted()
        const current = useAuthStore.getState().auth
        if (
          !isAdmin ||
          current.user?.id !== user?.id ||
          current.user?.role !== user?.role ||
          current.session?.sid !== sessionID
        ) {
          throw new DOMException('Aborted', 'AbortError')
        }
      }
      assertCurrentAdmin()
      const response = await getEffectiveSubscriptionUsers(
        {
          p: pagination.pageIndex + 1,
          page_size: pagination.pageSize,
        },
        signal
      )
      // Fence transports that settle after cancellation or an auth refresh.
      assertCurrentAdmin()
      if (!response.success || !response.data) {
        throw createServerError(response, t('Operation failed'))
      }
      return response.data
    },
    retry: false,
  })
  const page = isAdmin ? query.data : undefined

  const columns = useMemo<ColumnDef<EffectiveSubscriptionUser>[]>(
    () => [
      {
        accessorKey: 'user_id',
        header: t('User'),
        cell: ({ row }) => (
          <div className='min-w-0'>
            <div className='truncate font-medium'>
              {row.original.username || `#${row.original.user_id}`}
            </div>
            {row.original.display_name &&
              row.original.display_name !== row.original.username && (
                <div className='text-muted-foreground truncate text-xs'>
                  {row.original.display_name}
                </div>
              )}
          </div>
        ),
        size: 220,
        meta: { mobileTitle: true },
      },
      {
        accessorKey: 'plan_id',
        header: t('Plan'),
        cell: ({ row }) => (
          <div className='min-w-0'>
            <div className='truncate font-medium'>
              {row.original.plan_title || `#${row.original.plan_id}`}
            </div>
            <div className='text-muted-foreground text-xs'>
              {t('ID')}: {row.original.plan_id}
            </div>
          </div>
        ),
        size: 220,
      },
      {
        accessorKey: 'start_time',
        header: t('Start'),
        cell: ({ row }) => (
          <span className='text-sm whitespace-nowrap'>
            {formatTimestamp(row.original.start_time)}
          </span>
        ),
        size: 180,
      },
      {
        accessorKey: 'end_time',
        header: t('End'),
        cell: ({ row }) => (
          <span className='text-sm whitespace-nowrap'>
            {formatTimestamp(row.original.end_time)}
          </span>
        ),
        size: 180,
      },
      {
        accessorKey: 'subscription_id',
        header: t('ID'),
        cell: ({ row }) => <TableId value={row.original.subscription_id} />,
        size: 80,
        meta: { mobileHidden: true },
      },
    ],
    [t]
  )

  const { table } = useDataTable({
    data: page?.items ?? [],
    columns,
    totalCount: page?.total ?? 0,
    getRowId: (row) => String(row.subscription_id),
    pagination,
    onPaginationChange: setPagination,
    manualPagination: true,
    manualFiltering: true,
    columnFilters: [],
    columnVisibilityStorageKey: false,
    columnSizingStorageKey: false,
    ensurePageInRange: (pageCount) => {
      if (
        isAdmin &&
        query.isSuccess &&
        !query.isFetching &&
        pagination.pageIndex >= Math.max(1, pageCount)
      ) {
        setPagination((previous) => ({
          ...previous,
          pageIndex: Math.max(0, pageCount - 1),
        }))
      }
    },
  })

  if (!isAdmin) return null

  if (query.isError) {
    const description =
      query.error instanceof Error
        ? query.error.message
        : getServerErrorMessage(query.error, t('Operation failed'))
    return (
      <ErrorState
        description={description}
        onRetry={() => void query.refetch()}
      />
    )
  }

  return (
    <DataTablePage
      table={table}
      columns={columns}
      isLoading={query.isLoading}
      isFetching={query.isFetching}
      emptyTitle={t('No subscription records')}
      emptyDescription={t('No subscription records')}
      skeletonKeyPrefix='effective-subscriptions-skeleton'
      applyHeaderSize
    />
  )
}
