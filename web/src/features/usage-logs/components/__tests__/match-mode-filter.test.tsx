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
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import {
  createMemoryHistory,
  createRootRoute,
  createRoute,
  createRouter,
  RouterProvider,
} from '@tanstack/react-router'
import { getCoreRowModel, useReactTable } from '@tanstack/react-table'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { expect, it, vi } from 'vitest'

import { api } from '@/lib/api'

import { CommonLogsFilterBar } from '../common-logs-filter-bar'
import { UsageLogsProvider } from '../usage-logs-provider'

function Fixture() {
  const table = useReactTable({
    data: [],
    columns: [],
    getCoreRowModel: getCoreRowModel(),
  })
  return (
    <UsageLogsProvider>
      <CommonLogsFilterBar table={table} />
    </UsageLogsProvider>
  )
}

async function renderFilterBar(initialEntry: string) {
  const get = vi.spyOn(api, 'get').mockImplementation(async (url) => ({
    data: {
      success: true,
      data: url === '/api/user/self/groups' ? {} : { quota: 0, rpm: 0, tpm: 0 },
    },
  }))
  const root = createRootRoute()
  const auth = createRoute({ getParentRoute: () => root, id: '_authenticated' })
  const logs = createRoute({
    getParentRoute: () => auth,
    path: '/usage-logs/$section',
    component: Fixture,
    validateSearch: (search: Record<string, unknown>) => search,
  })
  const router = createRouter({
    routeTree: root.addChildren([auth.addChildren([logs])]),
    history: createMemoryHistory({ initialEntries: [initialEntry] }),
  })
  render(
    <QueryClientProvider
      client={
        new QueryClient({ defaultOptions: { queries: { retry: false } } })
      }
    >
      <RouterProvider router={router} />
    </QueryClientProvider>
  )
  await screen.findByRole('button', { name: 'Contains' })
  return { router, get }
}

it('defaults to contains matching and asks the statistics for fuzzy matching', async () => {
  const { get } = await renderFilterBar('/usage-logs/common')

  expect(screen.getByRole('button', { name: 'Contains' })).toHaveAttribute(
    'aria-pressed',
    'true'
  )
  expect(screen.getByRole('button', { name: 'Exact' })).toHaveAttribute(
    'aria-pressed',
    'false'
  )
  await waitFor(() =>
    expect(
      get.mock.calls
        .map((call) => String(call[0]))
        .some((url) => url.includes('match_mode=fuzzy'))
    ).toBe(true)
  )
})

it('records exact matching in the URL and asks for exact matching after search', async () => {
  const { router, get } = await renderFilterBar('/usage-logs/common')
  const user = userEvent.setup()

  await user.click(screen.getByRole('button', { name: 'Exact' }))
  expect(screen.getByRole('button', { name: 'Exact' })).toHaveAttribute(
    'aria-pressed',
    'true'
  )
  await user.click(screen.getByRole('button', { name: 'Search' }))

  await waitFor(() =>
    expect(router.state.location.search).toMatchObject({
      page: 1,
      match: 'exact',
    })
  )
  await waitFor(() =>
    expect(
      get.mock.calls
        .map((call) => String(call[0]))
        .some((url) => url.includes('match_mode=exact'))
    ).toBe(true)
  )
})

it('restores exact matching from the URL', async () => {
  await renderFilterBar('/usage-logs/common?match=exact')

  expect(screen.getByRole('button', { name: 'Exact' })).toHaveAttribute(
    'aria-pressed',
    'true'
  )
  expect(screen.getByRole('button', { name: 'Contains' })).toHaveAttribute(
    'aria-pressed',
    'false'
  )
})
