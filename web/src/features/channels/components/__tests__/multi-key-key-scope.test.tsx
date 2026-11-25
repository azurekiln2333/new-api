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

import type { Channel, ChannelBudgetView, KeyStatus } from '../../types'
import { ChannelsProvider, useChannels } from '../channels-provider'
import { MultiKeyManageDialog } from '../dialogs/multi-key-manage-dialog'

const i18n = createInstance()
await i18n.init({
  lng: 'en',
  resources: { en: { translation: {} } },
  initAsync: false,
})

const originalAuth = useAuthStore.getState().auth
let client: QueryClient

const channel: Channel = {
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
}

function keyStatus(overrides: Partial<KeyStatus>): KeyStatus {
  return { index: 0, status: 1, ...overrides }
}

function DialogHarness(props: { channel?: Channel }) {
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
          <OpenWithRow channel={props.channel ?? channel} />
        </ChannelsProvider>
      </RouterContextProvider>
    </QueryClientProvider>
  )
}

function OpenWithRow(props: { channel: Channel }) {
  const { setCurrentRow } = useChannels()
  useEffect(() => {
    setCurrentRow(props.channel)
  }, [props.channel, setCurrentRow])
  return <MultiKeyManageDialog open onOpenChange={() => {}} />
}

function mockKeyStatus(keys: KeyStatus[]) {
  return vi.spyOn(api, 'post').mockImplementation(async (url) => {
    if (url === '/api/channel/multi_key/manage') {
      return {
        data: {
          success: true,
          data: {
            keys,
            total: keys.length,
            page: 1,
            page_size: 10,
            total_pages: 1,
            enabled_count: keys.length,
            manual_disabled_count: 0,
            auto_disabled_count: 0,
          },
        },
      }
    }
    return { data: { success: true } }
  })
}

beforeEach(() => {
  client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
  useAuthStore.setState({
    auth: {
      ...originalAuth,
      user: { id: 1, username: 'root', role: ROLE.SUPER_ADMIN },
    },
  })
})

afterEach(() => {
  cleanup()
  client.clear()
  useAuthStore.setState({ auth: originalAuth })
  vi.restoreAllMocks()
})

async function renderDialog(keys: KeyStatus[]) {
  const post = mockKeyStatus(keys)
  render(
    <I18nextProvider i18n={i18n}>
      <DialogHarness />
    </I18nextProvider>
  )
  await screen.findByText('Allowed Models')
  return post
}

test('stable duplicate allocations submit the selected ID and separate CAS revisions', async () => {
  const quota: ChannelBudgetView = {
    scope_type: 'key',
    scope_id: '9007199254740995',
    channel_id: 7,
    ledger_enabled: true,
    enabled: true,
    limit_quota: 1000,
    period_used_quota: 30,
    lifetime_used_quota: 50,
    reserved_quota: 10,
    remaining_quota: 960,
    config_version: 8,
    reset_epoch: 2,
    accounting_started_at: 1,
  }
  const keys = [
    keyStatus({
      id: '9007199254740993',
      rev: 2,
      index: 0,
      key_preview: 'same…key',
      quota,
    }),
    keyStatus({
      id: '9007199254740995',
      rev: 4,
      index: 1,
      key_preview: 'same…key',
      user_id: 12,
      quota,
    }),
  ]
  const post = vi.spyOn(api, 'post').mockImplementation(async (_url, body) => {
    const action = (body as { action: string }).action
    if (action !== 'get_key_status') return { data: { success: true } }
    return {
      data: {
        success: true,
        data: {
          keys,
          revision: 10,
          total: 2,
          page: 1,
          page_size: 10,
          total_pages: 1,
          enabled_count: 2,
        },
      },
    }
  })
  render(
    <I18nextProvider i18n={i18n}>
      <DialogHarness />
    </I18nextProvider>
  )
  const user = userEvent.setup()
  const row = (await screen.findByText('9007199254740995')).closest('tr')
  expect(row).not.toBeNull()
  await user.click(
    within(row as HTMLElement).getByRole('button', { name: 'Set Quota' })
  )
  const input = screen.getByRole('textbox', {
    name: 'Local limit (site quota units)',
  })
  await user.clear(input)
  await user.type(input, '2000')
  await user.click(screen.getByRole('button', { name: 'Save' }))
  await waitFor(() =>
    expect(post).toHaveBeenCalledWith(
      '/api/channel/multi_key/manage',
      expect.objectContaining({
        action: 'set_key_quota',
        key_entry_id: '9007199254740995',
        expected_rev: 4,
        expected_config_version: 8,
        limit_quota: 2000,
      }),
      expect.objectContaining({ singleUseAuthorization: true })
    )
  )
  expect(
    post.mock.calls.some(
      ([, body]) =>
        (body as Record<string, unknown>).action === 'set_key_quota' &&
        (body as Record<string, unknown>).key_index !== undefined
    )
  ).toBe(false)
})

