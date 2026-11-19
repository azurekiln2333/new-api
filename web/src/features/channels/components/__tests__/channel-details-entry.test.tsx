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
import { cleanup, render, screen, waitFor, within } from '@testing-library/react'
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

it('opens the channel details when content inside the channel card body is clicked', async () => {
  const card = await renderCardView()

  // Click plain card content, not the outer grid wrapper: the card body is a
  // [role=button] itself and must not be mistaken for an inner control.
  await userEvent.setup().click(within(card).getByText('Used tokens'))

  expect(
    screen.getByRole('status', { name: 'Dialog state' })
  ).toHaveTextContent('channel-details:3')
  expect(
    screen.getByRole('dialog', { name: 'Channel details' })
  ).toHaveTextContent('prod')
})

it('keeps a click on an inner action button from opening the channel details', async () => {
  const card = await renderCardView()

  await userEvent
    .setup()
    .click(within(card).getByRole('button', { name: 'Open menu' }))

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

it('commits the inline priority on Enter without opening the channel details', async () => {
  const put = vi
    .spyOn(api, 'put')
    .mockResolvedValue({ data: { success: true, data: {} } } as never)
  const card = await renderCardView()
  const user = userEvent.setup()
  const priorityCell = card.querySelector<HTMLElement>(
    '[data-column-id="priority"]'
  )
  expect(priorityCell).not.toBeNull()
  const cell = within(priorityCell as HTMLElement)

  // Click the idle value to start inline editing, then commit with Enter: the
  // drawer must stay closed while the spinner keeps the key it handled.
  await user.click(cell.getByRole('button', { name: '0' }))
  expect(cell.getByRole('textbox')).toHaveFocus()

  await user.clear(cell.getByRole('textbox'))
  await user.type(cell.getByRole('textbox'), '42')
  await user.keyboard('{Enter}')

  await waitFor(() => expect(put).toHaveBeenCalled())
  expect(put).toHaveBeenCalledWith(
    '/api/channel/',
    expect.objectContaining({ id: 3, priority: 42 }),
    expect.anything()
  )
  expect(
    screen.getByRole('status', { name: 'Dialog state' })
  ).toHaveTextContent('none:-')
  expect(
    screen.queryByRole('dialog', { name: 'Channel details' })
  ).not.toBeInTheDocument()
})

it('opens the channel details from the focused card body by keyboard', async () => {
  const card = await renderCardView()
  const user = userEvent.setup()
  const body = card.querySelector<HTMLElement>(
    '[role=button][data-activates-channel-details]'
  )
  expect(body).not.toBeNull()
  body?.focus()
  expect(body).toHaveFocus()

  await user.keyboard('{Enter}')

  expect(
    screen.getByRole('status', { name: 'Dialog state' })
  ).toHaveTextContent('channel-details:3')
  expect(
    screen.getByRole('dialog', { name: 'Channel details' })
  ).toHaveTextContent('prod')
})

it('opens the channel details from the focused card body with Space', async () => {
  const card = await renderCardView()
  const user = userEvent.setup()
  card
    .querySelector<HTMLElement>('[role=button][data-activates-channel-details]')
    ?.focus()

  await user.keyboard(' ')

  expect(
    screen.getByRole('status', { name: 'Dialog state' })
  ).toHaveTextContent('channel-details:3')
})

it('pairs the card metrics into usage, priority/weight and response rows', async () => {
  const card = await renderCardView()
  const rows = [
    ...card.querySelectorAll<HTMLElement>('[data-card-metric-row]'),
  ]
  expect(rows.map((row) => row.dataset.cardMetricRow)).toEqual([
    'usage',
    'priority',
    'response',
  ])

  // Each row is a description list whose terms and values stay direct
  // siblings, so the label/value pairing survives assistive technology.
  for (const row of rows) {
    expect(row.tagName).toBe('DL')
    for (const term of row.querySelectorAll('dt')) {
      expect(term.parentElement?.querySelector(':scope > dd')).not.toBeNull()
    }
  }

  // Used amount and used tokens share one row, in that order.
  const usageLabels = [...rows[0].querySelectorAll('dt')].map(
    (label) => label.textContent
  )
  expect(usageLabels).toEqual(['Used / Remaining', 'Used tokens'])

  // Priority and weight are side by side in the same row.
  const priorityLabels = [...rows[1].querySelectorAll('dt')].map(
    (label) => label.textContent
  )
  expect(priorityLabels).toEqual(['Priority', 'Weight'])
  expect(
    rows[1].querySelectorAll('[data-slot="numeric-spinner-value"]')
  ).toHaveLength(2)
  expect(rows[1].querySelector('[data-column-id="priority"]')).not.toBeNull()

  // Response metrics stay in their own compact row.
  const responseLabels = [...rows[2].querySelectorAll('dt')].map(
    (label) => label.textContent
  )
  expect(responseLabels).toEqual(['Response', 'Last Tested'])
})

it('lets each card metric row wrap instead of overflowing a narrow card', async () => {
  const card = await renderCardView()
  const rows = card.querySelectorAll<HTMLElement>('[data-card-metric-row]')
  expect(rows).toHaveLength(3)
  for (const row of rows) {
    // Wrapping is the narrow-width fallback: pair members stack instead of
    // pushing past the card edge.
    expect(row.className).toContain('flex-wrap')
    expect(row.className).toContain('min-w-0')
  }
})

it('keeps the channel group out of the card and leaves it to the details', async () => {
  const card = await renderCardView()

  expect(within(card).queryByText('vip')).not.toBeInTheDocument()
  expect(card.querySelector('[data-group-model-row]')).toBeNull()
})
