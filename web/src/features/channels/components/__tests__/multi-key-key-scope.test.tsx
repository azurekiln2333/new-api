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
import { cleanup, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { createInstance } from 'i18next'
import { useEffect, useState } from 'react'
import { I18nextProvider } from 'react-i18next'
import { afterEach, beforeEach, expect, test, vi } from 'vitest'

import { api } from '@/lib/api'
import { ROLE } from '@/lib/roles'
import { useAuthStore } from '@/stores/auth-store'

import type { Channel, KeyStatus } from '../../types'
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

function DialogHarness() {
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
          <OpenWithRow />
        </ChannelsProvider>
      </RouterContextProvider>
    </QueryClientProvider>
  )
}

function OpenWithRow() {
  const { setCurrentRow } = useChannels()
  useEffect(() => {
    setCurrentRow(channel)
  }, [setCurrentRow])
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
