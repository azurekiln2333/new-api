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
import { act, renderHook, waitFor } from '@testing-library/react'
import type { ReactNode } from 'react'
import { afterEach, beforeEach, expect, test, vi } from 'vitest'

import { useAuthStore } from '@/stores/auth-store'

import * as api from '../../api'
import { usePlaygroundOptions } from '../use-playground-options'

vi.mock('../../api', () => ({
  getUserModels: vi.fn(),
  getUserGroups: vi.fn(),
  getPlaygroundChannels: vi.fn(),
}))
vi.mock('@/lib/handle-server-error', () => ({ handleServerError: vi.fn() }))

beforeEach(() => {
  vi.clearAllMocks()
  useAuthStore.getState().auth.setUser({ id: 1, username: 'admin', role: 100 })
  vi.mocked(api.getUserGroups).mockResolvedValue([
    { value: 'default', label: 'Default', ratio: 1 },
  ])
  vi.mocked(api.getUserModels).mockResolvedValue([
    { value: 'model-a', label: 'model-a' },
    { value: 'model-b', label: 'model-b' },
  ])
  vi.mocked(api.getPlaygroundChannels).mockResolvedValue([
    { id: 12, name: 'Scoped channel', models: ['model-b'] },
  ])
})

afterEach(() => useAuthStore.getState().auth.reset())

function setup(currentChannel?: number, currentModel = 'model-b') {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  const setModels = vi.fn()
  const updateConfig = vi.fn()
  const setGroups = vi.fn()
  const view = renderHook(
    () =>
      usePlaygroundOptions({
        currentGroup: 'default',
        currentModel,
        currentChannel,
        setModels,
        setGroups,
        updateConfig,
      }),
    {
      wrapper: ({ children }: { children: ReactNode }) => (
        <QueryClientProvider client={client}>{children}</QueryClientProvider>
      ),
    }
  )
  return { ...view, client, setModels, updateConfig }
}

test('filters models to the selected channel and preserves a valid selection', async () => {
  const view = setup(12)
  await waitFor(() => expect(view.result.current.canSend).toBe(true))
  expect(view.setModels).toHaveBeenLastCalledWith([
    { value: 'model-b', label: 'model-b' },
  ])
  expect(view.updateConfig).not.toHaveBeenCalled()
  expect(view.result.current.channels).toEqual([
    { value: 12, label: 'Scoped channel' },
  ])
})

test('keeps an unavailable explicit pin and blocks sending instead of routing elsewhere', async () => {
  const view = setup(12)
  await waitFor(() => expect(view.result.current.canSend).toBe(true))
  act(() => {
    view.client.setQueryData(['playground-channels', 1, 'default'], [])
  })
  await waitFor(() => expect(view.result.current.channelUnavailable).toBe(true))
  expect(view.result.current.canSend).toBe(false)
  expect(view.setModels).toHaveBeenLastCalledWith([])
  expect(view.updateConfig).not.toHaveBeenCalled()
})

test('does not query channel metadata for regular users and rejects forged local selection', async () => {
  useAuthStore.getState().auth.setUser({ id: 2, username: 'user', role: 1 })
  const view = setup(12)
  await waitFor(() => expect(api.getUserModels).toHaveBeenCalled())
  expect(api.getPlaygroundChannels).not.toHaveBeenCalled()
  expect(view.result.current.channels).toBeUndefined()
  expect(view.result.current.channelUnavailable).toBe(true)
  expect(view.result.current.canSend).toBe(false)
  expect(view.updateConfig).not.toHaveBeenCalled()
})
