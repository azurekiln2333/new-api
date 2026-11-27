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
import { ROLE } from '@/lib/roles'
import { useAuthStore } from '@/stores/auth-store'

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
const originalAuth = useAuthStore.getState().auth

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
  useAuthStore.setState({ auth: { ...originalAuth, user: { id: 1, username: 'root', role: ROLE.SUPER_ADMIN } } })
  client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
})

afterEach(() => {
  cleanup()
  client.clear()
  useAuthStore.setState({ auth: originalAuth })
  vi.restoreAllMocks()
})

test.each([
  { type: 1, protocol: 'messages' as const, setting: '{"supported_endpoints":["messages"]}', compact: false },
  { type: 14, protocol: 'chat' as const, setting: '', compact: false },
  { type: 1, protocol: 'responses' as const, setting: '{"supported_endpoints":["responses"]}', compact: true },
])('a type $type Key pinned to $protocol offers converted text clients without extra probes', async ({ type, protocol, setting, compact }) => {
  const { get } = renderDialog(makeChannel({ type, setting }), [
    { id: '9007199254740995', rev: 4, index: 0, status: 1, key_preview: 'same…key', upstream_protocol: protocol },
    { id: '9007199254740997', rev: 7, index: 1, status: 1, key_preview: 'same…key' },
  ])
  const user = userEvent.setup()
  await user.click(await screen.findByRole('combobox', { name: 'Tested key' }))
  await user.click(await screen.findByRole('option', { name: '1 · same…key · Enabled' }))
  await user.click(screen.getByRole('button', { name: 'Settings' }))
  await user.click(await screen.findByRole('combobox', { name: 'Endpoint Type' }))
  const list = within(await screen.findByRole('listbox'))
  expect(list.getByRole('option', { name: 'OpenAI (/v1/chat/completions)' })).toBeVisible()
  expect(list.getByRole('option', { name: 'Anthropic (/v1/messages)' })).toBeVisible()
  const responses = list.getByRole('option', { name: 'OpenAI Responses (/v1/responses)' })
  expect(responses).toBeVisible()
  expect(Boolean(list.queryByRole('option', { name: 'OpenAI Response Compaction (/v1/responses/compact)' }))).toBe(compact)
  expect(list.queryByRole('option', { name: 'Embeddings (/v1/embeddings)' })).not.toBeInTheDocument()
  expect(get.mock.calls.filter(([url]) => url === '/api/channel/test/7')).toHaveLength(0)
  await user.click(responses)
  await user.keyboard('{Escape}')
  await user.click((await testModelRow('model-a')).getByRole('button', { name: 'Test Connection' }))
  await waitFor(() => expect(get).toHaveBeenCalledWith('/api/channel/test/7', expect.objectContaining({ params: {
    model: 'model-a', stream: true, endpoint_type: 'openai-response',
    key_index: 0, key_entry_id: '9007199254740995', expected_rev: 4,
  } })))
  expect(get.mock.calls.filter(([url]) => url === '/api/channel/test/7')).toHaveLength(1)
})

test.each([
  { protocol: 'messages' as const, label: 'Anthropic (/v1/messages)' },
  { protocol: 'chat' as const, label: 'OpenAI (/v1/chat/completions)' },
  { protocol: 'responses' as const, label: 'OpenAI Responses (/v1/responses)' },
])('passthrough with a $protocol Key offers no converted client entrypoint', async ({ protocol, label }) => {
  const { get } = renderDialog(makeChannel({ setting: '{"pass_through_body_enabled":true}' }), [
    { index: 0, status: 1, key_preview: 'key…one', upstream_protocol: protocol },
    { index: 1, status: 1, key_preview: 'key…two' },
  ])
  const user = userEvent.setup()
  await user.click(await screen.findByRole('combobox', { name: 'Tested key' }))
  await user.click(await screen.findByRole('option', { name: '1 · key…one · Enabled' }))
  await user.click(screen.getByRole('button', { name: 'Settings' }))
  await user.click(await screen.findByRole('combobox', { name: 'Endpoint Type' }))
  const labels = within(await screen.findByRole('listbox')).getAllByRole('option').map((option) => option.textContent)
  const expected = [label]
  if (protocol === 'responses') expected.splice(1, 0, 'OpenAI Response Compaction (/v1/responses/compact)')
  expect(labels).toEqual(expected)
  expect(get.mock.calls.filter(([url]) => url === '/api/channel/test/7')).toHaveLength(0)
})