test('dedup apply submits only the explicitly confirmed server preview token', async () => {
  const preview = {
    preview_token: 'reviewed-plan',
    revision: 19,
    groups: [
      {
        survivor_id: '101',
        entry_ids: ['101', '102'],
        merged_period_used: 25,
        merged_lifetime_used: 70,
        limit_quota: 100,
        skipped: [
          { id: '103', reason: 'Key allocation has active requests or tasks' },
        ],
      },
    ],
  }
  const post = vi.spyOn(api, 'post').mockImplementation(async (_url, body) => {
    const params = body as { action: string; apply?: boolean }
    if (params.action === 'deduplicate_keys') {
      return {
        data: {
          success: true,
          data: params.apply
            ? { ...preview, applied: true, merged_groups: 1 }
            : preview,
        },
      }
    }
    return {
      data: {
        success: true,
        data: {
          keys: [
            keyStatus({
              id: '101',
              rev: 2,
              quota: { ledger_enabled: true } as ChannelBudgetView,
            }),
          ],
          revision: 19,
          total: 1,
          page: 1,
          page_size: 10,
          total_pages: 1,
          enabled_count: 1,
        },
      },
    }
  })
  render(
    <I18nextProvider i18n={i18n}>
      <DialogHarness />
    </I18nextProvider>
  )
  const user = userEvent.setup()
  await user.click(
    await screen.findByRole('button', { name: 'Deduplicate Keys' })
  )
  expect(await screen.findByText('103')).toBeVisible()
  expect(
    post.mock.calls.filter(
      ([, body]) => (body as { apply?: boolean }).apply === true
    )
  ).toHaveLength(0)
  await user.click(
    screen.getByRole('button', { name: 'Merge equivalent allocations' })
  )
  await waitFor(() =>
    expect(post).toHaveBeenCalledWith(
      '/api/channel/multi_key/manage',
      expect.objectContaining({
        action: 'deduplicate_keys',
        apply: true,
        preview_token: 'reviewed-plan',
        expected_revision: 19,
      }),
      expect.anything()
    )
  )
})

test('inactive allocation ledger hides budget controls rather than offering a fake unlimited account', async () => {
  await renderDialog([
    keyStatus({
      id: '1001',
      rev: 1,
      quota: { ledger_enabled: false, enabled: true } as ChannelBudgetView,
    }),
  ])
  expect(screen.getByRole('button', { name: 'Set Quota' })).toBeDisabled()
  expect(screen.getByRole('button', { name: 'Add allocation' })).toBeDisabled()
  expect(
    screen.getByRole('button', { name: 'Deduplicate Keys' })
  ).toBeDisabled()
  expect(screen.getByText('Not enabled')).toBeVisible()
})

