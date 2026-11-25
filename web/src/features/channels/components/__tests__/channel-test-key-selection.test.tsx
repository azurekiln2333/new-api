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
  act,
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

import { channelsQueryKeys } from '../../lib'
import type { Channel, KeyStatus, MultiKeyStatusResponse } from '../../types'
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
  const [open, setOpen] = useState(true)
  useEffect(() => {
    setCurrentRow(channel)
  }, [channel, setCurrentRow])
  return (
    <>
      <button type='button' onClick={() => setOpen(true)}>
        Open test dialog
      </button>
      <ChannelTestDialog open={open} onOpenChange={setOpen} />
    </>
  )
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
            keys: keys.map((key) => ({
              id: String(101 + key.index),
              rev: 1,
              ...key,
            })),
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
  expect(
    screen.queryByText(
      'Choose which key this test uses. Auto tests the key that serves the tested model.'
    )
  ).not.toBeInTheDocument()
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

test('an automatic test defaults to streaming without pinning a key', async () => {
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
  expect(config.params).toEqual({ model: 'model-a', stream: true })
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
  expect(config.params).toEqual({
    model: 'model-a',
    stream: true,
    key_index: 1,
    key_entry_id: '102',
    expected_rev: 1,
  })
})

test.each<{
  label: string
  models: string[]
  scopeMode?: KeyStatus['scope_mode']
  visible: string[]
}>([
  {
    label: 'a legacy key scoped to another model',
    models: ['model-b'],
    visible: ['model-b'],
  },
  {
    label: 'an explicit nonempty allowlist',
    models: ['model-b'],
    scopeMode: 'allowlist',
    visible: ['model-b'],
  },
  {
    label: 'an explicit empty allowlist',
    models: [],
    scopeMode: 'allowlist',
    visible: [],
  },
  {
    label: 'an unrestricted legacy key',
    models: [],
    visible: ['model-a', 'model-b'],
  },
  {
    label: 'an all-model key',
    models: [],
    scopeMode: 'all',
    visible: ['model-a', 'model-b'],
  },
  {
    label: 'an all-model key with a stale model list',
    models: ['model-b'],
    scopeMode: 'all',
    visible: ['model-a', 'model-b'],
  },
])(
  '$label filters visible and tested models without fallback',
  async ({ models, scopeMode, visible }) => {
    const { get } = renderDialog(makeChannel(), [
      {
        index: 0,
        status: 1,
        key_preview: 'sk-…mnop',
        models,
        scope_mode: scopeMode,
      },
      { index: 1, status: 1, key_preview: '…67' },
    ])
    const user = userEvent.setup()

    await user.click(
      await screen.findByRole('combobox', { name: 'Tested key' })
    )
    await user.click(
      await screen.findByRole('option', { name: '1 · sk-…mnop · Enabled' })
    )

    const table = screen.getByRole('table')
    for (const model of ['model-a', 'model-b']) {
      expect(within(table).queryByText(model) !== null).toBe(
        visible.includes(model)
      )
    }
    const batch = screen.getByRole('button', {
      name: `Test all ${visible.length} models`,
    })
    if (!visible.length) {
      expect(batch).toBeDisabled()
      expect(
        within(table).queryByRole('button', { name: 'Test Connection' })
      ).not.toBeInTheDocument()
      expect(get).not.toHaveBeenCalled()
      return
    }

    await user.click(batch)
    await waitFor(() =>
      expect(
        screen.getByRole('button', {
          name: `Test all ${visible.length} models`,
        })
      ).toBeEnabled()
    )
    expect(
      get.mock.calls.map(([url, config]) => ({ url, params: config?.params }))
    ).toEqual(
      visible.map((model) => ({
        url: '/api/channel/test/7',
        params: {
          model,
          stream: true,
          key_index: 0,
          key_entry_id: '101',
          expected_rev: 1,
        },
      }))
    )
  }
)

