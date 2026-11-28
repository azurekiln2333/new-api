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
import { Profiler, type ReactNode } from 'react'
import { afterEach, beforeEach, expect, test, vi } from 'vitest'

import { ModelGroupSelector } from '@/components/model-group-selector'
import { api as httpClient } from '@/lib/api'
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
  vi.restoreAllMocks()
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
      models={options.models}
      onModelChange={(model) => state.updateConfig('model', model)}
      selectedGroup={state.config.group}
      groups={options.groups}
      onGroupChange={(group) => state.updateConfig('group', group)}
      channels={options.channels}
      channelOptions={options.channelOptions}
      selectedChannel={state.config.channel_id}
      onChannelChange={(channel) => state.updateConfig('channel_id', channel)}
    />
  )
}

test('channel navigation shows pending, a retryable error, then real empty data', async () => {
  let rejectChannels!: (error: Error) => void
  vi.mocked(api.getPlaygroundChannels)
    .mockImplementationOnce(
      () =>
        new Promise((_resolve, reject) => {
          rejectChannels = reject
        })
    )
    .mockResolvedValueOnce([])
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
  await waitFor(() =>
    expect(screen.getByRole('combobox')).toHaveTextContent('Default')
  )
  await user.click(screen.getByRole('combobox'))
  await user.click(screen.getByRole('button', { name: 'Channels' }))
  expect(screen.getByRole('status')).toHaveTextContent('Loading...')
  expect(screen.queryByText('No channels available')).not.toBeInTheDocument()

  await act(async () => {
    rejectChannels(new Error('Failed to load playground channels'))
  })
  expect(await screen.findByRole('alert')).toHaveTextContent(
    'Failed to load playground channels'
  )
  expect(screen.queryByRole('status')).not.toBeInTheDocument()
  expect(screen.queryByText('No channels available')).not.toBeInTheDocument()
  await user.click(screen.getByRole('button', { name: 'Retry' }))
  expect(await screen.findByText('No channels available')).toBeVisible()
  expect(screen.queryByRole('alert')).not.toBeInTheDocument()
  expect(screen.getByRole('button', { name: 'Channels' })).toHaveAttribute(
    'aria-pressed',
    'true'
  )
})

test('a pending or failed channel read blocks a selected pin without choosing a fallback', async () => {
  let rejectChannels!: (error: Error) => void
  vi.mocked(api.getPlaygroundChannels).mockImplementationOnce(
    () =>
      new Promise((_resolve, reject) => {
        rejectChannels = reject
      })
  )
  const view = setup(12)
  expect(view.result.current.channelOptions?.isPending).toBe(true)
  expect(view.result.current.channels).toBeUndefined()
  expect(view.result.current.canSend).toBe(false)
  await act(async () => {
    rejectChannels(new Error('Failed to load playground channels'))
  })
  await waitFor(() =>
    expect(view.result.current.channelOptions?.error).toBe(
      'Failed to load playground channels'
    )
  )
  expect(view.result.current.channelUnavailable).toBe(true)
  expect(view.result.current.canSend).toBe(false)
  expect(view.result.current.channels).toBeUndefined()
  expect(view.updateConfig).not.toHaveBeenCalled()
})

test('fetches channel options for the current group and ignores late previous-group data', async () => {
  vi.mocked(api.getUserGroups).mockResolvedValue([
    { value: 'default', label: 'Default', ratio: 1 },
    { value: 'auto', label: 'Auto', ratio: 'auto' },
  ])
  let resolveDefault!: (channels: api.PlaygroundChannelOption[]) => void
  vi.mocked(api.getPlaygroundChannels).mockImplementation((group) => {
    if (group === 'default') {
      return new Promise((resolve) => {
        resolveDefault = resolve
      })
    }
    return Promise.resolve([
      { id: 23, name: 'Auto channel', models: ['model-a'] },
    ])
  })
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  clients.push(client)
  const updateConfig = vi.fn()
  const setModels = vi.fn()
  const setGroups = vi.fn()
  const view = renderHook(
    ({ group }) =>
      usePlaygroundOptions({
        currentGroup: group,
        currentModel: 'model-a',
        setGroups,
        setModels,
        updateConfig,
      }),
    {
      initialProps: { group: 'default' },
      wrapper: ({ children }: { children: ReactNode }) => (
        <QueryClientProvider client={client}>{children}</QueryClientProvider>
      ),
    }
  )
  const defaultSignal = vi.mocked(api.getPlaygroundChannels).mock.calls[0][1]
  expect(defaultSignal).toBeInstanceOf(AbortSignal)
  view.rerender({ group: 'auto' })
  await waitFor(() =>
    expect(view.result.current.channels).toEqual([
      { label: 'Auto channel', value: 23 },
    ])
  )
  expect(defaultSignal?.aborted).toBe(true)
  expect(api.getPlaygroundChannels).toHaveBeenLastCalledWith(
    'auto',
    expect.any(AbortSignal)
  )
  await act(async () => {
    resolveDefault([
      { id: 12, name: 'Previous group channel', models: ['model-b'] },
    ])
  })
  expect(view.result.current.channels).toEqual([
    { label: 'Auto channel', value: 23 },
  ])
})