test('appending the original key to a single static channel preserves both allocation IDs', async () => {
  const single = {
    ...channel,
    channel_info: {
      ...channel.channel_info,
      is_multi_key: false,
      key_entry_revision: 6,
    },
  }
  vi.spyOn(api, 'get').mockResolvedValue({
    data: { success: true, data: { ledger_enabled: true } },
  })
  const put = vi.spyOn(api, 'put').mockResolvedValue({
    data: { success: true, data: { id: channel.id, name: '' } },
  })
  const post = mockKeyStatus([
    keyStatus({ id: '9007199254740993', rev: 1, key_preview: 'tes…key' }),
    keyStatus({
      id: '9007199254740995',
      index: 1,
      rev: 1,
      key_preview: 'tes…key',
    }),
  ])
  render(
    <I18nextProvider i18n={i18n}>
      <DialogHarness channel={single} />
    </I18nextProvider>
  )
  const user = userEvent.setup()
  const input = await screen.findByLabelText('Upstream Key')
  expect(input).toHaveAttribute('type', 'password')
  expect(post).not.toHaveBeenCalled()
  await user.type(input, 'test-static-key')
  await user.click(screen.getByRole('button', { name: 'Add' }))
  const manager = await screen.findByRole('dialog', {
    name: /Multi-Key Management.*multi/,
  })
  expect(await within(manager).findByText('9007199254740993')).toBeVisible()
  expect(within(manager).getByText('9007199254740995')).toBeVisible()
  expect(screen.queryByLabelText('Upstream Key')).not.toBeInTheDocument()
  expect(put).toHaveBeenCalledExactlyOnceWith(
    '/api/channel/',
    {
      id: 7,
      key: 'test-static-key',
      key_mode: 'append',
      to_multi_key: true,
      expected_revision: 6,
    },
    expect.objectContaining({ singleUseAuthorization: true })
  )
})

test('an empty explicit allowlist means no models and is submitted with the stable identity', async () => {
  const post = await renderDialog([
    keyStatus({ id: '1001', rev: 3, scope_mode: 'allowlist', models: [] }),
  ])
  expect(screen.getByText('No models allowed')).toBeVisible()
  const user = userEvent.setup()
  await user.click(screen.getByRole('button', { name: 'Set Models' }))
  expect(screen.getByRole('switch', { name: 'All models' })).not.toBeChecked()
  await user.click(screen.getByRole('button', { name: 'Save' }))
  expect(post).toHaveBeenCalledWith(
    '/api/channel/multi_key/manage',
    expect.objectContaining({
      action: 'set_key_models',
      scope_mode: 'allowlist',
      models: [],
      key_entry_id: '1001',
      expected_rev: 3,
    }),
    expect.objectContaining({ singleUseAuthorization: true })
  )
})

test('each key row shows its allowed models and bound user', async () => {
  await renderDialog([
    keyStatus({
      index: 0,
      models: ['model-a'],
      user_id: 12,
      username: 'alice',
    }),
    keyStatus({ index: 1 }),
  ])

  expect(screen.getByText('model-a')).toBeVisible()
  expect(screen.getByText('alice (#12)')).toBeVisible()
  expect(screen.getByText('All models')).toBeVisible()
})

test('each key row shows the masked key preview next to its index', async () => {
  await renderDialog([
    keyStatus({ index: 0, key_preview: 'sk-…mnop' }),
    keyStatus({ index: 1 }),
  ])

  expect(screen.getByText('1. sk-…mnop')).toBeVisible()
  expect(screen.getByText('2. -')).toBeVisible()
})

test('setting models on a key posts the key index with the selected models', async () => {
  const post = await renderDialog([keyStatus({ index: 0 })])
  const user = userEvent.setup()

  await user.click(screen.getByRole('button', { name: 'Set Models' }))
  await user.click(screen.getByRole('combobox', { name: 'All models' }))
  await user.click(await screen.findByRole('option', { name: 'model-a' }))
  // The multi-select keeps its popup open; closing it restores the dialog body.
  await user.keyboard('{Escape}')
  await user.click(screen.getByRole('button', { name: 'Save' }))

  await waitFor(() =>
    expect(post).toHaveBeenCalledWith(
      '/api/channel/multi_key/manage',
      expect.objectContaining({
        channel_id: 7,
        action: 'set_key_models',
        key_index: 0,
        models: ['model-a'],
      }),
      expect.anything()
    )
  )
})

