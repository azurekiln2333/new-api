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
import { cleanup, render, screen } from '@testing-library/react'
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
    multi_key_size: 40,
    multi_key_polling_index: 0,
    multi_key_mode: 'random',
  },
  settings: '{}',
}

const manyKeys: KeyStatus[] = Array.from({ length: 10 }, (_, index) => ({
  index,
  status: 1,
}))

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
  vi.spyOn(api, 'post').mockImplementation(async (url) => {
    if (url === '/api/channel/multi_key/manage') {
      return {
        data: {
          success: true,
          data: {
            keys: manyKeys,
            total: 40,
            page: 1,
            page_size: 10,
            total_pages: 4,
            enabled_count: 40,
            manual_disabled_count: 0,
            auto_disabled_count: 0,
          },
        },
      }
    }
    return { data: { success: true } }
  })
  render(
    <I18nextProvider i18n={i18n}>
      <DialogHarness />
    </I18nextProvider>
  )
})

afterEach(() => {
  cleanup()
  client.clear()
  useAuthStore.setState({ auth: originalAuth })
  vi.restoreAllMocks()
})

async function findKeyList() {
  await screen.findByRole('columnheader', { name: 'Actions' })
  return screen.getByRole('region', { name: 'API Keys' })
}

test('dialog widens beyond the default narrow width on desktop viewports', async () => {
  await findKeyList()

  expect(screen.getByRole('dialog')).toHaveClass('sm:max-w-[min(90vw,72rem)]')
})

test('the key list is the only scroll region and is keyboard focusable', async () => {
  const keyList = await findKeyList()

  expect(keyList).toHaveClass('overflow-auto', 'min-h-0', 'flex-1')
  expect(keyList).toHaveAttribute('tabindex', '0')
  expect(screen.getByRole('dialog')).toContainElement(keyList)
})

test('statistics, toolbar and pagination stay outside the scrolling key list', async () => {
  const keyList = await findKeyList()

  expect(keyList).not.toContainElement(screen.getByText('Manual Disabled'))
  expect(keyList).not.toContainElement(screen.getByText('Auto Disabled'))
  expect(keyList).not.toContainElement(
    screen.getByRole('button', { name: 'Refresh' })
  )
  expect(keyList).not.toContainElement(screen.getByText('Page 1 of 4'))
})

test('column headers stick to the top of the key list', async () => {
  await findKeyList()

  expect(screen.getByRole('columnheader', { name: 'Status' })).toHaveClass(
    'sticky',
    'top-0'
  )
})

test('per-key actions stay pinned to the right edge without wrapping', async () => {
  await findKeyList()
  const [firstSetModels] = screen.getAllByRole('button', { name: 'Set Models' })

  expect(screen.getByRole('columnheader', { name: 'Actions' })).toHaveClass(
    'sticky',
    'top-0',
    'right-0'
  )
  expect(firstSetModels.closest('td')).toHaveClass('sticky', 'right-0')
  expect(firstSetModels.parentElement).toHaveClass('flex-nowrap')
})

test('opening the models editor keeps it outside the scrolling key list', async () => {
  const keyList = await findKeyList()
  const user = userEvent.setup()

  await user.click(screen.getAllByRole('button', { name: 'Set Models' })[0])

  const modelsSelect = screen.getByRole('combobox', { name: 'All models' })
  expect(keyList).not.toContainElement(modelsSelect)
  expect(screen.getByRole('button', { name: 'Save' })).toBeVisible()
})