test.each(['account', 'role', 'permission', 'group'] as const)(
  'masks the previous pinned catalog on every committed render during a deferred %s source change',
  async (change) => {
    useAuthStore.getState().auth.setUser({
      id: 1,
      username: 'admin',
      role: 10,
      permissions: { admin_permissions: { channel: { read: true } } },
    })
    vi.mocked(api.getUserGroups).mockResolvedValue([
      { value: 'default', label: 'Original group', ratio: 1 },
      { value: 'second', label: 'Second', ratio: 1 },
    ])
    vi.mocked(api.getPlaygroundChannels).mockResolvedValue([
      { id: 12, name: 'Private channel', models: ['private-bound-model'] },
    ])
    const client = new QueryClient({
      defaultOptions: { queries: { retry: false } },
    })
    clients.push(client)
    const commits: string[] = []
    render(
      <QueryClientProvider client={client}>
        <Profiler
          id='channel-catalog'
          onRender={() => commits.push(document.body.textContent ?? '')}
        >
          <ChannelSelectionHarness />
        </Profiler>
      </QueryClientProvider>
    )
    const user = userEvent.setup()
    const trigger = screen.getByRole('combobox')
    await waitFor(() => expect(trigger).toHaveTextContent('Original group'))
    await user.click(trigger)
    await user.click(screen.getByRole('button', { name: 'Channels' }))
    await user.click(
      await screen.findByRole('button', { name: 'Private channel' })
    )
    const privateModel = await screen.findByRole('option', {
      name: 'private-bound-model',
    })
    const popup = privateModel.closest('[data-slot="popover-content"]')
    expect(popup).not.toBeNull()
    await user.click(privateModel)
    await waitFor(() =>
      expect(trigger).toHaveTextContent('private-bound-model')
    )
    await waitFor(() => expect(popup).not.toBeInTheDocument())

    let rejectModels!: (error: Error) => void
    vi.mocked(api.getUserModels).mockImplementation(
      () =>
        new Promise((_resolve, reject) => {
          rejectModels = reject
        })
    )
    let resolveGroups!: (
      groups: Awaited<ReturnType<typeof api.getUserGroups>>
    ) => void
    vi.mocked(api.getUserGroups).mockImplementation(
      () =>
        new Promise((resolve) => {
          resolveGroups = resolve
        })
    )
    const modelCalls = vi.mocked(api.getUserModels).mock.calls.length
    const groupCalls = vi.mocked(api.getUserGroups).mock.calls.length
    const channelCalls = vi.mocked(api.getPlaygroundChannels).mock.calls.length
    if (change === 'group') {
      await user.click(trigger)
      commits.length = 0
      await user.click(screen.getByRole('button', { name: 'Groups' }))
      await user.click(screen.getByRole('button', { name: 'Second' }))
    } else {
      commits.length = 0
      act(() => {
        useAuthStore.getState().auth.setUser({
          id: change === 'account' ? 2 : 1,
          username: 'replacement',
          role: change === 'role' ? 1 : 10,
          permissions: {
            admin_permissions: { channel: { read: change !== 'permission' } },
          },
        })
      })
    }
    await waitFor(() =>
      expect(api.getUserModels).toHaveBeenCalledTimes(modelCalls + 1)
    )
    expect(commits.length).toBeGreaterThan(0)
    for (const text of commits) {
      expect(text).not.toContain('private-bound-model')
      if (change !== 'group') expect(text).not.toContain('Original group')
    }
    expect(trigger).not.toHaveTextContent('private-bound-model')
    expect(screen.queryByText('private-bound-model')).not.toBeInTheDocument()
    expect(api.getUserModels).toHaveBeenLastCalledWith(
      change === 'group' ? 'second' : 'default',
      expect.any(AbortSignal)
    )
    if (change !== 'group') {
      expect(api.getUserGroups).toHaveBeenCalledTimes(groupCalls + 1)
      expect(api.getUserGroups).toHaveBeenLastCalledWith(
        expect.any(AbortSignal)
      )
      await act(async () => {
        resolveGroups([
          { value: 'default', label: 'Replacement group', ratio: 1 },
        ])
      })
    }
    await act(async () => {
      rejectModels(new Error('replacement models unavailable'))
    })
    expect(trigger).not.toHaveTextContent('private-bound-model')
    for (const text of commits) {
      expect(text).not.toContain('private-bound-model')
      if (change !== 'group') expect(text).not.toContain('Original group')
    }
    if (change === 'role' || change === 'permission') {
      expect(api.getPlaygroundChannels).toHaveBeenCalledTimes(channelCalls)
    }
  }
)

