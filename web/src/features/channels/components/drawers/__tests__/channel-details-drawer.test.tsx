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
import { cleanup, render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { createInstance } from 'i18next'
import { I18nextProvider } from 'react-i18next'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { api } from '@/lib/api'
import { formatNumber, formatTimestampToDate } from '@/lib/format'

import type { Channel, ChannelUsageDetail } from '../../../types'
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

function mockDetailApi(payload: ChannelUsageDetail) {
  return vi.spyOn(api, 'get').mockImplementation(async (url) => {
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
}) {
  render(
    <I18nextProvider i18n={i18n}>
      <QueryClientProvider client={client}>
        <ChannelDetailsDrawer
          channel={props.channel}
          open={props.open ?? true}
          onOpenChange={props.onOpenChange ?? (() => {})}
        />
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
    expect(enabledRow.getByText(formatNumber(900_000, 'en'))).toBeVisible()
    expect(enabledRow.getByText(formatNumber(1_100_000, 'en'))).toBeVisible()
    expect(enabledRow.getByText(formatNumber(30, 'en'))).toBeVisible()
    expect(
      enabledRow.getByText(formatTimestampToDate(1_700_000_000))
    ).toBeVisible()

    const disabledRow = within(bodyRows[1])
    expect(disabledRow.getByText('gpt-4o-mini')).toBeVisible()
    expect(disabledRow.getByText('Disabled')).toBeVisible()
    expect(disabledRow.getByText(formatNumber(400_000, 'en'))).toBeVisible()
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
      within(overview).getByText(formatNumber(1_500_000, 'en'))
    ).toBeVisible()
    expect(
      within(overview).getByText(formatNumber(1_200_000, 'en'))
    ).toBeVisible()
    expect(
      within(overview).getByText(formatNumber(300_000, 'en'))
    ).toBeVisible()
    expect(within(overview).getByText(formatNumber(42, 'en'))).toBeVisible()
    expect(within(overview).getByText('Consumed quota')).toBeVisible()
  })

  it('shows the empty state when the channel has no recorded usage', async () => {
    mockDetailApi(detail())
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
})
