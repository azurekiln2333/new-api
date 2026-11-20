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
  act,
  cleanup,
  render,
  renderHook,
  screen,
  waitFor,
} from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import type { ReactNode } from 'react'
import { afterEach, beforeEach, expect, test, vi } from 'vitest'

import { ModelGroupSelector } from '@/components/model-group-selector'
import { useAuthStore } from '@/stores/auth-store'

import * as api from '../../api'
import { usePlaygroundOptions } from '../use-playground-options'
import { usePlaygroundState } from '../use-playground-state'

const clients: QueryClient[] = []

vi.mock('../../api', () => ({
  getUserModels: vi.fn(),
  getUserGroups: vi.fn(),
  getPlaygroundChannels: vi.fn(),
}))
vi.mock('@/lib/handle-server-error', () => ({ handleServerError: vi.fn() }))

beforeEach(() => {
  localStorage.clear()
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

afterEach(() => {
  cleanup()
  clients.splice(0).forEach((client) => client.clear())
  localStorage.clear()
  useAuthStore.getState().auth.reset()
})

function setup(currentChannel?: number, currentModel = 'model-b') {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  clients.push(client)
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
    view.client.setQueryData(
      ['playground-channels', 1, 100, true, 'default'],
      []
    )
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

function ChannelSelectionHarness() {
  const state = usePlaygroundState()
  const options = usePlaygroundOptions({
    currentGroup: state.config.group,
    currentModel: state.config.model,
    currentChannel: state.config.channel_id,
    setGroups: state.setGroups,
    setModels: state.setModels,
    updateConfig: state.updateConfig,
  })
  return (
    <ModelGroupSelector
      selectedModel={state.config.model}
      models={state.models}
      onModelChange={(model) => state.updateConfig('model', model)}
      selectedGroup={state.config.group}
      groups={state.groups}
      onGroupChange={(group) => state.updateConfig('group', group)}
      channels={options.channels}
      selectedChannel={state.config.channel_id}
      onChannelChange={(channel) => state.updateConfig('channel_id', channel)}
    />
  )
}

test('a regular user with channel permissions sees only groups even with cached channel metadata', async () => {
  useAuthStore.getState().auth.setUser({
    id: 2,
    username: 'custom-grant',
    role: 1,
    permissions: { admin_permissions: { channel: { read: true } } },
  })
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  clients.push(client)
  client.setQueryData(
    ['playground-channels', 2, 1, false, 'default'],
    [{ id: 99, name: 'Private channel outside group', models: ['model-b'] }]
  )
  render(
    <QueryClientProvider client={client}>
      <ChannelSelectionHarness />
    </QueryClientProvider>
  )
  const user = userEvent.setup()
  await waitFor(() =>
    expect(screen.getByRole('combobox')).toHaveTextContent('Default')
  )
  await user.click(screen.getByRole('combobox'))
  expect(screen.getByRole('button', { name: 'Default' })).toBeVisible()
  expect(
    screen.queryByRole('button', { name: 'Channels' })
  ).not.toBeInTheDocument()
  expect(
    screen.queryByRole('group', { name: 'Model navigation' })
  ).not.toBeInTheDocument()
  expect(
    screen.queryByText(/Private channel|Channel #99/)
  ).not.toBeInTheDocument()
  expect(api.getPlaygroundChannels).not.toHaveBeenCalled()
})

test('removes the selected channel and navigation immediately after an administrator is demoted', async () => {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  clients.push(client)
  render(
    <QueryClientProvider client={client}>
      <ChannelSelectionHarness />
    </QueryClientProvider>
  )
  const user = userEvent.setup()
  await user.click(screen.getByRole('combobox'))
  await user.click(await screen.findByRole('button', { name: 'Channels' }))
  await user.click(
    await screen.findByRole('button', { name: 'Scoped channel' })
  )
  expect(screen.getByRole('combobox')).toHaveTextContent('Scoped channel')
  const requestCount = vi.mocked(api.getPlaygroundChannels).mock.calls.length

  act(() =>
    useAuthStore.getState().auth.setUser({
      id: 1,
      username: 'demoted',
      role: 1,
      permissions: { admin_permissions: { channel: { read: true } } },
    })
  )
  expect(screen.getByRole('combobox')).toHaveTextContent('Default')
  expect(
    screen.queryByText(/Scoped channel|Channel #12/)
  ).not.toBeInTheDocument()
  expect(
    screen.queryByRole('button', { name: 'Channels' })
  ).not.toBeInTheDocument()
  expect(api.getPlaygroundChannels).toHaveBeenCalledTimes(requestCount)
})
