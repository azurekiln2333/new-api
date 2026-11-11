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
import { render, screen } from '@testing-library/react'
import { expect, it, vi } from 'vitest'

import { api } from '@/lib/api'

import { CommonLogsStats } from '../common-logs-stats'
import { UsageLogsProvider } from '../usage-logs-provider'

async function renderStats(stat: Record<string, number>) {
  const get = vi.spyOn(api, 'get').mockImplementation(async () => ({
    data: { success: true, data: stat },
  }))
  const root = createRootRoute()
  const auth = createRoute({ getParentRoute: () => root, id: '_authenticated' })
  const logs = createRoute({
    getParentRoute: () => auth,
    path: '/usage-logs/$section',
    component: () => (
      <UsageLogsProvider>
        <CommonLogsStats />
      </UsageLogsProvider>
    ),
    validateSearch: (search: Record<string, unknown>) => search,
  })
  const router = createRouter({
    routeTree: root.addChildren([auth.addChildren([logs])]),
    history: createMemoryHistory({ initialEntries: ['/usage-logs/common'] }),
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
  await screen.findByText('Cache Hit Rate')
  return get
}

it('shows the cache-read share of the input tokens next to TPM', async () => {
  const get = await renderStats({
    quota: 100,
    rpm: 3,
    tpm: 900,
    cache_read_tokens: 600,
    input_tokens: 1000,
  })

  expect(screen.getByText('60.0%')).toBeVisible()
  const statUrls = get.mock.calls.map((call) => String(call[0]))
  expect(statUrls.some((url) => url.startsWith('/api/log/self/stat'))).toBe(
    true
  )
})

it('shows a dash when the filtered logs report no input tokens', async () => {
  await renderStats({ quota: 0, rpm: 0, tpm: 0 })

  expect(screen.getByText('Cache Hit Rate')).toBeVisible()
  expect(screen.getByText('-')).toBeVisible()
})