test('an inherited Key retains channel capabilities after switching from a pinned Chat Key', async () => {
  renderDialog(makeChannel({ setting: '{"supported_endpoints":["chat"]}' }), [
    { index: 0, status: 1, key_preview: 'key…one', upstream_protocol: 'chat' },
    { index: 1, status: 1, key_preview: 'key…two', upstream_protocol: 'inherit' },
  ])
  const user = userEvent.setup()
  await user.click(await screen.findByRole('combobox', { name: 'Tested key' }))
  await user.click(await screen.findByRole('option', { name: '1 · key…one · Enabled' }))
  await user.click(screen.getByRole('button', { name: 'Settings' }))
  await user.click(await screen.findByRole('combobox', { name: 'Endpoint Type' }))
  await user.click(await screen.findByRole('option', { name: 'OpenAI Responses (/v1/responses)' }))
  await user.keyboard('{Escape}')
  await user.click(screen.getByRole('combobox', { name: 'Tested key' }))
  await user.click(await screen.findByRole('option', { name: '2 · key…two · Enabled' }))
  await user.click(screen.getByRole('button', { name: 'Settings' }))
  expect(await screen.findByRole('combobox', { name: 'Endpoint Type' })).toHaveValue('Auto detect (default)')
  await user.click(screen.getByRole('combobox', { name: 'Endpoint Type' }))
  const list = within(await screen.findByRole('listbox'))
  expect(list.queryByRole('option', { name: 'OpenAI Responses (/v1/responses)' })).not.toBeInTheDocument()
  expect(list.queryByRole('option', { name: 'OpenAI Response Compaction (/v1/responses/compact)' })).not.toBeInTheDocument()
  expect(list.getByRole('option', { name: 'Embeddings (/v1/embeddings)' })).toBeVisible()
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

test('a single-key channel loads its protocol without showing a key choice or starting a probe', async () => {
  const { post, get } = renderDialog(
    makeChannel({
      channel_info: {
        is_multi_key: false,
        multi_key_size: 1,
        multi_key_polling_index: 0,
        multi_key_mode: 'random',
      },
    }),
    [{ index: 0, status: 1, upstream_protocol: 'messages' }]
  )

  await screen.findByText('model-a')
  expect(
    screen.queryByRole('combobox', { name: 'Tested key' })
  ).not.toBeInTheDocument()
  expect(post).toHaveBeenCalledWith(
    '/api/channel/multi_key/manage',
    expect.objectContaining({ channel_id: 7, action: 'get_key_status', page_size: 1 }),
    expect.anything()
  )
  const user = userEvent.setup()
  await user.click(screen.getByRole('button', { name: 'Settings' }))
  await user.click(await screen.findByRole('combobox', { name: 'Endpoint Type' }))
  const list = within(await screen.findByRole('listbox'))
  expect(list.getByRole('option', { name: 'OpenAI Responses (/v1/responses)' })).toBeVisible()
  expect(list.queryByRole('option', { name: 'Embeddings (/v1/embeddings)' })).not.toBeInTheDocument()
  expect(get).not.toHaveBeenCalled()
})

test.each([
  { protocol: 'messages' as const, endpoints: ['responses'] },
  { protocol: 'chat' as const, endpoints: ['messages'] },
  { protocol: 'responses' as const, endpoints: ['chat'] },
])('a $protocol Key excluded by the channel cannot start any diagnostic', async ({ protocol, endpoints }) => {
  const { get } = renderDialog(
    makeChannel({ setting: JSON.stringify({ supported_endpoints: endpoints }) }),
    [{ index: 0, status: 1, upstream_protocol: protocol }]
  )
  const user = userEvent.setup()
  await user.click(await screen.findByRole('combobox', { name: 'Tested key' }))
  await user.click(await screen.findByRole('option', { name: '1 · - · Enabled' }))
  expect(await screen.findByRole('status')).toHaveTextContent(
    'This channel does not support a compatible upstream endpoint for this request'
  )
  expect(screen.getByRole('button', { name: 'Test all 0 models' })).toBeDisabled()
  expect(screen.queryByRole('button', { name: 'Test Connection' })).not.toBeInTheDocument()
  expect(get).not.toHaveBeenCalled()
})

test.each(['business', 'network', 'missing'])('a single-Key %s metadata failure blocks probes and supports retry', async (failure) => {
  const { post, get } = mockChannelTest([{ index: 0, status: 1, upstream_protocol: 'chat' }])
  if (failure === 'network') {
    post.mockRejectedValueOnce(new Error('unavailable'))
  } else {
    post.mockResolvedValueOnce({
      data: { success: failure === 'missing', message: 'Failed to load key status' },
    })
  }
  render(
    <I18nextProvider i18n={i18n}>
      <DialogHarness channel={makeChannel({ channel_info: {
        is_multi_key: false, multi_key_size: 1, multi_key_polling_index: 0, multi_key_mode: 'random',
      } })} />
    </I18nextProvider>
  )
  const user = userEvent.setup()
  await user.click(await screen.findByRole('button', { name: 'Retry' }))
  expect(get).not.toHaveBeenCalled()
  await screen.findByText('model-a')
  expect(screen.queryByRole('combobox', { name: 'Tested key' })).not.toBeInTheDocument()
  await user.click((await testModelRow('model-a')).getByRole('button', { name: 'Test Connection' }))
  await waitFor(() => expect(get).toHaveBeenCalledWith(
    '/api/channel/test/7',
    expect.objectContaining({ params: { model: 'model-a', stream: true } })
  ))
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

test('all-channel mode tests an out-of-scope model with its selected identity and saves only on explicit add', async () => {
  const { get, post } = renderDialog(makeChannel(), [
    { index: 0, id: '101', rev: 3, status: 1, key_preview: 'sk-…mnop', models: ['model-b'], scope_mode: 'allowlist' },
    { index: 1, id: '102', rev: 7, status: 1, key_preview: '…67', scope_mode: 'all' },
  ])
  const user = userEvent.setup()
  await user.click(await screen.findByRole('combobox', { name: 'Tested key' }))
  await user.click(await screen.findByRole('option', { name: '1 · sk-…mnop · Enabled' }))
  expect(screen.queryByText('model-a')).not.toBeInTheDocument()
  await user.click(screen.getByRole('switch', { name: 'All channel models' }))
  const row = await testModelRow('model-a')
  await user.click(row.getByRole('button', { name: 'Test Connection' }))
  expect(await row.findByRole('button', { name: 'Add to key' })).toBeVisible()
  expect(get).toHaveBeenCalledWith('/api/channel/test/7', expect.objectContaining({ params: {
    model: 'model-a', stream: true, key_index: 0, key_entry_id: '101', expected_rev: 3, model_scope: 'all',
  } }))
  expect(post.mock.calls.filter(([, body]) => (body as { action: string }).action === 'set_key_models')).toHaveLength(0)
  post.mockResolvedValueOnce({ data: { success: true, data: { revision: 8, channel_models: ['model-a', 'model-b'] } } })
  await user.click(row.getByRole('button', { name: 'Add to key' }))
  await waitFor(() => expect(post).toHaveBeenCalledWith('/api/channel/multi_key/manage', {
    channel_id: 7, action: 'set_key_models', key_index: undefined, key_entry_id: '101', expected_rev: 3,
    scope_mode: undefined, models: ['model-a'], add_models: true,
  }, expect.objectContaining({ singleUseAuthorization: true })))
})

test('turning all-channel mode off resets results and automatic tests never carry its scope override', async () => {
  const { get } = renderDialog(makeChannel(), [
    { index: 0, status: 1, key_preview: 'sk-…mnop', models: ['model-b'], scope_mode: 'allowlist' },
  ])
  const user = userEvent.setup()
  const key = await screen.findByRole('combobox', { name: 'Tested key' })
  await user.click(key)
  await user.click(await screen.findByRole('option', { name: '1 · sk-…mnop · Enabled' }))
  const scope = screen.getByRole('switch', { name: 'All channel models' })
  await user.click(scope)
  expect((await testModelRow('model-a')).getByText('Not tested')).toBeVisible()
  await user.click(scope)
  expect(screen.queryByText('model-a')).not.toBeInTheDocument()
  await user.click(scope)
  await user.click(key)
  await user.click(await screen.findByRole('option', { name: 'Auto (by tested model)' }))
  const row = await testModelRow('model-a')
  await user.click(row.getByRole('button', { name: 'Test Connection' }))
  await waitFor(() => expect(get).toHaveBeenCalledWith('/api/channel/test/7', expect.objectContaining({ params: { model: 'model-a', stream: true } })))
  expect(row.queryByRole('button', { name: 'Add to key' })).not.toBeInTheDocument()
})

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