test('binding a user posts the parsed user ID and rejects an invalid one', async () => {
  const post = await renderDialog([keyStatus({ index: 1 })])
  const user = userEvent.setup()

  await user.click(screen.getByRole('button', { name: 'Bind User' }))
  const userIdInput = screen.getByRole('spinbutton', { name: 'User ID' })
  expect(screen.getByRole('button', { name: 'Save' })).toBeDisabled()

  await user.type(userIdInput, '12')
  await user.click(screen.getByRole('button', { name: 'Save' }))

  await waitFor(() =>
    expect(post).toHaveBeenCalledWith(
      '/api/channel/multi_key/manage',
      expect.objectContaining({
        channel_id: 7,
        action: 'set_key_user',
        key_index: 1,
        user_id: 12,
      }),
      expect.anything()
    )
  )
})

test('one-key discovery requires selection and Save before adding models to the committed channel catalog', async () => {
  let applied = false
  const post = vi.spyOn(api, 'post').mockImplementation(async (url, body) => {
    if (url !== '/api/channel/multi_key/manage') {
      return { data: { success: true } }
    }
    const params = body as { action: string }
    if (params.action === 'fetch_key_models') {
      return {
        data: {
          success: true,
          data: {
            models: ['model-a', ' model-c ', 'model-c', '', 'model-d'],
          },
        },
      }
    }
    if (params.action === 'set_key_models') {
      applied = true
      return {
        data: {
          success: true,
          data: {
            revision: 11,
            channel_models: ['model-a', 'model-b', 'model-c'],
          },
        },
      }
    }
    return {
      data: {
        success: true,
        data: {
          keys: [
            keyStatus({
              id: '1001',
              rev: applied ? 4 : 3,
              index: 0,
              scope_mode: 'allowlist',
              models: applied ? ['model-c'] : [],
            }),
            keyStatus({
              id: '1002',
              rev: 7,
              index: 1,
              scope_mode: 'allowlist',
              models: ['model-b'],
            }),
          ],
          revision: applied ? 11 : 10,
          total: 2,
          page: 1,
          page_size: 10,
          total_pages: 1,
          enabled_count: 2,
          manual_disabled_count: 0,
          auto_disabled_count: 0,
        },
      },
    }
  })
  render(
    <I18nextProvider i18n={i18n}>
      <DialogHarness />
    </I18nextProvider>
  )
  const row = (await screen.findByText('1001')).closest('tr')
  const siblingRow = screen.getByText('1002').closest('tr')
  expect(row).not.toBeNull()
  expect(siblingRow).not.toBeNull()
  const user = userEvent.setup()

  await user.click(
    within(row as HTMLElement).getByRole('button', { name: 'Set Models' })
  )
  await user.click(screen.getByRole('button', { name: 'Fetch from upstream' }))

  expect(
    await screen.findByText('Models this key serves upstream:')
  ).toBeVisible()
  expect(post).toHaveBeenCalledWith(
    '/api/channel/multi_key/manage',
    {
      channel_id: 7,
      action: 'fetch_key_models',
      key_entry_id: '1001',
      expected_rev: 3,
      key_index: undefined,
    },
    expect.anything()
  )
  expect(
    post.mock.calls.some(
      ([, body]) =>
        (body as { action: string }).action === 'set_key_models' ||
        (body as { action: string }).action === 'sync_key_models'
    )
  ).toBe(false)
  await user.click(screen.getByRole('combobox', { name: 'Select models' }))
  expect(await screen.findByRole('option', { name: 'model-c' })).toBeVisible()
  await user.keyboard('{Escape}')
  const discoveredModel = screen.getByRole('button', { name: 'model-c' })
  expect(discoveredModel).toHaveAttribute('aria-pressed', 'false')
  await user.click(discoveredModel)
  expect(discoveredModel).toHaveAttribute('aria-pressed', 'true')
  await user.click(discoveredModel)
  expect(discoveredModel).toHaveAttribute('aria-pressed', 'false')
  await user.click(discoveredModel)
  expect(discoveredModel).toHaveAttribute('aria-pressed', 'true')
  expect(screen.getByRole('button', { name: 'model-d' })).toHaveAttribute(
    'aria-pressed',
    'false'
  )
  expect(
    post.mock.calls.some(
      ([, body]) => (body as { action: string }).action === 'set_key_models'
    )
  ).toBe(false)
  await user.click(screen.getByRole('button', { name: 'Save' }))

  await waitFor(() =>
    expect(post).toHaveBeenCalledWith(
      '/api/channel/multi_key/manage',
      {
        channel_id: 7,
        action: 'set_key_models',
        key_entry_id: '1001',
        expected_rev: 3,
        key_index: undefined,
        scope_mode: 'allowlist',
        models: ['model-c'],
      },
      expect.objectContaining({ singleUseAuthorization: true })
    )
  )
  await waitFor(() =>
    expect(
      screen.queryByRole('button', { name: 'Save' })
    ).not.toBeInTheDocument()
  )
  await waitFor(() => {
    const savedRow = screen.getByText('1001').closest('tr')
    expect(within(savedRow as HTMLElement).getByText('model-c')).toBeVisible()
  })
  const savedSiblingRow = screen.getByText('1002').closest('tr')
  expect(savedSiblingRow).not.toBeNull()
  expect(
    within(savedSiblingRow as HTMLElement).getByText('model-b')
  ).toBeVisible()
  expect(
    post.mock.calls.filter(
      ([, body]) => (body as { action: string }).action === 'set_key_models'
    )
  ).toHaveLength(1)
  expect(
    post.mock.calls.some(
      ([, body]) => (body as { action: string }).action === 'sync_key_models'
    )
  ).toBe(false)

  await user.click(
    within(savedSiblingRow as HTMLElement).getByRole('button', {
      name: 'Set Models',
    })
  )
  await user.click(screen.getByRole('combobox', { name: 'Select models' }))
  expect(await screen.findByRole('option', { name: 'model-a' })).toBeVisible()
  expect(screen.getByRole('option', { name: 'model-b' })).toBeVisible()
  expect(screen.getByRole('option', { name: 'model-c' })).toBeVisible()
  expect(
    screen.queryByRole('option', { name: 'model-d' })
  ).not.toBeInTheDocument()
  await user.keyboard('{Escape}')
  await user.click(screen.getByRole('button', { name: 'Cancel' }))
})