test('switching keys resets pagination and hidden model selection', async () => {
  const models = Array.from(
    { length: 31 },
    (_, index) => `model-${String(index + 1).padStart(2, '0')}`
  )
  const { get } = renderDialog(makeChannel({ models: models.join(',') }), [
    {
      index: 0,
      status: 1,
      key_preview: 'sk-…mnop',
      models: ['model-01'],
      scope_mode: 'allowlist',
    },
    { index: 1, status: 1, key_preview: '…67', scope_mode: 'all' },
  ])
  const user = userEvent.setup()
  const key = await screen.findByRole('combobox', { name: 'Tested key' })

  await user.click(screen.getByRole('button', { name: 'Go to next page' }))
  await user.click(
    await screen.findByRole('checkbox', { name: 'Select model model-31' })
  )
  expect(
    screen.getByRole('button', { name: 'Copy selected models' })
  ).toBeVisible()
  await user.click(key)
  await user.click(
    await screen.findByRole('option', { name: '1 · sk-…mnop · Enabled' })
  )

  expect(await screen.findByText('model-01')).toBeVisible()
  expect(screen.queryByText('model-31')).not.toBeInTheDocument()
  expect(
    screen.getByRole('checkbox', { name: 'Select model model-01' })
  ).not.toBeChecked()
  expect(
    screen.getByRole('button', { name: 'Go to previous page' })
  ).toBeDisabled()
  expect(
    screen.queryByRole('button', { name: 'Copy selected models' })
  ).not.toBeInTheDocument()

  await user.click(key)
  await user.click(
    await screen.findByRole('option', { name: 'Auto (by tested model)' })
  )
  expect(
    screen.getByRole('button', { name: 'Test all 31 models' })
  ).toBeEnabled()
  await user.click(screen.getByRole('button', { name: 'Go to next page' }))
  expect(
    await screen.findByRole('checkbox', { name: 'Select model model-31' })
  ).not.toBeChecked()
  expect(get).not.toHaveBeenCalled()
})

test('the selected key cannot change while its test is pending', async () => {
  const { get } = renderDialog(makeChannel(), [
    { index: 0, status: 1, key_preview: 'sk-…mnop' },
    { index: 1, status: 1, key_preview: '…67' },
  ])
  const user = userEvent.setup()
  const key = await screen.findByRole('combobox', { name: 'Tested key' })
  await user.click(key)
  await user.click(
    await screen.findByRole('option', { name: '1 · sk-…mnop · Enabled' })
  )
  let finishTest!: () => void
  get.mockImplementationOnce(
    () =>
      new Promise((resolve) => {
        finishTest = () =>
          resolve({ data: { success: true, message: '', time: 0.012 } })
      })
  )
  const row = await testModelRow('model-a')
  await user.click(row.getByRole('button', { name: 'Test Connection' }))

  expect(key).toBeDisabled()
  finishTest()
  await waitFor(() => expect(key).toBeEnabled())
  expect(get).toHaveBeenCalledWith(
    '/api/channel/test/7',
    expect.objectContaining({
      params: {
        model: 'model-a',
        stream: true,
        key_index: 0,
        key_entry_id: '101',
        expected_rev: 1,
      },
    })
  )
})

test.each(['reordered', 'revised', 'removed'])(
  'a %s selected allocation stays unavailable rather than retargeting its index',
  async (change) => {
    const { get } = renderDialog(makeChannel(), [
      { index: 0, id: '101', rev: 1, status: 1, key_preview: 'sk-…mnop' },
      { index: 1, id: '102', rev: 1, status: 1, key_preview: '…67' },
    ])
    const user = userEvent.setup()
    const key = await screen.findByRole('combobox', { name: 'Tested key' })
    await user.click(key)
    await user.click(
      await screen.findByRole('option', { name: '1 · sk-…mnop · Enabled' })
    )
    const queryKey = channelsQueryKeys.multiKeyStatus(7)
    const previous = client.getQueryData<MultiKeyStatusResponse>(queryKey)
    if (!previous?.data) throw new Error('Missing loaded key status')
    const original = previous.data.keys[0]
    const replacement = previous.data.keys[1]
    let keys: KeyStatus[] = [{ ...replacement, index: 0 }]
    if (change === 'reordered') keys.push({ ...original, index: 1 })
    if (change === 'revised') keys = [{ ...original, rev: 2 }, replacement]
    act(() => {
      client.setQueryData(queryKey, {
        ...previous,
        data: { ...previous.data, keys },
      })
    })

    await waitFor(() =>
      expect(key).toHaveValue(
        'Channel key configuration changed, reload and retry'
      )
    )
    expect(
      screen.getByRole('button', { name: 'Test all 0 models' })
    ).toBeDisabled()
    expect(
      screen.queryByRole('button', { name: 'Test Connection' })
    ).not.toBeInTheDocument()
    expect(get).not.toHaveBeenCalled()
  }
)

