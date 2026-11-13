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
  createRouter,
  RouterContextProvider,
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
import { useEffect, useState } from 'react'
import { I18nextProvider } from 'react-i18next'
import { afterEach, beforeEach, expect, test, vi } from 'vitest'

import { api } from '@/lib/api'

import type { Channel, KeyStatus } from '../../types'
import { ChannelsProvider, useChannels } from '../channels-provider'
import { ChannelTestDialog } from '../dialogs/channel-test-dialog'

const i18n = createInstance()
await i18n.init({
  lng: 'en',
  resources: { en: { translation: {} } },
  initAsync: false,
})

let client: QueryClient

function makeChannel(overrides: Partial<Channel> = {}): Channel {
  return {
    id: 7,
    type: 1,
    key: '',
    status: 1,
    name: 'multi',
    created_time: 0,
    test_time: 0,
    response_time: 0,
    other: '',
    balance: 0,
    balance_updated_time: 0,
    models: 'model-a,model-b',
    group: 'default',
    used_quota: 0,
    other_info: '',
    remark: '',
    max_input_tokens: 0,
    channel_info: {
      is_multi_key: true,
      multi_key_size: 2,
      multi_key_polling_index: 0,
      multi_key_mode: 'random',
    },
    settings: '{}',
    ...overrides,
  }
}

function DialogHarness({ channel }: { channel: Channel }) {
  const [router] = useState(() =>
    createRouter({
      routeTree: createRootRoute(),
      history: createMemoryHistory({ initialEntries: ['/'] }),
    })
  )
  return (
    <QueryClientProvider client={client}>
      <RouterContextProvider router={router}>
        <ChannelsProvider>
          <OpenWithRow channel={channel} />
        </ChannelsProvider>
      </RouterContextProvider>
    </QueryClientProvider>
  )
}

function OpenWithRow({ channel }: { channel: Channel }) {
  const { setCurrentRow } = useChannels()
  useEffect(() => {
    setCurrentRow(channel)
  }, [channel, setCurrentRow])
  return <ChannelTestDialog open onOpenChange={() => {}} />
}

function mockChannelTest(keys: KeyStatus[]) {
  const get = vi.spyOn(api, 'get').mockImplementation(async (url) => {
    if (url === '/api/channel/test/7') {
      return {
        data: { success: true, message: '', data: { response_time: 12 } },
      }
    }
    return { data: { success: true } }
  })
  const post = vi.spyOn(api, 'post').mockImplementation(async (url) => {
    if (url === '/api/channel/multi_key/manage') {
      return {
        data: {
          success: true,
          data: {
            keys,
            total: keys.length,
            page: 1,
            page_size: keys.length,
            total_pages: 1,
            enabled_count: keys.filter((key) => key.status === 1).length,
            manual_disabled_count: 0,
            auto_disabled_count: 0,
          },
        },
      }
    }
    return { data: { success: true } }
  })
  return { get, post }
}

function renderDialog(channel: Channel, keys: KeyStatus[]) {
  const mocks = mockChannelTest(keys)
  render(
    <I18nextProvider i18n={i18n}>
      <DialogHarness channel={channel} />
    </I18nextProvider>
  )
  return mocks
}

async function testModelRow(model: string) {
  const rows = await screen.findAllByRole('row')
  const row = rows.find((candidate) => within(candidate).queryByText(model))
  if (!row) throw new Error(`row for ${model} not found`)
  return within(row)
}

beforeEach(() => {
  client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
})

afterEach(() => {
  cleanup()
  client.clear()
  vi.restoreAllMocks()
})

test('a multi-key channel offers an automatic and a per-key choice', async () => {
  renderDialog(makeChannel(), [
    { index: 0, status: 1, key_preview: 'sk-…mnop' },
    { index: 1, status: 2, key_preview: '…67' },
  ])

  const select = await screen.findByRole('combobox', { name: 'Tested key' })
  await userEvent.setup().click(select)

  expect(
    await screen.findByRole('option', { name: 'Auto (by tested model)' })
  ).toBeVisible()
  expect(
    screen.getByRole('option', { name: '1 · sk-…mnop · Enabled' })
  ).toBeVisible()
  expect(
    screen.getByRole('option', { name: '2 · …67 · Disabled' })
  ).toBeVisible()
})

test('a single-key channel shows no key choice', async () => {
  const { post } = renderDialog(
    makeChannel({
      channel_info: {
        is_multi_key: false,
        multi_key_size: 1,
        multi_key_polling_index: 0,
        multi_key_mode: 'random',
      },
    }),
    []
  )

  await screen.findByText('model-a')
  expect(
    screen.queryByRole('combobox', { name: 'Tested key' })
  ).not.toBeInTheDocument()
  expect(post).not.toHaveBeenCalled()
})

test('an automatic test keeps the existing request without a key', async () => {
  const { get } = renderDialog(makeChannel(), [
    { index: 0, status: 1, key_preview: 'sk-…mnop' },
  ])

  // Wait for the key list to settle: its render is what enables the dialog body.
  await screen.findByRole('combobox', { name: 'Tested key' })
  const row = await testModelRow('model-a')
  await userEvent
    .setup()
    .click(row.getByRole('button', { name: 'Test Connection' }))

  await waitFor(() => expect(get).toHaveBeenCalled())
  const [, config] = get.mock.calls.find(
    ([url]) => url === '/api/channel/test/7'
  ) as [string, { params: Record<string, unknown> }]
  expect(config.params).toEqual({ model: 'model-a' })
  expect(config.params).not.toHaveProperty('key_index')
})

test('a named key is tested even when it is disabled', async () => {
  const { get } = renderDialog(makeChannel(), [
    { index: 0, status: 1, key_preview: 'sk-…mnop' },
    { index: 1, status: 2, key_preview: '…67' },
  ])
  const user = userEvent.setup()

  await user.click(await screen.findByRole('combobox', { name: 'Tested key' }))
  await user.click(
    await screen.findByRole('option', { name: '2 · …67 · Disabled' })
  )

  const row = await testModelRow('model-a')
  await user.click(row.getByRole('button', { name: 'Test Connection' }))

  await waitFor(() => expect(get).toHaveBeenCalled())
  const [, config] = get.mock.calls.find(
    ([url]) => url === '/api/channel/test/7'
  ) as [string, { params: Record<string, unknown> }]
  expect(config.params).toEqual({ model: 'model-a', key_index: 1 })
})

test('a key scoped to another model lets the channel pick the key', async () => {
  const { get } = renderDialog(makeChannel(), [
    { index: 0, status: 1, key_preview: 'sk-…mnop', models: ['model-b'] },
    { index: 1, status: 1, key_preview: '…67' },
  ])
  const user = userEvent.setup()

  await user.click(await screen.findByRole('combobox', { name: 'Tested key' }))
  await user.click(
    await screen.findByRole('option', { name: '1 · sk-…mnop · Enabled' })
  )

  const row = await testModelRow('model-a')
  await user.click(row.getByRole('button', { name: 'Test Connection' }))

  await waitFor(() => expect(get).toHaveBeenCalled())
  const [, config] = get.mock.calls.find(
    ([url]) => url === '/api/channel/test/7'
  ) as [string, { params: Record<string, unknown> }]
  expect(config.params).toEqual({ model: 'model-a' })
})