test.each(['response', 'rejection'])(
  'a late %s conflict from cancelled discovery cannot replace the new channel editor',
  async (outcome) => {
    const conflict = {
      success: false,
      error_code: 'channel_key_revision_conflict',
    }
    let resolveOld!: (value: { data: typeof conflict }) => void
    let rejectOld!: (reason: unknown) => void
    const oldDiscovery = new Promise<{ data: typeof conflict }>(
      (resolve, reject) => {
        resolveOld = resolve
        rejectOld = reject
      }
    )
    let resolveCurrent!: (value: {
      data: { success: boolean; data: { models: string[] } }
    }) => void
    const currentDiscovery = new Promise<{
      data: { success: boolean; data: { models: string[] } }
    }>((resolve) => {
      resolveCurrent = resolve
    })
    const post = vi.spyOn(api, 'post').mockImplementation(async (_url, body) => {
      const params = body as { action: string; channel_id: number }
      if (params.action === 'fetch_key_models') {
        return params.channel_id === 7 ? oldDiscovery : currentDiscovery
      }
      return {
        data: {
          success: true,
          data: {
            keys: [
              keyStatus({
                id: params.channel_id === 7 ? '1001' : '2001',
                rev: 3,
                scope_mode: 'allowlist',
                models: [],
              }),
            ],
            revision: 10,
            total: 1,
            page: 1,
            page_size: 10,
            total_pages: 1,
            enabled_count: 1,
          },
        },
      }
    })
    const view = (row: Channel) => (
      <I18nextProvider i18n={i18n}>
        <DialogHarness channel={row} />
      </I18nextProvider>
    )
    const { rerender } = render(view(channel))
    const user = userEvent.setup()
    await screen.findByText('1001')
    await user.click(screen.getByRole('button', { name: 'Set Models' }))
    await user.click(screen.getByRole('button', { name: 'Fetch from upstream' }))
    expect(screen.getByRole('button', { name: 'Save' })).toBeDisabled()
    await user.click(screen.getByRole('button', { name: 'Cancel' }))
    expect(
      screen.queryByRole('button', { name: 'Save' })
    ).not.toBeInTheDocument()

    rerender(view({ ...channel, id: 8, name: 'next channel' }))
    await screen.findByText('2001')
    await user.click(screen.getByRole('button', { name: 'Set Models' }))
    await user.click(screen.getByRole('button', { name: 'Fetch from upstream' }))
    expect(screen.getByRole('button', { name: 'Save' })).toBeDisabled()

    await act(async () => {
      if (outcome === 'response') resolveOld({ data: conflict })
      else rejectOld({ response: { status: 409, data: conflict } })
      await oldDiscovery.catch(() => {})
    })
    expect(screen.getByText('2001')).toBeVisible()
    expect(screen.queryByText('1001')).not.toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Save' })).toBeDisabled()
    expect(
      post.mock.calls.filter(([, body]) => {
        const params = body as { action: string; channel_id: number }
        return params.action === 'get_key_status' && params.channel_id === 7
      })
    ).toHaveLength(1)

    await act(async () => {
      resolveCurrent({
        data: { success: true, data: { models: ['current-key-model'] } },
      })
      await currentDiscovery
    })
    expect(
      await screen.findByRole('button', { name: 'current-key-model' })
    ).toBeVisible()
    expect(screen.getByRole('button', { name: 'Save' })).toBeEnabled()
    await user.click(screen.getByRole('button', { name: 'Cancel' }))
  }
)