test('aborts model and group reads on identity changes and never renders their late catalogs', async () => {
  let resolveModels!: (
    models: Awaited<ReturnType<typeof api.getUserModels>>
  ) => void
  let resolveGroups!: (
    groups: Awaited<ReturnType<typeof api.getUserGroups>>
  ) => void
  vi.mocked(api.getUserModels).mockImplementationOnce(
    () =>
      new Promise((resolve) => {
        resolveModels = resolve
      })
  )
  vi.mocked(api.getUserGroups).mockImplementationOnce(
    () =>
      new Promise((resolve) => {
        resolveGroups = resolve
      })
  )
  const view = setup()
  await waitFor(() => expect(resolveModels).toBeTypeOf('function'))
  await waitFor(() => expect(resolveGroups).toBeTypeOf('function'))
  const modelSignal = vi.mocked(api.getUserModels).mock.calls[0][1]
  const groupSignal = vi.mocked(api.getUserGroups).mock.calls[0][0]
  expect(modelSignal).toBeInstanceOf(AbortSignal)
  expect(groupSignal).toBeInstanceOf(AbortSignal)
  act(() =>
    useAuthStore.getState().auth.setUser({ id: 2, username: 'next', role: 1 })
  )
  await waitFor(() =>
    expect(view.result.current.models).toEqual([
      { value: 'model-a', label: 'model-a' },
      { value: 'model-b', label: 'model-b' },
    ])
  )
  await waitFor(() =>
    expect(view.result.current.groups).toEqual([
      { value: 'default', label: 'Default', ratio: 1 },
    ])
  )
  expect(modelSignal?.aborted).toBe(true)
  expect(groupSignal?.aborted).toBe(true)
  await act(async () => {
    resolveModels([{ value: 'old-private-model', label: 'old-private-model' }])
    resolveGroups([
      { value: 'old-private-group', label: 'old-private-group', ratio: 1 },
    ])
  })
  expect(view.result.current.models).not.toContainEqual({
    value: 'old-private-model',
    label: 'old-private-model',
  })
  expect(view.result.current.groups).toEqual([
    { value: 'default', label: 'Default', ratio: 1 },
  ])
})

test('channel API consumes the successful wire envelope and rejects malformed data instead of returning an empty list', async () => {
  const actual = await vi.importActual<typeof api>('../../api')
  const get = vi.spyOn(httpClient, 'get')
  const channels = [{ id: 12, name: 'Scoped channel', models: ['model-b'] }]
  get.mockResolvedValueOnce({ data: { success: true, data: channels } })
  const signal = new AbortController().signal
  await expect(actual.getPlaygroundChannels('auto', signal)).resolves.toEqual(
    channels
  )
  expect(get).toHaveBeenLastCalledWith('/pg/channels', {
    params: { group: 'auto' },
    signal,
  })
  for (const data of [
    { success: false, message: 'Failed to load playground channels' },
    { success: true },
    { success: true, data: null },
    { success: true, data: {} },
  ]) {
    get.mockResolvedValueOnce({ data })
    await expect(actual.getPlaygroundChannels('default')).rejects.toThrow()
  }
  get.mockResolvedValueOnce({ data: { success: true, data: [] } })
  await expect(actual.getPlaygroundChannels('default')).resolves.toEqual([])
})

test('forwards cancellation to the user model and group catalog requests', async () => {
  const actual = await vi.importActual<typeof api>('../../api')
  const get = vi.spyOn(httpClient, 'get')
  const signal = new AbortController().signal
  get.mockResolvedValueOnce({ data: { success: true, data: ['model-a'] } })
  await expect(actual.getUserModels('second', signal)).resolves.toEqual([
    { value: 'model-a', label: 'model-a' },
  ])
  expect(get).toHaveBeenLastCalledWith('/api/user/models', {
    params: { group: 'second' },
    signal,
  })
  get.mockResolvedValueOnce({
    data: { success: true, data: { second: { desc: 'Second', ratio: 1 } } },
  })
  await expect(actual.getUserGroups(signal)).resolves.toEqual([
    { value: 'second', label: 'second', desc: 'Second', ratio: 1 },
  ])
  expect(get).toHaveBeenLastCalledWith('/api/user/self/groups', { signal })
})

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
  const trigger = screen.getByRole('combobox')
  await user.click(trigger)
  await user.click(await screen.findByRole('button', { name: 'Channels' }))
  await user.click(
    await screen.findByRole('button', { name: 'Scoped channel' })
  )
  expect(trigger).toHaveTextContent('Scoped channel')
  const requestCount = vi.mocked(api.getPlaygroundChannels).mock.calls.length

  act(() =>
    useAuthStore.getState().auth.setUser({
      id: 1,
      username: 'demoted',
      role: 1,
      permissions: { admin_permissions: { channel: { read: true } } },
    })
  )
  expect(trigger).not.toHaveTextContent('Scoped channel')
  expect(
    screen.queryByText(/Scoped channel|Channel #12/)
  ).not.toBeInTheDocument()
  expect(
    screen.queryByRole('button', { name: 'Channels' })
  ).not.toBeInTheDocument()
  expect(api.getPlaygroundChannels).toHaveBeenCalledTimes(requestCount)
  await waitFor(() => expect(trigger).toHaveTextContent('Default'))
})
