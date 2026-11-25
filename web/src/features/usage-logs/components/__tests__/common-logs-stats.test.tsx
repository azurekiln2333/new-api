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
import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { expect, it, vi } from 'vitest'

import { api } from '@/lib/api'

import { CommonLogsStats } from '../common-logs-stats'
import { UsageLogsProvider } from '../usage-logs-provider'

async function renderStats(
  stat: Record<string, number>,
  options: { mobile?: boolean; pending?: boolean } = {}
) {
  if (options.mobile) {
    const matchMedia = window.matchMedia
    vi.spyOn(window, 'matchMedia').mockImplementation((query) => ({
      ...matchMedia(query),
      matches: query === '(max-width: 640px)' || matchMedia(query).matches,
    }))
  }
  const get = vi.spyOn(api, 'get').mockImplementation(async () => {
    if (options.pending) return new Promise<never>(() => {})
    return { data: { success: true, data: stat } }
  })
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
  if (options.pending) {
    await screen.findByRole('group', { name: 'Usage', busy: true })
  } else if (options.mobile) {
    await screen.findByRole('button', { name: /^Cache Hit Rate / })
  } else {
    await screen.findByText('Cache Hit Rate')
  }
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
  expect(screen.getByText('Cache Hit Rate')).toBeVisible()
  expect(
    screen.queryByRole('button', { name: /^Cache Hit Rate/ })
  ).not.toBeInTheDocument()
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

it.each([
  { mobile: false, viewport: 'desktop' },
  { mobile: true, viewport: 'mobile' },
])(
  'keeps exact large token throughput in the $viewport stats row',
  async (testCase) => {
    await renderStats(
      { quota: 0, rpm: 1, tpm: 318574217 },
      { mobile: testCase.mobile }
    )
    const count = screen.getByText('318,574,217')
    expect(count).toBeVisible()
    expect(count.parentElement).toHaveClass(
      'h-7',
      'shrink-0',
      'whitespace-nowrap'
    )
    expect(count.parentElement).not.toHaveClass('flex-wrap', 'truncate')
    const row = screen.getByRole('group', { name: 'Usage' })
    expect(row).toHaveClass(
      'flex-nowrap',
      'overflow-x-auto',
      'sm:flex-wrap',
      'sm:overflow-visible'
    )
    expect(row.children).toHaveLength(4)
  }
)

it('keeps the mobile cache label icon-only and exposes its meaning and inputs on tap', async () => {
  await renderStats(
    {
      quota: 100,
      rpm: 3,
      tpm: 900,
      cache_read_tokens: 600,
      input_tokens: 1000,
    },
    { mobile: true }
  )
  const user = userEvent.setup()
  const cache = screen.getByRole('button', { name: 'Cache Hit Rate 60.0%' })

  expect(cache).toHaveTextContent('60.0%')
  expect(within(cache).queryByText('Cache')).not.toBeInTheDocument()
  expect(within(cache).queryByText('Cache Hit Rate')).not.toBeInTheDocument()
  expect(cache.querySelector('svg')).toHaveAttribute('aria-hidden', 'true')
  expect(cache.parentElement).toHaveClass('shrink-0')
  expect(screen.queryByText('Cache Hit Rate')).not.toBeInTheDocument()

  await user.click(cache)

  const explanation = await screen.findByRole('dialog', {
    name: 'Cache Hit Rate',
  })
  expect(within(explanation).getByText('Cache Read')).toBeVisible()
  expect(within(explanation).getByText('600')).toBeVisible()
  expect(within(explanation).getByText('Input Tokens')).toBeVisible()
  expect(within(explanation).getByText('1,000')).toBeVisible()
  expect(cache).toHaveAttribute('aria-expanded', 'true')
})

it('opens the cache explanation from keyboard focus and returns focus on Escape', async () => {
  await renderStats({ quota: 0, rpm: 0, tpm: 0 }, { mobile: true })
  const user = userEvent.setup()
  const cache = screen.getByRole('button', { name: 'Cache Hit Rate -' })

  await user.tab()
  expect(cache).toHaveFocus()
  await user.keyboard('{Enter}')
  expect(
    await screen.findByRole('dialog', { name: 'Cache Hit Rate' })
  ).toBeVisible()

  await user.keyboard('{Escape}')
  await waitFor(() => expect(cache).toHaveFocus())
  expect(cache).toHaveAttribute('aria-expanded', 'false')
})

it('keeps all four loading placeholders on the same mobile scrolling row', async () => {
  await renderStats({}, { mobile: true, pending: true })
  const row = screen.getByRole('group', { name: 'Usage', busy: true })

  expect(row).toHaveClass('flex-nowrap', 'overflow-x-auto')
  const placeholders = row.querySelectorAll('[data-slot=skeleton]')
  expect(placeholders).toHaveLength(4)
  for (const placeholder of placeholders) {
    expect(placeholder).toHaveClass('h-7', 'shrink-0')
  }
})
