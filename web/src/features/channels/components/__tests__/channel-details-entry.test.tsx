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
import {
  cleanup,
  render,
  screen,
  waitFor,
  within,
} from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { createInstance } from 'i18next'
import { I18nextProvider } from 'react-i18next'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'

import { api } from '@/lib/api'
import {
  formatCompactNumber,
  formatTimestampToDate,
  formatTokens,
} from '@/lib/format'
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
const TEST_TIME = 1_700_000_000
const VIEW_MODE_STORAGE_KEY = 'channels:view-mode'
const CARD_SELECTOR = '[data-slot=data-table-card]'

const channel: Channel = {
  id: 3,
  type: 1,
  key: '',
  status: CHANNEL_STATUS.ENABLED,
  name: 'prod',
  created_time: 0,
  test_time: TEST_TIME,
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
  usage: { total_tokens: number } | null = { total_tokens: CONSUMED_TOKENS },
  items: Channel[] = [channel],
  budget: { enabled: boolean } = { enabled: false }
) {
  const get = vi.spyOn(api, 'get').mockImplementation(async (url) => {
    if (url === '/api/channel') {
      return {
        data: {
          success: true,
          data: { items, total: items.length, type_counts: {} },
        },
      }
    }
    if (url === '/api/channel/quota') {
      return {
        data: {
          success: true,
          data: {
            items: items.map((item) => ({
              scope_type: 'channel',
              scope_id: String(item.id),
              channel_id: item.id,
              ledger_enabled: true,
              enabled: budget.enabled,
              limit_quota: budget.enabled ? 5_000_000 : 0,
              period_used_quota: 120,
              lifetime_used_quota: 250,
              reserved_quota: 10,
              remaining_quota: budget.enabled ? 4_000_000 : null,
              config_version: 1,
              reset_epoch: 0,
              accounting_started_at: 1,
            })),
          },
        },
      }
    }
    if (url === '/api/channel/concurrency') {
      return {
        data: {
          success: true,
          data: {
            items: [
              {
                id: channel.id,
                current: 2,
                limit: 5,
                scope: 'instance',
                sample_time: 1_700_000_000_000,
              },
            ],
          },
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

/** Opens the card view with an enabled local budget. */
async function renderCardViewWithEnabledBudget() {
  localStorage.setItem(VIEW_MODE_STORAGE_KEY, 'card')
  await renderChannelsPage(undefined, [channel], { enabled: true })
  const card = document.querySelector(CARD_SELECTOR)
  expect(card).not.toBeNull()
  return card as HTMLElement
}

it('loads both local budgets from one page request, separately from upstream balance', async () => {
  localStorage.setItem(VIEW_MODE_STORAGE_KEY, 'card')
  const get = await renderChannelsPage(
    null,
    [channel, { ...channel, id: 9, name: 'stage' }],
    { enabled: true }
  )
  const budgets = await screen.findAllByRole('button', { name: 'Local Budget' })
  expect(budgets).toHaveLength(2)
  expect(
    get.mock.calls.filter(([url]) => url === '/api/channel/quota')
  ).toEqual([
    ['/api/channel/quota', expect.objectContaining({ params: { ids: '3,9' } })],
  ])
  await userEvent.setup().click(budgets[0])
  const budget = await screen.findByRole('dialog', { name: 'Local Budget' })
  expect(within(budget).getByText('Reserved')).toBeVisible()
  expect(within(budget).getByText('Lifetime used')).toBeVisible()
  expect(
    screen.getByRole('status', { name: 'Dialog state' })
  ).toHaveTextContent('none:')
})

it('shows the tokens consumed through a channel as a single compact line', async () => {
  const card = await renderCardView()

  // The card keeps the readable abbreviation only; the exact count and its
  // parenthesized explanation belong to hover and the details drawer.
  expect(
    await within(card).findByText(formatCompactNumber(CONSUMED_TOKENS, 'en'))
  ).toBeVisible()
  expect(
    within(card).queryByText(formatTokens(CONSUMED_TOKENS, 'en'))
  ).not.toBeInTheDocument()
})

it('keeps the exact consumed token count and abbreviation visible in table view', async () => {
  localStorage.setItem(VIEW_MODE_STORAGE_KEY, 'table')
  await renderChannelsPage()

  expect(
    await screen.findByText(formatTokens(CONSUMED_TOKENS, 'en'))
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

it('lays the card metrics out in two columns above a separate priority/weight row', async () => {
  const card = await renderCardView()
  const layout = card.querySelector<HTMLElement>(
    '[data-card-metric-layout="main"]'
  )
  expect(layout).not.toBeNull()

  const columns = [
    ...(layout?.querySelectorAll<HTMLElement>('[data-card-metric-column]') ??
      []),
  ]
  expect(columns.map((column) => column.dataset.cardMetricColumn)).toEqual([
    'usage',
    'state',
  ])

  // Each column is a description list whose terms and values stay direct
  // siblings, so the label/value pairing survives assistive technology.
  for (const column of columns) {
    expect(column.tagName).toBe('DL')
    for (const term of column.querySelectorAll('dt')) {
      expect(term.parentElement?.querySelector(':scope > dd')).not.toBeNull()
    }
  }

  // Left column: used tokens above the shared quota region.
  expect(
    [...columns[0].querySelectorAll('dt')].map((label) => label.textContent)
  ).toEqual(['Used tokens', 'Remaining / Total'])

  // Right column: response and last tested. Concurrency stays by the name.
  expect(
    [...columns[1].querySelectorAll('dt')].map((label) => label.textContent)
  ).toEqual(['Response', 'Last Tested'])

  // Priority and weight own a separate row below, side by side.
  const priorityRow = card.querySelector<HTMLElement>(
    '[data-card-metric-row="priority"]'
  )
  expect(priorityRow?.tagName).toBe('DL')
  expect(
    [...(priorityRow?.querySelectorAll('dt') ?? [])].map(
      (label) => label.textContent
    )
  ).toEqual(['Priority', 'Weight'])
  expect(
    priorityRow?.querySelectorAll('[data-slot="numeric-spinner-value"]')
  ).toHaveLength(2)
  expect(
    priorityRow?.querySelector('[data-column-id="priority"]')
  ).not.toBeNull()
})

it('lets the metric columns wrap instead of overflowing a narrow card', async () => {
  const card = await renderCardView()
  const layout = card.querySelector<HTMLElement>(
    '[data-card-metric-layout="main"]'
  )
  expect(layout?.className).toContain('flex-wrap')
  expect(layout?.className).toContain('min-w-0')

  for (const column of layout?.querySelectorAll<HTMLElement>(
    '[data-card-metric-column]'
  ) ?? []) {
    expect(column.className).toContain('min-w-0')
  }

  // The priority/weight row also wraps rather than pushing past the edge.
  const priorityRow = card.querySelector<HTMLElement>(
    '[data-card-metric-row="priority"]'
  )
  expect(priorityRow?.className).toContain('flex-wrap')
  expect(priorityRow?.className).toContain('min-w-0')
})

it('keeps the channel group out of the card and leaves it to the details', async () => {
  const card = await renderCardView()

  expect(within(card).queryByText('vip')).not.toBeInTheDocument()
  expect(card.querySelector('[data-group-model-row]')).toBeNull()
})

it('shows the live concurrency of the card from one batched page request', async () => {
  const card = await renderCardView()

  expect(await within(card).findByText('2/5')).toBeVisible()
  const concurrencyCalls = vi
    .mocked(api.get)
    .mock.calls.filter(([url]) => url === '/api/channel/concurrency')
  expect(concurrencyCalls).toHaveLength(1)
  expect(concurrencyCalls[0][1]).toMatchObject({
    params: { ids: String(channel.id) },
  })
})

it('renders an unavailable concurrency sample as unknown instead of zero', async () => {
  const card = await renderCardView()
  vi.mocked(api.get).mockImplementation(async (url) => {
    if (url === '/api/channel/concurrency') {
      return {
        data: {
          success: true,
          data: {
            items: [
              {
                id: channel.id,
                current: null,
                limit: 5,
                scope: 'cluster',
                sample_time: 1_700_000_000_000,
              },
            ],
          },
        },
      }
    }
    return { data: { success: true, data: [] } }
  })

  // Force one refresh with the unavailable payload.
  await userEvent.setup().click(await within(card).findByText('2/5'))
  await waitFor(() => expect(card.textContent).toContain('Unavailable'))
})

it('reuses the card sample when the same channel details are opened', async () => {
  const card = await renderCardView()
  expect(await within(card).findByText('2/5')).toBeVisible()
  const before = vi
    .mocked(api.get)
    .mock.calls.filter(([url]) => url === '/api/channel/concurrency').length

  await userEvent.setup().click(within(card).getByText('Used tokens'))

  expect(
    screen.getByRole('status', { name: 'Dialog state' })
  ).toHaveTextContent('channel-details:3')
  const after = vi
    .mocked(api.get)
    .mock.calls.filter(([url]) => url === '/api/channel/concurrency').length
  // The open drawer joins the existing visible set, so the key is unchanged and
  // the drawer renders the same sample without another coordinator round trip.
  expect(after).toBe(before)
})

it('orders the card metrics as tokens, quota, response and last test', async () => {
  const card = await renderCardView()

  // User-visible order: used Tokens first, then the quota region, then the
  // response and last-test readings. Concurrency sits in the name row instead.
  const usedTokens = await within(card).findByText('Used tokens')
  const quota = within(card).getByText('Remaining / Total')
  const response = within(card).getByText('Response')
  const lastTested = within(card).getByText('Last Tested')

  // Document order is the reading order for these labels.
  const labels = [usedTokens, quota, response, lastTested]
  for (let index = 1; index < labels.length; index++) {
    expect(
      labels[index - 1].compareDocumentPosition(labels[index]) &
        Node.DOCUMENT_POSITION_FOLLOWING
    ).toBeTruthy()
  }
  // Tokens come before the quota region, and the quota region lives in the
  // same metric column as the token count.
  expect(usedTokens.closest('[data-card-metric-column]')).toBe(
    quota.closest('[data-card-metric-column]')
  )
  // Priority/weight keep their own trailing row.
  expect(within(card).getByText('Priority')).toBeVisible()
  expect(
    quota.compareDocumentPosition(within(card).getByText('Priority')) &
      Node.DOCUMENT_POSITION_FOLLOWING
  ).toBeTruthy()
})

it('renders the last test as relative time, not the full timestamp', async () => {
  const card = await renderCardView()

  // The card reuses the existing test_time cell: relative text inline, exact
  // timestamp only in the tooltip.
  const label = await within(card).findByText('Last Tested')
  const value = label.nextElementSibling as HTMLElement
  expect(value.textContent).toMatch(/ago$/)
  expect(
    within(card).queryByText(formatTimestampToDate(TEST_TIME) as never)
  ).toBeNull()
})

it('fills the quota region with the enabled local budget remaining and limit', async () => {
  const card = await renderCardViewWithEnabledBudget()

  // `remaining / limit` comes from the server quota projection only, formatted
  // with the shared quota formatter.
  const label = await within(card).findByText('Remaining / Total')
  const value = label.nextElementSibling as HTMLElement
  await waitFor(() => expect(value.textContent).toContain('/'))
  expect(value.textContent).not.toContain('Unlimited')
  expect(value.textContent).toMatch(/\d/)
})

it('shows unavailable instead of the upstream balance when the local budget is unknown', async () => {
  localStorage.setItem(VIEW_MODE_STORAGE_KEY, 'card')
  vi.spyOn(api, 'get').mockImplementation(async (url) => {
    if (url === '/api/channel') {
      return {
        data: {
          success: true,
          data: { items: [channel], total: 1, type_counts: {} },
        },
      }
    }
    // No quota entry for this channel: the local budget state is unknown.
    if (url === '/api/channel/quota') {
      return { data: { success: true, data: { items: [] } } }
    }
    if (url === '/api/channel/usage') {
      return {
        data: {
          success: true,
          data: { items: [] },
        },
      }
    }
    return { data: { success: true, data: [] } }
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
  await waitFor(() =>
    expect(document.querySelector(CARD_SELECTOR)).not.toBeNull()
  )
  const card = document.querySelector(CARD_SELECTOR) as HTMLElement

  // The quota region must not present the upstream balance as the local budget.
  const label = await within(card).findByText('Remaining / Total')
  const value = label.nextElementSibling as HTMLElement
  await waitFor(() =>
    expect(value.textContent).toMatch(/Unavailable|Loading\.\.\./)
  )
})
