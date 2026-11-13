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
import { cleanup, render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { createInstance } from 'i18next'
import { I18nextProvider } from 'react-i18next'
import { afterEach, beforeEach, expect, test, vi } from 'vitest'

import { api } from '@/lib/api'

import type { Channel } from '../../types'
import { ChannelsProvider } from '../channels-provider'
import { ChannelsTable } from '../channels-table'

const i18n = createInstance()
await i18n.init({
  lng: 'en',
  resources: { en: { translation: {} } },
  initAsync: false,
})
const clients: QueryClient[] = []

function channel(baseUrl: string | null): Channel {
  return {
    id: 3,
    type: 1,
    key: '',
    status: 1,
    name: 'prod',
    created_time: 0,
    test_time: 0,
    response_time: 0,
    other: '',
    balance: 0,
    balance_updated_time: 0,
    base_url: baseUrl,
    models: 'gpt-4o',
    group: 'vip',
    used_quota: 0,
    other_info: '',
    remark: '',
    max_input_tokens: 0,
    channel_info: {
      is_multi_key: false,
      multi_key_size: 0,
      multi_key_polling_index: 0,
      multi_key_mode: 'random',
    },
    settings: '{}',
  }
}

beforeEach(() => {
  vi.spyOn(window, 'scrollTo').mockImplementation(() => {})
})

afterEach(() => {
  cleanup()
  localStorage.clear()
  clients.splice(0).forEach((client) => client.clear())
  vi.restoreAllMocks()
})

async function renderChannelsPage(params: {
  baseUrl: string | null
  viewMode: 'table' | 'card'
}) {
  localStorage.setItem('channels:view-mode', params.viewMode)
  vi.spyOn(api, 'get').mockImplementation(async (url) => {
    if (url === '/api/channel/search' || url === '/api/channel') {
      return {
        data: {
          success: true,
          data: { items: [channel(params.baseUrl)], total: 1, type_counts: {} },
        },
      }
    }
    return { data: { success: true, data: ['vip'] } }
  })
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  clients.push(client)
  const root = createRootRoute()
  const auth = createRoute({ getParentRoute: () => root, id: '_authenticated' })
  const channelsRoute = createRoute({
    getParentRoute: () => auth,
    path: 'channels/',
    component: () => (
      <ChannelsProvider>
        <ChannelsTable />
      </ChannelsProvider>
    ),
  })
  const router = createRouter({
    routeTree: root.addChildren([auth.addChildren([channelsRoute])]),
    history: createMemoryHistory({ initialEntries: ['/channels/'] }),
  })
  await router.load()
  render(
    <I18nextProvider i18n={i18n}>
      <QueryClientProvider client={client}>
        <RouterProvider router={router} />
      </QueryClientProvider>
    </I18nextProvider>
  )
  expect((await screen.findAllByText('prod')).length).toBeGreaterThan(0)
}

test('card view opens the channel address in a new tab without a window opener', async () => {
  const open = vi.spyOn(window, 'open').mockReturnValue(null)
  const user = userEvent.setup()
  await renderChannelsPage({
    baseUrl: 'https://upstream.example/v1',
    viewMode: 'card',
  })

  await user.click(screen.getByRole('button', { name: 'Open Channel Address' }))

  expect(open).toHaveBeenCalledWith(
    'https://upstream.example/v1',
    '_blank',
    'noopener,noreferrer'
  )
})

test.each([
  { baseUrl: null, why: 'no address' },
  { baseUrl: '   ', why: 'blank address' },
  { baseUrl: 'doubao-coding-plan', why: 'placeholder instead of a URL' },
  { baseUrl: 'ftp://upstream.example', why: 'unsupported scheme' },
])('card view hides the address button for $why', async ({ baseUrl }) => {
  await renderChannelsPage({ baseUrl, viewMode: 'card' })

  expect(
    screen.queryByRole('button', { name: 'Open Channel Address' })
  ).toBeNull()
})

test('table view keeps the plain row without the address button', async () => {
  await renderChannelsPage({
    baseUrl: 'https://upstream.example',
    viewMode: 'table',
  })

  expect(
    screen.queryByRole('button', { name: 'Open Channel Address' })
  ).toBeNull()
})