test('a server stale-selection rejection never retries through automatic routing', async () => {
  const { get } = renderDialog(makeChannel(), [
    { index: 0, status: 1, key_preview: 'sk-…mnop' },
  ])
  get.mockResolvedValue({
    data: {
      success: false,
      message: 'Channel key configuration changed, reload and retry',
    },
  })
  const user = userEvent.setup()
  await user.click(await screen.findByRole('combobox', { name: 'Tested key' }))
  await user.click(
    await screen.findByRole('option', { name: '1 · sk-…mnop · Enabled' })
  )
  const row = await testModelRow('model-a')
  await user.click(row.getByRole('button', { name: 'Test Connection' }))

  expect(await row.findByText('Failed')).toBeVisible()
  expect(get).toHaveBeenCalledTimes(1)
  expect(get).toHaveBeenCalledWith(
    '/api/channel/test/7',
    expect.objectContaining({
      params: {
        model: 'model-a',
        stream: true,
        key_index: 0,
        key_entry_id: '101',
        expected_rev: 1,
      },
    })
  )
})

test('switching overlapping keys clears the previous key failure and delete action', async () => {
  const { get } = renderDialog(makeChannel(), [
    { index: 0, status: 1, key_preview: 'sk-…mnop' },
    { index: 1, status: 1, key_preview: '…67' },
  ])
  get.mockResolvedValue({ data: { success: false, message: 'Unavailable' } })
  const put = vi.spyOn(api, 'put')
  const user = userEvent.setup()
  const key = await screen.findByRole('combobox', { name: 'Tested key' })
  await user.click(key)
  await user.click(
    await screen.findByRole('option', { name: '1 · sk-…mnop · Enabled' })
  )
  const row = await testModelRow('model-a')
  await user.click(row.getByRole('button', { name: 'Test Connection' }))
  await row.findByText('Failed')
  expect(
    screen.getByRole('button', { name: 'Delete failed models (1)' })
  ).toBeVisible()
  await user.click(key)
  await user.click(
    await screen.findByRole('option', { name: '2 · …67 · Enabled' })
  )

  expect((await testModelRow('model-a')).getByText('Not tested')).toBeVisible()
  expect(
    screen.queryByRole('button', { name: /Delete failed models/ })
  ).not.toBeInTheDocument()
  expect(put).not.toHaveBeenCalled()
})

test('deleting failures for a selected key preserves models hidden by its scope', async () => {
  const { get } = renderDialog(makeChannel(), [
    {
      index: 0,
      status: 1,
      key_preview: 'sk-…mnop',
      scope_mode: 'allowlist',
      models: ['model-b'],
    },
    { index: 1, status: 1, key_preview: '…67', scope_mode: 'all' },
  ])
  get.mockResolvedValue({ data: { success: false, message: 'Unavailable' } })
  const put = vi.spyOn(api, 'put').mockResolvedValue({
    data: { success: true },
  })
  const user = userEvent.setup()
  const key = await screen.findByRole('combobox', { name: 'Tested key' })
  const first = await testModelRow('model-a')
  await user.click(first.getByRole('button', { name: 'Test Connection' }))
  await first.findByText('Failed')

  await user.click(key)
  await user.click(
    await screen.findByRole('option', { name: '1 · sk-…mnop · Enabled' })
  )
  const second = await testModelRow('model-b')
  await user.click(second.getByRole('button', { name: 'Test Connection' }))
  await second.findByText('Failed')
  await user.click(
    screen.getByRole('button', { name: 'Delete failed models (1)' })
  )
  await user.click(
    within(await screen.findByRole('alertdialog')).getByRole('button', {
      name: 'Delete',
    })
  )

  await waitFor(() =>
    expect(put).toHaveBeenCalledWith(
      '/api/channel/',
      { id: 7, models: 'model-a' },
      expect.anything()
    )
  )
})

test('reopening a test dialog restores streaming without starting a probe', async () => {
  const { get } = renderDialog(makeChannel(), [])
  const user = userEvent.setup()
  await user.click(await screen.findByRole('button', { name: 'Settings' }))
  const stream = await screen.findByRole('switch', { name: 'Stream Mode' })
  expect(stream).toBeChecked()
  await user.click(stream)
  expect(stream).not.toBeChecked()

  await user.keyboard('{Escape}')
  await user.keyboard('{Escape}')
  await user.click(
    await screen.findByRole('button', { name: 'Open test dialog' })
  )
  await user.click(await screen.findByRole('button', { name: 'Settings' }))

  expect(
    await screen.findByRole('switch', { name: 'Stream Mode' })
  ).toBeChecked()
  expect(get).not.toHaveBeenCalled()
})
