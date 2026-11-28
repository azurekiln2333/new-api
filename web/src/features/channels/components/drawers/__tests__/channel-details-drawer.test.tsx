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
  cleanup,
  render,
  screen,
  within,
  waitFor,
} from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { createInstance } from 'i18next'
import { I18nextProvider } from 'react-i18next'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { api } from '@/lib/api'
import { formatNumber, formatTimestampToDate, formatTokens } from '@/lib/format'
import { ROLE } from '@/lib/roles'
import { useAuthStore } from '@/stores/auth-store'

import type {
  Channel,
  ChannelConcurrencyEntry,
  ChannelUsageDetail,
} from '../../../types'
import { ChannelConcurrencyContext } from '../../channel-concurrency-context'
import { ChannelDetailsDrawer } from '../channel-details-drawer'

const i18n = createInstance()
await i18n.init({
  lng: 'en',
  resources: { en: { translation: {} } },
  initAsync: false,
})

let client: QueryClient

const channel: Channel = {
  id: 7,
  type: 1,
  key: '',
  status: 1,
  name: 'prod-openai',
  created_time: 0,
  test_time: 0,
  response_time: 0,
  other: '',
  balance: 0,
  balance_updated_time: 0,
  models: 'gpt-4o,gpt-4o-mini',
  group: 'default',
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

function detail(
  overrides: Partial<ChannelUsageDetail> = {}
): ChannelUsageDetail {
  return {
    channel_id: 7,
    request_count: 0,
    prompt_tokens: 0,
    completion_tokens: 0,
    total_tokens: 0,
    quota: 0,
    models: [],
    ...overrides,
  }
}

const usageWithModels = detail({
  request_count: 42,
  prompt_tokens: 1_200_000,
  completion_tokens: 300_000,
  total_tokens: 1_500_000,
  models: [
    {
      model_name: 'gpt-4o',
      enabled: true,
      request_count: 30,
      prompt_tokens: 900_000,
      completion_tokens: 200_000,
      total_tokens: 1_100_000,
      quota: 0,
      last_used_at: 1_700_000_000,
    },
    {
      model_name: 'gpt-4o-mini',
      enabled: false,
      request_count: 12,
      prompt_tokens: 300_000,
      completion_tokens: 100_000,
      total_tokens: 400_000,
      quota: 0,
      last_used_at: 0,
    },
  ],
})

function mockDetailApi(
  payload: ChannelUsageDetail,
  currentChannel: () => Channel = () => channel
) {
  return vi.spyOn(api, 'get').mockImplementation(async (url) => {
    if (url === '/api/channel/7') {
      return { data: { success: true, data: currentChannel() } }
    }
    if (url === '/api/channel/7/usage') {
      return { data: { success: true, data: payload } }
    }
    throw new Error(`unexpected request: ${url}`)
  })
}

function renderDrawer(props: {
  channel: Channel | null
  open?: boolean
  onOpenChange?: (open: boolean) => void
  concurrency?: {
    entriesById: Record<number, ChannelConcurrencyEntry>
    isPending: boolean
    stale: boolean
  }
}) {
  render(
    <I18nextProvider i18n={i18n}>
      <QueryClientProvider client={client}>
        <ChannelConcurrencyContext.Provider
          value={
            props.concurrency ?? {
              entriesById: {},
              isPending: false,
              stale: false,
            }
          }
        >
          <ChannelDetailsDrawer
            channel={props.channel}
            open={props.open ?? true}
            onOpenChange={props.onOpenChange ?? (() => {})}
          />
        </ChannelConcurrencyContext.Provider>
      </QueryClientProvider>
    </I18nextProvider>
  )
}

beforeEach(() => {
  client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
})

afterEach(() => {
  cleanup()
  client.clear()
  vi.restoreAllMocks()
  useAuthStore.getState().auth.setUser(null)
})

describe('ChannelDetailsDrawer', () => {
  it('renders each model row with its enabled status and token numbers', async () => {
    mockDetailApi(usageWithModels)
    renderDrawer({ channel })

    const table = await screen.findByRole('table')
    const [headerRow, ...bodyRows] = within(table).getAllByRole('row')
    expect(
      within(headerRow)
        .getAllByRole('columnheader')
        .map((cell) => cell.textContent)
    ).toEqual([
      'Model',
      'Status',
      'Input tokens',
      'Output tokens',
      'Total tokens',
      'Requests',
      'Last used',
    ])

    const enabledRow = within(bodyRows[0])
    expect(enabledRow.getByText('gpt-4o')).toBeVisible()
    expect(enabledRow.getByText('Enabled')).toBeVisible()
    expect(enabledRow.getByText(formatTokens(900_000, 'en'))).toBeVisible()
    expect(enabledRow.getByText(formatTokens(1_100_000, 'en'))).toBeVisible()
    expect(enabledRow.getByText(formatNumber(30, 'en'))).toBeVisible()
    expect(
      enabledRow.getByText(formatTimestampToDate(1_700_000_000))
    ).toBeVisible()

    const disabledRow = within(bodyRows[1])
    expect(disabledRow.getByText('gpt-4o-mini')).toBeVisible()
    expect(disabledRow.getByText('Disabled')).toBeVisible()
    expect(disabledRow.getByText(formatTokens(400_000, 'en'))).toBeVisible()
    expect(disabledRow.queryByText(/\d{4}-\d{2}-\d{2}/)).not.toBeInTheDocument()
    expect(disabledRow.getByText('-')).toBeVisible()
  })

  it('summarises the channel totals above the model table', async () => {
    mockDetailApi(usageWithModels)
    renderDrawer({ channel })

    const overview = await screen.findByRole('region', {
      name: 'Model usage',
    })
    expect(within(overview).getByText('Total tokens')).toBeVisible()
    expect(
      within(overview).getByText(formatTokens(1_500_000, 'en'))
    ).toBeVisible()
    expect(
      within(overview).getByText(formatTokens(1_200_000, 'en'))
    ).toBeVisible()
    expect(
      within(overview).getByText(formatTokens(300_000, 'en'))
    ).toBeVisible()
    expect(within(overview).getByText(formatNumber(42, 'en'))).toBeVisible()
    expect(within(overview).getByText('Consumed quota')).toBeVisible()
  })

  it('shows a confirmed empty state when the channel has no recorded usage', async () => {
    mockDetailApi(
      detail({
        coverage: {
          complete: true,
          coverage: 'complete',
          historical_coverage: 'complete',
          live_coverage: 'complete',
          legacy_baseline_included: true,
          start_timestamp: 1_700_000_000,
          end_timestamp: 1_700_000_060,
        },
      })
    )
    renderDrawer({ channel })

    expect(await screen.findByText('No usage recorded yet')).toBeVisible()
    expect(screen.queryByRole('table')).not.toBeInTheDocument()
  })

  it('shows the server error and retries when the usage request fails', async () => {
    const get = vi
      .spyOn(api, 'get')
      .mockRejectedValueOnce(new Error('usage backend down'))

    renderDrawer({ channel })

    expect(await screen.findByText('usage backend down')).toBeVisible()
    expect(screen.getByText('Failed to load channel usage')).toBeVisible()

    get.mockResolvedValue({ data: { success: true, data: usageWithModels } })
    await userEvent.setup().click(screen.getByRole('button', { name: 'Retry' }))

    expect(await screen.findByRole('table')).toBeVisible()
    expect(get).toHaveBeenCalledTimes(2)
  })

  it('does not request usage while no channel is selected', () => {
    const get = vi.spyOn(api, 'get')
    renderDrawer({ channel: null })

    expect(get).not.toHaveBeenCalled()
    expect(screen.queryByRole('table')).not.toBeInTheDocument()
  })

  it('does not request usage while the drawer is closed', () => {
    const get = vi.spyOn(api, 'get')
    renderDrawer({ channel, open: false })

    expect(get).not.toHaveBeenCalled()
  })

  it('reloads the usage when the refresh button is pressed', async () => {
    const get = mockDetailApi(usageWithModels)
    renderDrawer({ channel })

    await screen.findByRole('table')
    await userEvent
      .setup()
      .click(screen.getByRole('button', { name: 'Refresh' }))

    expect(await screen.findByRole('table')).toBeVisible()
    expect(get).toHaveBeenCalledTimes(2)
  })

  it('settles to unavailable instead of waiting forever when no sample arrives', async () => {
    mockDetailApi(usageWithModels)
    renderDrawer({
      channel,
      concurrency: { entriesById: {}, isPending: false, stale: false },
    })

    const concurrency = await screen.findByRole('region', {
      name: 'Concurrency',
    })
    expect(within(concurrency).getByText('Unavailable')).toBeVisible()
    expect(within(concurrency).queryByText('…')).not.toBeInTheDocument()
  })

  it('shows the live concurrency sample shared with the cards', async () => {
    mockDetailApi(usageWithModels)
    renderDrawer({
      channel,
      concurrency: {
        entriesById: {
          7: {
            id: 7,
            current: 3,
            limit: 8,
            scope: 'cluster',
            sample_time: 1_700_000_000_000,
          },
        },
        isPending: false,
        stale: false,
      },
    })

    const concurrency = await screen.findByRole('region', {
      name: 'Concurrency',
    })
    const current = concurrency.querySelector('[data-concurrency-current]')
    expect(current?.textContent).toContain('3')
    expect(within(concurrency).getByText('8')).toBeVisible()
    expect(within(concurrency).getByText('Cluster')).toBeVisible()
    expect(
      within(concurrency).getByText(
        formatTimestampToDate(1_700_000_000_000, 'milliseconds')
      )
    ).toBeVisible()
  })

  it('reports an unlimited channel without inventing a concurrency cap', async () => {
    mockDetailApi(usageWithModels)
    renderDrawer({
      channel,
      concurrency: {
        entriesById: {
          7: {
            id: 7,
            current: 4,
            limit: null,
            scope: 'instance',
            sample_time: 1_700_000_000_000,
          },
        },
        isPending: false,
        stale: false,
      },
    })

    const concurrency = await screen.findByRole('region', {
      name: 'Concurrency',
    })
    expect(
      concurrency.querySelector('[data-concurrency-current]')?.textContent
    ).toContain('4')
    expect(within(concurrency).getByText('No limit')).toBeVisible()
    expect(within(concurrency).getByText('Instance')).toBeVisible()
  })

  it('shows an unavailable sample and stale warning instead of a false zero', async () => {
    mockDetailApi(usageWithModels)
    renderDrawer({
      channel,
      concurrency: {
        entriesById: {
          7: {
            id: 7,
            current: null,
            limit: 8,
            scope: 'cluster',
            sample_time: 1_700_000_000_000,
          },
        },
        isPending: false,
        stale: true,
      },
    })

    const concurrency = await screen.findByRole('region', {
      name: 'Concurrency',
    })
    expect(within(concurrency).getByText('Unavailable')).toBeVisible()
    expect(
      within(concurrency).getByText(
        'Last known sample; the latest refresh failed'
      )
    ).toBeVisible()
    expect(within(concurrency).queryByText('0 / 8')).not.toBeInTheDocument()
  })

  it('lists every channel group with the models it reaches', async () => {
    mockDetailApi(usageWithModels)
    renderDrawer({
      channel: {
        ...channel,
        group: 'default,vip',
        models: 'gpt-4o,gpt-4o-mini',
        settings: JSON.stringify({
          group_models: { vip: ['gpt-4o-mini'] },
        }),
      },
    })

    const groups = await screen.findByRole('region', { name: 'Groups' })
    expect(within(groups).getByText('default')).toBeVisible()
    expect(within(groups).getByText('vip')).toBeVisible()

    // The unscoped group reaches both channel models; the scoped one only the
    // model it is restricted to.
    const rows = within(groups).getAllByRole('definition')
    expect(rows).toHaveLength(2)
    expect(within(rows[0]).getByText('gpt-4o')).toBeVisible()
    expect(within(rows[0]).getByText('gpt-4o-mini')).toBeVisible()
    expect(within(rows[1]).queryByText('gpt-4o')).not.toBeInTheDocument()
    expect(within(rows[1]).getByText('gpt-4o-mini')).toBeVisible()
  })

  it('does not list a model the restricted group cannot reach', async () => {
    mockDetailApi(usageWithModels)
    renderDrawer({
      channel: {
        ...channel,
        group: 'vip',
        settings: JSON.stringify({
          group_models: { vip: ['gpt-4o', 'retired-model'] },
        }),
      },
    })

    const groups = await screen.findByRole('region', { name: 'Groups' })
    expect(within(groups).getByText('gpt-4o')).toBeVisible()
    expect(within(groups).queryByText('retired-model')).not.toBeInTheDocument()
    // The channel model that is not whitelisted stays hidden.
    expect(within(groups).queryByText('gpt-4o-mini')).not.toBeInTheDocument()
  })

  it('shows an empty model list instead of widening a group restricted to private keys', async () => {
    mockDetailApi(usageWithModels)
    renderDrawer({
      channel: {
        ...channel,
        group: 'default',
        models: 'gpt-4o,gpt-4o-mini',
        // The group may only use the second model, but the channel's public key
        // serves the first one, so this group reaches nothing here.
        settings: JSON.stringify({
          group_models: { default: ['gpt-4o-mini'] },
        }),
        channel_info: {
          is_multi_key: true,
          multi_key_size: 2,
          multi_key_models: { '0': ['gpt-4o'], '1': ['gpt-4o-mini'] },
          multi_key_users: { '1': 7 },
          multi_key_polling_index: 0,
          multi_key_mode: 'random',
        },
      },
    })

    const groups = await screen.findByRole('region', { name: 'Groups' })
    expect(within(groups).getByText('default')).toBeVisible()
    const definition = within(groups).getByRole('definition')
    expect(within(definition).queryByText('gpt-4o')).not.toBeInTheDocument()
    expect(
      within(definition).queryByText('gpt-4o-mini')
    ).not.toBeInTheDocument()
    expect(within(definition).getByText('-')).toBeVisible()
  })

  it('shows the group section while the usage request is still loading', async () => {
    vi.spyOn(api, 'get').mockImplementation(
      () => new Promise(() => undefined) as never
    )
    renderDrawer({
      channel: { ...channel, group: 'vip', models: 'gpt-4o' },
    })

    const groups = screen.getByRole('region', { name: 'Groups' })
    expect(within(groups).getByText('vip')).toBeVisible()
    expect(within(groups).getByText('gpt-4o')).toBeVisible()
  })
})

const unavailableChannel: Channel = {
  ...channel,
  channel_info: {
    ...channel.channel_info,
    model_unavailable: [
      {
        upstream_model: 'gpt-4o',
        key_fingerprint: 'a'.repeat(32),
        status_code: 404,
        evidence: 'error_code',
        observed_at: 1_700_000_000,
      },
    ],
  },
}

it('shows safe model observations without offering restore to a read-only administrator', async () => {
  mockDetailApi(usageWithModels, () => unavailableChannel)
  useAuthStore.getState().auth.setUser({
    id: 8,
    username: 'reader',
    role: ROLE.ADMIN,
    permissions: { admin_permissions: { channel: { read: true } } },
  })
  renderDrawer({ channel: unavailableChannel })
  const section = screen.getByRole('region', { name: 'Unavailable models' })
  expect(within(section).getByText('gpt-4o')).toBeVisible()
  expect(
    within(section).getByText(/Structured upstream model refusal.*404/)
  ).toBeVisible()
  expect(section).toHaveTextContent(formatTimestampToDate(1_700_000_000))
  expect(within(section).queryByRole('button')).not.toBeInTheDocument()
  expect(section).not.toHaveTextContent('a'.repeat(32))
})

it('confirms restoration and removes the stored observations after success', async () => {
  let restored = false
  mockDetailApi(usageWithModels, () =>
    restored ? channel : unavailableChannel
  )
  useAuthStore
    .getState()
    .auth.setUser({ id: 1, username: 'root', role: ROLE.SUPER_ADMIN })
  const post = vi.spyOn(api, 'post').mockImplementation(async () => {
    restored = true
    return { data: { success: true, data: true } }
  })
  renderDrawer({ channel: unavailableChannel })
  const section = screen.getByRole('region', { name: 'Unavailable models' })
  const user = userEvent.setup()
  await user.click(
    screen.getByRole('button', { name: 'Restore model availability' })
  )
  expect(post).not.toHaveBeenCalled()
  const confirmation = screen.getByRole('alertdialog', {
    name: 'Restore model availability',
  })
  await user.click(
    within(confirmation).getByRole('button', { name: 'Restore' })
  )
  await waitFor(() => expect(section).not.toBeInTheDocument())
  expect(post).toHaveBeenCalledWith(
    '/api/channel/7/model_unavailable/restore',
    {},
    expect.any(Object)
  )
})

it('keeps observations visible when restoration fails', async () => {
  mockDetailApi(usageWithModels, () => unavailableChannel)
  useAuthStore
    .getState()
    .auth.setUser({ id: 1, username: 'root', role: ROLE.SUPER_ADMIN })
  vi.spyOn(api, 'post').mockRejectedValue(new Error('restore failed'))
  renderDrawer({ channel: unavailableChannel })
  const section = screen.getByRole('region', { name: 'Unavailable models' })
  const user = userEvent.setup()
  await user.click(
    screen.getByRole('button', { name: 'Restore model availability' })
  )
  const confirmation = screen.getByRole('alertdialog', {
    name: 'Restore model availability',
  })
  await user.click(
    within(confirmation).getByRole('button', { name: 'Restore' })
  )
  await waitFor(() =>
    expect(
      within(confirmation).getByRole('button', { name: 'Restore' })
    ).toBeEnabled()
  )
  expect(section).toHaveTextContent('gpt-4o')
})
