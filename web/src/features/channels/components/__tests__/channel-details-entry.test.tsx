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
import { afterEach, beforeEach, expect, it, vi } from 'vitest'

import { api } from '@/lib/api'
import { formatCompactNumber } from '@/lib/format'
import { useAuthStore } from '@/stores/auth-store'

import { CHANNEL_STATUS } from '../../constants'
import type { Channel } from '../../types'
import { ChannelsProvider, useChannels } from '../channels-provider'
import { ChannelsTable } from '../channels-table'

const i18n = createInstance()
await i18n.init({
  lng: 'en',
  resources: { en: { translation: {} } },
  initAsync: false,
})
const clients: QueryClient[] = []

const CONSUMED_TOKENS = 1234567
const VIEW_MODE_STORAGE_KEY = 'channels:view-mode'
const CARD_SELECTOR = '[data-slot=data-table-card]'

const channel: Channel = {
  id: 3,
  type: 1,
  key: '',
  status: CHANNEL_STATUS.ENABLED,
  name: 'prod',
  created_time: 0,
  test_time: 0,
  response_time: 0,
  other: '',
  balance: 0,
  balance_updated_time: 0,
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

function DialogProbe() {
  const { open, currentRow } = useChannels()
  return (
    <div role='status' aria-label='Dialog state'>
      {`${open ?? 'none'}:${currentRow?.id ?? '-'}`}
    </div>
  )
}

/** Stand-in for the real drawer, so the entry test stays on list behaviour. */
function DetailsProbe() {
  const { open, currentRow } = useChannels()
  if (open !== 'channel-details' || !currentRow) {
    return null
  }
  return (
    <div role='dialog' aria-label='Channel details'>
      {currentRow.name}
    </div>
  )
}

beforeEach(() => {
  vi.spyOn(window, 'scrollTo').mockImplementation(() => {})
  localStorage.clear()
  // Row actions are permission-aware; sign in as a super admin for the table.
  useAuthStore.getState().auth.setUser({ id: 1, username: 'admin', role: 100 })
})

afterEach(() => {
  cleanup()
  localStorage.clear()
  useAuthStore.getState().auth.setUser(null)
  clients.splice(0).forEach((client) => client.clear())
  vi.restoreAllMocks()
})

/**
 * Renders the channel table the way the channels page does. `usage` is the
 * per-channel token aggregate returned by the batch usage endpoint; pass
 * `null` to simulate a channel without a usage record.
 */
async function renderChannelsPage(
  usage: { total_tokens: number } | null = { total_tokens: CONSUMED_TOKENS }
) {
  const get = vi.spyOn(api, 'get').mockImplementation(async (url) => {
    if (url === '/api/channel') {
      return {
        data: {
          success: true,
          data: { items: [channel], total: 1, type_counts: {} },
        },
      }
    }
    if (url === '/api/channel/usage') {
      return {
        data: {
          success: true,
          data: {
            items: usage
              ? [
                  {
                    channel_id: channel.id,
                    request_count: 12,
                    prompt_tokens: 1000000,
                    completion_tokens: 234567,
                    total_tokens: usage.total_tokens,
                    quota: 0,
                  },
                ]
              : [],
          },
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
        <DialogProbe />
        <DetailsProbe />
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
  return get
}

/** Opens the card view, leaving the table data to render afterwards. */
async function renderCardView(usage?: { total_tokens: number } | null) {
  localStorage.setItem(VIEW_MODE_STORAGE_KEY, 'card')
  await renderChannelsPage(usage)
  const card = document.querySelector(CARD_SELECTOR)
  expect(card).not.toBeNull()
  return card as HTMLElement
}

it('shows the tokens consumed through a channel', async () => {
  await renderChannelsPage()

  expect(
    await screen.findByText(formatCompactNumber(CONSUMED_TOKENS, 'en'))
  ).toBeVisible()
})

it('shows a dash instead of tokens when the channel has no usage record', async () => {
  const card = await renderCardView(null)

  expect(card.textContent).toContain('-')
  expect(
    screen.queryByText(formatCompactNumber(CONSUMED_TOKENS, 'en'))
  ).not.toBeInTheDocument()
})

it('opens the channel details when the channel card body is clicked', async () => {
  const card = await renderCardView()

  await userEvent.setup().click(card)

  expect(
    screen.getByRole('status', { name: 'Dialog state' })
  ).toHaveTextContent('channel-details:3')
  expect(
    screen.getByRole('dialog', { name: 'Channel details' })
  ).toHaveTextContent('prod')
})

it('keeps a click on an inner action button from opening the channel details', async () => {
  const card = await renderCardView()

  await userEvent.setup().click(card.querySelector('button') as HTMLElement)

  expect(
    screen.getByRole('status', { name: 'Dialog state' })
  ).not.toHaveTextContent('channel-details')
  expect(
    screen.queryByRole('dialog', { name: 'Channel details' })
  ).not.toBeInTheDocument()
})

it('opens the channel details when the channel name button is activated by keyboard', async () => {
  await renderChannelsPage()
  const user = userEvent.setup()

  screen.getByRole('button', { name: 'prod' }).focus()
  await user.keyboard('{Enter}')

  expect(
    screen.getByRole('status', { name: 'Dialog state' })
  ).toHaveTextContent('channel-details:3')
  expect(
    screen.getByRole('dialog', { name: 'Channel details' })
  ).toHaveTextContent('prod')
})