test('syncing models from upstream previews per-key counts before applying', async () => {
  const post = vi.spyOn(api, 'post').mockImplementation(async (url, body) => {
    if (url !== '/api/channel/multi_key/manage') {
      return { data: { success: true } }
    }
    const params = body as { action: string; apply?: boolean }
    if (params.action === 'sync_key_models' && params.apply) {
      return {
        data: {
          success: true,
          data: {
            results: [
              { key_index: 0, models: ['model-a'] },
              { key_index: 1, skipped: true },
            ],
            added_models: ['model-b'],
            applied: true,
          },
        },
      }
    }
    if (params.action === 'sync_key_models') {
      return {
        data: {
          success: true,
          data: {
            results: [
              { key_index: 0, models: ['model-a'] },
              { key_index: 1, skipped: true },
              { key_index: 2, models: [], error: 'Bad key' },
            ],
            added_models: ['model-b'],
            applied: false,
          },
        },
      }
    }
    return {
      data: {
        success: true,
        data: {
          keys: [keyStatus({ index: 0 }), keyStatus({ index: 1 })],
          total: 2,
          page: 1,
          page_size: 10,
          total_pages: 1,
          enabled_count: 1,
          manual_disabled_count: 1,
          auto_disabled_count: 0,
        },
      },
    }
  })
  render(
    <I18nextProvider i18n={i18n}>
      <DialogHarness />
    </I18nextProvider>
  )
  await screen.findByText('Allowed Models')
  const user = userEvent.setup()

  await user.click(
    screen.getByRole('button', { name: 'Sync models from upstream' })
  )

  expect(await screen.findByText('Models added to the channel')).toBeVisible()
  expect(screen.getByText('model-b')).toBeVisible()
  expect(screen.getByText('#3: Bad key')).toBeVisible()
  expect(screen.getByText(/Disabled keys are skipped/)).toBeVisible()
  expect(post).toHaveBeenCalledWith(
    '/api/channel/multi_key/manage',
    expect.objectContaining({ channel_id: 7, action: 'sync_key_models' }),
    expect.anything()
  )

  await user.click(screen.getByRole('button', { name: 'Apply' }))

  expect(await screen.findByText('Models synced')).toBeVisible()
  expect(post).toHaveBeenCalledWith(
    '/api/channel/multi_key/manage',
    expect.objectContaining({
      channel_id: 7,
      action: 'sync_key_models',
      apply: true,
    }),
    expect.anything()
  )
})
